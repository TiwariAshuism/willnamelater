# InfluAudit — Development Backlog

**Purpose:** a parallel-execution plan. Every task below is a self-contained brief
you can hand to one agent with no extra context. Tasks in the same **wave** touch
disjoint files and can run concurrently; waves are ordered by dependency.

**Last verified:** 2026-07-10, at commit `2c2b261`.

---

## How to run this

1. Do the waves in order. Within a wave, launch one agent per task — they own
   disjoint directories, so they will not collide.
2. **One rule the agents cannot break, ever:** never run `go get` / `go mod tidy`
   / edit `go.mod`/`go.sum`, and never touch `internal/app/**`. The composition
   root and the module graph are wired by a human (or a single dedicated agent)
   *after* a wave lands, because those files are shared and would race.
3. After every wave, a human runs the gate from `services/backend`:
   `gofmt -l ./... && go vet ./... && go test -race ./... && golangci-lint run ./... && go run ./cmd/openapigen -check`
   plus `ruff check` + `pytest` in `services/ml`.
4. Each agent's own Definition of Done is the same gate scoped to its directory.

### The binding rules every agent gets (paste into each brief)

- Read `.cursor/rules/{architecture,backend,review,testing}.mdc` first. They are binding.
- No placeholder/TODO/stub code, no dead code, no unused imports, no commented-out code.
- **No fabricated/mock/seed/sample DATA.** Test fakes implementing an interface are
  fine. API-shape fixtures derived from a public API reference are fine (say so in a
  comment). Invented users/metrics/rows and any `INSERT` of business data are not.
  A generated crypto salt is a key, not data.
- Handlers are thin: bind → call service → `httpx.RenderError(c, err)` → render. A
  wrapped error cause must never reach a response body.
- Do NOT commit any `*_handler_gen.go`. Do NOT run apigen with `-layers handler` or
  `-layers openapi` or `-layers repository`. Generate only `service` interfaces with
  `apigen -input internal/<m>/api/routes.go -output internal/<m> -module <MOD>/internal/<m> -layers service`,
  then hand-write handler + repository + service impl. (`MOD` = `github.com/getnyx/influaudit/backend`.)
- A module must not import another business module. Declare a consumer-side interface
  (a "port") in your own package; the composition root wires it. `internal/platform/*`
  and `internal/connector` are shared contracts and ARE importable.

### Reusable building blocks (do not reinvent — read before writing)

| Need | Use |
|---|---|
| typed errors → HTTP | `internal/platform/errs` (`errs.New/Wrap`, `errs.Kind*`, `errs.Status`) |
| envelope encryption | `internal/platform/crypto` (`Cipher.Seal/Open/Rewrap`, AAD binding) |
| transactions | `internal/platform/db` (`InTx`, `Pool`, `Beginner`) |
| HTTP error render / request-id / recovery | `internal/platform/httpx` |
| config + `Secret` type | `internal/platform/config` |
| connector contract | `internal/connector` (`Connector`, `Snapshot`, `Comment`, `RateLimitError`, `QuotaExhaustedError`) |
| quota (Reserve/Commit/Release) | `billing.Module.Quota()` — wired via a port |
| ingest a Snapshot | `metrics.Module.Ingest(ctx, influencerID, auditJobID, snap)` |
| daily quota ledger | `internal/connector/ratelimit` |
| module facade pattern | `internal/auth/auth.go`, `internal/metrics/metrics.go` |

### Ground truth at last verification

- **Real & wired:** `platform/*`, `connector` core + `youtube` + `ratelimit`,
  `auth`, `oauth`, `influencer`, `billing`, `metrics`, `cmd/{api,worker,migrate,healthcheck,openapigen}`.
  API boots, all 19 routes serve, `routes_test.go` guards spec↔router agreement.
- **Empty (0 files):** `audit`, `scoring`, `ml`, `llm`, `report`, `pdf`, `admin`,
  `alerts`, `bulkaudit`, `whitelabel`, `campaign`, `connector/{meta,csvimport,tiktok,x,linkedin}`.
- **Migrations already exist** for every table these modules own (000007 audit,
  000010 scoring, 000011 llm, 000012 report, 000013 dispute). **Do not add migrations**
  unless a task explicitly says so; the schema is done through 000016.
- **ML service** still runs the pre-research models (`anomaly.py` IsolationForest,
  `pods.py` HDBSCAN). The coordination-first rewrite (Wave 2) replaces them.

---

## WAVE 1 — parallel (3 agents). Foundational modules the orchestrator needs.

These have no dependency on `audit` and can be built now, concurrently. Each owns
one directory tree; none touches `internal/app`.

### 1.1 — `scoring` module  ·  owns `internal/scoring/**`
Build the Influence Score + Authenticity Score engine.
- `api/routes.go` (apigen source): `GET /influencers/:id/score` → latest score;
  `GET /influencers/:id/score/history` → time series.
- The core is a **pure function** `Compute(in ScoringInput) (Score, error)` over
  `(snapshot metrics, ml result, active weights, active benchmarks)` — no I/O, so it
  is the heaviest table-driven test target in the repo. Composite per PRD §6:
  `0.30·reach + 0.30·engagement_quality + 0.25·authenticity + 0.10·consistency + 0.05·content_quality`.
- Weights come from the `scoring_weights` table (000010), keyed `(niche, tier)`,
  versioned, `active` flag. Stamp `weights_version` and `benchmark_version` into every
  `score` row. A new vertical must be an INSERT, never a redeploy.
- Benchmarks from the `benchmark` table. **Cold start:** seed values are *published
  industry reference constants* (engagement-by-tier, CPM), `source='bootstrap'`, wide
  bands, low `n_samples`, and every subscore carries a `confidence` that stays low
  while `n_samples` is small. This is reference data (a tax table), not fabricated
  users — say so in a comment. Provide a corpus-recompute service method (nightly job
  wiring comes later) that writes a `source='corpus'` version once a `(niche,tier)`
  cell reaches `n_samples ≥ 30`.
- `score.contributing_platforms text[]` records which platforms fed the number, so a
  partial audit is never silently understated.
- Expose a `Module` facade + a `Scorer` method the audit orchestrator will call:
  `Score(ctx, auditJobID, influencerID, snapshots, fraud) (Score, error)`.
- **The seed constants MUST be provenance-labelled.** Do not copy the current
  `services/ml` `_ENGAGEMENT_CURVE` values — those are uncited vendor-blog numbers
  (see `product/research/fraud-detection-signals.md` §8). Use ranges you can cite, and
  label them `industry-bootstrap v1` in the report output.
- Tests: table-driven over the pure function (every weight, every benchmark cell,
  partial-platform cases, the low-confidence-at-low-n property).

### 1.2 — `ml` Go client module  ·  owns `internal/ml/**`
The backend's typed client to `services/ml`.
- No `api/routes.go` (it is called by the orchestrator, not over HTTP). A `Module` or
  plain constructor `New(baseURL string, doer Doer) *Client`.
- Methods mirroring the FastAPI contract: `ScoreFraud(ctx, FraudScoreRequest) (FraudScoreResponse, error)`,
  `DetectPods(...)`, `ClassifyComments(...)`. Copy the request/response shapes from
  `services/ml/app/schemas.py` exactly (they are the wire contract).
- Assemble the feature payload from `connector.Snapshot` (follower series, posts,
  and — critically — `Snapshot.Comments` with their `PostID`, so the co-commenter
  graph can be built).
- The HTTP client is an injected `Doer` interface so tests use a fake, never the
  network. On non-2xx, map the ML service's `{code,message}` error envelope onto
  `errs.Kind*`. On a timeout or connection failure, return `errs.KindUnavailable` so
  the orchestrator degrades to a partial audit instead of failing.
- **Coordinate with Wave 2:** the schema this client sends must match the rewritten
  `services/ml/app/schemas.py`. If Wave 2 runs first, mirror the new schema; if
  concurrent, mirror the CURRENT schema and leave a one-line note that
  `post_id`/`text` fields land in Wave 2. State which you did.
- Tests: table-driven over a fake `Doer` — request encoding, error-envelope mapping,
  timeout→KindUnavailable, and that `Snapshot.Comments[].PostID` survives assembly.

### 1.3 — `llm` module  ·  owns `internal/llm/**`
The Claude advisory layer.
- `Provider` interface: `GenerateReport(ctx, ReportInput) (ReportOutput, Usage, error)`.
  (`Chat` is phase 2 — declare it in the interface, but the audit path only needs
  `GenerateReport`.)
- Claude via `anthropic-sdk-go` — **read the `claude-api` skill / the SDK before
  writing a call.** Model `claude-opus-4-8`, adaptive thinking. Structured output via
  `output_config.format` with a JSON schema (`summary`, `weakness_fix_pairs[]`,
  `growth_tips[]`, `brand_fit`) so Go deserializes deterministically — never parse
  prose. Persist the parsed object in `llm_generation.content_jsonb` (000011).
- **Cost control (PRD §11.4 flags this):** the prompt is a stable system prefix
  (rubric + format spec + few-shot) followed by this audit's volatile metrics. Put a
  `cache_control: ephemeral` breakpoint on the last system block so the ~3k-token
  rubric caches across audits. The prefix MUST be byte-identical run to run — no
  timestamps/UUIDs ahead of the breakpoint, or the cache silently misses. Record
  `input_tokens`, `output_tokens`, `cost_micros`, and `cached` on every generation.
- Provider is a swappable seam (a future OpenAI impl drops in) — the `report` module
  depends only on the interface.
- Tests: the provider behind a fake Anthropic client (no network). Assert the cached
  prefix is stable across two calls with different audit data, the structured output
  is parsed into the typed struct, and usage/cost is recorded. Do NOT assert on
  invented LLM text.

---

## WAVE 2 — parallel with Wave 1 (1 agent). The ML rewrite. **Highest-evidence work.**

Owns `services/ml/**` only (Python, no Go). Independent of Wave 1 except the schema
coordination noted in 1.2. Drives `product/research/fraud-detection-signals.md`.

### 2.1 — coordination-first fraud detection
- `app/schemas.py`: add `post_id` to `PostMetrics`; add `text` to `CommentEvent`.
  Without `post_id`, a comment cannot be joined to its post and every per-post
  coordination feature is unrecoverable.
- `app/graph/cocomment.py` (new): co-commenter graph, **edge weight = number of
  shared videos/posts**, not binary co-occurrence (3-0 confirmed, arXiv 2311.05791).
- `app/models/cliques.py` (new, replaces `pods.py`): primary feature = **count of
  maximal cliques of size ≥ 5**; secondary = clique-membership fraction. Reported
  separation is three orders of magnitude (12,241/9,246/782 vs 26/24/20). Normalize by
  channel size and comment volume to blunt the large-fandom false positive.
  - Use `python-igraph` (verified installable; `networkx` is absent and slower).
  - Maximal-clique enumeration is worst-case exponential. GUARD IT: prune low-weight
    edges → k-core reduction → hard node cap → hard time budget, degrading to
    `partial: true` rather than hanging a request. A 50k-commenter channel must not
    stall an audit.
- `app/models/undbot.py` (new, replaces `anomaly.py`): UnDBot's three metrics —
  posting-type distribution, posting influence, `ff = (following+1)/(follower+1)`
  (counts only, no follower list). Weighted as a **tie-breaker, not the headline**:
  structural entropy needs a multi-account graph a single audit does not have. Be
  honest about that in code and output.
- Fix `_ENGAGEMENT_CURVE` in `app/features/engagement.py`: its only corroboration in
  24 sources was a competitor's marketing blog (research §8), and it feeds a
  customer-facing score. Make it a parameter read from the caller (the `benchmark`
  table via the Go `scoring` module), with explicit provenance labelling. Do not keep
  the hardcoded constants.
- Every response keeps: `confidence ∈ [0,1]`, `model_version`, `estimate: true`,
  per-signal contributions.
- `pyproject.toml`: add `python-igraph`; drop `scikit-learn` and `hdbscan`.
- Tests (label-free only): schema conformance, determinism, boundedness, and
  monotonicity — injecting more coordinated structure must not *decrease* the clique
  count (the trick already used in `features/follower.py`). No invented ground truth.

> **Blocker to flag, not to solve in code:** a cross-account commenter graph is
> behavioural data on people who never consented (research §7, cut short). The design
> minimizes exposure (salted HMAC via the existing `crypto_salt`, in-memory graph
> never persisted, retention). Shipping this to production needs a legal sign-off. It
> gates *deploying* the feature, not *building* it.

---

## WAVE 3 — sequential (1 agent, or human). The audit orchestrator. **The core.**

Depends on Wave 1 (scoring, ml, llm) + the already-built `billing.Quota`,
`metrics.Ingest`, and the connector registry. Owns `internal/audit/**`. Because it
consumes many modules through ports, and its wiring lands in `internal/app`, drive it
as one focused unit — not a fan-out.

### 3.1 — `audit` module
- `api/routes.go`: `POST /audits` (submit), `GET /audits/:id` (status + result),
  `GET /audits` (list mine). Tables: `audit_job`, `audit_platform_result` (000007) —
  read the migration; the `audit_status` enum is
  `queued|running|partial|succeeded|failed|canceled` and there is a partial unique
  index on `idempotency_key WHERE status IN ('queued','running')`.
- **Submit path** (one tx): `quota.Reserve(ctx, userID, "audit")` →
  insert `audit_job` with the client `idempotency_key` → enqueue an asynq task with
  `asynq.Unique`. A retried submit is a no-op on both the DB constraint and the queue.
- **Worker** (`audit:run` task handler): `errgroup` fan-out over the influencer's
  connected platforms, each with `context.WithTimeout`, each gated by the rate limiter
  and the `api_quota_ledger`. For each platform, write the `Snapshot` via
  `metrics.Ingest` and one `audit_platform_result` row.
- **Partial failure is first-class:** a `*connector.RateLimitError` or
  `*connector.QuotaExhaustedError` marks that platform `partial`/`error` and continues.
  It does NOT abort the audit. The job ends `partial` if some platforms produced data,
  `succeeded` if all did, `failed` only if none did.
- Then: `scoring.Score(...)` → `ml.ScoreFraud/DetectPods(...)` →
  `llm.GenerateReport(...)` over whatever succeeded. Persist each keyed on
  `audit_job_id`, transactionally, so re-running a step overwrites rather than dupes.
- **Quota lifecycle:** `quota.Commit` on `succeeded` AND `partial` (a partial audit
  delivered value); `quota.Release` only when zero platforms produced data. A failed
  audit must not burn the free tier's single monthly allowance.
- Consume every collaborator through a port declared in `internal/audit`:
  `Quota`, `Ingester`, `Scorer`, `FraudClient`, `Reporter`, `ConnectorRegistry`.
- Register the task handler — the module exposes `RegisterTasks(mux *asynq.ServeMux)`;
  `internal/app/tasks.go` (currently a no-op) calls it. **The human wires this.**
- Tests: the state machine (every transition), the submit-path idempotency, the
  partial-failure semantics (YouTube ok + Instagram rate-limited → `partial`, quota
  committed, `contributing_platforms` names only YouTube), and the quota
  commit/release branches — all over fakes, no live infra.

### 3.2 — composition-root wiring (HUMAN, after 3.1)
- Build `scoring`, `ml`, `llm`, `audit` in `internal/app/app.go`; satisfy the audit
  ports; call `audit.RegisterTasks` in `internal/app/tasks.go`; mount the audit routes.
- Regenerate the spec; `routes_test.go` must stay green.

---

## WAVE 4 — parallel (3 agents). Deliverable + remaining real connectors.

Depends on Wave 3 for end-to-end, but the modules themselves are independent and can
be built concurrently once their upstream interfaces exist.

### 4.1 — `report` + `pdf`  ·  owns `internal/report/**` and `internal/pdf/**`
- `report`: assemble the score + fraud result + LLM narrative into a structured report
  document (table `report`, 000012). `GET /audits/:id/report` → JSON;
  `GET /reports/:slug` → public badge projection (the `public_badge_slug` unique col).
- `pdf`: render the report to PDF via **gotenberg** (already in compose; HTML→PDF over
  HTTP, no headless-Chrome in-process) → object storage (S3-compatible, config already
  has `StorageConfig`). `GET /audits/:id/report.pdf`.
- The PDF must label the fraud estimate AS an estimate and show the benchmark
  provenance ("industry-bootstrap v1"). Tests: report assembly over fakes; the
  gotenberg client behind a fake `Doer`.

### 4.2 — `connector/meta` + `connector/csvimport`  ·  owns those two dirs
- `meta`: implement `connector.Connector` for Instagram over the Graph API — media,
  insights (reach/impressions/saves/shares), comments. Same shape as
  `connector/youtube`; injected `Doer`; `bucketed_calls` rate model. It stays
  `enabled: false` in `connectors.yaml` until Meta app review clears — do not enable it.
- `csvimport`: implement `connector.Connector` from an uploaded Instagram Insights /
  YouTube Studio CSV export. This is the REAL Instagram data path while Meta review is
  pending. Parse the documented export columns; `Source: "csv"` on the Snapshot.
- Register both builders — but that edit is in `internal/app`, so **flag it for the
  human**; do not touch `internal/app` yourself.
- Tests: normalization from documented API/CSV shapes → `Snapshot` (fixtures labelled
  as shape-derived, not captured user data); error classification; partial-on-quota.

### 4.3 — `admin`  ·  owns `internal/admin/**`
- The dispute queue (table `dispute`, 000013) — which is the ML labeling loop. Endpoints
  for filing a dispute against an audit, the admin review queue, and resolving one with
  a label. The API-cost dashboard reads `llm_generation` aggregates. Job-monitor
  endpoints surface asynq queue state.
- A resolved dispute must export its label in a shape `services/ml/training` can later
  consume (design the export, do not build a trainer). Tests over fakes.

---

## WAVE 5 — parallel (2 agents). Scaffolds + the frontend.

### 5.1 — deferred module scaffolds  ·  owns `internal/{alerts,bulkaudit,whitelabel,campaign}` + `connector/{tiktok,x,linkedin}`
Real interfaces + `api/routes.go` where they have HTTP surface, returning
`errs.ErrNotImplemented` at the service boundary. NOT empty dirs — these are honest
scaffolds so the shape exists and enabling one is a small change. No route wiring (the
human decides when to mount). The three connectors implement `connector.Connector` and
return `errs.ErrNotImplemented` from `Fetch` — they need developer apps + review we
don't have, so they cannot be honestly verified against a live API, and must not be
enabled in `connectors.yaml`.

### 5.2 — `apps/web`  ·  owns `apps/web/**` (Next.js 16)
The influencer dashboard. Read `.cursor/rules/nextjs.mdc` (App Router, strict TS,
Server Components, Tailwind v4). This version of Next.js has breaking changes — read
`node_modules/next/dist/docs/` before writing.
- Generate a typed client from `packages/contracts/openapi/influaudit.yaml`
  (openapi-typescript) into `packages/contracts/gen/ts`; the web app consumes it.
- Flows: register/login (session in an HttpOnly, Secure, SameSite cookie set by a
  route handler — the JWT never touches browser JS); connect a YouTube account
  (real Google OAuth); run an audit; view score + trend chart; download the PDF.
- Use the `dataviz` skill for any chart. Component tests (Vitest + Testing Library)
  and route-handler tests for the cookie + OAuth callback. Nothing mocked in the
  data layer — it calls the real backend.

---

## Cross-cutting tasks (any time, low coupling)

- **CI: `contracts` job** — a GitHub Actions job that runs `go run ./cmd/openapigen -check`
  and fails on drift. Owns `.github/workflows/contracts.yml`.
- **Nightly benchmark-corpus job** — wire the `scoring` corpus-recompute method (Wave
  1.1) to a scheduled asynq task. Depends on Wave 1.1 + Wave 3.
- **`services/ml/training`** — the dispute-queue → labels → supervised LightGBM path.
  Depends on Wave 4.3 producing labels. Design only until there is real dispute data;
  do NOT ship a model trained on nothing.
- **Legal sign-off (NOT a code task):** GDPR/DPDP + platform-ToS posture for storing a
  pseudonymized cross-account commenter graph (research §7). Gates *deploying* Wave 2's
  coordination features. Needs a human decision.
- **Re-run the 6 unverified research claims** (research §4): inter-arrival timing,
  comment spikes, near-duplicate text, CollATe's 0.905 TPR. Their verifiers died on a
  session limit. Re-run before building anything on them.

---

## Dependency graph (text)

```
Wave 1 (scoring, ml-client, llm)  ─┐
Wave 2 (ML rewrite)  ──────────────┤   (2 coordinates schema with 1.2)
                                   ▼
Wave 3 (audit orchestrator + app wiring)   ← the core; needs 1 and (ideally) 2
                                   │
                                   ▼
Wave 4 (report+pdf, meta+csv, admin)   ← needs 3 for end-to-end
                                   │
                                   ▼
Wave 5 (scaffolds, apps/web)           ← web needs a stable spec from 1–4
```

**Recommended order if you want a working audit fastest:** Wave 2 first (or alongside
Wave 1), so the orchestrator in Wave 3 calls the ML service you're keeping, not the
one you're replacing. Building Wave 3 against the current ML models means wiring the
`ml` client twice.

---

## Definition of Done for the whole system

The PRD core loop runs against real APIs, nothing mocked:
`docker compose up` healthy → register → connect a real YouTube channel via Google's
consent screen → run an audit → `audit_job.status` advances → `metric_point`/`post`/
`comment_sample` land with real numbers → `author_hash` has no raw channel IDs and is
stable per commenter → `fraud_result` carries confidence + a clique-count signal →
`llm_generation` shows real tokens and `cached=true` on the second audit → PDF renders
with the estimate labelled → exhaust the YouTube quota → audit finishes `partial`,
quota committed → force total failure → quota released, free audit not consumed →
`go test -race ./...`, `golangci-lint run`, `pytest`, `ruff` all green.
