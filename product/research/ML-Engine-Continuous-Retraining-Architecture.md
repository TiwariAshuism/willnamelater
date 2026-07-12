# ML Engine Architecture — Continuous Learning from OAuth Users
## InfluAudit / Instagram Verified Path

---

## 0. The Important Correction First

"Retrain with every user" as literal **online learning** (model updates on every single new data point in real time) is the wrong instinct for a paid product where trust = revenue. Here's why:

- A single OAuth-connected account with gamed/bought engagement (bot-inflated but "verified" numbers) would immediately poison a truly-online model.
- No validation gate = no way to catch a retrain that makes the model *worse* before it's already serving live scores to paying brands.
- You lose reproducibility — you can't say "this score was computed by model v42" if the model is a constantly-shifting target.

**What "crazy strong" actually looks like for a paid product isn't the fastest possible retrain loop — it's a retrain loop with airtight validation, versioning, and rollback, running frequently.** That's the design below.

---

## 1. Algorithm Choice — Per Model

Your data is fundamentally **tabular** (follower counts, engagement rates, ratios, categorical niche) at low-to-mid volume for a while. This is exactly the regime where **gradient-boosted trees beat deep learning** — don't reach for neural nets until you have serious volume (50K+ labeled rows). Here's the breakdown:

| Model | Algorithm | Why |
|---|---|---|
| **Reach/Impressions Estimator** | **LightGBM, quantile regression** | Fast, handles missing/sparse features well, quantile mode gives you a range (P10/P50/P90) instead of a fake-precise number — critical for a trustworthy paid report |
| **Fake Follower Probability** | **XGBoost classifier + Isolation Forest ensemble** | XGBoost for the primary signal-based classification; Isolation Forest catches *novel* anomaly patterns the classifier hasn't seen labeled examples of yet — stacking the two catches both known and emerging fraud patterns |
| **Valuation Regressor** | **LightGBM, quantile regression**, calibrated on reported deal outcomes | Same reasoning as reach — always show a range, and this model gets a unique feedback loop (see §4) no competitor easily replicates |
| **Demographics Estimator** | **Multi-task gradient boosting** (shared feature encoder, per-attribute output heads) or simpler: **separate LightGBM models per attribute (age bracket, gender split, geo)** at MVP | Demographics correlate with each other; multi-task helps once you have volume, but don't over-engineer this before you have data — start with separate simple models |
| **Engagement Pod / Bot Comment Detection** | **LLM zero-shot classification (Claude) + lightweight graph anomaly scoring** | This isn't really a "retrainable tabular model" problem — it's pattern detection on text + network structure. Don't force it into the GBM pipeline. |
| **Content Quality Scorer** | **Claude vision + caption scoring** | Same — LLM-based, not part of the retraining loop below |

**Why not deep learning / neural nets across the board?** At the data volumes you'll have for the first 12-18 months (hundreds to low tens-of-thousands of connected accounts), GBMs will out-perform, train faster, need less tuning, and are far easier to debug when a brand disputes a score. Revisit deep learning (e.g. a TabTransformer or a two-tower model for influencer-brand matching) once you're past ~50-100K labeled accounts — that's a Phase 3+ conversation, not now.

**Ensembling for "crazy strong":** rather than one model per task, run each critical model (reach, fake-follower, valuation) as a **small ensemble** — e.g. 3 LightGBM models trained on different bootstrap samples — and report both the mean prediction *and* the variance across the ensemble as an additional confidence signal. Cheap to build, meaningfully more robust, and gives you a second, independent confidence measure beyond the quantile range.

---

## 2. The Continuous Retraining Pipeline (Champion–Challenger)

This is the core architecture. Every new OAuth connection becomes a candidate labeled training row — but nothing touches production until it passes gates.

```
New OAuth connection
        │
        ▼
[Data Quality Filter] ── reject if: engagement anomaly score too high,
        │                account too new, obvious bot-follower spike
        ▼
[Feature Store]  ← accumulates clean labeled rows over time
        │
        ▼
[Scheduled Retrain Job]  (e.g. nightly, or every N new clean rows — whichever first)
        │
        ▼
[Challenger Model] trained on latest feature store snapshot
        │
        ▼
[Offline Validation]  ── compare Challenger vs current Champion on:
        │                  - held-out test set (never used in training)
        │                  - business metrics (calibration, error by tier/niche)
        ▼
   Pass? ──No──► discard, alert, log for review
        │
       Yes
        ▼
[Shadow Deployment]  ── Challenger runs in parallel on live traffic for
        │                a window (e.g. 3-7 days), predictions logged but
        │                NOT shown to users yet
        ▼
[Shadow Comparison]  ── does Challenger's live behavior match offline
        │                validation? (catches train/serve skew)
        ▼
   Pass? ──No──► discard, alert, log for review
        │
       Yes
        ▼
[Promote to Champion]  ── versioned, old Champion archived (instant rollback
                           if a problem surfaces post-promotion)
```

### Why this instead of "retrain instantly per user"
- **Data quality filter** stops an obviously-gamed account (bought followers, engagement pods) from teaching the model that fraud looks normal.
- **Champion-challenger** means production score-serving is always coming from a model that's already passed validation — no live experimentation on paying customers.
- **Shadow deployment** catches the classic ML failure mode where a model looks great on a held-out test set but behaves differently on real live traffic (distribution shift, pipeline bugs).
- **Versioning + rollback** matters enormously for a paid trust product — if a brand disputes a score and you need to explain "this was computed by model v47, here's its validation report," you need that paper trail.

### Retrain cadence (practical starting point)
- **MVP (first ~500 connected accounts):** weekly retrain, manual review of the validation report before promotion — you're still small enough to eyeball it.
- **Growth stage (500–10K accounts):** automated nightly retrain + automated promotion gate (no manual step) once you trust the validation metrics, with alerting if a challenger fails repeatedly (signal something's wrong upstream).
- **Scale stage (10K+):** consider event-triggered retrain (retrain when N new clean labeled rows accumulate, e.g. every 500 new accounts) rather than pure time-based, so the model updates faster during growth spurts.

---

## 3. Guarding Against Gaming (this matters more than the algorithm choice)

Your product's entire value proposition is trustworthiness. The moment someone realizes they can inflate their score by gaming what feeds the training data, the model — and your product's credibility — degrades for everyone.

- **Outlier rejection before training:** any OAuth account with a fake-follower-probability above a threshold from the *existing* model gets excluded from the next training batch as a labeled example (its own audit still runs, just doesn't get folded back into training data) — stops the classic feedback-poisoning loop where fraud gradually redefines "normal."
- **Stratified validation:** validate the challenger model separately per follower-tier and per niche — a model that improves on average but gets worse for micro-influencers specifically would ship silently otherwise.
- **Canary accounts:** maintain a small internal set of manually-verified "ground truth" accounts (known real, known fake-heavy) that every challenger model must score correctly before promotion — a cheap, high-signal regression test.
- **Audit trail:** log which model version + feature snapshot produced every score, indefinitely — non-negotiable for a paid product where scores affect real money (brand deals).

---

## 4. The Data Flywheel That's Unique to This Business

This is worth calling out because it's a genuine moat, not just an ML nicety:

- **Reach/valuation models get better with every OAuth connection** — more ground truth, tighter confidence intervals, better estimates for the non-connected accounts too.
- **If you add a lightweight "report your actual deal rate" feedback loop** (brand or influencer optionally reports what a sponsored post actually sold for after the fact), your valuation model gets calibrated against *real market transactions*, not just follower-count heuristics. No public dataset or competitor without your user base can replicate this — it compounds the longer you run.
- This is a strong argument for building the feedback-reporting feature earlier than you might otherwise prioritize it — it's small to build and disproportionately valuable to your model quality over time.

---

## 5. Infra / Stack Recommendation

Keeping this pragmatic and matched to what you already run:

- **Training pipeline:** Python (scikit-learn ecosystem, LightGBM/XGBoost, `optuna` for hyperparameter tuning) — this is genuinely the right tool here, don't force it into Go.
- **Orchestration:** simple cron-triggered job at MVP; move to Airflow or Dagster once retrain cadence gets complex (multiple models, dependencies between them).
- **Experiment tracking + model registry:** MLflow — tracks every training run, metrics, and versioned model artifacts; gives you the champion/challenger comparison and rollback capability practically for free.
- **Serving:** export trained models to **ONNX**, serve from a lightweight Go inference service (your Go backend calls this directly, no Python in the hot path) — keeps latency low and keeps your core backend in the stack you already own.
- **Feature store:** skip a dedicated feature store (Feast etc.) at MVP — it's real overhead for a small team. A well-indexed Postgres/Timescale table of "clean labeled rows" is enough until you're retraining multiple models on shared features at real scale.
- **Drift monitoring:** Evidently AI (open source) to track feature drift and prediction drift over time between retrains — flags when something's shifted (e.g. Instagram's algorithm changes engagement patterns platform-wide) even between scheduled retrains, so you can trigger an emergency retrain.

---

## 6. Phased Build Order

1. **Ship without ML first** — pure heuristic scoring (industry-benchmark tables, simple ratios) while you accumulate the first 100-500 OAuth-connected accounts. Don't build the ML pipeline before you have training data; it's wasted effort.
2. **First real model:** Reach Estimator only, trained as a one-off (no automated retrain loop yet), manually retrained monthly. Validate it actually beats the heuristic baseline before investing further.
3. **Add Fake Follower + Valuation models** once Reach Estimator is proven out, same manual-retrain cadence.
4. **Automate the champion-challenger pipeline** once you're retraining more than ~once a month and it's eating real time — this is when MLflow + scheduled jobs earn their keep.
5. **Add the deal-rate feedback loop** for valuation calibration — small feature, outsized long-term model quality impact.
6. **Only then** consider ensembling, multi-task demographics modeling, or deep learning approaches — once volume genuinely justifies it.

**The honest advice:** don't let "crazy strong ML" mean "crazy complex ML" out of the gate. The strongest version of this product for the first year is a well-validated, honestly-uncertain GBM pipeline with a bulletproof retraining safety net — not a fancy model architecture. The safety net is what makes it a paid, trustworthy product; the algorithm choice is almost secondary.

---

*Next natural pieces: the exact feature list + engineering for the Fake Follower model, the MLflow project structure, or the Postgres schema for the labeled training table.*
