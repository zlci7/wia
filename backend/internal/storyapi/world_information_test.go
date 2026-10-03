package storyapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	wiaapp "gameagent/backend/internal/app"
	wiaworld "gameagent/backend/internal/world"
)

func TestWorldInformationReadRoutesAreGenericAndReadOnly(t *testing.T) {
	root := apiStoryPacks(t)
	if err := os.CopyFS(filepath.Join(root, "repair-station"), os.DirFS(filepath.Join("..", "content", "testdata", "repair-station"))); err != nil {
		t.Fatal(err)
	}
	a, err := wiaapp.Open(t.Context(), wiaapp.Options{DataRoot: t.TempDir(), StoryPacksPath: root, Generator: apiGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	s := &Server{app: a}
	for _, tc := range []struct {
		game, clock, cash, display string
		amount, owned              int
	}{
		{"mist-embers", "1358-03-29 09:00", "cash", "8 金镑 14 苏勒", 8352, 3},
		{"repair-station", "2189-12-31 23:55", "credits", "100 积分", 10000, 2},
	} {
		t.Run(tc.game, func(t *testing.T) {
			pack, ok := a.Pack(tc.game)
			if !ok {
				t.Fatal("missing pack")
			}
			world, err := a.CreateStoryWorld(context.Background(), wiaapp.CreateWorldRequest{GameID: tc.game, ExpectedRevision: pack.Definition.Revision, RequestKey: "read-" + tc.game, Name: "read-only fixture"})
			if err != nil {
				t.Fatal(err)
			}
			before, err := a.ReadWorld(t.Context(), world.WorldID, 100)
			if err != nil {
				t.Fatal(err)
			}
			response := callAPI(t, s, "GET", "/api/v1/worlds/"+world.WorldID, "")
			if response.Code != 200 {
				t.Fatalf("GET=%d %s", response.Code, response.Body.String())
			}
			var body struct {
				World      wiaworld.WorldSummary    `json:"world"`
				States     []wiaworld.PublicState   `json:"states"`
				Items      []wiaworld.PublicItem    `json:"items"`
				Locations  []wiaworld.KnownLocation `json:"known_locations"`
				PlayerName string                   `json:"player_name"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.World.Clock != tc.clock || body.World.Calendar == nil || body.World.Calendar.Kind != "gregorian" || body.PlayerName == "" || len(body.Locations) < 2 {
				t.Fatalf("incomplete world information: %+v", body)
			}
			found, owned := false, 0
			for _, state := range body.States {
				if state.StateID == "mirror_dissonance" {
					t.Fatal("hidden anomaly exposed")
				}
				if state.StateID == tc.cash {
					found = true
					if state.Value.Integer != tc.amount || state.DisplayValue != tc.display || state.Currency == nil {
						t.Fatalf("cash=%+v", state)
					}
				}
			}
			for _, item := range body.Items {
				if item.HolderID == "player" {
					owned++
				}
				if tc.game == "mist-embers" && (item.InstanceID == "case-photo" || item.InstanceID == "address-note") && item.HolderID == "player" {
					t.Fatal("scene document became inventory")
				}
			}
			if !found || owned != tc.owned {
				t.Fatalf("cash found=%t owned=%d", found, owned)
			}
			if got := callAPI(t, s, "GET", "/api/v1/worlds/"+world.WorldID, ""); got.Code != 200 {
				t.Fatal(got.Code)
			}
			after, err := a.ReadWorld(t.Context(), world.WorldID, 100)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before.Summary, after.Summary) || !reflect.DeepEqual(before.States, after.States) || !reflect.DeepEqual(before.Items, after.Items) {
				t.Fatal("read modified world")
			}
		})
	}
}
