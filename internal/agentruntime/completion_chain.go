package agentruntime

import "context"

// CompletionGateChain runs validators in order and returns the first verdict
// that does not pass.
type CompletionGateChain struct {
	Gates []CompletionGate
}

func (chain CompletionGateChain) Validate(ctx context.Context, candidate CompletionCandidate) (CompletionVerdict, error) {
	var last CompletionVerdict
	for _, gate := range chain.Gates {
		if gate == nil {
			continue
		}
		verdict, err := gate.Validate(ctx, candidate)
		if err != nil {
			return verdict, err
		}
		last = verdict
		if verdict.Outcome != CompletionOutcomePass {
			return verdict, nil
		}
	}
	if last.Outcome == "" {
		return CompletionVerdict{Outcome: CompletionOutcomePass, Validator: "builtin.chain"}, nil
	}
	return last, nil
}
