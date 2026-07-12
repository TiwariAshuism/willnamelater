# Champion–Challenger Continuous-Retraining Architecture — Resolved Contract

This is the binding design the Go and Python build agents implement. It resolves
`product/research/ML-Engine-Continuous-Retraining-Architecture.md` into concrete
DDL, endpoint shapes, a frozen feature vector, an S3 layout, and gate criteria.
It **extends** the existing foundation — it does not replace it:

- `services/ml/training/{labels,features,gate,train,artifact,cli}.py` stay; new
  code is added alongside.
- `services/ml/app/registry/registry.py` stays; the champion resolution is
  **unchanged**, a shadow slot is added additively.
- `services/backend/internal/admin` (dispute label export) stays untouched. A new
  Go module `internal/mlops` owns the feature store, registry, canaries, and
  prediction log.

**Non-negotiables carried from the project rules and the two memory notes
(no-fake-data, OAuth-only, tokens encrypted at rest):**

- No migration seeds business data. No fabricated rows, labels, reach numbers,
  metrics, or canary ground truth.
- The whole pipeline is **gated on real data**. Below a floor it trains nothing,
  promotes nothing, and the serving registry stays on the honest cold-start
  `heuristic` state (`registry.HEURISTIC_VERSION`).
- A missing feature is **null**, never zero-filled. `features.py` already drops
  rows without a real feature vector; the feature store carries JSON `null` for
  an absent optional feature (LightGBM consumes it as native missing/NaN).

Module MOD = `github.com/getnyx/influaudit/backend`. Migrations HEAD is 000022;
new migrations start at **000023**.

---

## 0. Component map

```
                         ┌──────────────── Go backend (internal/mlops, NEW module) ───────────────┐
audit orchestrator ──►   │  RecordFeatureRow (in-proc port)  ─►  training_feature_row (000023)     │
(worker.go, per audit)   │  admin ResolveDispute backfills fraud_label (in-proc port)              │
                         │  GET  /v1/admin/mlops/feature-rows   (trainer reads)                    │
                         │  POST /v1/admin/mlops/models         (trainer registers challenger)     │
                         │  POST /v1/admin/mlops/models/{v}/promote (trainer promotes / rollback)  │
                         │  GET/POST /v1/admin/mlops/canaries    (trainer reads / admin registers) │
                         │  POST /v1/ml/predictions             (ML server logs shadow, svc-token) │
                         │  ml_model_version / ml_canary_account / ml_prediction_log (000024)      │
                         │  S3 artifacts  s3://<bucket>/ml-models/<model>/<version>/{manifest,model}│
                         └──────────────────────────────────────────────────────────────────────────┘
                                        ▲ HTTP (admin JWT)                 ▲ HTTP (service token)
                                        │                                  │
   make ml-train  ─────────────────────┘                                  │
   (services/ml/training/cli.py, offline)                     services/ml/app (FastAPI server)
     fetch feature-rows + dispute labels + canaries                shadow scoring → POST /v1/ml/predictions
     train challenger → register → validate → shadow → promote     registry.py reads $INFLUAUDIT_ML_ARTIFACTS
     write champion artifact into $INFLUAUDIT_ML_ARTIFACTS         (champion) + /shadow (challenger, NEW)
```

The **artifact directory** ($INFLUAUDIT_ML_ARTIFACTS, a volume the ML service
reads at call time) is the serving source of truth for the champion — exactly as
today. **S3** holds every version for archive/rollback/audit. **Postgres** holds
roles, validation reports, and the audit trail.

---

## 1. Feature store — `training_feature_row` (migration 000023)

One row per **completed** audit (any terminal audit that produced snapshots),
written best-effort by the audit orchestrator through an in-process port. A write
failure must **not** fail the audit (same policy as the advisory report step in
`worker.go`). Owner module: `internal/mlops`.

```sql
-- Owner: mlops module. One clean labeled training row per completed audit.
-- No migration seeds rows. quality_ok=false rows ARE stored (auditability) but
-- excluded from training by default. A feature that the platform did not report
-- is stored as JSON null inside features_jsonb, never zero-filled.
CREATE TABLE training_feature_row (
    audit_job_id             uuid PRIMARY KEY REFERENCES audit_job(id) ON DELETE CASCADE,
    influencer_id            uuid NOT NULL,
    platform                 text NOT NULL,          -- primary platform of the vector
    features_jsonb           jsonb NOT NULL,         -- the frozen feature vector, see §1.1

    -- MULTIPLE independent model targets. Either may be null; a row can lack both.
    fraud_label              boolean,                -- supervised fraud target, backfilled on dispute decision
    fraud_label_source       text,                  -- 'dispute_rejected' | 'dispute_upheld' | NULL
    reach_label              bigint,                 -- real reach from OAuth insights (median reached accounts)
    reach_label_source       text,                  -- 'instagram_insights' | NULL

    -- Anti-gaming / data-quality verdict, computed at capture from the CURRENT
    -- champion's estimate (§2). The audit still ran; this only affects training.
    quality_ok               boolean NOT NULL,
    quality_reasons          text[] NOT NULL DEFAULT '{}',

    model_version_at_capture text NOT NULL,          -- champion version that produced the fraud sub-vector
    verification_tier        text NOT NULL,          -- 'verified'|'estimated'|'unverified' (contract.DeriveVerificationTier)
    captured_at              timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX training_feature_row_captured_at ON training_feature_row (captured_at);
CREATE INDEX training_feature_row_clean       ON training_feature_row (captured_at) WHERE quality_ok;
CREATE INDEX training_feature_row_fraud_label ON training_feature_row (captured_at) WHERE fraud_label IS NOT NULL;
CREATE INDEX training_feature_row_reach_label ON training_feature_row (captured_at) WHERE reach_label IS NOT NULL;

CREATE TRIGGER trg_training_feature_row_set_updated_at BEFORE UPDATE ON training_feature_row
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

`fraud_label` is backfilled: when `admin.ResolveDispute` decides a dispute, the
admin service calls a consumer-side port (`TrainingLabelSink`, satisfied by
mlops) that does `UPDATE training_feature_row SET fraud_label=$1,
fraud_label_source=$2 WHERE audit_job_id=$3`. No-op when no row exists (audit
predates the feature store). `reach_label` is set at capture when an Instagram
Insights pull produced a real reach figure for the audit, else left null.

### 1.1 Frozen feature vector (`features_jsonb`)

Computed **once at capture** (Go, in mlops) and read **verbatim** by Python — the
feature store's whole purpose is to eliminate train/serve skew in feature
computation. The key order below is frozen; the Python fraud projection keeps
`training.features.FEATURE_ORDER` and simply reads these keys from the jsonb.

**Fraud sub-vector — copied verbatim from the audit's `fraud_result` row
(migration 000020), always present. Keys MUST equal the existing
`FEATURE_ORDER`:**

| key | type | source |
|---|---|---|
| `fake_follower_rate` | float | fraud_result.fake_follower_rate |
| `bot_comment_rate` | float | fraud_result.bot_comment_rate |
| `engagement_anomaly` | float | fraud_result.engagement_anomaly |
| `clique_count` | int | fraud_result.clique_count |
| `clique_membership_fraction` | float | fraud_result.clique_membership_fraction |
| `confidence` | float | fraud_result.confidence |

These six are produced by the ML service at score time and captured into
`fraud_result`, so the fraud model has **no train/serve skew** — it trains and
serves on the same computed signals.

**Descriptive / reach sub-vector — computed at capture from the audit snapshots.
Deterministic formulas (Go implements exactly these):**

| key | type | formula / source |
|---|---|---|
| `follower_count` | int | `Snapshot.Followers` |
| `following_count` | int \| null | following count **if the connector reports it** (see §1.2 gap) |
| `follower_following_ratio` | float \| null | `follower_count / (following_count + 1)`; null when following is null |
| `engagement_rate` | float \| null | mean over posts of `(likes+comments)/max(follower_count,1)`; null when 0 posts |
| `engagement_rate_variance` | float \| null | population variance of per-post engagement_rate; null when < 2 posts |
| `comment_like_ratio` | float \| null | `sum(comments) / (sum(likes) + 1)`; null when 0 posts |
| `posting_cadence_per_week` | float \| null | `post_count / max(weeks_between_earliest_and_latest_post, 1)`; null when < 2 posts |
| `account_age_days_proxy` | float \| null | days from earliest observed metric/post timestamp to `captured_at`. PROXY — platform APIs do not expose account creation date; documented as an estimate, never presented as truth |
| `post_count` | int | number of posts in the snapshot |
| `niche` | string | `scoring.contract.Profiles.NicheOf`; `""` → unknown cohort |
| `tier` | string | follower-tier bucket (same derivation scoring already uses) |
| `verified` | bool \| null | platform verified flag **if the connector reports it** (see §1.2 gap) |
| `platform` | string | `Snapshot.Platform` |

The **fraud challenger** trains on the six fraud keys only (existing
`FEATURE_ORDER`). The **reach challenger** trains on the full vector; LightGBM
consumes JSON `null` as native missing.

### 1.2 Foundation gaps — flag, do NOT fabricate

`connector.Snapshot` (services/backend/internal/connector/connector.go) today
carries `Followers` but **not** a following count and **not** a verified flag.
Per the no-zero-fill rule these features are stored as **null** until the
connector actually reports them. Build agents: adding `Following int64` /
`Verified *bool` to `connector.Snapshot` (Instagram Graph exposes `follows_count`
and `is_verified`) is a separate foundation change — flag it for the human; do
not zero-fill in the meantime.

---

## 2. Data-quality / anti-gaming filter (doc §3)

Computed at capture (Go, mlops) from the audit's own `fraud_result` (the CURRENT
champion's read) plus the snapshot. Each failing rule appends a reason;
`quality_ok = len(quality_reasons) == 0`. The audit's own result is unaffected —
only the training fold is. Thresholds are documented constants (tunable), single
source in the mlops service:

| reason code | rule | rationale |
|---|---|---|
| `fake_follower_estimate_high` | `fraud_result.fake_follower_rate >= 0.30` | doc §3 outlier rejection — don't let a gamed account teach the model that fraud looks normal (feedback-poisoning guard) |
| `account_too_new` | `account_age_days_proxy < 90` (and not null) | brand-new accounts have volatile, easily-gamed metrics |
| `follower_spike` | any adjacent-interval follower growth in the series `> 0.50` within 24h | bought-follower spike |
| `insufficient_posts` | `post_count < 5` | too few posts for stable engagement features |
| `no_fraud_estimate` | `fraud_result.present = false` (or no fraud row) | cannot quality-check without the current model's read |

Rejected rows are still stored (with the reasons) for admin review; the training
export excludes them by default.

---

## 3. Model registry, canaries, prediction log (migration 000024)

Postgres records **roles + reports + audit trail**; S3 holds **all artifacts**;
the artifact dir holds the **serving champion** (existing registry.py contract).

```sql
-- Owner: mlops module. Champion/challenger/archived registry + rollback trail.
-- No migration seeds rows. model_name is 'fraud' or 'reach'.
CREATE TABLE ml_model_version (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    model_name               text NOT NULL,
    version                  text NOT NULL,        -- artifact_version, e.g. 'lgbm-ab12cd34ef56'
    role                     text NOT NULL,        -- 'champion' | 'challenger' | 'archived' | 'rejected'
    s3_key                   text NOT NULL,        -- prefix: ml-models/<model_name>/<version>/
    manifest_jsonb           jsonb NOT NULL,       -- the manifest.json the registry serves (version, model_file, feature_order, ...)
    metrics_jsonb            jsonb NOT NULL,       -- offline validation metrics (per-class, per-tier, per-niche, reach calibration)
    validation_report_jsonb  jsonb NOT NULL,       -- full gate report (§4): every gate's verdict + evidence
    data_floor_counts        jsonb NOT NULL,       -- class/row counts at train time (honesty marker)
    feature_snapshot_hash    text NOT NULL,        -- sha256 over the ordered (audit_job_id, features, label) tuples used
    feature_snapshot_watermark timestamptz NOT NULL, -- rows with captured_at <= this were eligible (reproducibility)
    feature_row_count        integer NOT NULL,
    created_at               timestamptz NOT NULL DEFAULT now(),
    promoted_at              timestamptz,
    archived_at              timestamptz,
    UNIQUE (model_name, version)
);
-- At most one champion and one challenger per model at a time:
CREATE UNIQUE INDEX ml_model_version_one_champion  ON ml_model_version (model_name) WHERE role = 'champion';
CREATE UNIQUE INDEX ml_model_version_one_challenger ON ml_model_version (model_name) WHERE role = 'challenger';

-- Manually-verified ground-truth accounts every challenger must score correctly.
-- No migration seeds these; they are inserted operationally from REAL audited
-- accounts. Empty set => canary gate is skipped-with-warning (cold start).
CREATE TABLE ml_canary_account (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    model_name         text NOT NULL,
    label              text NOT NULL,              -- human description, e.g. 'known bought-follower account'
    features_jsonb     jsonb NOT NULL,             -- frozen feature vector (§1.1) for this account
    expected_label     boolean,                    -- fraud: true=fraudulent, false=clean (null for reach)
    expected_reach_min bigint,                     -- reach acceptance band (null for fraud)
    expected_reach_max bigint,
    source             text NOT NULL,              -- provenance note: how the ground truth was established
    active             boolean NOT NULL DEFAULT true,
    created_at         timestamptz NOT NULL DEFAULT now()
);

-- Shadow + audit trail: one row per shadow score. Append-only.
CREATE TABLE ml_prediction_log (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    model_name         text NOT NULL,
    audit_job_id       uuid,                        -- correlation; null for ad-hoc scores
    champion_version   text NOT NULL,
    champion_score     double precision NOT NULL,
    challenger_version text,                        -- null when no shadow model active
    challenger_score   double precision,
    features_hash      text NOT NULL,               -- sha256 of the scored feature vector (per-score snapshot ref)
    scored_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ml_prediction_log_shadow ON ml_prediction_log (model_name, challenger_version, scored_at)
    WHERE challenger_version IS NOT NULL;
```

Note: the **per-score champion audit trail** (doc §3 "log which model version
produced every score") is already satisfied by `fraud_result.model_version`.
`ml_prediction_log` exists specifically for the **shadow champion-vs-challenger
comparison** on live traffic, plus a durable second copy of the model_version
per score.

### 3.1 S3 artifact layout

Reuse the existing report bucket (`INFLUAUDIT_STORAGE__BUCKET`, LocalStack in
dev) and the hand-rolled `internal/platform/storage` client
(`PutObject`, `PresignGetURL`, `EnsureBucket`):

```
s3://<bucket>/ml-models/<model_name>/<version>/manifest.json
s3://<bucket>/ml-models/<model_name>/<version>/model.txt
```

`<version>` is the deterministic `training.artifact.artifact_version`
(`lgbm-<sha256[:12]>`). The **backend** writes to S3 (register endpoint); the
trainer never holds S3 credentials.

### 3.2 Serving artifact dir (registry.py — champion unchanged, shadow added)

```
$INFLUAUDIT_ML_ARTIFACTS/
  manifest.json          # CHAMPION — registry.py reads this today, UNCHANGED
  model.txt
  shadow/                # NEW, optional — challenger during the shadow window
    manifest.json
    model.txt
```

`registry.py` extension (additive only): add `shadow_ref()` /
`shadow_version()` resolving `<dir>/shadow/manifest.json` with the exact same
validation logic `_resolve()` already uses. The champion path
(`active_version`, `is_supervised`) is untouched, so cold start still reports
`heuristic`.

### 3.3 Promotion & rollback effect

Promotion (single DB transaction) + artifact materialization:

1. DB tx: target `version` → `role='champion'`, `promoted_at=now()`; previous
   champion → `role='archived'`, `archived_at=now()`; any remaining challenger of
   that model → `archived`.
2. Artifact: write the champion's `manifest.json` + `model.txt` into
   `$INFLUAUDIT_ML_ARTIFACTS/` (the CLI does this from the promote response, or
   re-fetches from S3), and clear `$INFLUAUDIT_ML_ARTIFACTS/shadow/`.

The ML service picks it up on its next request (registry reflects the filesystem
at call time — no restart).

**Rollback = promote an archived version.** The promote endpoint accepts a
version currently in `archived`; it flips it back to champion and re-materializes
its artifact from S3. Because an archived version was previously a champion it
already earned its gates, so the shadow-pass requirement is waived for a rollback
(and `override_shadow=true` exists for emergencies).

---

## 4. State machine & gate criteria

```
completed audit
   │  (mlops.RecordFeatureRow, best-effort)
   ▼
[capture] compute feature vector (§1.1) + quality verdict (§2)  ──►  training_feature_row
   │
   ▼  (dispute decided, later)  admin → TrainingLabelSink → backfill fraud_label
   │
   ▼  make ml-train (offline, scheduled: weekly MVP → nightly growth)
[G0 data floor] ── below floor ──► train nothing; registry stays 'heuristic'; STOP
   │ pass
   ▼
[train challenger]  (extends train.py; temporal split ordered by captured_at)
   │
   ▼  POST /v1/admin/mlops/models  (register challenger → S3 + DB role='challenger')
   ▼
[G1 held-out] [G2 stratified] [G3 canary] [G4 challenger-vs-champion]
   │ any fail ──► role='rejected', alert, store failing gate in validation_report; STOP
   │ all pass
   ▼  write challenger artifact to $INFLUAUDIT_ML_ARTIFACTS/shadow/
[shadow window]  ML server scores champion + challenger, logs pairs to ml_prediction_log
   │
   ▼  make ml-train (shadow-check pass, after window)
[G5 shadow] ── fail ──► role='rejected', clear shadow dir, alert; STOP
   │ pass
   ▼  POST /v1/admin/mlops/models/{version}/promote
[promote]  DB role flip + materialize champion artifact + clear shadow dir
```

### Gate criteria (each recorded in `validation_report_jsonb` with its verdict + evidence)

**G0 — Data floor (extends `training.gate`). Counts CREATORS, not rows.**

The unit of evidence is an INFLUENCER, not an audit row. Rows are keyed by
`audit_job_id` and the same creator is re-audited on a schedule, so a row count
is not a sample size: 25 creators audited monthly for 8 months is 200 rows and
25 accounts. A row floor calls that "enough data" and it is not.

- Fraud: `FLOOR_PER_CLASS = 50` labelled rows per class **and**
  `MIN_FRAUD_INFLUENCERS_PER_CLASS = 25` DISTINCT influencers per class, on rows
  with `quality_ok`, `fraud_label IS NOT NULL`, **and a trainable
  `fraud_label_evidence`** (see below). Below floor → no challenger,
  `registry.active_version` stays `heuristic`.
- Reach: `MIN_REACH_INFLUENCERS = 200` **DISTINCT INFLUENCERS** (not rows) with
  `quality_ok` AND `reach_label IS NOT NULL`. Below floor → no reach challenger.
- A row with no `influencer_id` counts as zero distinct creators: we cannot prove
  it is independent of any other row, and unprovable independence is not
  independence.

**Label admissibility (fraud) — an echo is not ground truth.** A `fraud_label`
is trainable only when `fraud_label_evidence` names an OBSERVATION the heuristic
could not have made: `platform_enforcement_action`, `creator_admission`,
`purchase_receipt_or_panel_invoice`, `brand_campaign_conversion_data`,
`manual_follower_sample_audit`. The value `none_reviewed_heuristic_only` means
the reviewer saw nothing the heuristic had not already computed — that label IS
the heuristic's output, and a model trained on it is a distillation of the
heuristic that every gate below would then certify as independent. Such rows,
and rows with a missing/unknown evidence kind, are **UNLABELLED** — neither y=1
nor y=0. Missing is not trainable: absence of a stated observation is not an
observation.

**G1 — Held-out test** (newest-20% temporal slice, never used in training, per
existing `TRAIN_FRACTION = 0.8`) — the split is **GROUPED BY INFLUENCER**
(`train.grouped_temporal_split`): whole creators go to one side or the other, so
no influencer is ever in both train and held-out. A row-wise split lets the model
memorize creators it has already seen and G1 then reports recall of a memory:
- Fraud: per-class precision **and** recall `>= 0.70` for **both** classes.
- Reach: P50 quantile MAPE `<= 0.35`, and empirical coverage of the [P10,P90]
  band within `[0.70, 0.90]`.

**G2 — Stratified per tier / per niche** (doc §3 — a model that improves on
average but regresses for micro-influencers must not ship):
- For every stratum (each `tier`, each `niche`) with `>= MIN_STRATUM_N = 30`
  held-out rows, the challenger must not regress vs the current champion beyond
  tolerance: fraud recall drop `<= 0.05`; reach MAPE increase `<= 0.05`.
- Strata with `< MIN_STRATUM_N` are reported but not gated (too few to judge
  honestly).

**G3 — Canary must-pass** (doc §3):
- Every `active` `ml_canary_account` for the model must be scored correctly:
  fraud → predicted label at the serving threshold matches `expected_label`
  (100%); reach → P50 within `[expected_reach_min, expected_reach_max]` (100%).
- **Empty canary set → gate skipped, warning recorded** in the report (honest
  cold start; promotion still allowed but flagged as canary-uncovered).

**G4 — Challenger-vs-champion** (aggregate, same held-out slice, champion
re-scored on it):
- Non-inferiority on the primary metric: fraud → mean per-class F1 challenger
  `>=` champion; reach → challenger MAPE `<=` champion MAPE. Combined with G2 (no
  stratum regression).
- **First-ever model (no champion): G4 auto-passes**; the model must still clear
  G1 and G3.

**G6 — Beats the raw heuristic (MANDATORY; no auto-pass, no skip).**

A challenger that cannot beat the heuristic it was trained downstream of is a
**distillation of that heuristic, not a model**. It adds no information — only
the appearance of one — and promoting it launders a rule of thumb into "our ML
says". This gate is the only one that can catch that, because G1–G4 all compare
the model to LABELS and are perfectly happy with a model that has merely
re-learned the existing score.

- Fraud: baseline = the raw heuristic composite `risk_score`, read straight off
  the held-out rows' frozen feature vectors. Challenger **AUC** must exceed the
  heuristic's by `HEURISTIC_AUC_MARGIN = 0.02`. AUC because it is invariant to
  the two scores' different scales; a tie is a FAIL.
- Reach: the pipeline ships **no heuristic reach estimator**, so the honest
  "what you get without a model" baseline is the constant train-median predictor.
  Challenger MAPE must beat it by `HEURISTIC_MAPE_MARGIN = 0.02`. If a real
  heuristic reach estimator is ever shipped, pass its predictions instead.
- No first-model auto-pass (a first model has MORE to prove). If the baseline
  cannot be evaluated on enough held-out rows (`G6_MIN_N`), the gate **FAILS** —
  unproven is not promotable, and "we couldn't check" is not "it passed".

**Serving skew (`g5_serving_skew`) — a PLUMBING CHECK. NOT A GATE.**

It measures PSI between the offline held-out and live shadow score
*distributions*. **It joins NO LABELS.** It therefore cannot say the challenger
is more accurate, and it is written so that it cannot express such a claim: its
result carries **no `pass` key**, only `skew_detected: true | false | null`. It
can VETO a promotion (skew found, or too little traffic to say) and it can never
grant one. `should_promote` raises if handed a dict with a `pass` key.

> A null "plumbing" challenger that merely echoes the champion produces an
> identical score distribution and PSI ≈ 0. Under the old name (`g5_shadow`, with
> a `pass` key) that zero-label result read as a shadow-window PASS on 50 pairs,
> which is how a heuristic echo could be registered as an ML champion.

**The real shadow arbiter does not exist yet.** The label-joined comparison —
`ml_prediction_log` JOIN `training_feature_row` ON `audit_job_id`, scoring
champion and challenger against outcomes that arrived AFTER the prediction — is a
SEPARATE, NOT-YET-BUILT gate. Nothing above substitutes for it. Until it is
built, there is no shadow evidence of accuracy, and the report must not be read
as if there were.

All gates (G1, G2, G3, G4, G6) pass → promotable. Any gate fails →
`role='rejected'`, alert, the failing gate is recorded. The register endpoint only
**records** a challenger; the **promote** endpoint re-checks the stored report's
gate verdicts server-side (defense in depth: it refuses to promote a version whose
report shows any required gate failing or absent — it validates the report's pass
flags and the floor/canary policy, it does not recompute model metrics).

---

## 5. Backend HTTP surface (exact shapes)

New module `internal/mlops`, following the backend rules: `api/routes.go` is the
apigen source (`apigen -layers service` only); handler + repository + service are
hand-written; the module reaches other modules only through consumer-side ports;
errors go through `httpx.RenderError` over the `errs` envelope; do **not** commit
`*_handler_gen.go` and note the human runs `cmd/openapigen`.

Admin routes (`/v1/admin/mlops/...`) are gated by the shared `AdminGuard` port
(reused pattern from admin) — the trainer authenticates with the same admin JWT
the existing `make ml-train` already passes as `--token`. The prediction-ingest
route (`/v1/ml/predictions`) is gated by a **service token** (`ServiceAuth` port,
bearer `INFLUAUDIT_ML_SERVICE_TOKEN`), because the caller is the ML server, not a
user.

### 5.1 GET `/v1/admin/mlops/feature-rows` — feature-row export (trainer reads)

Query: `?since=<rfc3339>&quality=ok|all&limit=<n>` (default `quality=ok`,
`limit=5000`). Mirrors the admin label-export pattern.

```json
{
  "count": 812,
  "rows": [
    {
      "audit_job_id": "…", "influencer_id": "…", "platform": "instagram",
      "features": { "fake_follower_rate": 0.04, "bot_comment_rate": 0.01,
                    "engagement_anomaly": 0.12, "clique_count": 0,
                    "clique_membership_fraction": 0.0, "confidence": 0.7,
                    "follower_count": 15200, "following_count": null,
                    "follower_following_ratio": null, "engagement_rate": 0.031,
                    "engagement_rate_variance": 0.0004, "comment_like_ratio": 0.02,
                    "posting_cadence_per_week": 3.5, "account_age_days_proxy": 540.0,
                    "post_count": 24, "niche": "fitness", "tier": "mid",
                    "verified": null, "platform": "instagram" },
      "fraud_label": true, "fraud_label_source": "dispute_rejected",
      "reach_label": 15234, "reach_label_source": "instagram_insights",
      "quality_ok": true, "quality_reasons": [],
      "model_version_at_capture": "lgbm-ab12cd34ef56",
      "verification_tier": "verified", "captured_at": "2026-07-10T12:00:00Z"
    }
  ]
}
```

### 5.2 POST `/v1/admin/mlops/models` — register challenger (trainer calls)

```json
{
  "model_name": "fraud",
  "version": "lgbm-ab12cd34ef56",
  "manifest": { "version": "lgbm-ab12cd34ef56", "model_file": "model.txt",
                "feature_order": ["fake_follower_rate", "…"], "metrics": {}, "class_counts": {} },
  "model_file_name": "model.txt",
  "model_file_b64": "<base64 of model bytes>",
  "metrics": { "held_out": {…}, "per_tier": {…}, "per_niche": {…} },
  "validation_report": { "g1_held_out": {"pass": true, …}, "g2_stratified": {…},
                          "g3_canary": {"pass": true, "skipped": false, …},
                          "g4_vs_champion": {…} },
  "feature_snapshot": { "row_count": 812, "max_captured_at": "2026-07-10T00:00:00Z",
                         "content_hash": "sha256:…" },
  "data_floor_counts": { "positive": 61, "negative": 74, "floor": 50 }
}
```

Effect: backend PUTs `model_file_b64` → `ml-models/fraud/<version>/model.txt` and
`manifest` → `.../manifest.json`; inserts `ml_model_version` `role='challenger'`
(replacing any existing challenger for the model → old one → `rejected`).
Idempotent on `(model_name, version)`. Response:

```json
{ "id": "…", "model_name": "fraud", "version": "lgbm-ab12cd34ef56",
  "role": "challenger", "s3_key": "ml-models/fraud/lgbm-ab12cd34ef56/", "created_at": "…" }
```

### 5.3 POST `/v1/admin/mlops/models/{version}/promote` — promote / rollback

```json
{ "model_name": "fraud", "reason": "scheduled promotion after 3-day shadow", "override_shadow": false }
```

Effect: §3.3 (validate stored report → DB role flip → return champion artifact for
materialization). Rollback = pass an `archived` version (shadow requirement
waived). Response:

```json
{ "model_name": "fraud", "champion_version": "lgbm-ab12cd34ef56",
  "previous_champion_version": "lgbm-99887766ffee",
  "manifest": {…}, "s3_key": "ml-models/fraud/lgbm-ab12cd34ef56/", "promoted_at": "…" }
```

### 5.4 POST `/v1/ml/predictions` — prediction-log ingest (ML server calls)

Service-token auth. Best-effort, append-only; returns `202 Accepted`.

```json
{ "model_name": "fraud", "audit_job_id": "…|null",
  "champion_version": "lgbm-…", "champion_score": 62.4,
  "challenger_version": "lgbm-…|null", "challenger_score": 58.1,
  "features_hash": "sha256:…", "scored_at": "2026-07-11T09:00:00Z" }
```

### 5.5 Canaries — GET/POST `/v1/admin/mlops/canaries`

GET `?model_name=fraud&active=true` → `{ "count", "canaries":[ {id, model_name,
label, features:{…}, expected_label, expected_reach_min, expected_reach_max,
source, active} ] }`. POST body = `{ model_name, label, features:{…},
expected_label?, expected_reach_min?, expected_reach_max?, source }`. Canaries are
inserted operationally from **real verified accounts** — no migration seeds them.

### 5.6 ML server (FastAPI) shadow emission

`FraudScoreRequest` (app/schemas.py + Go `ml.FraudScoreRequest`) gains an
**optional** `audit_ref: str | None = None` correlation field (backward
compatible; `extra="forbid"` still holds). When a `shadow/` artifact is present,
the fraud endpoint scores champion (returned) **and** challenger, and fires a
best-effort background POST to `/v1/ml/predictions` (fire-and-forget; a failure
never affects the response). When no shadow artifact is present, no shadow row is
written. The service-token and backend base URL are new env for the ML service
(`INFLUAUDIT_ML_SERVICE_TOKEN`, `INFLUAUDIT_BACKEND_BASE_URL`); the HTTP call is
lazy/stdlib so the runtime stays lean.

---

## 6. Gated-on-real-data vs testable now, and cold start

**Testable now (no real business data — use schema-derived fixtures / synthetic
metric dicts, noted as such in tests):**
- All migrations (000023, 000024) up/down.
- mlops handlers/repos/services (table-driven), incl. register S3 write against
  LocalStack, promote role-flip transaction, rollback, prediction ingest.
- The quality filter (pure function) with synthetic feature vectors.
- The gate-criteria functions G1–G5 (pure) with synthetic metrics dicts.
- `registry.py` shadow-slot resolution with a temp artifact dir.
- Feature-vector computation (pure) with synthetic snapshots.

**Gated on real data (cannot be exercised until real OAuth users exist):**
- Actual challenger training (needs real feature rows + real dispute labels ≥ G0
  floor).
- Promotion (needs a real passing validation report).
- Canary gate (needs real manually-verified accounts; empty → skipped-with-warning).
- Serving-skew reading (needs real live traffic) — and it is not an accuracy gate.
- Reach model (needs real Instagram Insights reach labels covering
  ≥ MIN_REACH_INFLUENCERS **distinct creators**).
- Fraud model (needs dispute labels backed by a real `fraud_label_evidence`
  observation; until the Go side emits that field, the fraud fold is EMPTY and
  the model correctly refuses to train).

**Not built:** the label-joined shadow arbiter (`ml_prediction_log` JOIN
`training_feature_row`) — the only thing that could tell us a challenger is
genuinely more accurate on live traffic. `g5_serving_skew` is not it.

**Honest cold-start behavior (day one, zero labels):** no `ml_model_version`
champion row; artifact dir empty; `registry.active_version()` = `heuristic`;
`is_supervised()` = false; the ML service serves the unsupervised
coordination-first path. `training_feature_row` accumulates (quality-filtered)
but trains nothing; canary and prediction-log tables are empty. The first
promotion happens only once real labels clear G0 **and** G1–G4 pass over real
data (G5 after a real shadow window). Nothing is ever trained on nothing, and a
gamed account never redefines "normal."

---

## 7. Composition-root wiring — FLAG FOR THE HUMAN (do not edit `internal/app/**`)

The build agents must NOT touch `internal/app/**`. These wirings are required and
must be done by the human:

1. Mount the `internal/mlops` routes (admin group + the service-token `/v1/ml`
   group) and run `cmd/openapigen` (do not commit `*_handler_gen.go`).
2. Adapt `mlops.RecordFeatureRow` onto a new `audit/port.FeatureRecorder` and
   call it best-effort in `audit` `scoreAndReport` (non-fatal).
3. Adapt `mlops.SetFraudLabel` onto a new `admin/port.TrainingLabelSink` and call
   it from `admin.ResolveDispute` after a decision (non-fatal).
4. Inject the `internal/platform/storage` client + bucket and the
   `INFLUAUDIT_ML_SERVICE_TOKEN` into the mlops service; inject
   `INFLUAUDIT_ML_SERVICE_TOKEN` + `INFLUAUDIT_BACKEND_BASE_URL` into the ML
   service (docker-compose `x-backend-env` / ml service env).
5. Add a `make ml-train` round that chains fetch → train → register → validate →
   shadow → promote (extending the existing target).
```
