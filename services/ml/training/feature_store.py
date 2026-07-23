"""Read clean, labelled feature rows from the backend feature-store export.

DB-free by design (mirrors ``labels.py``): the ML service never speaks Postgres,
it reaches the backend over HTTP. This module fetches the frozen feature vectors
the Go ``mlops`` module captured once at audit time (eliminating train/serve
skew) and projects them onto the two independent model targets from the contract
(``RETRAINING_ARCHITECTURE.md`` §1):

- a supervised FRAUD classifier trains on the six frozen fraud keys plus the
  ``fraud_label`` backfilled on dispute decisions, and
- a REACH quantile regressor trains on the full numeric vector plus the
  ``reach_label`` pulled from OAuth Instagram Insights.

A model trains **only on rows that carry its label**. A missing feature is
``null`` in the export and becomes ``NaN`` here (LightGBM's native missing) — it
is never zero-filled, which would teach the model that "no signal" is a specific
point in feature space.
"""

from __future__ import annotations

import json
import math
import urllib.parse
import urllib.request
from dataclasses import dataclass

from training.features import FEATURE_ORDER

# The full numeric input vector for the REACH model: the six frozen fraud keys
# followed by the descriptive numeric sub-vector (§1.1). The high-cardinality
# string keys (``niche``, ``tier``, ``platform``) are deliberately NOT model
# inputs — they are carried separately as stratification dimensions (§4 G2), so
# the regressor never depends on an unstable one-hot of an open category set.
# ``verified`` is a bool encoded as 1.0 / 0.0 / NaN. The order is frozen for the
# same positional reason ``FEATURE_ORDER`` is.
REACH_FEATURE_ORDER = (
    *FEATURE_ORDER,
    "follower_count",
    "following_count",
    "follower_following_ratio",
    "engagement_rate",
    "engagement_rate_variance",
    "comment_like_ratio",
    "posting_cadence_per_week",
    "account_age_days_proxy",
    "post_count",
    "verified",
)


# --------------------------------------------------------------------------- #
# Fraud label evidence (defect E): which observations may become y
# --------------------------------------------------------------------------- #
# The Go export stamps each backfilled fraud label with the KIND OF OBSERVATION
# the admin actually made (``fraud_label_evidence``, closed enum). Only an
# observation the heuristic could not have made is admissible as ground truth.
FRAUD_EVIDENCE_TRAINABLE = frozenset({
    "platform_enforcement_action",
    "creator_admission",
    "purchase_receipt_or_panel_invoice",
    "brand_campaign_conversion_data",
    "manual_follower_sample_audit",
})

# The admin reviewed the case and observed NOTHING the heuristic had not already
# computed. Such a label is the heuristic's own output wearing a human's badge.
# Training on it and calling the result an independent model is circular: the
# model learns to reproduce the heuristic, G1 reports a superb score (it is
# scored against the same echo), and the whole pipeline certifies a mirror.
# G0-G5 CANNOT SEE THIS — they check model-vs-labels and assume the labels are
# real. So it is excluded here, at the only place that knows.
FRAUD_EVIDENCE_HEURISTIC_ECHO = "none_reviewed_heuristic_only"


def fraud_label_is_trainable(row) -> bool:
    """Whether a row's fraud label is an OBSERVATION, admissible as y.

    A row whose ``fraud_label_evidence`` is missing is NOT trainable. Absence of
    a stated observation is not an observation: the field is not yet emitted by
    every writer, and defaulting an unknown provenance to "trainable" is exactly
    how an echo gets in. Such a row is UNLABELLED — neither y=1 nor y=0 — not a
    negative. (Assumption stated loudly because it means that until the Go side
    ships ``fraud_label_evidence``, the fraud fold is EMPTY and the fraud model
    correctly refuses to train. "Insufficient data" is the right answer, not a
    reason to relax this.)
    """
    evidence = row.get("fraud_label_evidence")
    if not isinstance(evidence, str):
        return False
    return evidence.strip() in FRAUD_EVIDENCE_TRAINABLE


@dataclass(frozen=True)
class Stratum:
    """The cohort a held-out row belongs to, for per-tier / per-niche gating."""

    tier: str
    niche: str


@dataclass(frozen=True)
class FraudDataset:
    """Fraud rows: 6-column matrix, binary labels, temporal keys, strata.

    Every list is parallel and index-aligned. ``captured_at`` drives the
    temporal train/held-out split; ``strata`` drives G2; ``influencer_ids`` is
    the GROUP KEY — the data floor counts distinct values of it and the split
    never puts one influencer on both sides.
    """

    features: list[list[float]]
    targets: list[int]
    captured_at: list[str]
    strata: list[Stratum]
    audit_job_ids: list[str]
    influencer_ids: list[str]
    excluded: dict


@dataclass(frozen=True)
class ReachDataset:
    """Reach rows: full numeric matrix, integer reach labels, temporal keys.

    ``targets`` is the SINGLE per-account MEDIAN reach the Go export derived
    from live Instagram Graph insights. ``within_account_spread`` is a parallel
    list of the per-account p10/p90 of that account's own posts WHEN the export
    supplies them — it is a MEASUREMENT DISCLOSURE, never a training target and
    never the model's predictive interval (see ``challenger.train_reach_
    challenger``).
    """

    features: list[list[float]]
    targets: list[float]
    captured_at: list[str]
    strata: list[Stratum]
    audit_job_ids: list[str]
    influencer_ids: list[str]
    within_account_spread: list[dict | None]


def _num(value) -> float:
    """Project a JSON value onto a float, mapping ``null`` to ``NaN`` (native
    missing) and never to zero. Booleans map to 1.0 / 0.0."""
    if value is None:
        return math.nan
    if isinstance(value, bool):
        return 1.0 if value else 0.0
    return float(value)


def parse_export(payload) -> list[dict]:
    """Extract the rows list from a feature-row export dict."""
    return payload.get("rows") or []


def fetch_feature_rows(
    base_url: str,
    *,
    token: str | None = None,
    since: str | None = None,
    quality: str = "ok",
    limit: int = 5000,
    timeout: int = 60,
) -> list[dict]:
    """GET ``/v1/admin/mlops/feature-rows`` and return its rows list.

    ``base_url`` is the endpoint (e.g. ``http://localhost:8080/v1/admin/mlops/
    feature-rows``). ``quality='ok'`` (the default) asks the backend to exclude
    anti-gaming-rejected rows from the training fold; ``'all'`` includes them.
    """
    params: dict[str, str] = {"quality": quality, "limit": str(limit)}
    if since:
        params["since"] = since
    url = base_url + ("&" if "?" in base_url else "?") + urllib.parse.urlencode(params)
    request = urllib.request.Request(url)
    request.add_header("Accept", "application/json")
    if token:
        request.add_header("Authorization", f"Bearer {token}")
    with urllib.request.urlopen(request, timeout=timeout) as resp:  # noqa: S310
        payload = json.loads(resp.read().decode("utf-8"))
    return parse_export(payload)


def fetch_canaries(
    base_url: str,
    *,
    model_name: str,
    token: str | None = None,
    active: bool = True,
    timeout: int = 30,
) -> list[dict]:
    """GET ``/v1/admin/mlops/canaries`` for a model and return its canaries list.

    An empty list is the honest cold-start reality (no verified accounts yet);
    the caller skips the canary gate with a recorded warning (§4 G3).
    """
    params = {"model_name": model_name, "active": "true" if active else "false"}
    url = base_url + ("&" if "?" in base_url else "?") + urllib.parse.urlencode(params)
    request = urllib.request.Request(url)
    request.add_header("Accept", "application/json")
    if token:
        request.add_header("Authorization", f"Bearer {token}")
    with urllib.request.urlopen(request, timeout=timeout) as resp:  # noqa: S310
        payload = json.loads(resp.read().decode("utf-8"))
    return payload.get("canaries") or []


def _stratum(feats: dict) -> Stratum:
    niche = feats.get("niche")
    tier = feats.get("tier")
    return Stratum(
        tier=str(tier) if tier else "unknown",
        niche=str(niche) if niche else "unknown",
    )


def _spread(row) -> dict | None:
    """The account's OWN post-to-post reach spread, when the export states it.

    Returned for DISCLOSURE only. It is deliberately not part of ``targets``:
    see the ``train_reach_challenger`` docstring for why swapping it in would
    destroy the model's meaning while every gate kept reporting green.
    """
    lo, hi = row.get("reach_label_p10"), row.get("reach_label_p90")
    if lo is None or hi is None:
        return None
    return {"within_account_p10": float(lo), "within_account_p90": float(hi)}


def to_fraud_dataset(rows) -> FraudDataset:
    """Project export rows onto the fraud training matrix.

    A row becomes a training example only when it carries a fraud label AND that
    label rests on an OBSERVATION (``fraud_label_is_trainable``). A label with
    no evidence kind, or with the heuristic-echo kind, is treated as UNLABELLED
    and dropped — not as a negative. The six frozen fraud keys are read verbatim
    from the stored jsonb (no recomputation).
    """
    features: list[list[float]] = []
    targets: list[int] = []
    captured_at: list[str] = []
    strata: list[Stratum] = []
    audit_job_ids: list[str] = []
    influencer_ids: list[str] = []
    excluded = {"heuristic_echo": 0, "evidence_missing_or_unknown": 0}
    for row in rows:
        if row.get("fraud_label") is None:
            continue
        if not fraud_label_is_trainable(row):
            key = (
                "heuristic_echo"
                if row.get("fraud_label_evidence") == FRAUD_EVIDENCE_HEURISTIC_ECHO
                else "evidence_missing_or_unknown"
            )
            excluded[key] += 1
            continue
        feats = row.get("features") or {}
        features.append([_num(feats.get(name)) for name in FEATURE_ORDER])
        targets.append(1 if row.get("fraud_label") else 0)
        captured_at.append(str(row.get("captured_at") or ""))
        strata.append(_stratum(feats))
        audit_job_ids.append(str(row.get("audit_job_id") or ""))
        influencer_ids.append(str(row.get("influencer_id") or ""))
    return FraudDataset(
        features, targets, captured_at, strata, audit_job_ids, influencer_ids, excluded
    )


def to_reach_dataset(rows) -> ReachDataset:
    """Project export rows onto the reach training matrix.

    Only rows that carry a real ``reach_label`` (the single per-account MEDIAN
    reach derived from Instagram Insights) are kept, and that median is the ONLY
    target. Missing features pass through as ``NaN`` — native LightGBM missing.
    """
    features: list[list[float]] = []
    targets: list[float] = []
    captured_at: list[str] = []
    strata: list[Stratum] = []
    audit_job_ids: list[str] = []
    influencer_ids: list[str] = []
    spread: list[dict | None] = []
    for row in rows:
        if row.get("reach_label") is None:
            continue
        feats = row.get("features") or {}
        features.append([_num(feats.get(name)) for name in REACH_FEATURE_ORDER])
        targets.append(float(row["reach_label"]))
        captured_at.append(str(row.get("captured_at") or ""))
        strata.append(_stratum(feats))
        audit_job_ids.append(str(row.get("audit_job_id") or ""))
        influencer_ids.append(str(row.get("influencer_id") or ""))
        spread.append(_spread(row))
    return ReachDataset(
        features, targets, captured_at, strata, audit_job_ids, influencer_ids, spread
    )
