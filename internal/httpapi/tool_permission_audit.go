package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

type toolPermissionAuditRecord = managedagents.ToolPermissionAuditRecord

type toolPermissionAuditCursor struct {
	Version     int       `json:"v"`
	Resource    string    `json:"resource"`
	CreatedAt   time.Time `json:"created_at"`
	TurnID      string    `json:"turn_id"`
	CallID      string    `json:"call_id"`
	Fingerprint string    `json:"fingerprint"`
}

func (s *Server) listSessionToolPermissionAudit(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("session_id"))
	decisionFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("decision")))
	switch decisionFilter {
	case "", "allow", "ask", "deny":
	default:
		writeError(w, fmt.Errorf("%w: decision must be allow, ask, or deny", managedagents.ErrInvalid))
		return
	}
	limit := 50
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil || parsed < 1 || parsed > 200 {
			writeError(w, fmt.Errorf("%w: limit must be between 1 and 200", managedagents.ErrInvalid))
			return
		}
		limit = parsed
	}
	toolFilter := strings.TrimSpace(r.URL.Query().Get("tool"))
	cursor, err := parseToolPermissionAuditCursor(r, sessionID, decisionFilter, toolFilter)
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid tool permission audit cursor: %v", managedagents.ErrInvalid, err))
		return
	}
	reader, ok := s.store.(managedagents.ToolPermissionAuditReader)
	if !ok {
		writeError(w, fmt.Errorf("%w: tool permission audit read model is not supported", managedagents.ErrInvalid))
		return
	}
	input := managedagents.ListToolPermissionAuditInput{
		SessionID: sessionID, Decision: decisionFilter, Tool: toolFilter, Limit: limit + 1,
	}
	if cursor != nil {
		input.Before = &cursor.CreatedAt
		input.BeforeTurnID = cursor.TurnID
		input.BeforeCallID = cursor.CallID
	}
	records, err := reader.ListToolPermissionAuditContext(r.Context(), input)
	if err != nil {
		writeError(w, err)
		return
	}
	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"records":     nonNilSlice(records),
		"next_cursor": nextToolPermissionAuditCursor(sessionID, decisionFilter, toolFilter, records, hasMore),
		"has_more":    hasMore,
	})
}

func projectToolPermissionAudit(events []managedagents.Event) []toolPermissionAuditRecord {
	return managedagents.ProjectToolPermissionAudit(events)
}

func parseToolPermissionAuditCursor(r *http.Request, sessionID, decision, tool string) (*toolPermissionAuditCursor, error) {
	value := strings.TrimSpace(r.URL.Query().Get("cursor"))
	if value == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, errors.New("cursor is not valid base64url")
	}
	var cursor toolPermissionAuditCursor
	if json.Unmarshal(raw, &cursor) != nil {
		return nil, errors.New("cursor payload is invalid")
	}
	if cursor.Version != 1 || cursor.Resource != "tool_permission_audit" || cursor.CreatedAt.IsZero() || strings.TrimSpace(cursor.TurnID) == "" || strings.TrimSpace(cursor.CallID) == "" {
		return nil, errors.New("cursor is not valid for tool permission audit")
	}
	if cursor.Fingerprint != toolPermissionAuditFingerprint(sessionID, decision, tool) {
		return nil, errors.New("cursor does not match the current filters")
	}
	return &cursor, nil
}

func nextToolPermissionAuditCursor(sessionID, decision, tool string, records []toolPermissionAuditRecord, hasMore bool) string {
	if !hasMore || len(records) == 0 {
		return ""
	}
	last := records[len(records)-1]
	cursor := toolPermissionAuditCursor{
		Version: 1, Resource: "tool_permission_audit", CreatedAt: last.CreatedAt,
		TurnID: last.TurnID, CallID: last.CallID,
		Fingerprint: toolPermissionAuditFingerprint(sessionID, decision, tool),
	}
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func toolPermissionAuditFingerprint(sessionID, decision, tool string) string {
	digest := sha256.Sum256([]byte(sessionID + "\n" + decision + "\n" + tool))
	return hex.EncodeToString(digest[:16])
}
