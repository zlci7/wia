package storyapi

import (
	"net/http"
	"strings"

	"gameagent/runtime/internal/storyapp"
)

// Author-facing routes: lead-character templates, the current world's lead profile,
// and the content workspace. Every handler stays thin: authentication, shape and
// version parsing only, with the story service owning the rules.
func (s *Server) personas(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		list, err := s.app.ListPersonas(r.Context())
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"personas": list})
	case "POST":
		var request storyapp.PersonaRequest
		if !decodeJSON(w, r, &request) {
			return
		}
		persona, err := s.app.CreatePersona(r.Context(), request)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, 201, map[string]any{"persona": persona})
	default:
		writeError(w, 405, "method_not_allowed", "personas use GET or POST")
	}
}

func (s *Server) personaRoute(w http.ResponseWriter, r *http.Request, personaID string) {
	if strings.TrimSpace(personaID) == "" || strings.Contains(personaID, "/") {
		writeError(w, 404, "not_found", "no such persona")
		return
	}
	switch r.Method {
	case "GET":
		persona, err := s.app.ReadPersona(r.Context(), personaID)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"persona": persona})
	case "PUT":
		var request storyapp.PersonaRequest
		if !decodeJSON(w, r, &request) {
			return
		}
		persona, err := s.app.UpdatePersona(r.Context(), personaID, request)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"persona": persona})
	case "DELETE":
		if err := s.app.DeletePersona(r.Context(), personaID); err != nil {
			writeAppError(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		writeError(w, 405, "method_not_allowed", "a persona uses GET, PUT or DELETE")
	}
}

func (s *Server) playerProfile(w http.ResponseWriter, r *http.Request, world string) {
	if r.Method != "PUT" {
		writeError(w, 405, "method_not_allowed", "the lead profile is written with PUT")
		return
	}
	var request storyapp.UpdatePlayerProfileRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	summary, err := s.app.UpdatePlayerProfile(r.Context(), world, request)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"world": summary})
}

func (s *Server) contentRoute(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "projects":
		s.contentProjects(w, r)
	case len(parts) == 2 && parts[0] == "projects":
		s.contentProject(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "drafts":
		s.createContentDraft(w, r, parts[1])
	case len(parts) == 2 && parts[0] == "drafts":
		s.contentDraft(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "drafts" && parts[2] == "assets":
		s.contentDraftAssets(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "drafts" && parts[2] == "preview":
		s.previewContentDraft(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "drafts" && parts[2] == "publish":
		s.publishContentDraft(w, r, parts[1])
	case len(parts) == 2 && parts[0] == "operations":
		s.contentOperation(w, r, parts[1])
	default:
		writeError(w, 404, "not_found", "no such content route")
	}
}

func (s *Server) publishContentDraft(w http.ResponseWriter, r *http.Request, draftID string) {
	if r.Method != "POST" {
		writeError(w, 405, "method_not_allowed", "publication uses POST")
		return
	}
	var request storyapp.PublishRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	request.DraftID = draftID
	operation, err := s.app.PublishContentDraft(r.Context(), request)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"operation": operation})
}

func (s *Server) contentOperation(w http.ResponseWriter, r *http.Request, operationID string) {
	if r.Method != "GET" {
		writeError(w, 405, "method_not_allowed", "operations are read with GET")
		return
	}
	operation, err := s.app.ReadContentOperation(r.Context(), operationID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"operation": operation})
}

// Image uploads and removals stay form-encoded: the name is a package-relative
// assets/ path and the bytes decide the media type.
func (s *Server) contentDraftAssets(w http.ResponseWriter, r *http.Request, draftID string) {
	switch r.Method {
	case "GET":
		assets, err := s.app.ListContentDraftAssets(r.Context(), draftID)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"assets": assets})
	case "POST":
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			writeError(w, 400, "invalid_request", "upload an image file with a name field")
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, 400, "invalid_request", "upload an image file with a name field")
			return
		}
		defer file.Close()
		body, err := storyapp.ReadUploadedAsset(file)
		if err != nil {
			writeAppError(w, err)
			return
		}
		name := r.FormValue("name")
		if name == "" && header != nil {
			name = "assets/" + header.Filename
		}
		asset, err := s.app.UploadContentDraftAsset(r.Context(), draftID, name, body)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, 201, map[string]any{"asset": asset})
	case "DELETE":
		if err := s.app.RemoveContentDraftAsset(r.Context(), draftID, r.URL.Query().Get("asset_id")); err != nil {
			writeAppError(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		writeError(w, 405, "method_not_allowed", "assets use GET, POST or DELETE")
	}
}

func (s *Server) contentProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		list, err := s.app.ListContentProjects(r.Context())
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"projects": list})
	case "POST":
		var request struct {
			GameID string `json:"game_id"`
			Title  string `json:"title"`
		}
		if !decodeJSON(w, r, &request) {
			return
		}
		project, err := s.app.CreateContentProject(r.Context(), request.GameID, request.Title)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, 201, map[string]any{"project": project})
	default:
		writeError(w, 405, "method_not_allowed", "projects use GET or POST")
	}
}

func (s *Server) contentProject(w http.ResponseWriter, r *http.Request, projectID string) {
	if r.Method != "GET" {
		writeError(w, 405, "method_not_allowed", "a project is read with GET")
		return
	}
	project, drafts, err := s.app.ReadContentProject(r.Context(), projectID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"project": project, "drafts": drafts})
}

func (s *Server) createContentDraft(w http.ResponseWriter, r *http.Request, projectID string) {
	if r.Method != "POST" {
		writeError(w, 405, "method_not_allowed", "drafts are created with POST")
		return
	}
	var request struct {
		BaseRevision string `json:"base_revision"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	draft, err := s.app.CreateContentDraft(r.Context(), projectID, request.BaseRevision)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"draft": draft})
}

func (s *Server) contentDraft(w http.ResponseWriter, r *http.Request, draftID string) {
	switch r.Method {
	case "GET":
		draft, err := s.app.ReadContentDraft(r.Context(), draftID)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"draft": draft})
	case "PUT":
		var request struct {
			ExpectedVersion int64                        `json:"expected_version"`
			Payload         storyapp.ContentDraftPayload `json:"payload"`
		}
		if !decodeJSON(w, r, &request) {
			return
		}
		draft, err := s.app.SaveContentDraft(r.Context(), draftID, request.Payload, request.ExpectedVersion)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"draft": draft})
	case "DELETE":
		if err := s.app.DeleteContentDraft(r.Context(), draftID); err != nil {
			writeAppError(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		writeError(w, 405, "method_not_allowed", "a draft uses GET, PUT or DELETE")
	}
}

func (s *Server) previewContentDraft(w http.ResponseWriter, r *http.Request, draftID string) {
	if r.Method != "GET" {
		writeError(w, 405, "method_not_allowed", "previews are read with GET")
		return
	}
	author := false
	if value := r.URL.Query().Get("view"); value != "" {
		if value != "player" && value != "author" {
			writeError(w, 400, "invalid_request", "view must be player or author")
			return
		}
		author = value == "author"
	}
	preview, err := s.app.PreviewContentDraft(r.Context(), draftID, author)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"preview": preview})
}
