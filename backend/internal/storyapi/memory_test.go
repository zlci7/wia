package storyapi

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"gameagent/backend/internal/app"
	"gameagent/backend/internal/memory"
)

func TestMemoryRoutesRequireOwnershipAndExplicitAuthorView(t *testing.T) {
	ctx := context.Background()
	app, err := app.Open(ctx, app.Options{DataRoot: t.TempDir(), Generator: apiGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	world, err := app.CreateWorld(ctx, "memory", "guided", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(Options{Addr: "127.0.0.1:0", App: app})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown()
	go server.Serve()
	base := server.URL() + "/api/v1/worlds/" + world.WorldID
	client := &http.Client{}
	response, _ := requestJSON(t, client, "GET", base+"/memory", nil)
	if response.StatusCode != 401 {
		t.Fatal(response.StatusCode)
	}
	bootstrap, _ := url.Parse(server.BrowserURL())
	jar, _ := cookiejar.New(nil)
	client.Jar = jar
	response, _ = requestJSON(t, client, "POST", server.URL()+"/api/session", map[string]string{"token": strings.TrimPrefix(bootstrap.Fragment, "token=")})
	if response.StatusCode != 204 {
		t.Fatal(response.StatusCode)
	}
	response, body := requestJSON(t, client, "GET", base+"/memory", nil)
	if response.StatusCode != 200 || strings.Contains(string(body), "knowledge") || strings.Contains(string(body), "信蜡") {
		t.Fatalf("public memory=%d %s", response.StatusCode, body)
	}
	for _, q := range []string{"scope=npc:innkeeper", "before_seq=-1", "before_seq=bad"} {
		response, _ = requestJSON(t, client, "GET", base+"/memory?"+q, nil)
		if response.StatusCode != 400 {
			t.Fatal(q, response.StatusCode)
		}
	}
	response, body = requestJSON(t, client, "GET", base+"/author-memory?scope=npc:innkeeper", nil)
	if response.StatusCode != 200 || !strings.Contains(string(body), "initial_concerns") {
		t.Fatalf("author=%d %s", response.StatusCode, body)
	}
	payload := memory.CorrectionRequest{RequestKey: "api-edit", ExpectedEpoch: world.ContextEpoch, Kind: "character", Scope: "npc:innkeeper", TargetID: "profile", Replacement: "客栈老板"}
	response, body = requestJSON(t, client, "POST", base+"/corrections", payload)
	if response.StatusCode != 202 {
		t.Fatalf("correct=%d %s", response.StatusCode, body)
	}
	response, _ = requestJSON(t, client, "POST", base+"/corrections", payload)
	if response.StatusCode != 202 {
		t.Fatal("idempotency", response.StatusCode)
	}
	response, _ = requestJSON(t, client, "GET", server.URL()+"/api/v1/worlds/foreign/memory", nil)
	if response.StatusCode != 404 {
		t.Fatal("scope", response.StatusCode)
	}
}
