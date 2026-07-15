package managedagents

import "context"

func (s *PostgresStore) CreateEnvironmentContext(ctx context.Context, input CreateEnvironmentInput) (Environment, error) {
	return s.createEnvironmentContext(ctx, input)
}
