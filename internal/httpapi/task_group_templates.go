package httpapi

import (
	"net/http"

	"tiggy-manage-agent/internal/tools"
)

func (s *Server) listTaskGroupTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, tools.AgentTaskGroupTemplateListResponse{
		Templates: tools.ListAgentTaskGroupTemplates(),
	})
}
