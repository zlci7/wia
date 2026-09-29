package storyapi

import (
	"gameagent/backend/internal/storyapp"
	"net/http"
)

func (s *Server) suggestions(w http.ResponseWriter, r *http.Request, world string) {
	if r.Method == "GET" {
		set, err := s.app.ReadSuggestions(r.Context(), world)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, 200, set)
		return
	}
	if r.Method != "POST" && r.Method != "PUT" {
		writeError(w, 405, "method_not_allowed", "use GET, POST or PUT")
		return
	}
	var req storyapp.SuggestionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if (r.Method == "PUT") != (req.Enabled != nil) {
		writeError(w, 400, "invalid_request", "invalid preference")
		return
	}
	set, err := s.app.RequestSuggestions(r.Context(), world, req)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, 200, set)
}
