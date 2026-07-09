-- Owner: admin module (internal/admin) -- disputes raised against audit results.

CREATE TABLE dispute (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    audit_job_id uuid NOT NULL REFERENCES audit_job(id) ON DELETE CASCADE,
    -- SET NULL on both actors: keep the dispute's audit trail even after an
    -- account is deleted.
    raised_by    uuid REFERENCES users(id) ON DELETE SET NULL,
    reason       text NOT NULL,
    status       text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'under_review', 'resolved', 'rejected')),
    resolution   text,
    resolved_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    resolved_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX dispute_audit_job_id_idx ON dispute (audit_job_id);
CREATE INDEX dispute_status_idx ON dispute (status);

CREATE TRIGGER trg_dispute_set_updated_at BEFORE UPDATE ON dispute
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
