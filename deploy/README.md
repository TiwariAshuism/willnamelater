# Deploy

Local development infrastructure for InfluAudit.

## Phase 1 scope

`docker-compose.yml` provisions only the stateful and third-party dependencies:

| Service     | Image                              | Host port | Purpose                                              |
|-------------|------------------------------------|-----------|------------------------------------------------------|
| `postgres`  | `timescale/timescaledb:latest-pg16`| `5432`    | Primary datastore with TimescaleDB for time-series.  |
| `redis`     | `redis:7-alpine`                   | `6379`    | Cache and `asynq` job queue (append-only persistence). |
| `gotenberg` | `gotenberg/gotenberg:8`            | `3000`    | HTML-to-PDF rendering for audit reports.             |

The Go backend (`services/backend`) and the Python ML service (`services/ml`)
run **on the host** during phase 1 and connect to these containers over the
exposed ports. Their Dockerfiles do not exist yet, so no application images are
defined in the compose file.

## Usage

```bash
docker compose -f deploy/docker-compose.yml up -d      # start
docker compose -f deploy/docker-compose.yml ps         # status + health
docker compose -f deploy/docker-compose.yml logs -f    # tail logs
docker compose -f deploy/docker-compose.yml down        # stop (keep volumes)
docker compose -f deploy/docker-compose.yml down -v     # stop + wipe data
```

Every service declares a healthcheck; `ps` shows `healthy` once each is ready.
Dependents that reference these should use `depends_on` with
`condition: service_healthy`.

## Configuration

Values are read from the environment with development defaults, so the stack
comes up with no `.env` file. Override any of these to change credentials or
avoid host port clashes:

| Variable            | Default      |
|---------------------|--------------|
| `POSTGRES_USER`     | `influaudit` |
| `POSTGRES_PASSWORD` | `influaudit` |
| `POSTGRES_DB`       | `influaudit` |
| `POSTGRES_PORT`     | `5432`       |
| `REDIS_PORT`        | `6379`       |
| `GOTENBERG_PORT`    | `3000`       |

The defaults are for local development only. Do not reuse them anywhere else.

Data persists in named volumes (`influaudit_postgres-data`,
`influaudit_redis-data`); remove them with `down -v`.
