package storyapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"gameagent/backend/internal/app"
)

func TestPlayRoutesKeepAuthenticationAndRejectUnknownFields(t *testing.T) {
	a, err := app.Open(t.Context(), app.Options{DataRoot: t.TempDir(), StoryPacksPath: apiStoryPacks(t), Generator: apiGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	s, err := New(Options{Addr: "127.0.0.1:0", App: a})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown()
	go s.Serve()
	pack, _ := a.Pack("mist-embers")
	payload := map[string]any{"game_id": "mist-embers", "expected_revision": pack.Definition.Revision, "request_key": "create-browser"}
	response, _ := requestJSON(t, http.DefaultClient, "POST", s.URL()+"/api/v1/play-sessions", payload)
	if response.StatusCode != 401 {
		t.Fatal("unauthenticated creation", response.StatusCode)
	}
	u, _ := url.Parse(s.BrowserURL())
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	requestJSON(t, client, "POST", s.URL()+"/api/session", map[string]string{"token": strings.TrimPrefix(u.Fragment, "token=")})
	response, body := requestJSON(t, client, "POST", s.URL()+"/api/v1/play-sessions", payload)
	var view app.PlayView
	if response.StatusCode != 201 || json.Unmarshal([]byte(body), &view) != nil {
		t.Fatal(response.StatusCode, body)
	}
	response, _ = requestJSON(t, client, "GET", s.URL()+"/api/v1/play-sessions/"+view.ID, nil)
	if response.StatusCode != 200 {
		t.Fatal("read", response.StatusCode)
	}
	response, _ = requestJSON(t, client, "POST", s.URL()+"/api/v1/play-sessions/"+view.ID+"/turns", map[string]any{"request_key": "unknown", "expected_turn": 0, "input": "我问来访者。", "transport": "non_stream", "author_truth": "override"})
	if response.StatusCode != 400 {
		t.Fatal("unknown fields", response.StatusCode)
	}
	response, _ = requestJSON(t, client, "GET", s.URL()+"/api/v1/play-sessions/"+view.ID+"/turns", nil)
	if response.StatusCode != 405 {
		t.Fatal("method", response.StatusCode)
	}
	response, _ = requestJSON(t, client, "POST", s.URL()+"/api/v1/play-sessions/"+view.ID+"/turns/bogus/cancel", nil)
	if response.StatusCode != 404 {
		t.Fatal("foreign run", response.StatusCode)
	}
	response, _ = requestJSON(t, client, "DELETE", s.URL()+"/api/v1/play-sessions/"+view.ID, nil)
	if response.StatusCode != 204 {
		t.Fatal("close", response.StatusCode)
	}
	if _, err := a.ReadPlay(view.ID); err == nil {
		t.Fatal("closed play")
	}
	worlds, err := a.ListWorlds(context.Background())
	if err != nil || len(worlds) != 0 {
		t.Fatal("worlds changed")
	}
}
