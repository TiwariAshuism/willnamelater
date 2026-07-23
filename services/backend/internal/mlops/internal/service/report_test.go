package service

import (
	"encoding/json"
	"testing"

	"github.com/getnyx/influaudit/backend/internal/mlops/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// mv builds a fraud challenger with the given report and floor JSON.
func mv(report, floor string) model.Version {
	return model.Version{
		ModelName:        model.ModelFraud,
		Version:          "lgbm-x",
		Role:             model.RoleChallenger,
		ValidationReport: json.RawMessage(report),
		DataFloorCounts:  json.RawMessage(floor),
	}
}

// okFloor clears BOTH floors: the per-class row counts and the per-class DISTINCT
// CREATOR counts. The creator counts are the ones that matter — 61 positive rows
// from 3 creators would be 3 examples, not 61.
const okFloor = `{"positive":61,"negative":74,"positive_influencers":40,"negative_influencers":52,"floor":50}`

func TestValidatePromotableAllGatesPass(t *testing.T) {
	report := `{"g1_held_out":{"pass":true},"g2_stratified":{"pass":true},"g3_canary":{"pass":true},"g4_vs_champion":{"pass":true},"g6_beats_heuristic":{"pass":true}}`
	if err := validatePromotable(mv(report, okFloor), 3); err != nil {
		t.Fatalf("all gates passing must be promotable: %v", err)
	}
}

// An empty canary set is skipped-with-warning and still promotable (an honest cold
// start for a model that ALREADY has a champion — the first-champion case is
// blocked outright in PromoteModel).
func TestValidatePromotableCanarySkippedOnEmptySet(t *testing.T) {
	report := `{"g1_held_out":{"pass":true},"g3_canary":{"pass":false,"skipped":true},"g4_vs_champion":{"pass":true},"g6_beats_heuristic":{"pass":true}}`
	if err := validatePromotable(mv(report, okFloor), 0); err != nil {
		t.Fatalf("a skipped canary gate on an EMPTY set must still be promotable: %v", err)
	}
}

// The server, not the report, decides whether the canary set is empty: a report
// claiming G3 was skipped while canaries exist on file skipped a check it could
// have run, and must not promote.
func TestValidatePromotableCanarySkippedWithCanariesOnFileIsConflict(t *testing.T) {
	report := `{"g1_held_out":{"pass":true},"g3_canary":{"pass":false,"skipped":true},"g4_vs_champion":{"pass":true},"g6_beats_heuristic":{"pass":true}}`
	if err := validatePromotable(mv(report, okFloor), 2); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("a 'skipped' G3 with canaries on file must be a conflict, got %v", err)
	}
}

func TestValidatePromotableMissingG1(t *testing.T) {
	report := `{"g3_canary":{"pass":true},"g4_vs_champion":{"pass":true},"g6_beats_heuristic":{"pass":true}}`
	if err := validatePromotable(mv(report, okFloor), 1); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("a missing G1 must block promotion, got %v", err)
	}
}

func TestValidatePromotableCanaryFailsNotSkipped(t *testing.T) {
	report := `{"g1_held_out":{"pass":true},"g3_canary":{"pass":false,"skipped":false},"g4_vs_champion":{"pass":true},"g6_beats_heuristic":{"pass":true}}`
	if err := validatePromotable(mv(report, okFloor), 1); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("a failed (not skipped) canary gate must block promotion, got %v", err)
	}
}

// The shadow gate DOES NOT EXIST, and this test pins that honestly rather than
// pretending otherwise.
//
// The Python "g5" is a serving-SKEW check: it compares score distributions (PSI)
// and joins NO LABELS, so it can never say a challenger is more ACCURATE. It never
// even emitted its key, so the old Go check was a no-op over a nil field — a
// safety gate that had never once run. It is now structurally incapable of
// carrying a `pass`, and the Go report deliberately has no field for it.
//
// A stray g5 verdict in a stored report must therefore be INERT: it must neither
// grant nor block a promotion, because it is not evidence of anything. What blocks
// promotion is G6 — the challenger must beat the raw heuristic.
//
// The real label-joined arbiter (ml_prediction_log JOIN training_feature_row) is
// NOT BUILT. There is currently no evidence-of-accuracy-on-live-traffic gate.
func TestAStaleShadowVerdictIsInert(t *testing.T) {
	base := `"g1_held_out":{"pass":true},"g3_canary":{"pass":true},"g4_vs_champion":{"pass":true},` +
		`"g6_beats_heuristic":{"pass":true}`

	// A "failing" shadow verdict cannot block a challenger that passed the gates
	// that actually mean something: it never measured accuracy in the first place.
	failing := `{` + base + `,"g5_shadow":{"pass":false}}`
	if err := validatePromotable(mv(failing, okFloor), 1); err != nil {
		t.Fatalf("a skew verdict is not an accuracy verdict and must not block: %v", err)
	}

	// And a "passing" one cannot rescue a challenger that failed G6 — which is the
	// dangerous direction, and the one that used to be exploitable: a null
	// "plumbing" challenger scores identically to the champion, PSI ~= 0, and the
	// shadow gate would have waved it through with zero labels behind it.
	distillation := `{"g1_held_out":{"pass":true},"g3_canary":{"pass":true},` +
		`"g4_vs_champion":{"pass":true},"g6_beats_heuristic":{"pass":false},"g5_shadow":{"pass":true}}`
	if err := validatePromotable(mv(distillation, okFloor), 1); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("no skew verdict may rescue a challenger that lost to the heuristic, got %v", err)
	}
}

func TestValidateDataFloorReach(t *testing.T) {
	report := `{"g1_held_out":{"pass":true},"g3_canary":{"pass":true,"skipped":false},"g4_vs_champion":{"pass":true},"g6_beats_heuristic":{"pass":true}}`
	reachMV := model.Version{
		ModelName: model.ModelReach, Role: model.RoleChallenger,
		ValidationReport: json.RawMessage(report),
		DataFloorCounts:  json.RawMessage(`{"rows":250,"distinct_influencers":220,"floor":200}`),
	}
	if err := validatePromotable(reachMV, 1); err != nil {
		t.Fatalf("250 rows from 220 creators clears the floor: %v", err)
	}

	reachMV.DataFloorCounts = json.RawMessage(`{"rows":150,"distinct_influencers":140,"floor":200}`)
	if err := validatePromotable(reachMV, 1); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("150 reach rows is below the 200 floor, got %v", err)
	}
}

// THE floor that actually matters. Rows are keyed by audit_job_id and the same
// creator is re-audited on a schedule, so 250 rows can be 20 creators audited a
// dozen times each — and a model trained on that memorizes creators rather than
// learning to generalize, while G1 reports a beautiful, meaningless number.
//
// A row count that clears the row floor must still be REFUSED when it rests on too
// few distinct creators.
func TestValidateDataFloorReachRejectsAFewCreatorsAuditedRepeatedly(t *testing.T) {
	report := `{"g1_held_out":{"pass":true},"g3_canary":{"pass":true,"skipped":false},"g4_vs_champion":{"pass":true},"g6_beats_heuristic":{"pass":true}}`
	reachMV := model.Version{
		ModelName: model.ModelReach, Role: model.RoleChallenger,
		ValidationReport: json.RawMessage(report),
		// 250 rows — clears the row floor — from only 20 creators.
		DataFloorCounts: json.RawMessage(`{"rows":250,"distinct_influencers":20,"floor":200}`),
	}
	if err := validatePromotable(reachMV, 1); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("250 rows from 20 creators is not 250 independent examples, got %v", err)
	}

	// A trainer that cannot say how many creators it saw has not established that its
	// rows are independent. Missing is a failure, never a pass.
	reachMV.DataFloorCounts = json.RawMessage(`{"rows":250,"floor":200}`)
	if err := validatePromotable(reachMV, 1); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("an unstated creator count must fail the floor, got %v", err)
	}
}

// G6 is mandatory and unskippable: a challenger that cannot beat the raw heuristic
// has learned nothing the heuristic did not already assert. It is a distillation,
// and promoting it would put a version number on the same opinion and call it ML.
// An ABSENT gate is an unrun gate, and an unrun gate is not a passed one.
func TestValidatePromotableRequiresBeatingTheHeuristic(t *testing.T) {
	// Every other gate passes; g6 is simply absent.
	absent := `{"g1_held_out":{"pass":true},"g2_stratified":{"pass":true},"g3_canary":{"pass":true},"g4_vs_champion":{"pass":true}}`
	if err := validatePromotable(mv(absent, okFloor), 3); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("an absent g6 must refuse promotion, got %v", err)
	}

	failing := `{"g1_held_out":{"pass":true},"g2_stratified":{"pass":true},"g3_canary":{"pass":true},` +
		`"g4_vs_champion":{"pass":true},"g6_beats_heuristic":{"pass":false}}`
	if err := validatePromotable(mv(failing, okFloor), 3); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("a challenger that loses to the heuristic must not promote, got %v", err)
	}
}
