package managedagents

import "context"

type objectRefCreator interface {
	CreateObjectRef(input CreateObjectRefInput) (ObjectRef, error)
}

type objectRefReader interface {
	GetObjectRef(id string) (ObjectRef, error)
}

type objectRefArtifactCounter interface {
	CountSessionArtifactsByObjectRef(objectRefID string) (int, error)
}

type objectRefDeleter interface {
	DeleteObjectRef(id string) error
}

type sessionArtifactCreator interface {
	CreateSessionArtifact(input CreateSessionArtifactInput) (SessionArtifact, error)
}

type sessionArtifactReader interface {
	GetSessionArtifact(sessionID string, artifactID string) (SessionArtifact, error)
}

type sessionArtifactDeleter interface {
	DeleteSessionArtifact(sessionID string, artifactID string) error
}

type sessionArtifactLister interface {
	ListSessionArtifacts(sessionID string) ([]SessionArtifact, error)
}

func CreateObjectRefWithContext(ctx context.Context, store objectRefCreator, input CreateObjectRefInput) (ObjectRef, error) {
	if scoped, ok := store.(ObjectArtifactContextStore); ok {
		return scoped.CreateObjectRefContext(ctx, input)
	}
	return store.CreateObjectRef(input)
}

func GetObjectRefWithContext(ctx context.Context, store objectRefReader, id string) (ObjectRef, error) {
	if scoped, ok := store.(ObjectArtifactContextStore); ok {
		return scoped.GetObjectRefContext(ctx, id)
	}
	return store.GetObjectRef(id)
}

func CountSessionArtifactsByObjectRefWithContext(ctx context.Context, store objectRefArtifactCounter, objectRefID string) (int, error) {
	if scoped, ok := store.(ObjectArtifactContextStore); ok {
		return scoped.CountSessionArtifactsByObjectRefContext(ctx, objectRefID)
	}
	return store.CountSessionArtifactsByObjectRef(objectRefID)
}

func DeleteObjectRefWithContext(ctx context.Context, store objectRefDeleter, id string) error {
	if scoped, ok := store.(ObjectArtifactContextStore); ok {
		return scoped.DeleteObjectRefContext(ctx, id)
	}
	return store.DeleteObjectRef(id)
}

func CreateSessionArtifactWithContext(ctx context.Context, store sessionArtifactCreator, input CreateSessionArtifactInput) (SessionArtifact, error) {
	if scoped, ok := store.(ObjectArtifactContextStore); ok {
		return scoped.CreateSessionArtifactContext(ctx, input)
	}
	return store.CreateSessionArtifact(input)
}

func GetSessionArtifactWithContext(ctx context.Context, store sessionArtifactReader, sessionID string, artifactID string) (SessionArtifact, error) {
	if scoped, ok := store.(ObjectArtifactContextStore); ok {
		return scoped.GetSessionArtifactContext(ctx, sessionID, artifactID)
	}
	return store.GetSessionArtifact(sessionID, artifactID)
}

func DeleteSessionArtifactWithContext(ctx context.Context, store sessionArtifactDeleter, sessionID string, artifactID string) error {
	if scoped, ok := store.(ObjectArtifactContextStore); ok {
		return scoped.DeleteSessionArtifactContext(ctx, sessionID, artifactID)
	}
	return store.DeleteSessionArtifact(sessionID, artifactID)
}

func ListSessionArtifactsWithContext(ctx context.Context, store sessionArtifactLister, sessionID string) ([]SessionArtifact, error) {
	if scoped, ok := store.(ObjectArtifactContextStore); ok {
		return scoped.ListSessionArtifactsContext(ctx, sessionID)
	}
	return store.ListSessionArtifacts(sessionID)
}
