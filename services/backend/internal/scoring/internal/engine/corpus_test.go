package engine

import (
	"testing"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/scoring/contract"
)

// verifiedObs builds one verified observation.
func verifiedObs(id uuid.UUID, niche, tier string, er float64) CorpusObservation {
	return CorpusObservation{
		InfluencerID:     id,
		Niche:            niche,
		Tier:             tier,
		EngagementRate:   er,
		VerificationTier: contract.VerificationVerified,
	}
}

// TestCorpusCellsCountsPeopleNotAudits is defect B at the pure layer: thirty audits
// of three influencers is a sample of THREE. It publishes nothing, because a
// reference band that every other creator is percentiled against cannot be built out
// of three people — let alone one, which is what the old count(*) aggregation would
// happily have done with thirty re-audits of a single creator.
func TestCorpusCellsCountsPeopleNotAudits(t *testing.T) {
	t.Parallel()

	people := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	var obs []CorpusObservation
	for i := 0; i < 30; i++ {
		obs = append(obs, verifiedObs(people[i%len(people)], "beauty", tierMicro, 0.02+float64(i)*0.0001))
	}

	if cells := CorpusCells(obs, corpusThreshold); len(cells) != 0 {
		t.Fatalf("published %d cells from 30 audits of 3 people: %+v", len(cells), cells)
	}

	// The same influencer audited a thousand times is still one sample.
	one := uuid.New()
	var many []CorpusObservation
	for i := 0; i < 1_000; i++ {
		many = append(many, verifiedObs(one, "beauty", tierMicro, 0.05))
	}
	if cells := CorpusCells(many, corpusThreshold); len(cells) != 0 {
		t.Fatalf("one creator audited 1000 times published a benchmark: %+v", cells)
	}
}

// TestCorpusCellsRequiresVerifiedProvenance pins that CSV/upload-sourced scores
// never enter a benchmark. A creator must not be able to move the band they are
// judged against by uploading numbers they typed themselves.
func TestCorpusCellsRequiresVerifiedProvenance(t *testing.T) {
	t.Parallel()

	var obs []CorpusObservation
	for i := 0; i < corpusThreshold; i++ {
		o := verifiedObs(uuid.New(), "beauty", tierMicro, 0.03)
		o.VerificationTier = contract.VerificationEstimated // an uploaded export
		obs = append(obs, o)
	}
	if cells := CorpusCells(obs, corpusThreshold); len(cells) != 0 {
		t.Fatalf("upload-sourced scores entered the corpus: %+v", cells)
	}

	// Mixed: 30 verified people publish, and the 30 uploaded ones do not inflate the
	// count.
	for i := 0; i < corpusThreshold; i++ {
		obs = append(obs, verifiedObs(uuid.New(), "beauty", tierMicro, 0.03))
	}
	cells := CorpusCells(obs, corpusThreshold)
	if len(cells) != 1 {
		t.Fatalf("cells = %d, want 1", len(cells))
	}
	if cells[0].Influencers != corpusThreshold {
		t.Fatalf("influencers = %d, want %d (uploads must not be counted)", cells[0].Influencers, corpusThreshold)
	}
}

// TestCorpusCellsPublishesAtThreshold checks the happy path: enough distinct,
// verified influencers produce a cell whose percentiles are ordered and whose
// sample count is the number of people behind it.
func TestCorpusCellsPublishesAtThreshold(t *testing.T) {
	t.Parallel()

	var obs []CorpusObservation
	for i := 0; i < corpusThreshold; i++ {
		obs = append(obs, verifiedObs(uuid.New(), "beauty", tierMicro, 0.01+float64(i)*0.002))
	}
	cells := CorpusCells(obs, corpusThreshold)
	if len(cells) != 1 {
		t.Fatalf("cells = %d, want 1", len(cells))
	}
	c := cells[0]
	if c.Influencers != corpusThreshold {
		t.Fatalf("influencers = %d, want %d", c.Influencers, corpusThreshold)
	}
	if !(c.P10 <= c.P25 && c.P25 <= c.P50 && c.P50 <= c.P75 && c.P75 <= c.P90) {
		t.Fatalf("percentiles not ordered: %+v", c)
	}
	if c.Mean <= 0 || c.Stddev <= 0 {
		t.Fatalf("summary stats not computed: %+v", c)
	}
}

// TestCorpusCellsDropsUnattributableRows: a score with no influencer or no cell
// cannot be counted as a person or placed in a band.
func TestCorpusCellsDropsUnattributableRows(t *testing.T) {
	t.Parallel()

	obs := []CorpusObservation{
		verifiedObs(uuid.Nil, "beauty", tierMicro, 0.03),
		verifiedObs(uuid.New(), "", tierMicro, 0.03),
		verifiedObs(uuid.New(), "beauty", "", 0.03),
	}
	if cells := CorpusCells(obs, 1); len(cells) != 0 {
		t.Fatalf("unattributable rows entered the corpus: %+v", cells)
	}
}

// TestPercentileCont checks the interpolation matches the percentile_cont
// definition it replaced, so moving the aggregation out of SQL did not silently
// move the bands.
func TestPercentileCont(t *testing.T) {
	t.Parallel()

	xs := []float64{0, 1, 2, 3, 4} // sorted
	tests := []struct {
		q    float64
		want float64
	}{
		{0, 0},
		{0.25, 1},
		{0.5, 2},
		{0.75, 3},
		{0.9, 3.6}, // 0.9*(5-1) = 3.6 -> between 3 and 4
		{1, 4},
	}
	for _, tt := range tests {
		if got := percentileCont(xs, tt.q); !approx(got, tt.want) {
			t.Fatalf("percentileCont(%v) = %v, want %v", tt.q, got, tt.want)
		}
	}
	if got := percentileCont(nil, 0.5); got != 0 {
		t.Fatalf("empty = %v, want 0", got)
	}
	if got := percentileCont([]float64{7}, 0.9); got != 7 {
		t.Fatalf("single = %v, want 7", got)
	}
}
