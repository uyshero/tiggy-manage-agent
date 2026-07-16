package httpapi

import (
	"net/http"

	"tiggy-manage-agent/internal/managedagents"
)

func (s *Server) getSessionTaskPlan(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(managedagents.SessionTaskPlanReader)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "task plan reads are not supported"})
		return
	}
	plan, err := store.GetCurrentSessionTaskPlanContext(r.Context(), r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Plan managedagents.SessionTaskPlan `json:"plan"`
	}{Plan: plan})
}

func (s *Server) listSessionTaskPlans(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(managedagents.SessionTaskPlanReader)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "task plan reads are not supported"})
		return
	}
	plans, err := store.ListSessionTaskPlansContext(r.Context(), r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Plans []managedagents.SessionTaskPlan `json:"plans"`
	}{Plans: nonNilSlice(plans)})
}
