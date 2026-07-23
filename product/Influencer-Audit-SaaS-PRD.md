# Product Requirements Document
## InfluAudit — AI-Powered Influencer Audit, Valuation & Growth Platform

**Version:** 1.0
**Author:** Ashu
**Status:** Draft for build planning

---

## 1. Problem Statement

Brands and agencies waste budget on influencers who look big but perform poorly — fake followers, bought engagement, mismatched audiences, or declining reach. There's no fast, trustworthy, standardized way to:

1. **Audit** an influencer's real reach, engagement quality, and audience authenticity across platforms.
2. **Value** them in ₹/$ terms so brands know what a fair rate looks like.
3. **Coach** the influencer with actionable, personalized tips to grow and fix weak spots.

InfluAudit solves this with automated multi-platform audits, an AI-generated trust/value score, and an LLM-driven advisory layer — sold as a SaaS to both **influencers** (self-audit + growth) and **brands/agencies** (vetting + discovery).

---

## 2. Vision & Goals

Build a single platform where:
- An influencer connects/enters their handles (Instagram, Facebook, YouTube, TikTok, X, LinkedIn, personal website/blog) and gets a **comprehensive audit report** in minutes.
- A brand can **search, filter, and shortlist** influencers by audit score, niche, audience geography, and rate-per-engagement.
- Every report ends with **LLM-generated, personalized recommendations** — not just numbers, but "here's what to fix and how."

**North Star Metric:** Number of audits completed per month that lead to either a brand deal shortlist or a measurable influencer engagement improvement in the following 30 days.

---

## 3. Target Users / Personas

| Persona | Who | Core Need |
|---|---|---|
| **Micro/Nano Influencer** | 5K–100K followers, wants brand deals | Prove authenticity, know fair pricing, get growth tips |
| **Established Influencer / Creator** | 100K+ followers, multi-platform | Track performance trends, benchmark vs competitors |
| **Brand Marketing Manager** | Runs influencer campaigns | Vet influencers fast, avoid fraud, compare ROI |
| **Talent/Marketing Agency** | Manages multiple influencers or campaigns | Bulk audits, white-label reports, client-ready PDFs |
| **Platform Admin** | You / internal team | Manage plans, monitor API usage/costs, moderate data |

---

## 4. Core Product Pillars

1. **Multi-Platform Data Ingestion** — pull real metrics from each platform.
2. **Authenticity & Fraud Detection** — fake follower / bot / engagement-pod detection.
3. **Valuation Engine** — translate metrics into a $/₹ rate and an Influence Score.
4. **LLM Advisory Layer** — narrative report + personalized, actionable tips.
5. **Dashboards** — one for influencers (self-serve), one for brands (discovery/vetting).
6. **Reporting & Sharing** — exportable PDF/branded report, shareable public score badge.
7. **Monetization** — freemium + subscription + pay-per-audit + B2B API.

---

## 5. Feature List (Detailed)

### 5.1 Platform Connectors / Data Ingestion
- **Instagram** — via Meta Graph API (Instagram Business/Creator account required for full data: followers, reach, impressions, saves, story metrics). Public-profile fallback via limited scraping for basic stats (with legal caveats — see §11).
- **Facebook Page** — Meta Graph API: page likes, post reach, engagement, video views.
- **YouTube** — YouTube Data API v3 + YouTube Analytics API (for owned channels): subscribers, views, watch time, CTR, audience retention.
- **TikTok** — TikTok for Developers API (Login Kit + Display API) where available; fallback to public metrics scraping.
- **X (Twitter)** — X API v2: followers, engagement rate, tweet impressions.
- **LinkedIn** — LinkedIn Marketing API for company/creator pages (limited access tier).
- **Website/Blog** — Ahrefs-style SEO check: domain rating, organic traffic estimate, backlinks, page speed, Core Web Vitals (can reuse Ahrefs API you already have access to).
- **Manual/CSV import** — for influencers who want to upload platform-exported analytics (Instagram Insights export, YouTube Studio export) for higher accuracy than public scraping.

**Data refresh:** on-demand audit + optional scheduled re-audit (weekly/monthly) for tracked influencers.

### 5.2 Authenticity & Fraud Detection Engine
- **Fake follower estimation**: statistical modeling on follower growth pattern anomalies (sudden spikes), follower-to-following ratio, profile completeness sampling, bot-like username patterns.
- **Engagement authenticity**: engagement rate vs. follower count expected curve (benchmarked by niche/tier), comment quality analysis (generic/spam comments like emoji-only or repeated phrases detected via LLM classification), like-to-comment ratio anomalies.
- **Engagement pod / group detection**: same commenters appearing across unrelated posts/accounts within tight time windows.
- **Audience geography & demographics estimate**: using available platform insights (if connected) or inferred via comment language/profile sampling.
- **Content authenticity score**: sponsored-post disclosure compliance check (ASCI/FTC hashtag detection), repost/plagiarism flags.

Output: a single **Authenticity Score (0–100)** with a breakdown by sub-metric, plus red/yellow/green flags.

### 5.3 Valuation Engine
- **Influence Score (0–100)**: weighted composite of reach, engagement rate, authenticity, consistency, and content quality — configurable weights per niche (beauty vs. tech vs. finance behave differently).
- **Rate Card Estimator**: suggested price per post/reel/story/video based on:
  - Follower tier benchmark tables (industry-standard CPM-style formulas)
  - Engagement rate multiplier
  - Niche multiplier (finance/tech typically pay more per follower than lifestyle)
  - Geography multiplier (US/UK audience worth more than low-CPM markets, adjustable)
- **Cost-per-engagement & cost-per-1000-reach** benchmarking against category averages.
- **Growth trajectory valuation**: projects value 3/6/12 months out based on growth trend, useful for negotiating longer contracts.
- **Comparison mode**: valuate 2–5 influencers side by side for brand decision-making.

### 5.4 LLM-Powered Advisory Layer
This is the differentiator — using Claude (or a swappable LLM provider) for:
- **Narrative Audit Report**: turns raw metrics into a plain-English summary ("Your engagement rate of 2.1% is below the 3.4% benchmark for beauty micro-influencers, driven mainly by low comment engagement on Reels...").
- **Personalized Growth Tips**: content ideas, best posting times (derived from historical engagement-by-hour data), caption/hook improvement suggestions, hashtag strategy, collab suggestions based on similar-niche creators.
- **Weakness → Fix Mapping**: every red flag in the audit maps to a specific, prioritized recommendation (e.g., "Low Story completion rate → shorten stories to 3 frames, add polls").
- **Brand-Fit Summary** (for brand-side users): LLM explains *why* an influencer is/isn't a good fit for a given campaign brief, based on audience match and content tone analysis.
- **Chat-with-your-report**: influencer or brand can ask follow-up questions about the audit ("Why is my authenticity score low?") in a chat interface grounded in that audit's data (RAG over the report + raw metrics).
- **Caption/Content Analyzer**: paste a draft caption, LLM scores it for hook strength, CTA clarity, tone-brand fit.

### 5.5 Influencer Dashboard
- Connect accounts (OAuth where available), run audit, view score history/trends (line charts over time).
- Downloadable/shareable **PDF report** and a **public badge/widget** ("Verified Influence Score: 82/100") embeddable on their website/media kit.
- Personalized **Tips Feed** — ongoing, updated tips (not just one-time).
- **Media Kit Generator**: auto-builds a shareable one-pager (stats + rate card + audience demo) for pitching brands.

### 5.6 Brand/Agency Dashboard
- **Search & Discover**: filter influencers by niche, platform, follower range, engagement rate, authenticity score, geography, budget range.
- **Bulk Audit**: upload a list of handles → batch-run audits (useful for agencies vetting 50+ creators for a campaign).
- **Campaign ROI Tracker**: after a campaign, input actual results, compare to the pre-campaign valuation/estimate.
- **Shortlist & Compare**: save influencers, side-by-side comparison view.
- **White-label reports**: agencies can brand the PDF output with their own logo (higher-tier plan).

### 5.7 Alerts & Monitoring (for tracked/subscribed influencers)
- Sudden follower drop/spike alert.
- Engagement rate falling below historical baseline.
- New fake-follower-pattern detected.
- Competitor benchmark shift (e.g., "a similar creator in your niche grew 20% this month").

### 5.8 Admin/Platform Ops
- API usage & cost monitoring per platform connector (Meta/YouTube/TikTok APIs often rate-limited/paid at scale).
- Manual override / dispute resolution (influencer flags an audit as inaccurate → admin review queue).
- Audit job queue monitoring (for scraping/API failures, retries).

### 5.9 Monetization Model
| Tier | Who | Price idea | Includes |
|---|---|---|---|
| **Free** | Influencers | ₹0 | 1 audit/month, basic score, no PDF export |
| **Creator Pro** | Influencers | ₹499–999/mo | Unlimited audits, full LLM tips, PDF + media kit, trend tracking |
| **Brand Starter** | Small brands | ₹2,999/mo | 20 audits/mo, search & discover, comparison tool |
| **Agency** | Agencies | ₹9,999+/mo | Bulk audits, white-label reports, team seats, API access |
| **Enterprise/API** | Large brands, MarTech | Custom | Full API access, SLA, custom scoring weights |

Additional: **pay-per-audit** (₹99–199 one-off) for occasional brand users who don't want a subscription.

---

## 6. Scoring Algorithm (High-Level Design)

```
Influence Score = 
    (0.30 × Reach Score) +
    (0.30 × Engagement Quality Score) +
    (0.25 × Authenticity Score) +
    (0.10 × Consistency Score) +
    (0.05 × Content Quality Score)
```
- Each sub-score normalized 0–100 against **niche + tier peer benchmarks** (a nano-influencer isn't compared to a mega-influencer).
- Weights configurable per vertical (finance vs. fashion vs. gaming) — store as a config table, not hardcoded, so you can tune without redeploying.
- Recompute on every audit; store historical snapshots for trend charts.

---

## 7. Technical Architecture (Suggested)

Given your existing stack strengths (Go backend, Android/Kotlin, prior SDUI/microservice experience), here's a pragmatic architecture:

### Backend
- **Language:** Go — services for ingestion, scoring, orchestration (matches your existing production patterns: hexagonal architecture, worker pools, circuit breakers you've already built before).
- **Audit Job Queue:** Kafka or Redis Streams — each audit request becomes a job; workers fan out to platform connectors in parallel, aggregate, then trigger scoring + LLM narrative generation.
- **Platform Connector Services:** one microservice per platform (Instagram, YouTube, TikTok, etc.) — isolates rate-limit/API-key handling and lets you swap scraping fallback independently.
- **Scoring Service:** stateless Go service, pure computation over ingested metrics + benchmark tables.
- **LLM Service:** wraps Claude API calls — narrative generation, tips, chat-with-report (RAG using the audit's own data as context, no need for a heavy vector DB at first; a per-report JSON context blob is enough at MVP scale).

### Data Layer
- **Postgres** — core relational data (users, influencers, audits, scores, subscriptions).
- **TimescaleDB (Postgres extension)** — time-series metrics for trend charts (follower count/engagement over time) — reuse Postgres infra, avoid a separate DB.
- **Object storage (S3-compatible)** — generated PDF reports, media kits.
- **Redis** — caching platform API responses (avoid re-hitting rate-limited APIs), session cache.

### Frontend
- **Web dashboard:** Next.js (React) — you've already built CoBuild's site in Next.js, so reuse patterns; Framer Motion for polished report/score reveal animations.
- **Mobile (optional phase 2):** Android/Kotlin Compose app for influencers to check scores on the go — plays to your Android strength directly.

### Auth & Integrations
- OAuth 2.0 for Meta (Instagram/Facebook), Google (YouTube), TikTok, X — standard "Login with Platform" to get first-party data access with user consent.
- Stripe/Razorpay for billing (Razorpay given India-first audience).

### Infra
- Containerized services (Docker/K8s) — you've worked with this before.
- Observability: OpenTelemetry + Grafana/Prometheus (you've used OTel in past projects).

---

## 8. Compliance, Legal & Risk Notes (Important — Read Before Building)

- **Platform ToS:** Instagram/Facebook/TikTok/X all restrict scraping and require official APIs for most data. Public-scraping fallback carries real legal/ToS risk and can get your IPs/accounts blocked — treat it as a stopgap for unclaimed profiles only, and prioritize OAuth-based official API access for connected/claimed accounts.
- **Meta Graph API:** Instagram data (beyond public basics) requires the account to be a **Business/Creator account** and the user to explicitly connect via OAuth — you cannot pull deep insights (reach, story metrics) for accounts that haven't connected.
- **Data privacy:** You'll be storing personal/behavioral data — need a clear privacy policy, data retention limits, and (if targeting EU/global brands) GDPR-style consent flows.
- **Rate limits & cost:** Meta/YouTube/TikTok APIs have quotas; at scale, API costs and quota management become a real engineering + business constraint — budget for this early.
- **Valuation disclaimer:** Rate estimates should be clearly labeled as *estimates*, not guarantees — avoid legal exposure if a brand under/overpays based on your number.

---

## 9. MVP Scope (Phase 1 — build this first)

Keep it tight. MVP should prove the core loop: **connect → audit → score → LLM tips → shareable report.**

1. Instagram + YouTube connectors only (highest demand, most mature APIs).
2. Core scoring engine (Influence Score + Authenticity Score).
3. LLM narrative report + tips (single LLM call per audit, no chat yet).
4. Influencer dashboard: run audit, view score, download PDF.
5. Basic brand search (filter by score/niche/followers) — no bulk audit yet.
6. Freemium + one paid tier only (skip Agency/Enterprise at MVP).
7. Manual CSV import as accuracy fallback where API data is thin.

**Explicitly defer to Phase 2+:** TikTok/X/LinkedIn connectors, website/SEO audit module, chat-with-report, white-label, alerts/monitoring, bulk audits, mobile app.

---

## 10. Success Metrics (KPIs)

| Metric | Target signal |
|---|---|
| Audits completed / week | Core usage volume |
| Free → Paid conversion rate | Monetization health |
| Report share rate (PDF/badge shared externally) | Viral/organic growth loop |
| Brand shortlist → deal conversion (self-reported) | Real-world value proof |
| Influencer 30-day return rate | Retention / tips actually being used |
| Audit accuracy dispute rate | Trust/data quality |

---

## 11. Open Questions to Resolve Before Building

1. **Geography-first launch:** India-first (Razorpay, INR pricing) or global from day one? Affects payment stack and benchmark data sourcing.
2. **Which platform first — Instagram or YouTube?** Instagram has broader influencer coverage; YouTube has richer official Analytics API data if creators connect.
3. **Scraping fallback — how much legal risk are you willing to carry** for unclaimed/public profiles vs. requiring OAuth connection only?
4. **LLM cost model:** one narrative generation per audit could get expensive at scale — consider caching/templating parts of the report and reserving full LLM generation for the "insights" section only.
5. **Do you want a public influencer directory** (SEO/growth play, like a "Clearbit for influencers") or keep it gated behind brand logins only?

---

*This PRD is meant as a working document — happy to turn any section (e.g., the scoring algorithm, DB schema, or MVP sprint plan) into a deeper spec or start scaffolding the Go backend/Next.js frontend next.*
