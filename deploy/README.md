# deploy/

Everything needed to run InfluAudit locally and in production.

| | |
|---|---|
| **[ARCHITECTURE.md](ARCHITECTURE.md)** | How production runs, and why it can leave any cloud. **Read this first.** |
| **[MIGRATION.md](MIGRATION.md)** | The GCP → Azure runbook. |

## Layout

```
deploy/
  docker-compose.yml        Local dev stack. `make up`.
  docker-compose.prod.yml   Production. Standalone, NOT an overlay — prod removes
                            postgres/redis/localstack in favour of managed ones,
                            and a compose overlay cannot remove a service.
  Caddyfile                 TLS + reverse proxy. Automatic Let's Encrypt.
  otel-collector.yaml       The observability seam. Swapping telemetry backends
                            happens HERE, never in application code.
  prometheus.yml  loki.yml  tempo.yml
  grafana/provisioning/     Datasources and dashboards AS CODE, so they already
                            exist on the new VM on migration day.

  .sops.yaml                Secrets policy (SOPS + age).
  secrets/
    prod.enc.env.example    The template. Copy, fill, `sops --encrypt`.
    prod.enc.env            Encrypted, committed to git. Values encrypted, keys in
                            plaintext — so a diff shows WHICH secret rotated.

  scripts/
    bootstrap-vm.sh         Once per VM. The only file that assumes an OS, and it
                            names no cloud.
    deploy.sh               verify signatures → pull → MIGRATE → up → smoke → rollback
    rollback.sh             Restores the previous image. Does NOT revert migrations —
                            read its header before you wish it did.
    ssh-entrypoint.sh       The forced command behind the CI deploy key, so a leaked
                            key cannot get a shell.
    backup.sh               Nightly pg_dump → R2. THE script that makes a cloud
                            migration possible at all.
    restore.sh              The other half. Exercised monthly by dr-drill.yml.

  terraform/
    modules/{network,compute,database,cache}/{gcp,azure,aws}/
        variables.tf and outputs.tf are BYTE-IDENTICAL across the three clouds;
        only main.tf differs, and CI asserts it. That identity IS the portability
        guarantee.
    modules/dns/cloudflare/          ONE implementation, on purpose.
    modules/storage/cloudflare_r2/   ONE implementation, on purpose.
    envs/prod-gcp/  prod-azure/  prod-aws/
        prod-gcp and prod-azure differ by FOUR `source =` lines. Diff them.
```

## Local development

```bash
make up            # full stack, detached
make logs          # follow
make down          # stop, keep data
make clean         # stop and DROP the data volumes
```

| Service | Port |
|---|---|
| API | 8080 |
| Web | via `make web` (3000) |
| ML | 8000 |
| Postgres (TimescaleDB) | 5432 |
| Redis | 6379 |
| LocalStack S3 | 4566 |
| Gotenberg | 3000 |
| **Mailpit UI** | **8025** |

The api and worker **refuse to boot** unless every platform enabled in
`packages/config/connectors.yaml` has a registered connector implementation. That
fail-fast is deliberate: an audit that silently covers fewer platforms than
configured is worse than one that does not start.

Outbound mail goes to **mailpit** and nothing is delivered. Read what the app sent
at <http://localhost:8025> — the report-ready notification on publish is otherwise
invisible in dev.

Migrations run automatically: the one-shot `migrate` container must exit 0 before
api and worker start, so the schema is never older than the code reading it.

## Production

CI normally does this (`.github/workflows/deploy.yml`). By hand:

```bash
make secrets-edit                  # sops deploy/secrets/prod.enc.env
make prod-deploy VERSION=<sha>
make prod-rollback VERSION=<sha>
make prod-logs
make dr-drill                      # restore the latest backup into a scratch DB
```

**Rolling back is just deploying an older SHA.** The image is still in GHCR; there
is nothing to rebuild.

## Three things to know before touching any of this

1. **Migrations run before the new containers start, and a failure aborts the deploy
   with the old version still serving.** Rollback restores the *image*, never the
   *schema*. Therefore: **every migration must be safe against the previous image.**
   Add columns, don't rename them; ship the code that reads a column one release
   *after* the migration that adds it.

2. **`INFLUAUDIT_CRYPTO__MASTER_KEY` seals OAuth tokens at rest** and cannot be
   rotated without re-encrypting `oauth_connections`. Carry it unchanged across a
   cloud migration — it is the one piece of production state that lives in neither
   Postgres nor object storage.

3. **The web app has no `NEXT_PUBLIC_*`, by design.** Every setting is read at
   runtime on the server, which is what lets one image run on any cloud with no
   rebuild. Adding a build-time public env var silently ends that.
