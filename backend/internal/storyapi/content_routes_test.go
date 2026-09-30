package storyapi

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"gameagent/backend/internal/content"
	"gameagent/backend/internal/storyapp"
)

func contentTestServer(t *testing.T) (*Server, *storyapp.App) {
	t.Helper()
	app, err := storyapp.Open(context.Background(), storyapp.Options{DataRoot: t.TempDir(), Generator: apiGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return &Server{app: app}, app
}

func callAPI(t *testing.T, s *Server, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	s.handleAPI(recorder, httptest.NewRequest(method, target, strings.NewReader(body)))
	return recorder
}

// Author routes expose the workspace without leaking server paths, and version
// conflicts surface as a conflict rather than an overwrite.
func TestContentRoutes(t *testing.T) {
	s, _ := contentTestServer(t)
	if got := callAPI(t, s, "GET", "/api/v1/personas", "").Code; got != 200 {
		t.Fatalf("list personas: %d", got)
	}
	if got := callAPI(t, s, "POST", "/api/v1/personas", `{"name":"陆舟","profile":"夜行客"}`).Code; got != 201 {
		t.Fatalf("create persona: %d", got)
	}
	if got := callAPI(t, s, "POST", "/api/v1/personas", `{"name":"","profile":"x"}`).Code; got != 400 {
		t.Fatalf("invalid persona: %d", got)
	}
	if got := callAPI(t, s, "DELETE", "/api/v1/personas/persona_missing", "").Code; got != 404 {
		t.Fatalf("missing persona: %d", got)
	}
	if got := callAPI(t, s, "PATCH", "/api/v1/personas/persona_x", "").Code; got != 405 {
		t.Fatalf("bad persona method: %d", got)
	}

	created := callAPI(t, s, "POST", "/api/v1/content/projects", `{"game_id":"harbor","title":"港口"}`)
	if created.Code != 201 {
		t.Fatalf("create project: %d %s", created.Code, created.Body.String())
	}
	var projectPayload struct {
		Project content.ContentProject `json:"project"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &projectPayload); err != nil {
		t.Fatal(err)
	}
	if got := callAPI(t, s, "POST", "/api/v1/content/projects", `{"game_id":"harbor","title":"重复"}`).Code; got != 409 {
		t.Fatalf("duplicate project: %d", got)
	}
	if got := callAPI(t, s, "GET", "/api/v1/content/projects/"+projectPayload.Project.ProjectID, "").Code; got != 200 {
		t.Fatalf("read project: %d", got)
	}

	createdDraft := callAPI(t, s, "POST", "/api/v1/content/projects/"+projectPayload.Project.ProjectID+"/drafts", `{}`)
	if createdDraft.Code != 201 {
		t.Fatalf("create draft: %d %s", createdDraft.Code, createdDraft.Body.String())
	}
	var draftPayload struct {
		Draft content.ContentDraft `json:"draft"`
	}
	if err := json.Unmarshal(createdDraft.Body.Bytes(), &draftPayload); err != nil {
		t.Fatal(err)
	}
	if draftPayload.Draft.Status != "editing" || draftPayload.Draft.Payload.GameID != "harbor" {
		t.Fatalf("draft shape: %+v", draftPayload.Draft)
	}
	draftPath := "/api/v1/content/drafts/" + draftPayload.Draft.DraftID
	if got := callAPI(t, s, "GET", draftPath, "").Code; got != 200 {
		t.Fatalf("read draft: %d", got)
	}
	if got := callAPI(t, s, "GET", draftPath+"/preview", "").Code; got != 200 {
		t.Fatalf("player preview: %d", got)
	}
	if got := callAPI(t, s, "GET", draftPath+"/preview?view=author", "").Code; got != 200 {
		t.Fatalf("author preview: %d", got)
	}
	if got := callAPI(t, s, "GET", draftPath+"/preview?view=secret", "").Code; got != 400 {
		t.Fatalf("unknown view: %d", got)
	}
	if got := callAPI(t, s, "POST", "/api/v1/content/drafts/unknown/preview", "").Code; got != 405 {
		t.Fatalf("preview method: %d", got)
	}
	if got := callAPI(t, s, "GET", "/api/v1/content/nonsense", "").Code; got != 404 {
		t.Fatalf("unknown content route: %d", got)
	}

	body, err := json.Marshal(map[string]any{"expected_version": 1, "payload": draftPayload.Draft.Payload})
	if err != nil {
		t.Fatal(err)
	}
	saved := callAPI(t, s, "PUT", draftPath, string(body))
	if saved.Code != 200 {
		t.Fatalf("save draft: %d %s", saved.Code, saved.Body.String())
	}
	if again := callAPI(t, s, "PUT", draftPath, string(body)); again.Code != 409 {
		t.Fatalf("stale save: %d", again.Code)
	}
	if got := callAPI(t, s, "PUT", draftPath, `{"payload":{}}`).Code; got != 400 {
		t.Fatalf("missing version: %d", got)
	}
	if got := callAPI(t, s, "DELETE", draftPath, "").Code; got != 204 {
		t.Fatalf("delete draft: %d", got)
	}
	if got := callAPI(t, s, "GET", draftPath, "").Code; got != 404 {
		t.Fatalf("deleted draft: %d", got)
	}

	// The player preview must not carry author material over the wire.
	draft2 := callAPI(t, s, "POST", "/api/v1/content/projects/"+projectPayload.Project.ProjectID+"/drafts", `{}`)
	if err := json.Unmarshal(draft2.Body.Bytes(), &draftPayload); err != nil {
		t.Fatal(err)
	}
	preview := callAPI(t, s, "GET", "/api/v1/content/drafts/"+draftPayload.Draft.DraftID+"/preview", "")
	if strings.Contains(preview.Body.String(), "author_facts") || strings.Contains(preview.Body.String(), "author_characters") {
		t.Fatalf("player preview carries author fields: %s", preview.Body.String())
	}

	// Publication reports its operation, and an incomplete draft fails as a content
	// error instead of publishing something unusable.
	publishPath := "/api/v1/content/drafts/" + draftPayload.Draft.DraftID + "/publish"
	if got := callAPI(t, s, "GET", publishPath, "").Code; got != 405 {
		t.Fatalf("publish method: %d", got)
	}
	publishBody, err := json.Marshal(map[string]any{"request_key": "publish-route", "expected_draft_version": draftPayload.Draft.Version, "expected_project_version": projectPayload.Project.Version})
	if err != nil {
		t.Fatal(err)
	}
	if got := callAPI(t, s, "POST", publishPath, string(publishBody)).Code; got != 400 {
		t.Fatalf("incomplete draft published: %d", got)
	}
	if got := callAPI(t, s, "GET", "/api/v1/content/operations/publish_missing", "").Code; got != 404 {
		t.Fatalf("unknown operation: %d", got)
	}
	if got := callAPI(t, s, "POST", "/api/v1/content/imports/preview", "not-form").Code; got != 400 {
		t.Fatalf("bad import upload: %d", got)
	}
	if got := callAPI(t, s, "GET", "/api/v1/content/imports/preview", "").Code; got != 405 {
		t.Fatalf("import method: %d", got)
	}
	// An upload past the import ceiling is refused at the boundary, before the service
	// ever parses it into a draft.
	var oversized bytes.Buffer
	writer := multipart.NewWriter(&oversized)
	_ = writer.WriteField("project_id", "project_missing")
	part, _ := writer.CreateFormFile("file", "big.zip")
	_, _ = part.Write(bytes.Repeat([]byte("x"), int(content.ImportUploadLimit())+4096))
	_ = writer.Close()
	request := httptest.NewRequest("POST", "/api/v1/content/imports/preview", &oversized)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	s.handleAPI(recorder, request)
	if recorder.Code != 400 {
		t.Fatalf("oversized import upload: %d", recorder.Code)
	}
	if got := callAPI(t, s, "POST", "/api/v1/content/imports/confirm", `{"draft_id":"draft_missing"}`).Code; got != 404 {
		t.Fatalf("unknown preview confirmation: %d", got)
	}
	if got := callAPI(t, s, "GET", "/api/v1/content/revisions/harbor/r-2026-09-28-abcd1234/export", "").Code; got != 404 {
		t.Fatalf("unknown export: %d", got)
	}
	if got := callAPI(t, s, "POST", "/api/v1/content/revisions/harbor/r-2026-09-28-abcd1234/export", "").Code; got != 405 {
		t.Fatalf("export method: %d", got)
	}
}

// The lead profile route carries the world epoch and reports conflicts.
func TestPlayerProfileRoute(t *testing.T) {
	s, app := contentTestServer(t)
	world, err := app.CreateWorld(context.Background(), "主角路由", "guided", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/worlds/" + world.WorldID + "/player-profile"
	if got := callAPI(t, s, "GET", path, "").Code; got != 405 {
		t.Fatalf("profile method: %d", got)
	}
	if got := callAPI(t, s, "PUT", path, `{"player_name":"陆舟","player_profile":"夜行客"}`).Code; got != 400 {
		t.Fatalf("missing epoch: %d", got)
	}
	snapshot, err := app.ReadWorld(context.Background(), world.WorldID, 5)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"player_name": "陆舟", "player_profile": "夜行客", "expected_context_epoch": snapshot.Summary.ContextEpoch})
	if got := callAPI(t, s, "PUT", path, string(body)).Code; got != 200 {
		t.Fatalf("update profile: %d", got)
	}
	if got := callAPI(t, s, "PUT", path, string(body)).Code; got != 409 {
		t.Fatalf("stale epoch: %d", got)
	}
}
