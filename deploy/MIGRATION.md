# Cloud Migration Runbook

GCP → Azure. Then Azure → AWS is the same runbook with `azure` swapped for `aws`, and it will be faster because you will have done it once.

---

## Why this document is short

**Five things do not move at all:**

| | Where it lives | Why it doesn't move |
|---|---|---|
| DNS | Cloudflare | The cutover *is* a DNS change. It must not be on the cloud being left. |
| Object storage | Cloudflare R2 | S3-compatible, on no cloud. **Not a migration step.** |
| Container registry | GHCR | Pullable from any VM anywhere. |
| Terraform state | R2 | State on GCS would make leaving GCP depend on GCP. |
| Backups | R2 | Already portable (`pg_dump --format=custom`), already where the new cloud can reach them. |

**What actually moves:** a VM, a Postgres, a Redis. That is all.

**Downtime budget:** 30–60 minutes of write downtime. RTO budget 4 hours.

---

## T-7 — Stand up Azure alongside GCP

Both clouds run in parallel for a week. Budget one week of duplicate infrastructure; it is the cheapest insurance you will ever buy.

**1. Create the Azure stack.**
```bash
cd deploy/terraform/envs/prod-azure
terraform init
terraform plan     # already validated on every PR by terraform.yml — see below
terraform apply
```
`envs/prod-azure/` already exists and is **validated in CI on every pull request**, even though nothing deploys it. A migration target you have never planned is not a target, it is a hope.

Diff it against `prod-gcp/main.tf` and confirm what you are about to rely on:

```bash
diff deploy/terraform/envs/prod-{gcp,azure}/main.tf
```

Four `source =` lines, a provider block, and Azure's mandatory resource group. **Not one module input changes** — `instance_size = "medium"` means medium on both.

**2. Bootstrap the VM.**
```bash
scp deploy/scripts/bootstrap-vm.sh azure-vm:/tmp/
ssh azure-vm 'sudo bash /tmp/bootstrap-vm.sh'
# then: install the CI deploy key with its forced command, place .age.key, clone the repo
```

**3. Deploy the current version to Azure.** It comes up fully, pointed at the *Azure* database, and serves nothing — no DNS points at it yet.
```bash
ssh azure-vm '/opt/influaudit/deploy/scripts/deploy.sh <current-sha>'
```

**4. ⭐ Restore production data into the Azure database — the step that decides everything.**
```bash
BACKUP_AGE_KEY_FILE=~/.age.key \
  deploy/scripts/restore.sh "postgres://...azure...?sslmode=require"
```
Then run migrations against it. **The schema is already current, so this is a no-op — and that no-op is itself the assertion.**

> **This is the step where a non-portable architecture dies.** It survives here for one specific reason: migration `000008_metric_point.up.sql` checks `pg_available_extensions` for `timescaledb` and, when it is absent, builds `metric_point` as a **natively partitioned table** instead of a hypertable. Azure Flexible Server *does* offer TimescaleDB — and the module deliberately does **not** enable it, because depending on it would re-introduce the lock-in for nothing the product uses.

**5. Smoke-test Azure directly, before any DNS exists:**
```bash
curl -H 'Host: api.influaudit.com' http://<azure-ip>/readyz
```

**6. Restore the `caddy-data` volume** from the backup so the new host already holds valid certificates. No ACME round-trip at the moment of cutover.

**7. If the database exceeds ~20 GB, switch to logical replication now**, not a dump at cutover:
```sql
-- on Cloud SQL
CREATE PUBLICATION influaudit FOR ALL TABLES;
-- on Azure
CREATE SUBSCRIPTION influaudit CONNECTION '...' PUBLICATION influaudit;
```
Let it catch up across the whole T-7 week. The cutover then becomes a seconds-long event instead of a dump/restore window.

---

## T-1

- [ ] Drop the Cloudflare TTL on `api.` and `app.` to **60s** (`dns_ttl = 60`, `terraform apply`).
- [ ] Freeze deploys.
- [ ] Announce the maintenance window.
- [ ] Run `dr-drill.yml` once manually. If the restore fails, **stop** — you have no proven path back and no business cutting over.

---

## T-0 — The cutover

**1. Stop the workers first.**
```bash
ssh gcp-vm 'docker compose -f /opt/influaudit/deploy/docker-compose.prod.yml stop worker'
```
In-flight audits drain. Nothing new is consumed. asynq leaves unstarted tasks in Redis.

**2. Stop the API.** This begins the write downtime.
```bash
ssh gcp-vm 'docker compose ... stop api web'
```
There is no read-only mode. Stopping it is the honest thirty minutes.

**3. Final delta dump → restore.** Minutes, on a small database.
```bash
deploy/scripts/backup.sh                      # final dump, straight to R2
BACKUP_AGE_KEY_FILE=~/.age.key deploy/scripts/restore.sh "postgres://...azure..."
```
*(If you set up logical replication at T-7: wait for zero lag, then `DROP SUBSCRIPTION`. Seconds, not minutes.)*

**4. Redis: do nothing. Deliberately.**

The queue was drained by step 1 and is re-enqueueable regardless. The cache is derived from Postgres and re-warms on its own. The only scheduled task is the nightly corpus recompute, which is **idempotent** and runs again tomorrow.

> This paragraph exists so that nobody spends a day of the migration building a Redis migration that protects nothing.

**5. Flip DNS.**
```bash
cd deploy/terraform/envs/prod-azure && terraform apply   # target_ip = the Azure VM
```
**This is the cutover.** One A record. 60-second TTL.

**6. Start Azure.** It is already `up` from T-7 step 3; Caddy already holds certificates from step 6.

**7. Verify.**
```bash
curl -fsS https://api.influaudit.com/readyz
curl -fsS https://api.influaudit.com/healthz     # confirm the version
```
Watch Grafana — which is on the *new* VM, and whose dashboards came from git, so they are already there. Watch the GCP VM's Caddy log go to zero.

---

## T+0 → T+7

**Do not delete GCP.** Leave it stopped with the database retained. **That is your rollback: flip DNS back.**

### ⚠️ The one that will bite someone

`INFLUAUDIT_CRYPTO__MASTER_KEY` seals every OAuth token at rest (`internal/platform/crypto/envelope.go`, AES-256-GCM envelope). You **cannot** rotate it without re-encrypting the `oauth_connections` table — every creator's Instagram and YouTube connection would break at once.

**Carry the same master key across the migration, unchanged.** It lives in SOPS, so it moves with the repository and requires no action. Rotate it *later*, as its own project, with a proper re-encryption migration.

It is the only piece of production state that lives in neither Postgres nor object storage. **Write it on the whiteboard.**

### Everything else, rotate at T+7
Database password, Redis password, R2 keys, SSH deploy key. `make secrets-edit`, then redeploy.

### T+7
```bash
cd deploy/terraform/envs/prod-gcp && terraform destroy
```

---

## Azure → AWS

The same runbook. `envs/prod-aws/` needs its `main.tf` written — copy `prod-azure/main.tf`, change the four `source =` lines to `../../modules/*/aws`, swap the provider block, and drop the resource group (only Azure needs one). The module contract does the rest.

One AWS-specific wrinkle, already handled: the network module comma-joins its private subnet ids into the contract's single `network_id` string, and the database and cache modules split it back apart. That ugliness is confined to the one cloud that needs it — which is the entire design principle, applied.

---

## Rollback

At any point before T+7:

```bash
cd deploy/terraform/envs/prod-gcp && terraform apply   # target_ip back to the GCP VM
ssh gcp-vm 'docker compose ... up -d'
```

DNS propagates in 60 seconds. This works because you did not delete anything.

**The only irreversible step is the final data cutover.** Writes that landed on Azure after step 5 are not on GCP. If you must roll back *after* traffic has been served, dump Azure and restore to GCP — which is the same `backup.sh` / `restore.sh` pair, in the other direction, and works for exactly the same reason.

---

## Near-zero-downtime cutover (the upgrade path)

The runbook above is the **honest-30-minutes** version: stop the API, dump, restore, flip
DNS. It is correct, simple, and the *right* choice pre-launch and at low traffic — do not
build anything more until live traffic makes 30 minutes of write-downtime unacceptable.

When it does, three changes turn the cutover into a **seconds-long write pause with reads
never dropping**. Two are already in this runbook; one is code that does not exist yet.

**What changes vs the standard cutover:**

- **Data lag → 0** — logical replication from T-7 (already documented at T-7 step 7). The
  target is continuously current, so there is no dump window to be down for. Set
  `publish_via_partition_root = true` so the partitioned `metric_point` replicates through
  its root.
- **Write pause → seconds** — an app-level **read-only mode**, the one missing piece
  (§T-0 step 2 notes "there is no read-only mode"). It must serve reads and reject/park
  writes on a flag. The synchronous write surface to freeze is small because audits are
  async (asynq drains and re-enqueues).
- **Reroute → instant** — put Cloudflare in **proxied mode** (orange-cloud) so the origin
  swap takes effect at the edge with no 60-second client DNS tail. (Alternative: keep the
  old origin reverse-proxying to the new one for the TTL drain.)

**The cutover then becomes:**

1. **Confirm replication lag ≈ 0.** `SELECT * FROM pg_stat_replication` on the source; the
   target has been catching up all week.
2. **Stop the workers.** Queue drains; unstarted asynq tasks remain, re-enqueueable.
3. **Flip the app to read-only.** Reads keep serving; writes are rejected/parked. This is
   the *only* user-visible degradation, and it lasts seconds.
4. **Drain the final WAL, then promote.** Wait for lag = 0, `DROP SUBSCRIPTION` on the
   target. It is now authoritative and read-write. *(No sequence resync — every PK is
   `gen_random_uuid()`, so there are no sequences to advance.)*
5. **Swap the Cloudflare origin to the target.** In proxied mode this is instant at the
   edge — no DNS-cache tail.
6. **Lift read-only; start the workers.** Writes resume against the new primary.
7. **Keep the old cloud read-only and reverse-replicate** (new → old) for the observation
   window, so even a *post*-cutover rollback loses nothing. This is what makes the "only
   irreversible step" above reversible too.

**Result: reads never stop; writes pause only for steps 3–6 — a few seconds.** That is what
zero-downtime honestly means at this scale: a planned, seconds-long write freeze in a
low-traffic window, not literally zero.

> ⚠️ Write the read-only-mode code against *real* traffic patterns, not guessed ones.
> Building this pre-launch is effort spent on a load you cannot observe yet.
