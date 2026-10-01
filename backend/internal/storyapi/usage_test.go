package storyapi

import (
	"context"
	"gameagent/backend/internal/app"
	"net/http/httptest"
	"testing"
)

func TestUsageRouteValidation(t *testing.T) {
	a, err := app.Open(context.Background(), app.Options{StoryPacksPath: apiStoryPacks(t), DataRoot: t.TempDir(), Generator: apiGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	s := &Server{app: a}
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/api/v1/usage", 200}, {"POST", "/api/v1/usage", 405},
		{"GET", "/api/v1/usage?before_id=0", 400}, {"GET", "/api/v1/usage?before_id=-1", 400},
		{"GET", "/api/v1/usage?before_id=bad", 400}, {"GET", "/api/v1/usage?before_id=1&before_id=2", 400},
		{"GET", "/api/v1/usage?world_id=missing", 404},
	} {
		w := httptest.NewRecorder()
		s.handleAPI(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
}
