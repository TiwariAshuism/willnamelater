# Research: Fraud-Detection Signals for the InfluAudit ML Engine

**Date:** 2026-07-09
**Question:** Which ML input signals most effectively detect influencer fraud (fake followers,
bot engagement, engagement pods), and which are computable using **only** official OAuth-based
platform APIs with no scraping?
**Method:** Automated deep-research harness — 5 search angles, 24 sources fetched, 107 claims
extracted, 3-vote adversarial verification per claim (2 of 3 refutes kills a claim).

> **Run integrity.** This run was **degraded**. Of 107 extracted claims, only 25 reached
> verification before the session token budget was exhausted; 20 verifier agents and the final
> synthesis step failed outright. Results below: **16 confirmed, 3 refuted, 6 unverified.**
> The unverified claims are *not* endorsed — they are recorded so the work is not repeated.
> Re-running verification on them is tracked as an open action at the bottom.

---

## 1. Bottom line

**The engine is optimizing at the wrong level.** The strongest confirmed finding is that
per-account features are weak against evolved inauthentic accounts, while coordination
(group-structure) features carry the discriminative power.

Today `services/ml` computes five signals. Four are per-account (`growth_spike_signal`,
`follower_following_signal`, `engagement_deviation_signal`, `like_comment_ratio_signal`, plus an
IsolationForest fitted per request). Only the HDBSCAN pod detector operates on coordination
structure — and it is a side endpoint rather than the core of the fraud score.

**Recommended reordering:** coordination features become primary; per-account heuristics become
tie-breakers and explainability aids.

**Necessary counterweight.** The claim that unsupervised group detection *overcomes* the
generalization weakness of supervised classifiers was **refuted 0–3**. Coordination-first narrows
the cold-start gap; it does not close it. This nuance must survive into any customer-facing copy.

---

## 2. Confirmed findings (survived 3-vote adversarial verification)

### 2.1 Coordination beats per-account analysis

| Claim | Vote | Source |
|---|---|---|
| "Modern social bots are hardly distinguishable from legitimate human accounts when analyzed individually" — so single-account feature detection generalizes poorly to novel bots. | 2–1 | [arXiv 2007.03604](https://arxiv.org/pdf/2007.03604) |
| Group/coordination-based detection outperforms individual detection, because coordinated accounts leave more detectable traces than sophisticated single bots do. | 3–0 | [arXiv 2007.03604](https://arxiv.org/pdf/2007.03604) |
| Coordinated inauthentic behavior manifests as measurable graph/matrix structure — near-fully-connected communities, dense adjacency blocks, spectral patterns — validating clustering coefficient and community modularity as pod signals. | 2–1 | [arXiv 2007.03604](https://arxiv.org/pdf/2007.03604) |

This directly contradicts the current weighting of the composite fraud score.

### 2.2 The co-commenter graph is the highest-evidence, API-feasible structure

| Claim | Vote | Source |
|---|---|---|
| A commenter co-occurrence graph (edge if two users commented on the same video within a short window; **edge weight = number of common videos**) captures "lockstep" coordinated engagement; fake-comment clusters show high internal edge density. | 3–0 | [arXiv 1512.05457](https://arxiv.org/pdf/1512.05457) |
| A co-commenter graph usable for pod detection is buildable **entirely from YouTube Data API output** — comment details, timestamps, commenter IDs — with no follower-list access and no scraping. | 3–0 | [arXiv 2311.05791](https://arxiv.org/pdf/2311.05791) |
| Concrete construction: edges between commenters who commented on the same video (within one or across several channels), **weighted by number of shared videos**. | 3–0 | [arXiv 2311.05791](https://arxiv.org/pdf/2311.05791) |
| **Maximal cliques of ≥5 members** are the concrete graph-structural signal of coordinated "mob" commenting. Suspicious channels showed clique counts of **12,241 / 9,246 / 782**; benign-leaning channels **26 / 24 / 20**. | 3–0 | [arXiv 2311.05791](https://arxiv.org/pdf/2311.05791) |

The three-orders-of-magnitude separation on clique counts is the single most concrete, defensible
feature surfaced by this research.

### 2.3 Unsupervised coordination detection is viable without labels

| Claim | Vote | Source |
|---|---|---|
| Coordinated behavior can be detected by an unsupervised pipeline: bipartite account-to-feature network → project to account-to-account similarity network → connected components / community detection. **No labeled training data required.** | 3–0 | [Coordinated-networks survey](https://www.readkong.com/page/uncovering-coordinated-networks-on-social-media-methods-1139022) |
| Temporal synchronization is a usable coordination signal: bin action timestamps into fixed intervals (e.g. 30 minutes) with minimum-support thresholds to detect groups acting in unison. | 3–0 | same |

### 2.4 The strongest published accuracy is semi-supervised — a cold-start caution

| Claim | Vote | Source |
|---|---|---|
| The 98% manual-review precision on the YouTube Comments graph is **semi-supervised**: it requires a seed set of known abusive accounts and searches for behaviorally-similar neighbors via local spectral diffusion. | 3–0 | [arXiv 1512.05457](https://arxiv.org/pdf/1512.05457) |

We have no seeds at cold start. **We may not cite or imply this accuracy figure.** The dispute
queue is the mechanism that eventually produces those seeds.

### 2.5 UnDBot — the best structural fit for our zero-label state

| Claim | Vote | Source |
|---|---|---|
| An unsupervised, label-free bot-detection framework (UnDBot) can be built entirely from aggregate account behavior metrics using structural information theory / structural entropy, with no labeled training data, and reportedly outperforms other unsupervised bot-detection models. | 3–0 | [ACM 10.1145/3660522](https://dl.acm.org/doi/10.1145/3660522) |
| It reduces to **three interpretable metrics**: Posting Type Distribution, Posting Influence (avg. comments+likes+reshares per original post), and Follow-to-follower Ratio — used to build a weighted multi-relational user graph. | 3–0 | same |
| The Follow-to-follower Ratio uses **counts only**: `ff = (following + 1) / (follower + 1)`. **No follower list required.** | 2–0 | same |

The last row independently validates the one per-account heuristic worth keeping. Note our
`follower_following_signal` uses a two-sided `log10` band; the published formula is this plain
ratio. Align, or record a deliberate decision not to.

### 2.6 Fake-follower detection proper is out of reach

| Claim | Vote | Source |
|---|---|---|
| The follower-map method requires the **complete follower list** plus each follower's account-creation date and estimated follow-time — unavailable under OAuth-only, no-follower-list constraints. **Infeasible for InfluAudit.** | 3–0 | [EPJ Data Science](https://epjdatascience.springeropen.com/articles/10.1140/epjds/s13688-024-00499-6) |
| The real discriminating signal is temporal: bought accounts share creation dates and follow the target successively in bursts. | 3–0 | same |

The signal is real; the data is unreachable through official APIs. This vindicates the decision to
label every fraud output an **estimate** and never to report a measured fake-follower percentage.
Any vendor claiming a precise fake-follower percentage without follower-list access is estimating too.

---

## 3. Refuted claims (killed by ≥2 of 3 verifiers)

Recorded so they are not re-proposed.

1. **0–3 — "Unsupervised/semi-supervised group detectors overcome the generalization
   deficiencies of supervised classifiers."** ([arXiv 2007.03604](https://arxiv.org/pdf/2007.03604))
   Group detection *outperforms* individual detection (confirmed, §2.1), but it does **not**
   overcome the generalization problem. Do not conflate the two.

2. **1–2 — "Coordinated inauthentic liking can be detected by clustering purely on binary
   like/vote data (GMM or k-means + logistic regression), needing no profile, follower-list, or
   content features."** ([ResearchGate PoC](https://www.researchgate.net/publication/370764019_Detecting_Coordinated_Inauthentic_Behavior_in_Likes_on_Social_Media_Proof_of_Concept))
   Related confirmed finding (3–0): that method was evaluated **only on a synthetic agent-based
   simulation**, never on real social-media-like data. It is a proof of concept arguing that
   platforms should release reactions data. It offers no real-world accuracy evidence.

3. **0–3 — A compound claim attributing a specific co-commenter construction (prune edges below
   10 shared videos; maximal cliques ≥5; avg. degree, clustering coefficient, modularity, clique
   statistics) to [CEUR-WS paper6_jot.pdf](https://ceur-ws.org/Vol-3138/paper6_jot.pdf).**
   **This does not contradict §2.2.** The construction itself is confirmed 3–0 from
   [arXiv 2311.05791](https://arxiv.org/pdf/2311.05791). The claim was killed for
   *mis-attribution* to the wrong source, not because the technique is wrong. Cite arXiv 2311.05791.

---

## 4. Unverified — promising, not endorsed

Their verifier agents died on the session limit. Three of the four are signals we specifically
asked about, and all are cheap to compute. **Do not build on these until verified.**

1. Inter-arrival time distributions differ between bots and humans, with bots showing pronounced
   regular-interval peaks (10, 15, 30 minutes).
2. Sudden spikes in comment activity flag collusive engagement (computable from `commentThreads`
   timestamps alone).
3. Near-duplicate comment-text similarity discriminates collusive comment activity.
4. The CollATe neural model (comment time-series anomaly + comment similarity + metadata) achieves
   a **0.905 true-positive rate** for collusive comments.
5. The clique-membership *fraction* (unique commenters in maximal cliques ÷ total nodes; one channel
   reported at 0.68) discriminates coordinated channels. Same paper as the confirmed clique counts
   (arXiv 2311.05791), so this is the strongest of the unverified set.
6. The arXiv 2311.05791 data was collected via the official YouTube Data API v3 in accordance with
   YouTube's ToS, with commenter/video IDs anonymized before graph construction.

Item 6 matters for §7 (legal). Item 5 is safe to implement at reduced confidence given its
provenance.

---

## 5. Signals to add, by feasibility tier

### (a) Implementable today — public YouTube Data API only

| # | Signal | Source fields | Evidence | False-positive risk | Cost |
|---|---|---|---|---|---|
| 1 | **Maximal-clique count (cliques ≥5)** in the co-commenter graph | `commentThreads`: author channel ID, video ID | 3–0; 12,241/9,246/782 vs 26/24/20 | Large fandoms co-comment legitimately; must normalize by channel size and comment volume | Medium |
| 2 | **Shared-video edge weighting** (replace binary co-occurrence) | same | 3–0, two independent papers | Low — strictly more information than current binary edges | Low |
| 3 | **Clique-membership fraction** | same | *Unverified* (§4.5) | As #1 | Low, once #1 exists |
| 4 | **Temporal binning** of comment timestamps (~30-min bins, min-support threshold) | `commentThreads.publishedAt` | 3–0 | Timezone-clustered genuine audiences; scheduled-post effects | Low |
| 5 | **Internal edge density / clustering coefficient / modularity** of detected clusters | derived from graph | 2–1 and 3–0 | As #1 | Low, once #2 exists |

### (b) Implementable, OAuth-connected accounts only

| # | Signal | Source | Evidence | Notes |
|---|---|---|---|---|
| 6 | **UnDBot's three metrics** — posting-type distribution, posting influence, follow-to-follower ratio → structural entropy over a multi-relational graph | Counts + post lists; `ff` needs only follower/following counts | 3–0 (×2), 2–0 | Best fit for zero-label cold start. Replaces per-request IsolationForest as the per-account model. |
| 7 | Instagram `reach`, `impressions`, `saves`, `shares` ratios | Meta Graph API insights | Not directly evidenced by this corpus | Include as inputs; treat as exploratory until evidenced |
| 8 | YouTube Analytics retention / traffic-source anomalies | YouTube Analytics API | Not evidenced by this corpus | Exploratory |

### (c) Infeasible — do not pursue

| Signal | Why |
|---|---|
| **Follower-map / fake-follower percentage** (follow-rank × account-creation-date) | Needs full follower list + per-follower creation dates. 3–0 confirmed unavailable. Keep reporting an **estimate**, never a percentage. |
| Semi-supervised spectral seed diffusion (the 98%-precision method) | Requires labeled abusive seed accounts. Revisit once the dispute queue has produced labels. |

---

## 6. Blocking schema gaps

**None of the tier-(a) features can be built on the current wire contract** (`services/ml/app/schemas.py`).

| Gap | Consequence |
|---|---|
| `PostMetrics` has **no `post_id`** | Comments carry `post_id`; post metrics do not. A comment cannot be joined to its post. Blocks every per-post feature: velocity, per-video cliques, shared-video edge weights. |
| `CommentEvent` has **no `text`** | `CommentsClassifyRequest` is a separate endpoint with no shared identifier, so text signals can never reach pod detection. Blocks near-duplicate detection (§4.3). |
| No Instagram Insights fields in `FraudScoreRequest` | `reach`, `impressions`, `saves`, `shares` are exposed by the Graph API for connected accounts and are absent from the request entirely. |
| `follower_series` is **daily** (`FollowerPoint.count` per day) | Sub-daily burstiness — precisely the temporal signal confirmed in §2.3 — is unrecoverable at that resolution. |

---

## 7. Legal and ToS constraints

Verification of the ToS/GDPR angle was **cut short** by the session limit; treat this section as
incomplete pending re-run.

What we do have: arXiv 2311.05791's authors collected comment data via the official YouTube Data
API v3 in accordance with YouTube's Terms of Service, and **anonymized commenter and video IDs
before graph construction** (unverified, §4.6).

**Implication for us.** A co-commenter graph is cross-account behavioral data about people who are
not our users and never consented. Before shipping tier-(a):

- Store commenter identifiers **hashed/pseudonymized**, never raw. `comment_sample.author_hash`
  in the schema already anticipates this — confirm the hash is salted and non-reversible.
- Define a retention limit for comment-derived graph data.
- Confirm GDPR/DPDP posture for processing third-party commenter behavior, and confirm that
  cross-account graph construction does not violate YouTube ToS or the Meta Platform Terms.

This is a genuine open legal question, not a formality.

---

## 8. Evidence-quality warning: `_ENGAGEMENT_CURVE`

`services/ml/app/features/engagement.py` hardcodes an expected-engagement curve
(`10k → 5.0%`, `100k → 3.5%`, `500k → 2.0%`, `1M → 1.5%`, floor `1.2%`) described in-code as
"midpoints of commonly cited micro/macro benchmark ranges."

**Nothing in this research corroborates those numbers.** The only corpus sources discussing such
curves are [HypeAuditor's blog](https://hypeauditor.com/blog/hypeauditor-fake-followers-detection/)
and [stormy.ai](https://stormy.ai/blog/best-ai-tools-detect-influencer-fraud) — **vendor marketing,
not independent evidence.**

That curve anchors `engagement_deviation_signal`, which feeds the composite fraud score shown to
customers. It is an uncited constant driving a customer-facing number. Either source it properly
or replace it with corpus-derived percentiles from the `benchmark` table, which was designed
precisely to grow real percentiles (`source='bootstrap'` → `source='corpus'`).

---

## 9. Recommended actions

1. **Reweight the fraud score** so coordination features dominate and per-account heuristics act as
   tie-breakers and explanations. (§2.1)
2. **Schema first** — add `post_id` to `PostMetrics`, `text` to `CommentEvent`. Nothing else in
   tier (a) is buildable until this lands. (§6)
3. **Rewrite `models/pods.py`**: shared-video edge weights, maximal-clique counts (≥5) as the
   primary pod feature, clique-membership fraction as secondary. Normalize by channel size and
   comment volume to blunt the large-fandom false positive.
4. **Rewrite the pod tests** around a label-free monotonicity property — injecting more coordinated
   structure must not *decrease* the clique count — mirroring the trick already used in
   `features/follower.py`.
5. **Replace the per-request IsolationForest with UnDBot's three metrics** as the per-account model.
   (§2.5)
6. **Fix or source `_ENGAGEMENT_CURVE`.** (§8)
7. **Resolve the legal question** on storing pseudonymized commenter IDs and cross-account graphs
   before tier-(a) ships. (§7)
8. **Re-run verification** on the six unverified claims (§4) — the workflow resumes from cache, so
   only the dead agents re-run.
9. **Never cite the 98% figure.** It is semi-supervised and needs seeds we do not have. (§2.4)

---

## 10. Sources

Primary (confirmed claims drawn from these):

- [arXiv 1512.05457](https://arxiv.org/pdf/1512.05457) — lockstep behavior, engagement graph, semi-supervised spectral diffusion, 98% precision
- [arXiv 2311.05791](https://arxiv.org/pdf/2311.05791) — co-commenter graph construction, maximal cliques, clique-count separation
- [arXiv 2007.03604](https://arxiv.org/pdf/2007.03604) — individual vs. group detection, graph/matrix structure of coordination
- [ACM 10.1145/3660522](https://dl.acm.org/doi/10.1145/3660522) — UnDBot, structural entropy, three interpretable metrics
- [EPJ Data Science 13688-024-00499-6](https://epjdatascience.springeropen.com/articles/10.1140/epjds/s13688-024-00499-6) — follower maps; infeasibility under OAuth-only
- [Coordinated-networks survey](https://www.readkong.com/page/uncovering-coordinated-networks-on-social-media-methods-1139022) — unsupervised bipartite→projection→community pipeline, temporal synchronization

Consulted, low weight or refuted:

- [ResearchGate 370764019](https://www.researchgate.net/publication/370764019_Detecting_Coordinated_Inauthentic_Behavior_in_Likes_on_Social_Media_Proof_of_Concept) — synthetic-simulation-only proof of concept
- [CEUR-WS Vol-3138 paper6](https://ceur-ws.org/Vol-3138/paper6_jot.pdf) — mis-attributed construction; cite arXiv 2311.05791 instead
- [eugeneyan.com — bootstrapping data labels](https://eugeneyan.com/writing/bootstrapping-data-labels/) — label bootstrapping for the dispute-queue loop

**Vendor marketing — not independent evidence, do not cite as such:**

- [HypeAuditor blog](https://hypeauditor.com/blog/hypeauditor-fake-followers-detection/)
- [stormy.ai](https://stormy.ai/blog/best-ai-tools-detect-influencer-fraud)
- [ViralMango analytics comparison](https://analytics.viralmango.com/compare/)

Full run artifacts: 24 sources, 107 claims, 25 verified. Stats:
`{"angles": 5, "sources": 24, "claims": 107, "verified": 25, "confirmed": 16, "killed": 3, "unverified": 6}`
