package managedagents

import "context"

type environmentCreator interface {
	CreateEnvironment(input CreateEnvironmentInput) (Environment, error)
}

type environmentReader interface {
	GetEnvironment(id string) (Environment, error)
}

type environmentLister interface {
	ListEnvironments() ([]Environment, error)
}

func CreateEnvironmentWithContext(ctx context.Context, store environmentCreator, input CreateEnvironmentInput) (Environment, error) {
	if scoped, ok := store.(EnvironmentContextStore); ok {
		return scoped.CreateEnvironmentContext(ctx, input)
	}
	return store.CreateEnvironment(input)
}

func GetEnvironmentWithContext(ctx context.Context, store any, id string) (Environment, error) {
	if scoped, ok := store.(EnvironmentContextStore); ok {
		return scoped.GetEnvironmentContext(ctx, id)
	}
	if reader, ok := store.(environmentReader); ok {
		return reader.GetEnvironment(id)
	}
	return Environment{}, ErrInvalid
}

func ListEnvironmentsWithContext(ctx context.Context, store any) ([]Environment, error) {
	if scoped, ok := store.(EnvironmentContextStore); ok {
		return scoped.ListEnvironmentsContext(ctx)
	}
	if lister, ok := store.(environmentLister); ok {
		return lister.ListEnvironments()
	}
	return nil, ErrInvalid
}
