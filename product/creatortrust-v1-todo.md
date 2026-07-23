# CreatorTrust v1 — Pending Items

Status after waves 1–7 + the open-issues pass. Backend gate green (gofmt/build/
vet/`test -race`/lint 0/openapigen-drift) and web gate green (tsc/eslint/vitest/
next build). This file tracks what remains.

Legend: **[EXT]** external/operator gate · **[DEFER]** deliberately deferred
(building it would violate an honesty invariant) · **[DONE]** fixed in the
open-issues pass.

---

## Fixed in the open-issues pass ([DONE])

- **[DONE] `business_management` scope** added to the Instagram block (`connectors.yaml`).
- **[DONE] Auto-audit on signup.** OAuth-as-signup now auto-submits the creator's
  first audit (best-effort) via `oauth.AuditStarter` → `audit.SubmitAuditForOwner`,
  so "connect → score" needs no manual step.
- **[DONE] Audience snapshot** on the result page — `GET /influencers/:id/profile-summary`
  (metrics module) + `AudienceSnapshot` web component (age/gender/country, graceful
  <100-follower / 48h-lag empty state). **Language is intentionally omitted**: Meta's
  follower_demographics does not expose it, so it is not shown rather than faked.
- **[DONE] Verified-metrics strip** — followers/ER/reach-ratio/save-rate/share-rate/
  cadence via the same endpoint; every value is a pointer that renders "not measured"
  when nil, never 0.
- **[DONE] Media-kit readiness meter** — the completeness meter (fraction + checklist),
  never scored.
- **[DONE] `oauth_grant` funnel event** fired server-side on a successful signup callback.
- **[DONE] Reels retention → score.** `reels_watch_time` now folds into Engagement
  Authenticity as a documented directional sub-signal (dropped when no Reels pulled).
- **[DONE] Follower-growth spike detection across audits.** A `scoring.MetricHistory`
  port (adapted onto `metrics.InstagramFollowerSeries`) loads prior follower readings
  so growth smoothness fires on a first audit combined with earlier ones; duplicate
  (current) readings are deduped.
- **[DONE] Format-diversity sub-signal** for Consistency & Reliability, reading
  `Post.MediaType` (now also persisted, migration `000035`).

---

## Still pending

### [EXT] Launch gates (not code)
- [ ] **Meta App Review.** Hard gate; the Instagram connector stays `enabled: false`
      until it passes. Register the redirect URIs (`.../api/oauth/[provider]/callback`
      and `.../api/oauth/signup/callback`), set `META_APP_ID`/`META_APP_SECRET`, flip
      the flag.
- [ ] **Apply migrations `000030`–`000035` to a live DB.** `make migrate-reset` +
      up/down round-trip. **Note `000035`:** `post.media_type` already exists (from
      `000009`), so `000035` up is `ADD COLUMN IF NOT EXISTS` and its down is a
      deliberate no-op (the column is owned by `000009`). A plain `ADD COLUMN` would
      crash `migrate up` on existing environments — do not "simplify" it.
- [ ] **Live end-to-end** once the stack is up + review passes: landing → pre-flight →
      signup (auto-audit) → result (audience/strip/factors) → publish → `/@handle`
      (confirm a non-owner open registers `share_open`). Until then, exercise against
      the CSV-import path + LocalStack S3.
- [ ] **Confirm exact Meta field names/versions** for saves/shares/reach/Reels watch
      time against live Graph docs (PRD §18; some shifted in Jan 2025).

### [DEFER] Do NOT build yet (correct deferrals)
- **Niche-fit sub-signal (Audience Quality).** There is no citable niche×demographic
  prior at cold start; inventing one would be the fabrication the engine forbids.
  Revisit when the corpus yields real per-niche demographic percentiles.
- **Comment quality → the score.** Display-only by design — the ML firewall
  (`services/ml/app/features/fraud_vector.py`) blocks it from any weighted score until
  its weight is fitted against resolved dispute labels and `FEATURE_ORDER_VERSION` is
  bumped. Also: there is no true *sentiment* model, only quality buckets.

---

## Explicitly out of v1 (per PRD — do NOT build now)

Media-kit generation (Phase 2) · brand directory/search (Phase 3) · campaigns/
bidding/reviews/marketplace Trust Score (Phase 4) · YouTube/TikTok expansion · growth
advice · the Declared zone · the instant-teaser accelerator.
