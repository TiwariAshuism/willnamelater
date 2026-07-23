# Deployment Architecture

How InfluAudit runs in production, and why it can leave any cloud in an afternoon.

---

## The one idea

> **Every cloud-specific thing is either (a) behind a Terraform module with an interface identical across clouds, or (b) not on the cloud at all.**

Everything below is an application of that sentence.

## What was already true

The application was cloud-agnostic before any of this was written, and that is not an accident of luck — it is the thing that made a portable deployment cheap instead of a rewrite:

| Concern | How it stays portable |
|---|---|
| **Cloud SDKs** | There are none. Not `cloud.google.com/*`, not `aws-sdk-go`, not `Azure/*`, not `boto3` — in Go, Python, or the web app. |
| **Object storage** | `internal/platform/storage` speaks the **S3 API** over hand-rolled SigV4 (stdlib only). Behind a consumer-owned port (`report/port.Storage`). |
| **Secrets** | The app asks for secrets **by name** (`SecretLookup func(name) string`) and never learns where they came from. |
| **Config** | koanf: `INFLUAUDIT_*` env vars, `__` for nesting. No cloud metadata service, no cloud parameter store. |
| **Database** | Plain Postgres. Migration `000008` **falls back from a TimescaleDB hypertable to native declarative partitioning when the extension is absent** — which is why every managed Postgres works with zero code change. |
| **Telemetry** | OTLP, and only OTLP. The app has never heard of Prometheus, Grafana, or CloudWatch. |
| **Web app** | **No `NEXT_PUBLIC_*` anywhere, by design.** Every setting is read at runtime on the server. One image runs on any cloud with no rebuild — the same property the Go binaries have. **Do not break this.** |

The one thing that was *not* portable: `internal/platform/redis` had no TLS support, and **every managed Redis is TLS-only**. That was fixed first, because nothing else in this document worked without it.

---

## The shape

```
        ┌── Cloudflare ────────────────┐   ┌── GitHub ──────────┐
        │  DNS  ·  R2 (object storage) │   │  GHCR (images)     │   NOT ON THE
        │  Terraform state             │   │  Actions (CI/CD)   │   COMPUTE CLOUD.
        │  R2 also holds the backups   │   │                    │   NEVER MIGRATES.
        └──────────────┬───────────────┘   └─────────┬──────────┘
                       │                             │ docker pull
   Internet ──► ┌──────▼─────────────────────────────▼──────────────────┐
                │  ONE VM  (GCP today · Azure tomorrow · AWS after)     │
                │  ┌────────────────────────────────────────────────┐  │
                │  │ Caddy :443  — auto-TLS, the only public port    │  │
                │  │   api.influaudit.com  → api  :8080  (Go)       │  │
                │  │   app.influaudit.com  → web  :3000  (Next.js)  │  │
                │  ├────────────────────────────────────────────────┤  │
                │  │ internal: worker · ml · gotenberg              │  │
                │  │           otel-collector → prom · loki · tempo │  │
                │  │                          → grafana             │  │
                │  └────────────────────────────────────────────────┘  │
                └──────────────────────┬───────────────────────────────┘
                                       │ private network, TLS
                        ┌──────────────▼──────────────┐
                        │  MANAGED Postgres           │  ← the only cloud services
                        │  MANAGED Redis              │     we actually consume
                        └─────────────────────────────┘
```

Inside the VM: `docker compose`. Outside: Terraform. That is the whole system.

---

## The five things that do not move

This is the list that makes a migration short. Each of these lives deliberately **off** the compute cloud:

1. **DNS → Cloudflare.** The cutover *is* a DNS change. Putting the zone in Cloud DNS would mean re-pointing the nameservers of the domain you are actively serving from, during the window you can least afford a mistake.
2. **Object storage → Cloudflare R2.** S3-compatible, so the existing SigV4 client works unchanged. Because it is on none of the three clouds, **storage is not a migration step at all** — no data to copy, no share URLs to re-mint. (Azure Blob is the one major store with no S3 API. That is Azure's problem, and it stays Azure's problem.)
3. **Container registry → GHCR.** No cloud credentials to push, pullable from any VM anywhere. A registry on the compute cloud means a migration begins with a full image re-push and a credential-helper rewrite.
4. **Terraform state → R2.** If the state describing your GCP infrastructure lives in GCS, then leaving GCP has a bootstrap dependency on GCP.
5. **Observability → self-hosted, dashboards in git.** CloudWatch/Azure Monitor/Cloud Monitoring are one checkbox to adopt and a rewrite to leave — and the bill comes due exactly when you are mid-incident.

The app speaks **only OTLP**, so switching to Grafana Cloud or Honeycomb later is an exporter block in `otel-collector.yaml`, not a code change.

### The observability stack, and what nearly broke it

```
app ──OTLP───► otel-collector ──► prometheus  (metrics)
                               └► tempo       (traces)
app ──stdout─► docker ──► promtail ──► loki   (logs)
                                          └──► grafana ──► SMTP alerts
```

**`promtail` is not optional.** Without it Loki runs, the datasource resolves, every dashboard is green — and there are no logs in any of them. An empty log panel reads as *"no errors"* rather than *"no logs"*. One of the four alert rules (`logs-stopped`) exists purely to contradict that.

**Every third-party image is pinned to an exact version.** They were briefly on `:latest`, and validating the configs against the real binaries caught what that costs: the then-current `grafana/tempo:latest` had **dropped** the top-level `ingester` and `compactor` config keys, so `tempo.yml` — valid on 2.7.2 — fails to parse on it. On `:latest` that surfaces as **the monitoring tier refusing to start, at deploy time, with no change to this repository.** The tier you rely on to tell you what is broken is the worst possible place to accept an unannounced upgrade. Bump deliberately, and re-run the config validation.

---

## The portability contract (Terraform)

`modules/{network,compute,database,cache}/{gcp,azure,aws}/variables.tf` and `outputs.tf` are **byte-identical across all three clouds**. Only `main.tf` differs. `.github/workflows/terraform.yml` asserts this on every PR, so it cannot rot silently.

The crux is that the env stack names **t-shirt sizes, never machine types**:

```hcl
module "database" {
  source = "../../modules/database/gcp"   # ← the ONLY line that changes
  instance_size         = "medium"        # not "db-custom-2-7680"
  storage_gb            = 100
  backup_retention_days = 14
  high_availability     = true
}
```

Each cloud's `main.tf` owns a `locals.sizes` lookup that translates `"medium"` into its own vocabulary. So:

> **`envs/prod-gcp/main.tf` and `envs/prod-azure/main.tf` differ by four `source =` lines, a provider block, and Azure's mandatory resource group. Nothing else. Not one module input.**

Diff them. That diff is the architecture.

---

## Why a VM and not Kubernetes

At one node, Kubernetes is a ~$150–300/mo control-plane floor plus cluster ops, to solve problems this system does not have. Docker Compose on a VM is also, incidentally, **the most portable target that exists**: every cloud rents Linux VMs identically.

The VM is **cattle**. It holds no state — Postgres and Redis are managed, object storage is off-cloud, images come from GHCR. `bootstrap-vm.sh` configures it and `deploy.sh` fills it. If it dies, you make another one in fifteen minutes. That property is the whole reason the RTO is short, and it is worth protecting.

### When to graduate to Kubernetes

Graduate when **any** of these is true — not before:

- You need more than one node (HA, or you have outgrown the biggest sensible VM).
- You need **zero-downtime rolling deploys**. Today a deploy is a brief container restart behind Caddy.
- You need **per-service autoscaling** — the ML service and the API scaling independently.

**What changes:** `docker-compose.prod.yml` → a Helm chart or Kustomize overlay. Caddy → ingress-nginx + cert-manager. The SOPS env file → External Secrets Operator (or keep SOPS via `helm-secrets`). The SSH deploy → ArgoCD or `kubectl apply`.

**What does NOT change:** the images, the ports, the config contract, the Terraform module interfaces, or **one line of business logic**.

That last sentence is the test of whether this architecture is right. It passes.

---

## Scalability path

| Stage | Shape | What changes |
|---|---|---|
| 1 (**today**) | One VM + compose, managed PG/Redis | — |
| 2 | Bigger VM | `vm_size = "large"`. One word. |
| 3 | 2+ VMs behind a cloud LB | Add an `lb` module; Caddy stops doing TLS. `worker` scales separately from `api`. |
| 4 | Kubernetes | See above. The images and config do not change. |
| 5 | Autoscaling | HPA on the api and worker deployments. |
| 6 | Multi-region | The DB is the hard part, not the compute: read replicas, then a decision about writes. |
| 7 | Multi-cloud | Already possible — the module contract means both stacks can exist at once. Whether it is *wise* is a different question. |

The costly transition is 3 → 4. Everything before it is a variable change.

---

## Deploy, and why the order matters

```
verify signatures → pull → record last-good → MIGRATE → up --wait → smoke test → rollback on failure
```

Two decisions in there are load-bearing:

**Migrations run as an explicit, blocking, foreground step *before* the new containers start**, and a non-zero exit aborts the deploy with the old version still serving. They are deliberately *not* a compose `depends_on: service_completed_successfully` the way they are in dev — that re-runs them on every `up`, and on a **rollback** it would apply the new schema under the old image.

**Rollback does not revert migrations.** Auto-reverting DDL under load is how an outage becomes data loss. The contract that makes forward-only rollback safe lives on the migration, not the script:

> ### Every migration must be safe against the previous image.

Add columns, don't rename. Add tables, don't drop. New columns nullable or defaulted. Ship the code that *reads* a column one release *after* the migration that adds it. Then the old image can always run against the new schema, and rolling back the code is enough. If a *migration* broke production, that is a fix-forward situation and no script can save you.

## Secrets

SOPS + age. The encrypted file is **committed to git** with values encrypted and keys in plaintext — so a diff shows *which* secret rotated without revealing it. Zero cloud services, zero new runtime dependencies, reviewable in PRs, survives total VM loss, and **moves clouds by doing nothing at all**.

Rejected: a fetch-at-boot `SecretsProvider` with cloud adapters. It would put a cloud SDK into a codebase whose defining property is having none, it makes the migration *harder* (the app must now learn a new secret backend per cloud), and it is the only option that cannot boot when the secret service is down. The seam already exists (`SecretLookup`) if that day ever comes — **don't pre-build it.**

> ⚠️ **`INFLUAUDIT_CRYPTO__MASTER_KEY` seals OAuth tokens at rest.** It cannot be rotated without re-encrypting `oauth_connections`, and it is the one piece of production state that lives in neither Postgres nor object storage. **Carry it unchanged across a cloud migration.** See [MIGRATION.md](MIGRATION.md).

## Backups: two layers, and only one of them matters

**Layer 1 — the managed database's automated backups + PITR.** Excellent RPO. **Useless for leaving the cloud**: a Cloud SQL backup cannot be restored into Azure. This is the layer that makes you feel safe and keeps you trapped.

**Layer 2 — `pg_dump --format=custom` → R2, age-encrypted, nightly.** Restores into **any** Postgres 16 on **any** cloud. And because it lands in R2, on migration day the backups are *already* where the new cloud can reach them. This is the layer that lets you leave.

`dr-drill.yml` restores the latest dump into a scratch Postgres **monthly** and asserts row counts. *A backup you have not restored is a rumour.*

**Redis is deliberately not backed up.** It holds the asynq queue (re-enqueueable) and the cache (derived from Postgres). Backing it up would protect nothing.

| | Target | Achieved by |
|---|---|---|
| RPO, normal | ≤ 5 min | managed PITR |
| RPO, cloud loss | ≤ 24 h | nightly `pg_dump` → R2 |
| RTO, VM loss | ≤ 15 min | `terraform apply` + `deploy.sh` at the last-good SHA. **The VM holds no state.** |
| RTO, cloud loss | ≤ 4 h | [MIGRATION.md](MIGRATION.md) |

## The vendor lock-in checklist

What we did **not** do, and what we did instead:

| ❌ The trap | ✅ Instead |
|---|---|
| A cloud SDK in application code | Ports + adapters. Zero cloud SDKs, still. |
| A proprietary datastore (DynamoDB, Firestore, Cosmos) | Plain Postgres. |
| **TimescaleDB as a hard requirement** | Migration 000008 falls back to native partitioning. |
| Cloud logging/monitoring APIs | OTLP → a collector we own. |
| Cloud IAM for app auth | RS256 JWT, signed with our own key. |
| Cloud-native object storage SDK | The S3 API, and a store that is on no cloud. |
| ECR/GCR/ACR | GHCR. |
| Cloud KMS for the master key | An age-encrypted env var. |
| **SES for mail** | SMTP. Every provider speaks it; switching is four env vars. |
| State in a cloud bucket | State in R2 — no bootstrap dependency on the cloud you are leaving. |

## Cost

Roughly, at MVP: VM ~$25–50 · managed Postgres ~$50–90 · managed Redis ~$15–40 · R2 ~$1 + **$0 egress** · GHCR $0 · Cloudflare DNS $0 · observability $0 (self-hosted).

**≈ $100–180/mo.** Kubernetes would add a control-plane floor and cluster ops for a workload that fits comfortably on one box. Don't.
