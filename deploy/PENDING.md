# Pending — Deployment

What is **not** done, honestly. Written the day the deployment layer was built, against
a working tree that passes every gate but **has never been deployed**.

Companion to [ARCHITECTURE.md](ARCHITECTURE.md) (how it works) and
[MIGRATION.md](MIGRATION.md) (how to leave a cloud).

---

## What IS done and verified

So the list below is read against a known baseline.

| | Verified how |
|---|---|
| Redis TLS + config guards | 61/61 Go packages, `-race`, `golangci-lint` 0 issues; prod refusal reproduced against the real binary |
| Images build | backend (distroless), ml, web (standalone) all build; web serves; build context 1.4 GB → 30.9 MB |
| Version stamping | `/healthz` returns the git SHA (it returned `dev` before) |
| Email | SMTP client tested against a real socket; publish survives a dead relay |
| Compose | dev + prod both `config --quiet` clean; dev stack boots, `/readyz` 200 |
| Terraform | `fmt` clean; **`validate` passes on prod-gcp, prod-azure, prod-aws** (run via Docker) |
| Portability contract | all 8 `variables.tf`/`outputs.tf` byte-identical across 3 clouds |
| Observability configs | validated against **pinned** binaries: caddy 2.9, tempo 2.7.2, loki 3.3.2, promtail 3.3.2, prometheus v3.1.0, otelcol 0.117.0 |
| Secrets guard | `secrets-check.sh` catches plaintext, and partial-plaintext, and redacts what it finds |

---

## P0 — Blocks the first deploy. Only you can do these.

- [ ] **Generate two age keypairs** (`age-keygen`). One human (`ops`, held offline), one CI.
      Put the **public** halves in `deploy/.sops.yaml` — it currently holds **placeholders that do not work**.
- [ ] **Create `deploy/secrets/prod.enc.env`** from `prod.enc.env.example`, fill it, then
      `sops --encrypt --in-place`. **Run `make secrets-check` before committing** — the file
      is committed on purpose, and committing it unencrypted publishes your credentials.
- [ ] **`make hooks`** — installs the pre-commit guard for the above.
- [ ] **Cloudflare**: account, zone, R2 buckets (`influaudit-reports`, `influaudit-backups`,
      `influaudit-tfstate`), API token.
- [ ] **Fill the three `terraform.tfvars`** from the `.example` files (project/subscription
      ids, Cloudflare ids, SSH public key).
- [ ] **Set the R2 account id** in `envs/*/backend.tf` — currently the literal
      `REPLACE_ACCOUNT_ID`.
- [ ] **GitHub**: a `production` Environment with a required reviewer, and the secrets
      `SSH_PRIVATE_KEY`, `SSH_HOST`, `SSH_USER`, `SSH_KNOWN_HOSTS`, `SOPS_AGE_KEY`,
      `STORAGE_ENDPOINT`, `STORAGE_ACCESS_KEY`, `STORAGE_SECRET_KEY`, `BACKUP_AGE_KEY`.
- [ ] **An SMTP provider.** Postmark or Resend. **Not SES** — it needs an AWS IAM identity,
      which is exactly the small coupling that turns a two-day migration into a two-week one.

## P1 — Written but never executed. Assume something is wrong.

Every one of these is code I wrote and could not run. Treat the first deploy as a test of
them, not as a deploy.

- [ ] **`deploy.sh` end to end.** cosign verify → pull → migrate → up → smoke → rollback.
      Most likely to bite: the cosign `--certificate-identity-regexp` must match your actual
      org/repo path.
- [ ] **SOPS decrypt on the VM** (`/run/influaudit/.env`, tmpfs, 0600).
- [ ] **GHCR pull from the VM** — needs a read-only PAT in the VM's docker config.
- [ ] **Caddy ACME issuance.** Requires DNS already pointing at the VM and port 80 reachable.
      **The Cloudflare records must stay unproxied (grey cloud)** or HTTP-01 fails silently
      and you find out ~60 days later at renewal.
- [ ] **`ssh-entrypoint.sh` forced command.** Confirm a leaked key genuinely cannot get a shell.
- [ ] **The backup timer.** `systemctl list-timers influaudit-backup.timer`, then
      `make backup-now`, then confirm an object actually lands in R2.
- [ ] **`dr-drill.yml`.** Run it manually once. **If the restore fails you have no proven
      path back and no business cutting anything over.**
- [ ] **Grafana provisioning** — datasources resolve, the dashboard appears, an alert fires
      and an email arrives.

### Deliberately prove the failure paths

Not optional. A rollback that has never rolled back is a rumour.

- [ ] Deploy a **knowingly broken SHA** to staging and confirm `deploy.sh` rolls itself back.
- [ ] Kill `promtail` and confirm the `logs-stopped` alert fires. (**An empty log panel reads
      as "no errors" rather than "no logs"** — that alert is the only thing that contradicts it.)
- [ ] Fill the disk and confirm the `disk-filling` alert fires. It is the most common way a
      single-VM deployment actually dies.

## P2 — Real gaps, not blocking

- [ ] **`envs/prod-aws/` is validated but never planned** against a real AWS account. Its
      network module comma-joins subnet ids into the contract's single `network_id` string;
      that translation is the least-exercised code in the Terraform.
- [ ] **No staging environment.** `envs/staging-gcp/` does not exist. It should, and it should
      be where every one of the P1 items is tested. **This is the highest-value P2 item.**
- [ ] **HTTP metric names in the dashboard and alerts are assumed**, not confirmed. They follow
      OTel semconv (`http_server_request_duration_*`), but the exact names depend on the Go
      instrumentation. Check them against `/metrics` on the first deploy — a dashboard querying
      a metric that does not exist is a blank panel, which looks like zero traffic.
- [ ] **No log→trace correlation proven.** The Loki datasource regexes `trace_id` out of the log
      line; that depends on the app's slog handler actually emitting it.
- [ ] **`sync-artifacts.sh` is unexercised** — no champion model has ever been promoted, so
      nothing has ever been synced.

## P3 — Known, deliberately deferred

- [ ] **asynq leaks its types** through `internal/admin/port/port.go` (a public port package
      importing `hibiken/asynq` and returning `*asynq.QueueInfo`). **Deliberately not fixed:**
      asynq is a Redis library, and Redis is managed on every cloud, so this costs nothing on a
      migration. Fix it when you have a reason that is not this document.
- [ ] **`backend.yml` and `ml.yml` duplicate jobs already in `ci.yml`.** Pre-existing, harmless,
      wasteful. Delete them when someone is annoyed enough.
- [ ] **No rate limiting / WAF.** Caddy can do both when you need them.
- [ ] **No IP allowlist on `/v1/admin/*`.** Worth a `@admin` matcher in the Caddyfile before you
      have real customers.
- [ ] **Master key rotation has no story.** `INFLUAUDIT_CRYPTO__MASTER_KEY` seals OAuth tokens at
      rest; rotating it requires re-encrypting `oauth_connections`. It needs a proper migration,
      and it is its own project. **Until then it must be carried across a cloud migration
      unchanged** — see MIGRATION.md.

---

## The two things most likely to hurt you

**1. A backup you have not restored is a rumour.** `backup.sh` writing a file to R2 every night
proves that `backup.sh` runs. It proves *nothing* about whether that file can be turned back into
a database. Run `dr-drill.yml` before you trust any of this.

**2. The monitoring tier is the worst place to accept a surprise.** Every third-party image is
pinned, because validating the configs caught that current `grafana/tempo:latest` had dropped the
`ingester` and `compactor` config keys — on `:latest` that appears as the monitoring stack
refusing to start, at deploy time, with no change to this repository. When you bump one of those
pins, **re-run the config validation**, which is the only reason we found it.
