package storyapi

import (
	"net/http"
	"strings"

	"gameagent/backend/internal/storyapp"
)

// Promotion routes keep the same split the service enforces: the ordinary preview
// only carries public facts, while the private experience needs the explicit author
// view, and confirming needs the world's context epoch.
func (s *Server) characterPromotions(w http.ResponseWriter, r *http.Request, world string) {
	switch r.Method {
	case "GET":
		bystander := r.URL.Query().Get("bystander_id")
		if strings.TrimSpace(bystander) == "" {
			writeError(w, 400, "invalid_request", "bystander_id is required")
			return
		}
		author := false
		if view := r.URL.Query().Get("view"); view != "" {
			if view != "player" && view != "author" {
				writeError(w, 400, "invalid_request", "view must be player or author")
				return
			}
			author = view == "author"
		}
		preview, err := s.app.PreviewCharacterPromotion(r.Context(), world, bystander, author)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"promotion": preview})
	case "POST":
		var request storyapp.PromotionRequest
		if !decodeJSON(w, r, &request) {
			return
		}
		character, err := s.app.PromoteCharacter(r.Context(), world, request)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, 201, map[string]any{"character": character})
	default:
		writeError(w, 405, "method_not_allowed", "promotions use GET or POST")
	}
}

// A world serves only the images its own snapshot recorded.
func (s *Server) worldAssetRoute(w http.ResponseWriter, r *http.Request, world, entityID string) {
	if r.Method != "GET" {
		writeError(w, 405, "method_not_allowed", "world assets are read with GET")
		return
	}
	body, mime, err := s.app.WorldCharacterAsset(r.Context(), world, strings.TrimSuffix(entityID, "/asset"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.WriteHeader(200)
	_, _ = w.Write(body)
}
