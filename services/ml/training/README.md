# `services/ml/training` — supervised fraud model

**Status: implemented and data-floor gated.** The pipeline is real
(`labels.py` → `features.py` → `gate.py` → `train.py` → `artifact.py`, driven by
`cli.py`), but it **trains nothing and writes no artifact until real labelled
dispute data clears the floor of ≥50 feature-bearing examples per class**. Below
the floor the ML service keeps serving the unsupervised cold-start models and the
registry stays `heuristic` — a classifier fit on an empty or near-empty label set
produces a confidently wrong model, exactly the failure mode InfluAudit exists to
expose in other people's numbers. A trained model only ever *augments* the
cold-start path, never replaces it.

Run it with `make ml-train` (or `python -m training.cli --labels-url … --out
$INFLUAUDIT_ML_ARTIFACTS`): it fetches labels from the admin export and either
writes `model.txt` + `manifest.json` into `$INFLUAUDIT_ML_ARTIFACTS` (which the
running service picks up on its next request) or reports the shortfall and writes
nothing.

## Where the labels come from

The label source is the **dispute-review loop** in the Go `admin` module
(`internal/admin`). A creator or brand files a dispute against an audit; an admin
resolves it with a decision. That decision is the labelling act:

- **rejected** → the audit's fraud flag stands → the account is confirmed
  coordinated/fraudulent → **label = `true`** (positive example).
- **upheld** (resolved) → the flag was a false positive → the account is
  confirmed legitimate → **label = `false`** (negative example).

The admin module exports every *decided* dispute through
`GET /v1/admin/training/labels` (admin-only), as `LabelExportResponse`:

```jsonc
{
  "count": 2,
  "labels": [
    {
      "dispute_id": "…",
      "audit_job_id": "…",
      "label": true,                 // true = fraudulent (dispute rejected)
      "has_features": true,          // false when the audit stored no fraud row
      "features": {                  // the audit's persisted fraud_result, verbatim
        "present": true,
        "fake_follower_rate": 0.31,
        "bot_comment_rate": 0.42,
        "engagement_anomaly": 0.18,
        "clique_count": 9,
        "clique_membership_fraction": 0.42,
        "confidence": 0.6,
        "model_version": "clique-v1"
      },
      "resolved_at": "2026-07-01T12:00:00Z"
    }
  ]
}
```

The feature vector is **the fraud estimate the audit actually recorded**
(`fraud_result`, migration `000020`), never a recomputation and never a
fabricated all-zero vector. When the disputed audit produced no fraud row,
`has_features` is `false` and `features` is absent — the trainer must decide
whether to drop such an example, not silently treat it as an all-zero feature
vector (that would teach the model that "no signal" ⇒ a specific point in feature
space, which is false).

## The feature contract (freeze this)

The trainable columns, all from `FraudFeatures`:

| Feature | Meaning | Range |
|---|---|---|
| `fake_follower_rate` | per-account fake-follower estimate | [0,1] |
| `bot_comment_rate` | coordination rate (= clique membership fraction) | [0,1] |
| `engagement_anomaly` | engagement deviation from benchmark | [0,1] |
| `clique_count` | maximal co-commenter cliques of size ≥ 5 (primary signal) | ≥ 0 |
| `clique_membership_fraction` | share of commenters inside a coordinated clique | [0,1] |
| `confidence` | the unsupervised model's self-reported confidence | [0,1] |

`model_version` is **metadata, not a feature** — it segments examples by which
unsupervised model version produced them, so a distribution shift after a model
change is detectable and old examples can be excluded. `present` gates the row
(only `present && has_features` rows are trainable).

## Planned trainer (when data exists)

- **Model:** LightGBM binary classifier (`fraudulent` vs `legitimate`).
  Gradient-boosted trees handle the small tabular feature set, mixed scales, and
  the non-linear `clique_count` signal without feature engineering, and give
  per-feature importances that keep the score explainable — a hard requirement
  for a customer-facing, disputable number.
- **Minimum data gate:** do not fit below a floor of labelled, feature-bearing
  examples per class (start at **≥ 50 per class**, revisit with a learning
  curve). Below the floor, `services/ml` keeps serving the current
  **unsupervised** coordination-first models; the supervised model only ever
  *augments* them once it clears held-out validation, it does not replace the
  cold-start path.
- **Validation:** temporal split (train on older decisions, validate on newer)
  to catch the model over-fitting to a single review era; report precision/recall
  per class, not accuracy (the label set will be class-imbalanced).
- **Output:** a versioned model artifact + its metrics, surfaced through the same
  `estimate: true` / `confidence` / `model_version` envelope every other ML
  response already carries, so a supervised score is never presented as ground
  truth.

## What must NOT happen

- No model trained below the data floor and shipped as if it were validated.
- No treating `has_features:false` rows as all-zero feature vectors.
- No dropping the `estimate`/`confidence` envelope — a learned score is still an
  estimate, and a disputed one at that.
- No consuming a cross-account commenter graph feature without the legal
  sign-off that gates deploying the coordination features at all (see
  `product/research/fraud-detection-signals.md` §7 and `product/BACKLOG.md`).
