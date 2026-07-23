-- Owner: billing module (internal/billing).

CREATE TABLE plan (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code         text NOT NULL,
    name         text NOT NULL,
    price_micros bigint NOT NULL DEFAULT 0,   -- monthly price, micros of currency
    currency     text NOT NULL DEFAULT 'USD',
    audit_quota  integer NOT NULL DEFAULT 0,  -- audits per billing period; -1 = unlimited
    bulk_quota   integer NOT NULL DEFAULT 0,  -- bulk audits per period; -1 = unlimited
    bulk_enabled boolean NOT NULL DEFAULT false,
    active       boolean NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (code)
);

CREATE TRIGGER trg_plan_set_updated_at BEFORE UPDATE ON plan
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE subscription (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- RESTRICT: a plan referenced by any subscription cannot be deleted; it is
    -- retired by setting plan.active = false instead.
    plan_id                  uuid NOT NULL REFERENCES plan(id) ON DELETE RESTRICT,
    status                   text NOT NULL CHECK (status IN ('trialing', 'active', 'past_due', 'canceled', 'expired')),
    current_period_start     timestamptz NOT NULL,
    current_period_end       timestamptz NOT NULL,
    cancel_at_period_end     boolean NOT NULL DEFAULT false,
    external_customer_id     text,
    external_subscription_id text,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX subscription_external_key ON subscription (external_subscription_id)
    WHERE external_subscription_id IS NOT NULL;
CREATE INDEX subscription_user_id_idx ON subscription (user_id);
CREATE INDEX subscription_plan_id_idx ON subscription (plan_id);
-- At most one live subscription per user.
CREATE UNIQUE INDEX subscription_one_live_per_user ON subscription (user_id)
    WHERE status IN ('trialing', 'active', 'past_due');

CREATE TRIGGER trg_subscription_set_updated_at BEFORE UPDATE ON subscription
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE usage_counter (
    -- Natural key: one running counter per user per billing period.
    user_id          uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    period           text NOT NULL,          -- billing period as 'YYYY-MM'
    audits_used      integer NOT NULL DEFAULT 0,
    bulk_audits_used integer NOT NULL DEFAULT 0,
    llm_cost_micros  bigint NOT NULL DEFAULT 0,
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, period)
);

CREATE TRIGGER trg_usage_counter_set_updated_at BEFORE UPDATE ON usage_counter
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
