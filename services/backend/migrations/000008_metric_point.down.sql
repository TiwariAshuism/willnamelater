-- CASCADE drops the hypertable's chunks or the partitioned table's partitions.
-- The timescaledb extension itself is left installed: it is cluster-scoped and
-- may be shared, and dropping it is an operator decision, not a schema rollback.
DROP TABLE IF EXISTS metric_point CASCADE;
