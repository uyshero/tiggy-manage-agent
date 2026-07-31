package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
)

func (s *Server) newModelInvocationInput(r *http.Request, provider managedagents.LLMProvider, model managedagents.LLMModel, capability string, startedAt time.Time) managedagents.RecordModelInvocationInput {
	workspaceID := requestWorkspaceID(r, managedagents.DefaultWorkspaceID)
	principalID := "local-development"
	serviceIdentityID := ""
	authType := AuthModeDisabled
	if principal, ok := PrincipalFromRequest(r); ok {
		principalID = principal.Subject
		authType = principal.AuthType
		serviceIdentityID = principal.ServiceIdentityID
	}
	return managedagents.RecordModelInvocationInput{
		WorkspaceID: workspaceID, PrincipalID: principalID, ServiceIdentityID: serviceIdentityID, AuthType: authType,
		RequestID: requestIDFromRequest(r), Capability: capability,
		ProviderID: provider.ID, ProviderType: provider.ProviderType, Model: model.Model,
		StartedAt: startedAt,
	}
}

func (s *Server) recordModelInvocation(ctxRequest *http.Request, input managedagents.RecordModelInvocationInput) {
	store, ok := s.store.(managedagents.ModelInvocationStore)
	if !ok {
		s.logger.Error("model invocation store is unavailable", "request_id", input.RequestID, "capability", input.Capability)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctxRequest.Context()), 3*time.Second)
	defer cancel()
	if _, err := store.RecordModelInvocationContext(ctx, input); err != nil {
		s.logger.Error("record model invocation failed", "request_id", input.RequestID, "capability", input.Capability, "error", err)
	}
}

func completeModelInvocation(input *managedagents.RecordModelInvocationInput, status string, errorCode string) {
	completedAt := time.Now().UTC()
	input.Status = status
	input.ErrorCode = strings.TrimSpace(errorCode)
	input.CompletedAt = completedAt
	input.LatencyMillis = completedAt.Sub(input.StartedAt).Milliseconds()
}

func failedModelInvocationCode(err error) string {
	var providerError *llm.ProviderError
	if errors.As(err, &providerError) && providerError.Class != "" {
		return "provider_" + string(providerError.Class)
	}
	return "model_provider_error"
}

func modelMessagesCharacterCount(messages []llm.Message) int64 {
	var count int64
	for _, message := range messages {
		for _, part := range message.Content {
			count += int64(utf8.RuneCountInString(part.Text))
		}
	}
	return count
}

func stringsCharacterCount(values []string) int64 {
	var count int64
	for _, value := range values {
		count += int64(utf8.RuneCountInString(value))
	}
	return count
}
