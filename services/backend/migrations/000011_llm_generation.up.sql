-- Owner: llm module (internal/llm).

CREATE TABLE llm_generation (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- SET NULL: keep the cost/usage record for accounting even if the audit is
    -- deleted.
    audit_job_id  uuid REFERENCES audit_job(id) ON DELETE SET NULL,
    purpose       text NOT NULL,   -- e.g. 'summary', 'recommendation'
    model         text NOT NULL,
    -- Hash of the fully rendered prompt; the cache key for reusing a completion.
    prompt_hash   text NOT NULL,
    input_tokens  integer NOT NULL DEFAULT 0,
    output_tokens integer NOT NULL DEFAULT 0,
    cost_micros   bigint NOT NULL DEFAULT 0,
    cached        boolean NOT NULL DEFAULT false,
    latency_ms    integer,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX llm_generation_audit_job_id_idx ON llm_generation (audit_job_id);
CREATE INDEX llm_generation_prompt_hash_idx ON llm_generation (prompt_hash);
