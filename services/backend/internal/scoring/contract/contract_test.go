package contract

import (
	"encoding/json"
	"testing"
)

// TestSubscoreDecodesLegacyConfidence pins the migration path for score rows
// persisted before Basis/Support existed: the legacy "confidence" number becomes
// Support, and the basis and support kind stay EMPTY. The old row genuinely does
// not record what produced its value or what the number meant, and back-filling a
// label here would invent provenance nobody stamped.
func TestSubscoreDecodesLegacyConfidence(t *testing.T) {
	t.Parallel()

	var s Subscore
	if err := json.Unmarshal([]byte(`{"value":62.5,"confidence":0.4}`), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Value != 62.5 || s.Support != 0.4 {
		t.Fatalf("legacy row = %+v, want value 62.5 support 0.4", s)
	}
	if s.Basis != "" || s.SupportKind != "" {
		t.Fatalf("legacy row invented provenance: basis=%q kind=%q", s.Basis, s.SupportKind)
	}
}

// TestSubscoreRoundTrips checks the current shape survives the jsonb column.
func TestSubscoreRoundTrips(t *testing.T) {
	t.Parallel()

	in := Subscore{Value: 71, Basis: BasisCorpus, Support: 0.6, SupportKind: SupportConfidence}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Subscore
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("round trip = %+v, want %+v", out, in)
	}
	// A support of 0 must survive as 0, not vanish: "no support" is a claim.
	unsupported := Subscore{Value: 50, Basis: BasisClosedForm, Support: 0, SupportKind: SupportNone}
	raw, err = json.Marshal(unsupported)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != unsupported {
		t.Fatalf("zero-support round trip = %+v, want %+v", out, unsupported)
	}
}

// TestModelBasis checks a champion's basis names its version, and that an
// unversioned champion still declares that a MODEL produced the number rather than
// passing itself off as a closed form.
func TestModelBasis(t *testing.T) {
	t.Parallel()

	if got := ModelBasis("fraud-2026-06-01"); got != "model:fraud-2026-06-01" {
		t.Fatalf("ModelBasis = %q", got)
	}
	if got := ModelBasis(""); got != "model:unversioned" {
		t.Fatalf("ModelBasis(\"\") = %q, want model:unversioned", got)
	}
}
