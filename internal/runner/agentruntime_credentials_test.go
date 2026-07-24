package runner

import "testing"

func TestLLMAPIKeyPrefersManagedEnvironmentAndFallsBackToProcessEnvironment(t *testing.T) {
	const envName = "TMA_TEST_RUNTIME_LLM_KEY"
	t.Setenv(envName, "process-key")

	if got := llmAPIKey(envName, map[string]string{envName: "managed-key"}); got != "managed-key" {
		t.Fatalf("managed environment key = %q, want managed-key", got)
	}
	if got := llmAPIKey(envName, nil); got != "process-key" {
		t.Fatalf("process environment fallback = %q, want process-key", got)
	}
	if got := llmAPIKey("", map[string]string{envName: "managed-key"}); got != "" {
		t.Fatalf("empty credential reference resolved to %q", got)
	}
}
