-- Owner: audit module (internal/audit).

CREATE TYPE audit_status AS ENUM ('queued', 'running', 'partial', 'succeeded', 'failed', 'canceled');

CREATE TABLE audit_job (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- SET NULL: retain the job and its results if the influencer record is
    -- removed; the platform results still describe what was measured.
    influencer_id       uuid REFERENCES influencer(id) ON DELETE SET NULL,
    -- Deduplicates retried submissions: the same key never enqueues twice.
    idempotency_key     text NOT NULL,
    status              audit_status NOT NULL DEFAULT 'queued',
    requested_platforms platform[] NOT NULL DEFAULT '{}',
    priority            integer NOT NULL DEFAULT 0,
    attempts            integer NOT NULL DEFAULT 0,
    error_code          text,
    error_message       text,
    requested_at        timestamptz NOT NULL DEFAULT now(),
    started_at          timestamptz,
    finished_at         timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (idempotency_key)
);

CREATE INDEX audit_job_user_id_idx ON audit_job (user_id);
CREATE INDEX audit_job_influencer_id_idx ON audit_job (influencer_id);
-- Worker pickup: highest priority, oldest first, among non-terminal jobs.
CREATE INDEX audit_job_queue_idx ON audit_job (priority DESC, requested_at)
    WHERE status IN ('queued', 'running');

CREATE TRIGGER trg_audit_job_set_updated_at BEFORE UPDATE ON audit_job
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE audit_platform_result (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    audit_job_id  uuid NOT NULL REFERENCES audit_job(id) ON DELETE CASCADE,
    platform      platform NOT NULL,
    status        text NOT NULL CHECK (status IN ('ok', 'partial', 'skipped', 'error')),
    error_code    text,
    error_message text,
    fetched_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (audit_job_id, platform)
);

CREATE INDEX audit_platform_result_job_idx ON audit_platform_result (audit_job_id);

CREATE TRIGGER trg_audit_platform_result_set_updated_at BEFORE UPDATE ON audit_platform_result
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
