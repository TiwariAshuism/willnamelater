package service

import (
	"encoding/json"

	"github.com/getnyx/influaudit/backend/internal/mlops/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Data-floor constants (the honest cold-start gate, G0). They are the server-side
// authority: promotion re-checks the challenger's recorded counts against these,
// so nothing is ever promoted below the floor no matter what the trainer emitted.
const (
	// floorFraudPerClass is the minimum labelled rows per class (positive and
	// negative) a fraud challenger needs.
	floorFraudPerClass = 50
	// floorReachRows is the minimum reach-labelled rows a reach challenger needs.
	floorReachRows = 200
)

// gateReport is the subset of validation_report_jsonb the promote endpoint
// re-checks: each required gate's pass verdict (G3 may instead be skipped on an
// empty canary set). It validates the report's recorded verdicts — it does not
// recompute model metrics, which is the trainer's job over real data.
type gateReport struct {
	G1HeldOut    *gateVerdict `json:"g1_held_out"`
	G2Stratified *gateVerdict `json:"g2_stratified"`
	G3Canary     *gateVerdict `json:"g3_canary"`
	G4VsChampion *gateVerdict `json:"g4_vs_champion"`
	G5Shadow     *gateVerdict `json:"g5_shadow"`
}

// gateVerdict is one gate's recorded outcome. Skipped is meaningful only for G3
// (an empty canary set is skipped-with-warning, an honest cold start).
type gateVerdict struct {
	Pass    bool `json:"pass"`
	Skipped bool `json:"skipped"`
}

// dataFloorCounts is the subset of data_floor_counts the floor check reads. Fraud
// carries Positive/Negative; reach carries Rows.
type dataFloorCounts struct {
	Positive *int `json:"positive"`
	Negative *int `json:"negative"`
	Rows     *int `json:"rows"`
}

// validatePromotable re-checks a challenger's recorded gate report and data floor
// before a promotion. A rollback (promoting an archived former champion) waives
// every gate — it already earned them when it was champion. overrideShadow waives
// only the shadow gate, for an emergency promotion. Any failing or absent
// required gate is a conflict, so a version whose report does not honestly show
// the gates passing can never be promoted.
func validatePromotable(mv model.Version, isRollback, overrideShadow bool) error {
	if isRollback {
		return nil
	}

	if err := validateDataFloor(mv.ModelName, mv.DataFloorCounts); err != nil {
		return err
	}

	var report gateReport
	if err := json.Unmarshal(nonNilJSON(mv.ValidationReport), &report); err != nil {
		return errs.Wrap(err, errs.KindInvalid, "mlops.report_unreadable", "the stored validation report could not be read")
	}

	// G1 held-out and G4 challenger-vs-champion are unconditionally required. For a
	// model's first champion the trainer records G4 as an auto-pass (pass=true), so
	// the same check covers it.
	if !passed(report.G1HeldOut) {
		return errGateNotPassed("g1_held_out")
	}
	if !passed(report.G4VsChampion) {
		return errGateNotPassed("g4_vs_champion")
	}
	// G3 canary must pass, unless it was skipped on an empty canary set.
	if report.G3Canary == nil || (!report.G3Canary.Pass && !report.G3Canary.Skipped) {
		return errGateNotPassed("g3_canary")
	}
	// G2 stratified, when present, must show no regressing stratum.
	if report.G2Stratified != nil && !report.G2Stratified.Pass {
		return errGateNotPassed("g2_stratified")
	}
	// G5 shadow, when present, must pass unless explicitly overridden.
	if !overrideShadow && report.G5Shadow != nil && !report.G5Shadow.Pass {
		return errGateNotPassed("g5_shadow")
	}
	return nil
}

// validateDataFloor enforces G0 server-side: the challenger's recorded counts
// must meet the floor for its model. Missing counts are a floor failure, not a
// pass — nothing is promoted on unproven data.
func validateDataFloor(modelName string, raw json.RawMessage) error {
	var counts dataFloorCounts
	if err := json.Unmarshal(nonNilJSON(raw), &counts); err != nil {
		return errs.Wrap(err, errs.KindInvalid, "mlops.floor_unreadable", "the stored data-floor counts could not be read")
	}
	switch modelName {
	case model.ModelFraud:
		if counts.Positive == nil || counts.Negative == nil ||
			*counts.Positive < floorFraudPerClass || *counts.Negative < floorFraudPerClass {
			return errs.New(errs.KindConflict, "mlops.data_floor_not_met",
				"the fraud challenger is below the per-class data floor")
		}
	case model.ModelReach:
		if counts.Rows == nil || *counts.Rows < floorReachRows {
			return errs.New(errs.KindConflict, "mlops.data_floor_not_met",
				"the reach challenger is below the row data floor")
		}
	}
	return nil
}

// passed reports whether a required gate is present and passing.
func passed(v *gateVerdict) bool {
	return v != nil && v.Pass
}

// nonNilJSON returns raw, or a JSON null literal when raw is nil, so Unmarshal
// never fails on an absent field.
func nonNilJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("null")
	}
	return raw
}

// errGateNotPassed is the conflict returned when a required gate did not pass.
func errGateNotPassed(gate string) error {
	return errs.New(errs.KindConflict, "mlops.gate_not_passed",
		"cannot promote: gate "+gate+" is not recorded as passing")
}
