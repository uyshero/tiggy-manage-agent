package managedagents

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPostgresArtifactExchangeClaimAndImportCompletion(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	workspaceID := createPostgresIntegrationWorkspace(t, store, "artifact-exchange")
	otherWorkspaceID := createPostgresIntegrationWorkspace(t, store, "artifact-exchange-other")
	suffix := strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", "")
	environment, err := store.CreateEnvironment(CreateEnvironmentInput{
		WorkspaceID: workspaceID, Name: "artifact-exchange-environment-" + suffix,
	})
	if err != nil {
		t.Fatalf("create exchange environment: %v", err)
	}
	agent, err := store.CreateAgent(CreateAgentInput{
		WorkspaceID: workspaceID, EnvironmentID: environment.ID,
		Name: "artifact-exchange-agent-" + suffix, Model: "test-model", System: "exchange test",
	})
	if err != nil {
		t.Fatalf("create exchange agent: %v", err)
	}
	session, err := store.CreateSession(CreateSessionInput{
		WorkspaceID: workspaceID, OwnerID: "exchange-owner", AgentID: agent.ID,
		EnvironmentID: environment.ID, CreatedBy: "exchange-owner",
	})
	if err != nil {
		t.Fatalf("create exchange session: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = store.db.ExecContext(cleanupCtx, `DELETE FROM artifact_exchanges WHERE workspace_id IN ($1, $2)`, workspaceID, otherWorkspaceID)
		_, _ = store.db.ExecContext(cleanupCtx, `DELETE FROM session_artifacts WHERE session_id = $1`, session.ID)
		_, _ = store.db.ExecContext(cleanupCtx, `DELETE FROM object_refs WHERE workspace_id = $1 AND object_key LIKE $2`, workspaceID, "%/imports/%")
		_, _ = store.db.ExecContext(cleanupCtx, `DELETE FROM sessions WHERE id = $1`, session.ID)
		_, _ = store.db.ExecContext(cleanupCtx, `DELETE FROM agents WHERE id = $1`, agent.ID)
		_, _ = store.db.ExecContext(cleanupCtx, `DELETE FROM environments WHERE id = $1`, environment.ID)
	})

	ctx, err := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: workspaceID, OwnerID: "exchange-owner"})
	if err != nil {
		t.Fatal(err)
	}
	otherCtx, err := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: otherWorkspaceID, OwnerID: "exchange-owner"})
	if err != nil {
		t.Fatal(err)
	}
	tokenHash := sha256.Sum256([]byte("integration-exchange-token"))
	expectedSize := int64(7)
	created, err := store.CreateArtifactExchangeContext(ctx, CreateArtifactExchangeInput{
		WorkspaceID: workspaceID, OwnerID: "exchange-owner",
		Direction: ArtifactExchangeDirectionImport, SessionID: session.ID,
		Filename: "result.txt", ArtifactType: ArtifactTypeFile, Visibility: ObjectVisibilitySession,
		ContentType: "text/plain", ExpectedSizeBytes: &expectedSize, MaxSizeBytes: expectedSize,
		TokenHash: tokenHash[:], ExpiresAt: time.Now().UTC().Add(time.Minute), CreatedBy: "exchange-owner",
	})
	if err != nil {
		t.Fatalf("create artifact exchange: %v", err)
	}
	if created.Status != ArtifactExchangeStatusPending || created.SessionID != session.ID || created.ExpectedSizeBytes == nil || *created.ExpectedSizeBytes != expectedSize {
		t.Fatalf("unexpected created exchange: %+v", created)
	}
	if _, err := store.GetArtifactExchangeContext(otherCtx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace exchange lookup returned %v", err)
	}

	claimedAt := time.Now().UTC()
	claimed, err := store.ClaimArtifactExchangeContext(ctx, ClaimArtifactExchangeInput{
		WorkspaceID: workspaceID, ID: created.ID, Direction: ArtifactExchangeDirectionImport,
		TokenHash: tokenHash[:], ClaimedAt: claimedAt,
	})
	if err != nil || claimed.Status != ArtifactExchangeStatusProcessing || claimed.ClaimedAt == nil {
		t.Fatalf("claim artifact exchange: exchange=%+v err=%v", claimed, err)
	}
	if _, err := store.ClaimArtifactExchangeContext(ctx, ClaimArtifactExchangeInput{
		WorkspaceID: workspaceID, ID: created.ID, Direction: ArtifactExchangeDirectionImport,
		TokenHash: tokenHash[:], ClaimedAt: claimedAt.Add(time.Millisecond),
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replayed exchange claim returned %v", err)
	}

	completed, objectRef, artifact, err := store.CompleteArtifactImportContext(ctx, CompleteArtifactImportInput{
		WorkspaceID: workspaceID, ID: created.ID, CompletedAt: time.Now().UTC(),
		ObjectRef: CreateObjectRefInput{
			StorageProvider: ObjectStorageProviderS3, Bucket: "integration-artifacts",
			ObjectKey:   workspaceID + "/" + session.ID + "/imports/" + created.ID + "-result.txt",
			ContentType: "text/plain", SizeBytes: expectedSize,
			ChecksumSHA256: strings.Repeat("a", 64), Visibility: ObjectVisibilitySession,
		},
		Artifact: CreateSessionArtifactInput{ArtifactType: ArtifactTypeFile},
	})
	if err != nil {
		t.Fatalf("complete artifact import: %v", err)
	}
	if completed.Status != ArtifactExchangeStatusCompleted || completed.ObjectRefID != objectRef.ID || completed.ArtifactID != artifact.ID || artifact.SessionID != session.ID {
		t.Fatalf("unexpected completed exchange: exchange=%+v object=%+v artifact=%+v", completed, objectRef, artifact)
	}
}
