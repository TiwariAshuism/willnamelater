-- Owner: metrics module (internal/metrics).
--
-- metric_point is a high-volume time series. Where TimescaleDB is available it
-- is a hypertable; where the extension is absent -- common on managed Postgres
-- (RDS, Cloud SQL, Azure) -- it degrades to a native RANGE-partitioned table
-- with the SAME columns and indexes.
--
-- The CREATE TABLE lives inside the guard because the two shapes are built
-- differently: a hypertable starts as an ordinary table that create_hypertable
-- converts, whereas a native partitioned table must declare PARTITION BY at
-- creation and cannot be converted afterwards.
--
-- No foreign keys are declared here on purpose. FK support on hypertables is
-- version-dependent, and per-row referential checks are prohibitively costly at
-- this ingest rate; the metrics module validates influencer_id and audit_job_id
-- against their tables before writing.
DO $$
DECLARE
    has_timescale boolean := false;
BEGIN
    IF EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'timescaledb') THEN
        BEGIN
            CREATE EXTENSION IF NOT EXISTS timescaledb;
            has_timescale := true;
        EXCEPTION WHEN OTHERS THEN
            -- Listed in the catalog but not loadable (e.g. absent from
            -- shared_preload_libraries): fall through to native partitioning.
            has_timescale := false;
        END;
    END IF;

    IF has_timescale THEN
        CREATE TABLE metric_point (
            "time"        timestamptz NOT NULL,
            influencer_id uuid NOT NULL,
            platform      platform NOT NULL,
            metric        text NOT NULL,
            value         double precision NOT NULL,
            audit_job_id  uuid
        );
        PERFORM create_hypertable('metric_point', 'time');
    ELSE
        CREATE TABLE metric_point (
            "time"        timestamptz NOT NULL,
            influencer_id uuid NOT NULL,
            platform      platform NOT NULL,
            metric        text NOT NULL,
            value         double precision NOT NULL,
            audit_job_id  uuid
        ) PARTITION BY RANGE ("time");

        -- A DEFAULT partition keeps the table writable without Timescale's
        -- automatic chunk management; the metrics module provisions dated range
        -- partitions ahead of ingest and detaches the default as it fills.
        CREATE TABLE metric_point_default PARTITION OF metric_point DEFAULT;
    END IF;
END
$$;

-- Indexes apply identically to the hypertable and the partitioned table. Every
-- unique index includes "time" because a partitioned table requires the
-- partition key in any unique constraint (Timescale enforces the same rule).
CREATE UNIQUE INDEX metric_point_series_key
    ON metric_point (influencer_id, platform, metric, "time");
CREATE INDEX metric_point_influencer_time_idx
    ON metric_point (influencer_id, "time" DESC);
CREATE INDEX metric_point_audit_job_idx
    ON metric_point (audit_job_id);
