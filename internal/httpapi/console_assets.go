package httpapi

import "net/http"

func (s *Server) getConsole(w http.ResponseWriter, _ *http.Request) {
	content, err := inspectorAssets.ReadFile("console/index.html")
	if err != nil {
		s.logger.Error("console index read failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "console unavailable"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(content); err != nil {
		s.logger.Warn("console response write failed", "error", err)
	}
}
