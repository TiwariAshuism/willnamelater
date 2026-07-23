# Database migrations

golang-migrate migrations for the InfluAudit Postgres (+ optional TimescaleDB)
schema. Every migration is a paired `NNNNNN_<name>.up.sql` / `.down.sql`; the
6-digit prefix is the version and files apply in ascending order. Every `up`
has a working `down`, and running the whole set down drops objects in reverse
dependency order.

## Migration order

| Version | Name              | Owner module | Tables |
|---------|-------------------|--------------|--------|
| 000001  | init_extensions   | platform     | `pgcrypto`, `platform` enum, `set_updated_at()` |
| 000002  | auth              | auth         | `users`, `sessions` |
| 000003  | oauth_token       | oauth        | `oauth_token` |
| 000004  | billing           | billing      | `plan`, `subscription`, `usage_counter` |
| 000005  | influencer        | influencer   | `influencer`, `influencer_handle` |
| 000006  | connector         | connector    | `connector_config`, `api_quota_ledger` |
| 000007  | audit             | audit        | `audit_status` enum, `audit_job`, `audit_platform_result` |
| 000008  | metric_point      | metrics      | `metric_point` (hypertable / partitioned) |
| 000009  | content           | connector    | `post`, `comment_sample` |
| 000010  | scoring           | scoring      | `scoring_weights`, `benchmark`, `score`, `fraud_result` |
| 000011  | llm_generation    | llm          | `llm_generation` |
| 000012  | report            | report       | `report` |
| 000013  | dispute           | admin        | `dispute` |

## Conventions

- **UUID keys** default via `gen_random_uuid()` (from `pgcrypto`). Natural keys
  are used where they are genuinely the identity: `connector_config.platform`,
  `api_quota_ledger(platform, day)`, and `usage_counter(user_id, period)`.
- **Shared `platform` enum** (`internal/audit`, `internal/connector`,
  `internal/metrics`, etc. all reference it) is defined once in `000001`. Adding
  a network is a single-line change there.
- **`updated_at`** is maintained by a `BEFORE UPDATE` trigger calling
  `set_updated_at()`; the function is owned by `000001`, triggers are created
  per table and removed automatically by `DROP TABLE`.
- **Secrets at rest** are never stored in plaintext. `oauth_token`
  (`access_token_enc`, `refresh_token_enc`, `dek_wrapped`) and
  `connector_config` (`client_secret_enc`, `dek_wrapped`) hold only ciphertext
  produced by `internal/platform/crypto` envelope encryption.
- **No data** is inserted by any migration. Reference constants (benchmarks,
  scoring weights, plans) are seeded by application code, not by migrations.

## TimescaleDB and the native-partition fallback

`metric_point` is a high-volume time series. Managed Postgres offerings (RDS,
Cloud SQL, Azure Database) frequently do **not** ship TimescaleDB, so migration
`000008` adapts at apply time inside a single `DO $$ ... $$` block:

1. It checks `pg_available_extensions` for `timescaledb`.
2. If present, it runs `CREATE EXTENSION IF NOT EXISTS timescaledb`. Should that
   fail (e.g. the extension is catalogued but not in `shared_preload_libraries`),
   the exception is caught and the block falls back.
3. **Timescale path:** create a plain table and convert it with
   `create_hypertable('metric_point', 'time')`.
4. **Fallback path:** create a native `PARTITION BY RANGE ("time")` table with
   identical columns, plus a `DEFAULT` partition so it is immediately writable.

The indexes are created once, after the block, and apply to whichever shape was
built. Every unique index includes `"time"` because both a hypertable and a
partitioned table require the partition key in any unique constraint.

`metric_point` intentionally declares **no foreign keys**: FK support on
hypertables is version-dependent and per-row referential checks are too costly
at ingest volume. `internal/metrics` validates `influencer_id` and
`audit_job_id` before writing.

The `000008` down drops the table with `CASCADE` (removing chunks or
partitions). It deliberately leaves the `timescaledb` extension installed —
the extension is cluster-scoped and possibly shared, so removing it is an
operator decision rather than a schema rollback.

## Running

Set `DATABASE_URL`, e.g.
`postgres://user:pass@localhost:5432/influaudit?sslmode=disable`.

```bash
# Apply all pending migrations
migrate -path ./migrations -database "$DATABASE_URL" up

# Roll back the most recent migration
migrate -path ./migrations -database "$DATABASE_URL" down 1

# Roll everything back
migrate -path ./migrations -database "$DATABASE_URL" down -all

# Inspect current version / recover from a dirty state
migrate -path ./migrations -database "$DATABASE_URL" version
migrate -path ./migrations -database "$DATABASE_URL" force <version>
```

The same files are embeddable via the golang-migrate `file://` and `iofs`
source drivers for in-process migration on service startup.
