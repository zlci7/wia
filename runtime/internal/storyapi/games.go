package storyapi

import (
	"net/http"
	"strings"
)

func (s *Server) gameRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method_not_allowed", "use GET")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/games/"), "/")
	if len(parts) == 1 {
		game, err := s.app.Game(parts[0])
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"game": game})
		return
	}
	if len(parts) == 2 && parts[1] == "cover" {
		body, mime, err := s.app.GameCover(parts[0], r.URL.Query().Get("revision"))
		if err != nil {
			writeAppError(w, err)
			return
		}
		serveCover(w, body, mime)
		return
	}
	writeError(w, 404, "not_found", "no such story resource")
}

func serveCover(w http.ResponseWriter, body []byte, mime string) {
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-cache")
	_, _ = w.Write(body)
}
