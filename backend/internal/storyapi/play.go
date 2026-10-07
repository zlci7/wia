package storyapi

import (
	"net/http"
	"strings"

	"gameagent/backend/internal/app"
)

func (s *Server) playRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/play-sessions")
	if path == "" {
		if r.Method != http.MethodPost {
			writeError(w, 405, "method_not_allowed", "play sessions are created with POST")
			return
		}
		var request app.PlayCreateRequest
		if !decodeJSON(w, r, &request) {
			return
		}
		view, err := s.app.CreatePlay(r.Context(), request)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, 201, view)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			view, err := s.app.ReadPlay(parts[0])
			if err != nil {
				writeAppError(w, err)
				return
			}
			writeJSON(w, 200, view)
		case http.MethodDelete:
			if err := s.app.ClosePlay(parts[0]); err != nil {
				writeAppError(w, err)
				return
			}
			w.WriteHeader(204)
		default:
			writeError(w, 405, "method_not_allowed", "play sessions use GET or DELETE")
		}
		return
	}
	if len(parts) == 2 && parts[1] == "turns" {
		if r.Method != http.MethodPost {
			writeError(w, 405, "method_not_allowed", "play turns use POST")
			return
		}
		var request app.PlayRequest
		if !decodeJSON(w, r, &request) {
			return
		}
		view, err := s.app.SubmitPlay(parts[0], request)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, 202, view)
		return
	}
	if len(parts) == 2 && parts[1] == "suggestions" {
		if r.Method != http.MethodPost {
			writeError(w, 405, "method_not_allowed", "suggestions use POST")
			return
		}
		var request struct {
			ExpectedTurn int64 `json:"expected_turn"`
			Enabled      bool  `json:"enabled"`
		}
		if !decodeJSON(w, r, &request) {
			return
		}
		view, err := s.app.RequestPlaySuggestions(parts[0], request.ExpectedTurn, request.Enabled)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, 202, view)
		return
	}
	if len(parts) == 4 && parts[1] == "turns" && parts[3] == "cancel" {
		if r.Method != http.MethodPost {
			writeError(w, 405, "method_not_allowed", "cancellation uses POST")
			return
		}
		view, err := s.app.CancelPlay(parts[0], parts[2])
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, 202, view)
		return
	}
	writeError(w, 404, "not_found", "play route not found")
}
