package storyapi

import (
	"context"
	"net/http"
	"testing"
)

func TestPlayerPromotionRoutesAreUnavailable(t *testing.T) {
	s, app := contentTestServer(t)
	world, err := app.CreateWorld(context.Background(), "route test", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	before, err := app.ReadWorld(context.Background(), world.WorldID, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		response := callAPI(t, s, method, "/api/v1/worlds/"+world.WorldID+"/character-promotions?bystander_id=passer&view=author", `{"request_key":"promotion"}`)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s promotion route: %d %s", method, response.Code, response.Body.String())
		}
	}
	after, err := app.ReadWorld(context.Background(), world.WorldID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if before.Summary.ContextEpoch != after.Summary.ContextEpoch || len(before.Characters) != len(after.Characters) {
		t.Fatal("unavailable promotion route changed the world")
	}
	// The unavailable route must not reach an application service at all.
	if got := callAPI(t, &Server{}, http.MethodPost, "/api/v1/worlds/missing/character-promotions", "invalid JSON").Code; got != http.StatusNotFound {
		t.Fatalf("disabled route reached request parsing: %d", got)
	}
}
