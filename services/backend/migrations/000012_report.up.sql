-- Owner: report module (internal/report).

CREATE TABLE report (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    audit_job_id uuid NOT NULL REFERENCES audit_job(id) ON DELETE CASCADE,
    -- SET NULL: a report survives rescoring; it retains its rendered artifact.
    score_id     uuid REFERENCES score(id) ON DELETE SET NULL,
    format       text NOT NULL DEFAULT 'pdf'     CHECK (format IN ('pdf', 'html', 'json')),
    status       text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'generating', 'ready', 'failed')),
    storage_key  text,
    public_slug  text,
    size_bytes   bigint,
    checksum     text,
    generated_at timestamptz,
    expires_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX report_public_slug_key ON report (public_slug) WHERE public_slug IS NOT NULL;
CREATE INDEX report_audit_job_id_idx ON report (audit_job_id);

CREATE TRIGGER trg_report_set_updated_at BEFORE UPDATE ON report
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
