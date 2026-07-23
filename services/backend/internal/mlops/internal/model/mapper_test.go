package model

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/mlops/contract"
)

func evidenceOf(e contract.FraudLabelEvidence) *string {
	s := string(e)
	return &s
}

// labelledRow is a captured row carrying a fraud label with the given evidence.
func labelledRow(fraudulent bool, ev *string, reasons ...string) FeatureRow {
	return FeatureRow{
		AuditJobID:         uuid.New(),
		InfluencerID:       uuid.New(),
		Features:           json.RawMessage(`{"risk_score":80}`),
		FraudLabel:         &fraudulent,
		FraudLabelEvidence: ev,
		QualityReasons:     reasons,
	}
}

// THE anti-laundering guarantee, pinned at the only seam the trainer reads.
//
// A dispute exists only because the heuristic flagged the account, and the
// adjudicator sees the heuristic's own score — so "an admin agreed with the flag"
// carries no observation the heuristic had not already made. Exporting it as a y
// would train the model to predict its own opinion, and the G0-G5 gates cannot
// detect that: they check the model against the labels and ASSUME the labels are
// real.
//
// So the echo does not leave this process wearing a label. Note this row has NO
// quality reasons at all — TrainingEligible would happily pass it — which is
// exactly why the nulling lives here and not only in the eligibility rule.
func TestExportNullsAFraudLabelWithNoObservationBehindIt(t *testing.T) {
	t.Parallel()

	item := ToFeatureRowItem(labelledRow(true, evidenceOf(contract.EvidenceHeuristicOnly)))

	if item.FraudLabel != nil {
		t.Fatalf("a heuristic-echo label must be exported as UNLABELLED, got %v", *item.FraudLabel)
	}
	// The evidence still ships: the trainer must be able to see that the row exists
	// and why it carries no label, so a near-empty positive class reads as the honest
	// state of the world rather than as a bug.
	if item.FraudLabelEvidence != string(contract.EvidenceHeuristicOnly) {
		t.Fatalf("the evidence kind must survive the nulling, got %q", item.FraudLabelEvidence)
	}
}

// The absence of a stated observation is not an observation. A legacy row that
// predates the evidence enum carries a label and no evidence, and must be treated
// exactly like an echo — otherwise the laundering path reopens through the back
// door the moment anyone forgets to backfill.
func TestExportNullsAFraudLabelWithNoEvidenceRecorded(t *testing.T) {
	t.Parallel()

	if item := ToFeatureRowItem(labelledRow(true, nil)); item.FraudLabel != nil {
		t.Fatal("a label with no recorded evidence must be exported as UNLABELLED")
	}
}

// The rule is not "drop every label" — an OBSERVED one is real ground truth and
// must survive intact, or the fraud model can never train at all.
func TestExportKeepsAnEvidenceBackedFraudLabel(t *testing.T) {
	t.Parallel()

	for _, ev := range []contract.FraudLabelEvidence{
		contract.EvidencePlatformEnforcement,
		contract.EvidenceCreatorAdmission,
		contract.EvidencePurchaseReceipt,
		contract.EvidenceBrandConversionData,
		contract.EvidenceManualFollowerAudit,
	} {
		item := ToFeatureRowItem(labelledRow(true, evidenceOf(ev)))
		if item.FraudLabel == nil || !*item.FraudLabel {
			t.Fatalf("%s is an observation and its label must survive the export", ev)
		}
	}
}

// TrainingEligible is the OTHER half of the rule: it decides whether our own
// fraud-risk opinion may censor a row. It may be waived only by an observation —
// never by the bare presence of a label.
func TestTrainingEligibleWaivesFraudRiskOnlyForAnObservation(t *testing.T) {
	t.Parallel()

	fraudulent := true
	reasons := []string{ReasonFraudRiskHigh}

	if !TrainingEligible(reasons, &fraudulent, evidenceOf(contract.EvidenceManualFollowerAudit)) {
		t.Fatal("an evidence-backed label must not be censored by our own fraud estimate")
	}
	if TrainingEligible(reasons, &fraudulent, evidenceOf(contract.EvidenceHeuristicOnly)) {
		t.Fatal("a heuristic echo must NOT waive the fraud-risk exclusion")
	}
	if TrainingEligible(reasons, &fraudulent, nil) {
		t.Fatal("a label with no recorded observation must NOT waive the exclusion")
	}
	if TrainingEligible(reasons, nil, nil) {
		t.Fatal("an unlabelled high-risk row stays excluded (the anti-gaming filter)")
	}

	// An OBSERVED data-quality fact is never waived, whatever the label rests on:
	// it is a fact about the data, not our model's opinion of the account.
	observed := []string{"insufficient_posts"}
	if TrainingEligible(observed, &fraudulent, evidenceOf(contract.EvidenceManualFollowerAudit)) {
		t.Fatal("an observed data-quality exclusion must survive even an evidence-backed label")
	}
}
