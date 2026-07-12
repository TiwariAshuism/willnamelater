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
	// floorReachInfluencers is the floor that actually matters: DISTINCT CREATORS.
	//
	// Counting rows is the bug. Rows are keyed by audit_job_id and the same creator
	// is re-audited on a schedule, so 200 rows can be 20 creators audited ten times —
	// and the model then memorizes creators rather than learning to generalize. The
	// trainer's split is grouped by influencer; this is the server-side mirror of the
	// same rule, so a challenger cannot be registered against a panel that only looks
	// large.
	floorReachInfluencers = 200
	// floorFraudInfluencersPerClass is the same rule for the fraud model.
	floorFraudInfluencersPerClass = 25
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
	// G6BeatsHeuristic is MANDATORY and has no skip: a challenger that cannot beat
	// the raw heuristic score on held-out rows has learned nothing the heuristic did
	// not already assert. It is a DISTILLATION, not a model, and promoting it would
	// put a version number on the same opinion and call it ML.
	//
	// Absent => refuse. A trainer that did not record the gate did not run it, and an
	// unrun gate is not a passed one.
	G6BeatsHeuristic *gateVerdict `json:"g6_beats_heuristic"`

	// NOTE: there is deliberately no G5 field. The Python "g5" is a SERVING-SKEW
	// check (a PSI comparison that joins NO LABELS), and it carries no `pass` key
	// precisely so it cannot express an accuracy verdict. Unmarshalling it into a
	// gateVerdict would yield Pass:false and block every promotion. The real
	// label-joined arbiter — ml_prediction_log JOIN training_feature_row ON
	// audit_job_id — IS NOT BUILT. There is currently no evidence-of-accuracy-on-
	// live-traffic gate anywhere in this system, and pretending otherwise is worse
	// than admitting it.
}

// gateVerdict is one gate's recorded outcome. Skipped is meaningful only for G3
// (an empty canary set is skipped-with-warning, an honest cold start).
type gateVerdict struct {
	Pass    bool `json:"pass"`
	Skipped bool `json:"skipped"`
}

// dataFloorCounts is the subset of data_floor_counts the floor check reads. Fraud
// carries Positive/Negative; reach carries Rows.
// The *Influencers counts are what the floor actually rests on. Rows are keyed by
// audit_job_id and the same creator is re-audited on a schedule, so a row count
// says nothing about how many INDEPENDENT examples exist. A nil count is a floor
// failure, never a pass.
type dataFloorCounts struct {
	Positive            *int `json:"positive"`
	Negative            *int `json:"negative"`
	PositiveInfluencers *int `json:"positive_influencers"`
	NegativeInfluencers *int `json:"negative_influencers"`
	Rows                *int `json:"rows"`
	DistinctInfluencers *int `json:"distinct_influencers"`
}

// validatePromotable re-checks a challenger's recorded gate report and data floor
// before a promotion. The caller has already established this is not a rollback
// (a rollback — promoting an archived version that previously served as champion,
// promoted_at set — waives every gate, because it earned them when it was
// champion; a never-promoted archived version is not a rollback and lands here).
// overrideShadow waives only the shadow gate, for an emergency promotion. Any
// failing or absent required gate is a conflict, so a version whose report does
// not honestly show the gates passing can never be promoted.
//
// activeCanaries is the model's live canary count, which the trainer cannot be
// trusted to have reported: a G3 'skipped' verdict is only acceptable when the
// canary set is ACTUALLY empty. With canaries on file, a report claiming the gate
// was skipped is a report that skipped a check it could have run.
// NOTE ON override_shadow: it now waives NOTHING, and that is the honest state.
// It existed to waive a "G5 shadow" gate that, on inspection, never existed: the
// Python g5 joins no labels (it is a PSI serving-skew check) and the trainer never
// even emitted the key, so the Go check had always been a no-op over a nil field.
// The lever is kept on the API for the trainer's CLI, but it is deliberately not
// consulted here — a flag that appears to waive a safety gate, while the gate it
// names does not exist, is worse than no flag at all. When the real label-joined
// arbiter is built, wire it in explicitly and give it its own override.
func validatePromotable(mv model.Version, activeCanaries int) error {
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
	// G3 canary must pass. A 'skipped' verdict is honest only on an empty canary set
	// — and the SERVER, not the report, decides whether the set is empty.
	if report.G3Canary == nil {
		return errGateNotPassed("g3_canary")
	}
	honestlySkipped := report.G3Canary.Skipped && activeCanaries == 0
	if !report.G3Canary.Pass && !honestlySkipped {
		return errGateNotPassed("g3_canary")
	}
	// G2 stratified, when present, must show no regressing stratum.
	if report.G2Stratified != nil && !report.G2Stratified.Pass {
		return errGateNotPassed("g2_stratified")
	}
	// G6 must beat the raw heuristic. Unconditionally required, never skippable, and
	// NOT waivable by overrideShadow: the emergency lever exists for a shadow window
	// we could not run, not for a model that failed to prove it is a model.
	if !passed(report.G6BeatsHeuristic) {
		return errGateNotPassed("g6_beats_heuristic")
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
		// The counts must also rest on enough DISTINCT CREATORS. A missing count is a
		// failure, not a pass: a trainer that cannot say how many creators it saw has
		// not established that its rows are independent examples.
		if counts.PositiveInfluencers == nil || counts.NegativeInfluencers == nil ||
			*counts.PositiveInfluencers < floorFraudInfluencersPerClass ||
			*counts.NegativeInfluencers < floorFraudInfluencersPerClass {
			return errs.New(errs.KindConflict, "mlops.data_floor_not_met",
				"the fraud challenger is below the per-class distinct-creator floor")
		}
	case model.ModelReach:
		if counts.Rows == nil || *counts.Rows < floorReachRows {
			return errs.New(errs.KindConflict, "mlops.data_floor_not_met",
				"the reach challenger is below the row data floor")
		}
		if counts.DistinctInfluencers == nil || *counts.DistinctInfluencers < floorReachInfluencers {
			return errs.New(errs.KindConflict, "mlops.data_floor_not_met",
				"the reach challenger is below the distinct-creator floor")
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
