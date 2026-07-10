-- Owner: llm module (internal/llm) -- store the generated report content.
--
-- llm_generation (000011) recorded only the token/cost accounting for each
-- generation, not the report itself. The report module needs the narrative back
-- to render the deliverable (JSON view + PDF), so the generated ReportOutput is
-- stored here as JSON, keyed to the same row as its cost. Keeping content and
-- cost on one row means a report can never exist without its accounting, and a
-- restated audit overwrites both together.
--
-- Nullable on purpose: a generation row may be written for accounting before or
-- without content (e.g. a future non-report purpose), and the report read treats
-- a null content as "no narrative yet" rather than an error.

ALTER TABLE llm_generation
    ADD COLUMN content_jsonb jsonb;

COMMENT ON COLUMN llm_generation.content_jsonb IS
    'The generated report body (llm.ReportOutput) as JSON. NULL = generated for accounting only, no stored narrative.';
