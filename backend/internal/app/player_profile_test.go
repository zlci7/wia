package app

import (
	"context"
	"errors"
	"gameagent/backend/internal/story"
	"strings"
	"testing"

	"gameagent/backend/internal/model"
)

// Editing the lead character advances the world epoch, leaves committed prose and
// events untouched, and drops derived material built on the old profile.
func TestUpdatePlayerProfileAdvancesEpochAndKeepsHistory(t *testing.T) {
	ctx := context.Background()
	g := &suggestionProbe{requests: make(chan model.TextRequest, 2)}
	a, w, req := suggestionWorldFixture(t, g)
	if _, err := a.RequestSuggestions(ctx, w.WorldID, req); err != nil {
		t.Fatal(err)
	}
	if set := awaitSuggestion(t, a, w.WorldID); set.Status != "ready" {
		t.Fatalf("fixture suggestion set: %+v", set)
	}
	before, err := a.ReadWorld(ctx, w.WorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if before.Summary.ContextEpoch != req.Basis.Epoch {
		t.Fatalf("fixture epoch: %+v", before.Summary)
	}

	if _, err = a.UpdatePlayerProfile(ctx, w.WorldID, UpdatePlayerProfileRequest{PlayerName: "陆舟", PlayerProfile: "替人送信的夜行客", ExpectedContextEpoch: 0}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing epoch accepted: %v", err)
	}
	if _, err = a.UpdatePlayerProfile(ctx, w.WorldID, UpdatePlayerProfileRequest{PlayerName: "", PlayerProfile: "简介", ExpectedContextEpoch: before.Summary.ContextEpoch}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("blank name accepted: %v", err)
	}
	if _, err = a.UpdatePlayerProfile(ctx, w.WorldID, UpdatePlayerProfileRequest{PlayerName: strings.Repeat("长", 81), PlayerProfile: "简介", ExpectedContextEpoch: before.Summary.ContextEpoch}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("overlong name accepted: %v", err)
	}
	if _, err = a.UpdatePlayerProfile(ctx, w.WorldID, UpdatePlayerProfileRequest{PlayerName: "陆舟", PlayerProfile: "简介", ExpectedContextEpoch: before.Summary.ContextEpoch + 5}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale epoch accepted: %v", err)
	}

	summary, err := a.UpdatePlayerProfile(ctx, w.WorldID, UpdatePlayerProfileRequest{PlayerName: "  陆舟  ", PlayerProfile: "  替人送信的夜行客  ", ExpectedContextEpoch: before.Summary.ContextEpoch})
	if err != nil {
		t.Fatal(err)
	}
	if summary.ContextEpoch != before.Summary.ContextEpoch+1 {
		t.Fatalf("epoch did not advance: %+v", summary)
	}
	after, err := a.ReadWorld(ctx, w.WorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if after.PlayerName != "陆舟" || after.PlayerProfile != "替人送信的夜行客" {
		t.Fatalf("profile not stored: %q %q", after.PlayerName, after.PlayerProfile)
	}
	if after.Summary.MessageHead != before.Summary.MessageHead || after.Summary.EventHead != before.Summary.EventHead || len(after.Messages) != len(before.Messages) {
		t.Fatal("profile edit rewrote committed history")
	}
	set, err := a.ReadSuggestions(ctx, w.WorldID)
	if err != nil || set.Status == "ready" || len(set.Items) != 0 {
		t.Fatalf("derived suggestions survived the epoch change: %+v %v", set, err)
	}

	// Re-sending the same profile is a no-op rather than a new epoch.
	again, err := a.UpdatePlayerProfile(ctx, w.WorldID, UpdatePlayerProfileRequest{PlayerName: "陆舟", PlayerProfile: "替人送信的夜行客", ExpectedContextEpoch: summary.ContextEpoch})
	if err != nil || again.ContextEpoch != summary.ContextEpoch {
		t.Fatalf("identical profile advanced the epoch: %+v %v", again, err)
	}
}

// A pack that fixes the lead character keeps the profile immutable.
func TestUpdatePlayerProfileRejectsFixedPlayer(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	pack := testPack(a, GameID)
	definition := pack.Definition
	definition.Summary.Player = story.Player{Name: "固定主角", Profile: "固定简介", Editable: false}
	world, err := a.createWorldFromPack(ctx, loadedPack{Definition: definition}, CreateWorldRequest{
		GameID: GameID, ExpectedRevision: definition.Revision, RequestKey: "fixed-player", Name: "固定主角存档", Activate: true,
	}, "fixed-player-hash")
	if err != nil {
		t.Fatal(err)
	}
	before, err := a.ReadWorld(ctx, world.WorldID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.UpdatePlayerProfile(ctx, world.WorldID, UpdatePlayerProfileRequest{PlayerName: "别人", PlayerProfile: "别的简介", ExpectedContextEpoch: before.Summary.ContextEpoch}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("fixed player accepted an edit: %v", err)
	}
	if after, err := a.ReadWorld(ctx, world.WorldID, 5); err != nil || after.PlayerName != "固定主角" || after.Summary.ContextEpoch != before.Summary.ContextEpoch {
		t.Fatalf("fixed player changed: %+v %v", after.PlayerName, err)
	}
}
