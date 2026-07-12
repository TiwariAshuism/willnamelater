-- Drops the mlops model registry, canary set, and prediction log. Their indexes
-- are dropped with them.
DROP TABLE IF EXISTS ml_prediction_log;
DROP TABLE IF EXISTS ml_canary_account;
DROP TABLE IF EXISTS ml_model_version;
