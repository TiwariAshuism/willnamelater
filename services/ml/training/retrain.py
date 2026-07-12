"""Champion–challenger retraining orchestrator (the ``make ml-retrain`` entry).

Chains the contract's state machine (§4): fetch the frozen feature rows →
G0 data floor → train a challenger → G1–G4 offline validation → register the
challenger + write the shadow artifact → (after a real shadow window) G5 →
promote. Every step is **gated on real data**: below the floor, or on any failed
gate, or with no live shadow traffic, it writes nothing to the champion slot,
promotes nothing, prints the shortfall, and exits 0 — the serving registry stays
on the honest ``heuristic`` cold-start state.

Pure decision logic lives in ``gate``/``validate``; this module wires model
scoring (LightGBM, lazy) to those decisions and to the backend HTTP endpoints.
"""

from __future__ import annotations

import argparse
import sys

from training import challenger as ch
from training import promotion, validate
from training.artifact import (
    MODEL_FILENAME,
    build_manifest,
    clear_shadow_artifact,
    write_artifact,
    write_shadow_artifact,
)
from training.feature_store import (
    REACH_FEATURE_ORDER,
    fetch_canaries,
    fetch_feature_rows,
    to_fraud_dataset,
    to_reach_dataset,
)
from training.features import FEATURE_ORDER
from training.gate import meets_floor, meets_reach_floor
from training.train import TRAIN_FRACTION


def _temporal_split(captured_at, fraction=TRAIN_FRACTION):
    """Indices split into (train, held_out) by captured_at ascending; the newest
    slice is held out and never trained on (temporal validation, §4 G1)."""
    order = sorted(range(len(captured_at)), key=lambda i: (captured_at[i], i))
    split = max(1, int(len(order) * fraction))
    return order[:split], order[split:]


def _load_fraud_champion(out_dir):
    """Best-effort load of the current champion for re-scoring in G2/G4. Returns
    None on cold start or any incompatibility (then G4 auto-passes)."""
    from pathlib import Path

    path = Path(out_dir) / MODEL_FILENAME
    if not path.is_file():
        return None
    try:
        return ch.load_fraud_challenger(path.read_bytes())
    except Exception:  # noqa: BLE001 — a champion we can't parse is treated as none
        return None


def _load_reach_champion(out_dir):
    from pathlib import Path

    path = Path(out_dir) / MODEL_FILENAME
    if not path.is_file():
        return None
    try:
        return ch.load_reach_challenger(path.read_bytes())
    except Exception:  # noqa: BLE001
        return None


def _fraud_canary_results(canaries, model):
    """Score each canary's frozen vector with the challenger for G3."""
    scored = []
    for c in canaries:
        if c.get("expected_label") is None:
            continue
        feats = c.get("features") or {}
        vec = [_num(feats.get(k)) for k in FEATURE_ORDER]
        score = model.scores([vec])[0]
        scored.append({"label": c.get("label"),
                       "expected_label": bool(c["expected_label"]), "score": score})
    return scored


def _reach_canary_results(canaries, model):
    scored = []
    for c in canaries:
        if c.get("expected_reach_min") is None or c.get("expected_reach_max") is None:
            continue
        feats = c.get("features") or {}
        vec = [_num(feats.get(k)) for k in REACH_FEATURE_ORDER]
        p50 = model.scores([vec])[0]
        scored.append({"label": c.get("label"),
                       "expected_min": float(c["expected_reach_min"]),
                       "expected_max": float(c["expected_reach_max"]), "p50": p50})
    return scored


def _num(value) -> float:
    import math

    if value is None:
        return math.nan
    if isinstance(value, bool):
        return 1.0 if value else 0.0
    return float(value)


def run_fraud(args) -> int:
    rows = fetch_feature_rows(args.feature_rows_url, token=args.token, since=args.since)
    ds = to_fraud_dataset(rows)

    ok, counts = meets_floor(ds.targets)
    if not ok:
        print(
            f"[fraud] below data floor: {counts['positive']} positive / "
            f"{counts['negative']} negative labelled rows, need >= {counts['floor']} "
            "per class. No challenger; registry stays 'heuristic'."
        )
        return 0

    train_idx, val_idx = _temporal_split(ds.captured_at)
    model = ch.train_fraud_challenger(
        [ds.features[i] for i in train_idx], [ds.targets[i] for i in train_idx]
    )

    y_true = [ds.targets[i] for i in val_idx]
    strata = [ds.strata[i] for i in val_idx]
    chal_scores = model.scores([ds.features[i] for i in val_idx])

    champion = _load_fraud_champion(args.out)
    champ_scores = (
        champion.scores([ds.features[i] for i in val_idx]) if champion else None
    )

    canaries = _fetch_canaries_safe(args.canaries_url, "fraud", args.token)
    canary_results = _fraud_canary_results(canaries, model)

    report = validate.build_fraud_report(
        y_true, chal_scores, champ_scores, strata, canary_results
    )
    return _finish("fraud", args, model, report, ds, counts, FEATURE_ORDER)


def run_reach(args) -> int:
    rows = fetch_feature_rows(args.feature_rows_url, token=args.token, since=args.since)
    ds = to_reach_dataset(rows)

    ok, counts = meets_reach_floor(len(ds.targets))
    if not ok:
        print(
            f"[reach] below data floor: {counts['rows']} rows with a real reach "
            f"label, need >= {counts['floor']}. No challenger; registry stays "
            "'heuristic'."
        )
        return 0

    train_idx, val_idx = _temporal_split(ds.captured_at)
    model = ch.train_reach_challenger(
        [ds.features[i] for i in train_idx],
        [ds.targets[i] for i in train_idx],
        REACH_FEATURE_ORDER,
    )

    y_true = [ds.targets[i] for i in val_idx]
    strata = [ds.strata[i] for i in val_idx]
    preds = model.predict([ds.features[i] for i in val_idx])
    p10, p50, p90 = ch.reach_bands(preds)

    champion = _load_reach_champion(args.out)
    champ_p50 = (
        champion.scores([ds.features[i] for i in val_idx]) if champion else None
    )

    canaries = _fetch_canaries_safe(args.canaries_url, "reach", args.token)
    canary_results = _reach_canary_results(canaries, model)

    report = validate.build_reach_report(
        y_true, p10, p50, p90, champ_p50, strata, canary_results
    )
    return _finish("reach", args, model, report, ds, counts, REACH_FEATURE_ORDER)


def _fetch_canaries_safe(url, model_name, token):
    if not url:
        return []
    return fetch_canaries(url, model_name=model_name, token=token)


def _finish(model_name, args, model, report, ds, floor_counts, feature_order) -> int:
    model_bytes = model.to_bytes()
    if not report["all_required_pass"]:
        failing = [k for k in validate.GATE_KEYS
                   if not report[k]["pass"] and not report[k].get("skipped")]
        print(
            f"[{model_name}] validation FAILED (gates: {', '.join(failing)}). "
            "Challenger discarded; nothing promoted; registry unchanged."
        )
        # A backend that records the challenger still marks it rejected; the
        # offline run writes nothing to the serving artifact dir.
        return 0

    snap = {
        "row_count": len(ds.features),
        "max_captured_at": promotion.max_captured_at(ds.captured_at),
        "content_hash": promotion.snapshot_hash(
            ds.audit_job_ids, ds.features, ds.targets
        ),
    }
    manifest = build_manifest(
        model_bytes, feature_order=feature_order,
        metrics=report["g1_held_out"].get("evidence", {}),
        extra={"validation_report": report, "feature_snapshot": snap},
    )
    print(f"[{model_name}] validation PASSED — challenger {manifest['version']}")

    # Write the challenger into the shadow slot; the ML server scores it in
    # parallel with the champion over the shadow window (§4 G5).
    write_shadow_artifact(
        args.out, model_bytes, feature_order=feature_order,
        metrics=manifest["metrics"], extra={"feature_snapshot": snap},
    )
    print(f"[{model_name}] wrote shadow artifact to {args.out}/shadow")

    if args.models_url:
        payload = promotion.build_register_payload(
            model_name=model_name, manifest=manifest, model_bytes=model_bytes,
            metrics=manifest["metrics"], validation_report=report,
            feature_snapshot=snap, data_floor_counts=floor_counts,
        )
        resp = promotion.register_challenger(
            args.models_url, payload, token=args.token
        )
        print(f"[{model_name}] registered challenger: {resp.get('role', 'challenger')}")

    if args.promote:
        _promote(model_name, args, model_bytes, manifest, feature_order)
    else:
        print(
            f"[{model_name}] not promoting: shadow window (G5) runs on live "
            "traffic. Re-run with --promote after the window closes and G5 passes."
        )
    return 0


def _promote(model_name, args, model_bytes, manifest, feature_order) -> None:
    """Materialize the champion artifact and call the promote endpoint. G5 is the
    backend's / operator's gate here; --promote asserts the shadow window closed
    and passed (the backend re-validates the stored report)."""
    champion_extra = {
        k: manifest[k]
        for k in ("validation_report", "feature_snapshot")
        if k in manifest
    }
    # Confirm the promotion with the backend FIRST: it re-validates the stored gate
    # report + data-floor counts and flips the champion role in a transaction, and
    # raises on any rejection. Only once that succeeds do we mutate the serving
    # artifact dir — otherwise a rejected promote (e.g. a failed server-side
    # re-check) would leave the serving champion and the registry diverged (H5).
    if args.models_url:
        resp = promotion.promote(
            args.models_url, manifest["version"], model_name=model_name,
            reason=args.reason, override_shadow=args.override_shadow, token=args.token,
        )
        prev = resp.get("previous_champion_version")
        print(
            f"[{model_name}] promoted in registry; previous champion archived: {prev}"
        )
    write_artifact(
        args.out, model_bytes, feature_order=feature_order,
        metrics=manifest["metrics"], extra=champion_extra,
    )
    clear_shadow_artifact(args.out)
    print(f"[{model_name}] materialized champion {manifest['version']} to {args.out}")


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        description="Champion-challenger retraining: fetch, train, validate, "
        "register, shadow, promote. Gated on real data at every step."
    )
    p.add_argument("--model", choices=["fraud", "reach"], required=True)
    p.add_argument("--feature-rows-url", required=True,
                   help="GET .../v1/admin/mlops/feature-rows")
    p.add_argument("--canaries-url", default=None,
                   help="GET .../v1/admin/mlops/canaries (optional; empty=>G3 skip)")
    p.add_argument("--models-url", default=None,
                   help="POST .../v1/admin/mlops/models (register/promote); "
                   "omit for a dry offline validation run")
    p.add_argument("--token", default=None, help="admin bearer token")
    p.add_argument("--out", required=True,
                   help="artifact dir the ML service reads (INFLUAUDIT_ML_ARTIFACTS)")
    p.add_argument("--since", default=None, help="only rows captured after RFC3339")
    p.add_argument("--promote", action="store_true",
                   help="promote after the shadow window (G5) has closed and passed")
    p.add_argument("--override-shadow", action="store_true",
                   help="emergency: waive the shadow requirement on promote")
    p.add_argument("--reason", default="scheduled promotion")
    return p


def main(argv=None) -> int:
    args = build_parser().parse_args(argv)
    return run_fraud(args) if args.model == "fraud" else run_reach(args)


if __name__ == "__main__":
    sys.exit(main())
