package managedagents

import "testing"

func TestValidateEvaluationDatasetItemsAndSummary(t *testing.T) {
	items, err := ValidateEvaluationDatasetItems([]CreateEvaluationDatasetItemInput{{
		Prompt: "  Explain RLS.  ", ExpectedOutput: "  Tenant isolation. ", Tags: []string{"security", "security"},
	}})
	if err != nil {
		t.Fatalf("validate dataset items: %v", err)
	}
	if items[0].Prompt != "Explain RLS." || items[0].ExpectedOutput != "Tenant isolation." || len(items[0].Tags) != 1 {
		t.Fatalf("unexpected normalized items: %+v", items)
	}
	if _, err := ValidateEvaluationDatasetItems(nil); err == nil {
		t.Fatal("expected empty dataset validation error")
	}

	summary := SummarizeEvaluationExperiment([]EvaluationExperimentItem{
		{Status: EvaluationExperimentItemStatusCompleted, Conclusion: EvaluationConclusionLeft, LeftAverage: 4, RightAverage: 3},
		{Status: EvaluationExperimentItemStatusCompleted, Conclusion: EvaluationConclusionTie, LeftAverage: 5, RightAverage: 5},
		{Status: EvaluationExperimentItemStatusFailed},
		{Status: EvaluationExperimentItemStatusRunning},
	})
	if summary.Total != 4 || summary.Completed != 2 || summary.Failed != 1 || summary.Running != 1 || summary.LeftWins != 1 || summary.Ties != 1 || summary.LeftAverage != 4.5 || summary.RightAverage != 4 {
		t.Fatalf("unexpected experiment summary: %+v", summary)
	}
}
