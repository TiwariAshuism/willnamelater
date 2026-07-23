-- Owner: mlops module (internal/mlops). The champion/challenger model registry,
-- the manually-verified canary set, and the shadow prediction log. No migration
-- seeds any row: a model version exists only once a challenger clears the data
-- floor and the offline gates over REAL data, a canary is inserted operationally
-- from a real verified account, and a prediction is logged only for real live
-- shadow traffic. Day one every table is empty and the serving registry stays on
-- the honest cold-start 'heuristic' state.
--
-- Postgres records roles + validation reports + the audit trail; S3 holds every
-- artifact; the artifact directory holds the serving champion. model_name is
-- 'fraud' or 'reach'.

CREATE TABLE ml_model_version (
    id                         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    model_name                 text NOT NULL,
    version                    text NOT NULL,        -- artifact_version, e.g. 'lgbm-ab12cd34ef56'
    role                       text NOT NULL,        -- 'champion' | 'challenger' | 'archived' | 'rejected'
    s3_key                     text NOT NULL,        -- prefix: ml-models/<model_name>/<version>/
    manifest_jsonb             jsonb NOT NULL,       -- the manifest.json the registry serves
    metrics_jsonb              jsonb NOT NULL,       -- offline validation metrics (per-class, per-tier, per-niche, reach calibration)
    validation_report_jsonb    jsonb NOT NULL,       -- full gate report: every gate's verdict + evidence
    data_floor_counts          jsonb NOT NULL,       -- class/row counts at train time (honesty marker)
    feature_snapshot_hash      text NOT NULL,        -- sha256 over the ordered (audit_job_id, features, label) tuples used
    feature_snapshot_watermark timestamptz NOT NULL, -- rows with captured_at <= this were eligible (reproducibility)
    feature_row_count          integer NOT NULL,
    created_at                 timestamptz NOT NULL DEFAULT now(),
    promoted_at                timestamptz,
    archived_at                timestamptz,
    UNIQUE (model_name, version)
);

-- At most one champion and one challenger per model at a time.
CREATE UNIQUE INDEX ml_model_version_one_champion  ON ml_model_version (model_name) WHERE role = 'champion';
CREATE UNIQUE INDEX ml_model_version_one_challenger ON ml_model_version (model_name) WHERE role = 'challenger';

-- Manually-verified ground-truth accounts every challenger must score correctly.
-- No migration seeds these; they are inserted operationally from REAL audited
-- accounts. An empty set means the canary gate is skipped-with-warning (cold
-- start).
CREATE TABLE ml_canary_account (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    model_name         text NOT NULL,
    label              text NOT NULL,              -- human description, e.g. 'known bought-follower account'
    features_jsonb     jsonb NOT NULL,             -- frozen feature vector for this account
    expected_label     boolean,                    -- fraud: true=fraudulent, false=clean (null for reach)
    expected_reach_min bigint,                     -- reach acceptance band (null for fraud)
    expected_reach_max bigint,
    source             text NOT NULL,              -- provenance note: how the ground truth was established
    active             boolean NOT NULL DEFAULT true,
    created_at         timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ml_canary_account_lookup ON ml_canary_account (model_name) WHERE active;

-- Shadow + audit trail: one row per shadow score. Append-only.
CREATE TABLE ml_prediction_log (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    model_name         text NOT NULL,
    audit_job_id       uuid,                        -- correlation; null for ad-hoc scores
    champion_version   text NOT NULL,
    champion_score     double precision NOT NULL,
    challenger_version text,                        -- null when no shadow model active
    challenger_score   double precision,
    features_hash      text NOT NULL,               -- sha256 of the scored feature vector
    scored_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ml_prediction_log_shadow ON ml_prediction_log (model_name, challenger_version, scored_at)
    WHERE challenger_version IS NOT NULL;
