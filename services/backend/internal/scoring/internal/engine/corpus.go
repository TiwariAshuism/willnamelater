package engine

import (
	"sort"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/scoring/contract"
)

// CorpusObservation is ONE PERSISTED SCORE's contribution to the reference
// population: which influencer it describes, the cell it fell in, the engagement
// rate that was actually observed, and the provenance of the data behind it.
//
// The repository reads one row per distinct influencer (its DISTINCT ON keeps the
// newest), but CorpusCells de-duplicates again over InfluencerID. The duplication
// is deliberate: the guarantee that a benchmark counts PEOPLE, not audits, is the
// one that quietly failed before, and it is now pinned by a test that cannot be
// satisfied by SQL nobody runs in CI.
type CorpusObservation struct {
	InfluencerID     uuid.UUID
	Niche            string
	Tier             string
	EngagementRate   float64
	VerificationTier string
}

// CorpusCell is one (niche, tier) engagement-rate distribution aggregated from
// the reference population: the percentiles and summary statistics, plus the
// number of DISTINCT INFLUENCERS behind them. Influencers is the only sample count
// that means anything here — it is what a corpus benchmark's confidence is derived
// from, and what the publication threshold is checked against.
type CorpusCell struct {
	Niche       string
	Tier        string
	Influencers int
	P10         float64
	P25         float64
	P50         float64
	P75         float64
	P90         float64
	Mean        float64
	Stddev      float64
}

// CorpusCells aggregates observations into per-(niche, tier) distributions,
// returning only the cells that rest on at least minDistinct DISTINCT
// INFLUENCERS. It is pure: the caller supplies the rows.
//
// Three rules govern what may enter a benchmark that every other creator is then
// percentiled against:
//
//  1. ONE ROW PER INFLUENCER. The previous aggregation was a count(*) over the
//     score table, so thirty re-audits of a single creator published a "corpus
//     benchmark" of SampleSize 30 built from one person — and every other creator
//     was ranked against that one person's engagement rate. Repeat audits of the
//     same influencer are now collapsed to their newest observation, and a cell of
//     30 audits across 3 people is 3 samples, which publishes nothing.
//
//  2. PROVENANCE IS A FILTER. Only observations whose data came from a live,
//     authenticated API pull (VerificationVerified) may enter. A CSV or uploaded
//     export is a number the creator handed us; letting it into the reference
//     population would let a creator move the band that other creators are judged
//     against, by uploading whatever they like.
//
//  3. EXCLUSIONS ARE OBSERVATION-ONLY. The gates above turn on WHO the row is about
//     and HOW its data was obtained — facts independent of anything we computed.
//     No row is ever excluded for a fraud-score-derived reason (a low authenticity
//     subscore, a high risk score, a "suspicious" verdict). Conditioning the
//     reference population on our own heuristic's output would close the loop: the
//     corpus would come to define "normal" as "whatever our heuristic already likes"
//     and the percentiles would drift to confirm it. Do not add such a filter here.
func CorpusCells(obs []CorpusObservation, minDistinct int) []CorpusCell {
	if minDistinct < 1 {
		minDistinct = 1
	}

	type cellKey struct{ niche, tier string }
	seen := make(map[uuid.UUID]bool, len(obs))
	grouped := make(map[cellKey][]float64)

	for _, o := range obs {
		if o.InfluencerID == uuid.Nil || o.Niche == "" || o.Tier == "" {
			// An observation we cannot attribute to a person or a cell cannot be counted
			// as either.
			continue
		}
		if o.VerificationTier != contract.VerificationVerified {
			continue // rule 2: not live-API-sourced, so not reference material.
		}
		if seen[o.InfluencerID] {
			continue // rule 1: this person is already represented in the corpus.
		}
		seen[o.InfluencerID] = true
		k := cellKey{niche: o.Niche, tier: o.Tier}
		grouped[k] = append(grouped[k], o.EngagementRate)
	}

	out := make([]CorpusCell, 0, len(grouped))
	for k, rates := range grouped {
		if len(rates) < minDistinct {
			continue
		}
		sort.Float64s(rates)
		out = append(out, CorpusCell{
			Niche:       k.niche,
			Tier:        k.tier,
			Influencers: len(rates),
			P10:         percentileCont(rates, 0.10),
			P25:         percentileCont(rates, 0.25),
			P50:         percentileCont(rates, 0.50),
			P75:         percentileCont(rates, 0.75),
			P90:         percentileCont(rates, 0.90),
			Mean:        mean(rates),
			Stddev:      stddev(rates),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Niche != out[j].Niche {
			return out[i].Niche < out[j].Niche
		}
		return out[i].Tier < out[j].Tier
	})
	return out
}

// percentileCont is the continuous percentile of an ASCENDING-SORTED slice,
// interpolating linearly between the two straddling ranks — the same definition
// Postgres's percentile_cont uses, so moving the aggregation out of SQL did not
// quietly change the numbers.
func percentileCont(sorted []float64, q float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	pos := q * float64(n-1)
	lo := int(pos)
	if lo >= n-1 {
		return sorted[n-1]
	}
	frac := pos - float64(lo)
	return sorted[lo] + frac*(sorted[lo+1]-sorted[lo])
}
