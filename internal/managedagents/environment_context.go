package managedagents

import "context"

type environmentCreator interface {
	CreateEnvironment(input CreateEnvironmentInput) (Environment, error)
}

func CreateEnvironmentWithContext(ctx context.Context, store environmentCreator, input CreateEnvironmentInput) (Environment, error) {
	if scoped, ok := store.(EnvironmentContextStore); ok {
		return scoped.CreateEnvironmentContext(ctx, input)
	}
	return store.CreateEnvironment(input)
}
