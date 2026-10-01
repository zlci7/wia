package storyapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"gameagent/backend/internal/app"
)

func TestSuggestionRoutes(t *testing.T) {
	ctx := context.Background()
	a, err := app.Open(ctx, app.Options{StoryPacksPath: apiStoryPacks(t), DataRoot: t.TempDir(), Generator: apiGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	w, err := a.CreateWorld(ctx, "test", "guided", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{app: a}
	for _, tc := range []struct {
		method, world, body string
		code                int
	}{
		{"GET", w.WorldID, "", 200},
		{"GET", "missing", "", 404},
		{"DELETE", w.WorldID, "", 405},
		{"PUT", w.WorldID, `{}`, 400},
		{"POST", w.WorldID, `{"enabled":false}`, 400},
		{"POST", w.WorldID, `{}`, 409},
	} {
		r := httptest.NewRecorder()
		s.handleAPI(r, httptest.NewRequest(tc.method, "/api/v1/worlds/"+tc.world+"/suggestions", strings.NewReader(tc.body)))
		if r.Code != tc.code {
			t.Fatalf("%s %s: %d %s", tc.method, tc.world, r.Code, r.Body.String())
		}
	}
	usage, err := a.ReadUsage(ctx, "", 0)
	if err != nil || usage.Totals.Calls != 0 {
		t.Fatal("reads/invalid writes generated calls", err)
	}
}
