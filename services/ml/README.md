# InfluAudit ML Service

FastAPI service for the authenticity/fraud pillar of InfluAudit: fraud scoring,
engagement-pod detection, and comment-quality classification. It is called by
the Go `internal/ml` module.

## The central constraint: cold start, no labels

**There is no labeled training data yet.** We do not know which accounts are
genuinely fraudulent, so we cannot train — and have not trained — a supervised
model. Everything this service returns is an **estimate**, and every response
says so explicitly.

Concretely, that honesty shows up in three ways on every scoring response:

- `estimate: true` — always, while the service is in its cold-start state.
- `confidence` in `[0, 1]` — driven by how much data the request supplied, and
  **capped** (0.65 for fraud, 0.6 for pods/comments) because unsupervised output
  is never fully trusted.
- `model_version` — reported as `heuristic` until a trained artifact exists.
- Per-signal `contributions` — the score is never an opaque number; each signal
  and its weighted contribution is returned so a fake-follower percentage is
  never presented as fact.

## How each capability works with no labels

- **Fraud scoring** (`POST /v1/fraud/score`) combines a per-call
  `IsolationForest` — fitted on *the request's own* follower-growth series, then
  discarded — with four explicit heuristics: follower/following balance,
  growth-spike sharpness, engagement rate vs. a published size-adjusted
  benchmark curve, and like-to-comment ratio. Nothing is loaded from a
  pretrained artifact.
- **Pod detection** (`POST /v1/pods/detect`) runs `HDBSCAN` over a commenter
  co-occurrence matrix built entirely from the request payload. Dense groups of
  accounts that repeatedly comment together become candidate pods; everyone else
  is left as noise.
- **Comment classification** (`POST /v1/comments/classify`) applies transparent
  rules (emoji-only, duplicated text, generic filler) — a stopgap until the LLM
  classifier and labels exist.

## The path to a supervised model

The **dispute queue is the labeling loop**. When an influencer or admin disputes
an audit (PRD §5.8), that resolution becomes a label. Once enough labels
accumulate, a supervised model (a LightGBM fraud classifier) will be trained and
dropped into the artifact directory with a `manifest.json`. `app/registry` is
already built to detect that artifact and switch `model_version` off `heuristic`
— but **no placeholder model ships today**, because a fake artifact would let the
rest of the system trust output that was never trained.

## Endpoints

| Method | Path                    | Purpose                                |
|--------|-------------------------|----------------------------------------|
| POST   | `/v1/fraud/score`       | Account authenticity risk estimate     |
| POST   | `/v1/pods/detect`       | Engagement-pod clusters                 |
| POST   | `/v1/comments/classify` | Comment-quality buckets                 |
| GET    | `/healthz`              | Liveness + active model version         |

## Layout

```
app/
  schemas.py        # pydantic v2 wire contract with the Go internal/ml module
  features/         # pure signal extraction (follower, engagement, comments)
  models/           # per-call estimators + heuristic composite (anomaly, pods, heuristics)
  registry/         # versioned model loading; reports 'heuristic' in cold start
  api/              # thin FastAPI routers
  main.py           # app factory
tests/              # schema conformance, determinism, boundedness, monotonicity
```

## Development

```bash
python -m pip install -e ".[dev]"
python -m ruff check app tests
python -m pytest -q
uvicorn app.main:app --reload
```

## Testing philosophy

Tests never assert a fabricated fraud verdict. They assert properties that are
true without labels: response-schema conformance, determinism for identical
input, boundedness (score in `[0, 100]`, confidence in `[0, 1]`), and
**monotonicity** — e.g. a sharper injected follower spike must never *lower* the
risk score. Pod tests assert the structural recoverability of an engineered
co-occurring clique, not that any real account is fraudulent.
