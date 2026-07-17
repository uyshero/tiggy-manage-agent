package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"tiggy-manage-agent/internal/agenteval"
)

func main() {
	fixtures := flag.String("fixtures", "testdata/agent-quality/completion-gate.json", "path to the deterministic agent quality suite")
	flag.Parse()

	suite, err := agenteval.LoadSuite(*fixtures)
	if err != nil {
		fatal(err)
	}
	report, err := agenteval.Evaluate(context.Background(), suite)
	if err != nil {
		fatal(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fatal(fmt.Errorf("write agent quality report: %w", err))
	}
	if !report.Passed {
		os.Exit(1)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "agent quality evaluation: %v\n", err)
	os.Exit(2)
}
