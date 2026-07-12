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

const okFloor = `{"positive":61,"negative":74,"floor":50}`

func TestValidatePromotableAllGatesPass(t *testing.T) {
	report := `{"g1_held_out":{"pass":true},"g2_stratified":{"pass":true},"g3_canary":{"pass":true},"g4_vs_champion":{"pass":true}}`
	if err := validatePromotable(mv(report, okFloor), false, false); err != nil {
		t.Fatalf("all gates passing must be promotable: %v", err)
	}
}

// An empty canary set is skipped-with-warning and still promotable (honest cold
// start), covered by the skipped flag.
func TestValidatePromotableCanarySkipped(t *testing.T) {
	report := `{"g1_held_out":{"pass":true},"g3_canary":{"pass":false,"skipped":true},"g4_vs_champion":{"pass":true}}`
	if err := validatePromotable(mv(report, okFloor), false, false); err != nil {
		t.Fatalf("a skipped canary gate must still be promotable: %v", err)
	}
}

func TestValidatePromotableMissingG1(t *testing.T) {
	report := `{"g3_canary":{"pass":true},"g4_vs_champion":{"pass":true}}`
	if err := validatePromotable(mv(report, okFloor), false, false); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("a missing G1 must block promotion, got %v", err)
	}
}

func TestValidatePromotableCanaryFailsNotSkipped(t *testing.T) {
	report := `{"g1_held_out":{"pass":true},"g3_canary":{"pass":false,"skipped":false},"g4_vs_champion":{"pass":true}}`
	if err := validatePromotable(mv(report, okFloor), false, false); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("a failed (not skipped) canary gate must block promotion, got %v", err)
	}
}

func TestValidatePromotableShadowFailsBlocksUnlessOverride(t *testing.T) {
	report := `{"g1_held_out":{"pass":true},"g3_canary":{"pass":true},"g4_vs_champion":{"pass":true},"g5_shadow":{"pass":false}}`
	if err := validatePromotable(mv(report, okFloor), false, false); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("a failed shadow gate must block promotion, got %v", err)
	}
	if err := validatePromotable(mv(report, okFloor), false, true); err != nil {
		t.Fatalf("override_shadow must waive the shadow gate: %v", err)
	}
}

func TestValidatePromotableRollbackWaivesEverything(t *testing.T) {
	if err := validatePromotable(mv(`{}`, `{}`), true, false); err != nil {
		t.Fatalf("a rollback must waive every gate and the floor: %v", err)
	}
}

func TestValidateDataFloorReach(t *testing.T) {
	report := `{"g1_held_out":{"pass":true},"g3_canary":{"pass":true,"skipped":false},"g4_vs_champion":{"pass":true}}`
	reachMV := model.Version{
		ModelName: model.ModelReach, Role: model.RoleChallenger,
		ValidationReport: json.RawMessage(report), DataFloorCounts: json.RawMessage(`{"rows":250,"floor":200}`),
	}
	if err := validatePromotable(reachMV, false, false); err != nil {
		t.Fatalf("250 reach rows clears the 200 floor: %v", err)
	}

	reachMV.DataFloorCounts = json.RawMessage(`{"rows":150,"floor":200}`)
	if err := validatePromotable(reachMV, false, false); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("150 reach rows is below the 200 floor, got %v", err)
	}
}
