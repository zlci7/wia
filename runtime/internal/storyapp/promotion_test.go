package storyapp

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

// seedBystanderExperience records one committed result attributed to a passer-by,
// which is the only way a passer-by gains experience.
func seedBystanderExperience(t *testing.T, a *App, worldID, bystanderID string, index int, content string) string {
	t.Helper()
	ctx := context.Background()
	path, _, err := a.worldRecord(ctx, worldID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	eventID := "event-bystander-" + strconv.Itoa(index)
	var seq int64
	if err = store.db.QueryRow(`SELECT COALESCE(MAX(seq),0)+1 FROM events`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`INSERT INTO events(seq,event_id,event_type,actor_id,target_id,content,run_id,stage,scene_version,source_type,created_at) VALUES(?,?,?,?,?,?,?,3,1,'action_succeeded',?)`,
		seq, eventID, "player_action_result", "player", "", content, "run-bystander-"+strconv.Itoa(index), nowText()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`INSERT INTO perceptions(recipient_id,source_event_id,source_type,content,stage,scene_version,created_at) VALUES(?,?,'action_succeeded',?,3,1,?)`,
		bystanderID, eventID, content, nowText()); err != nil {
		t.Fatal(err)
	}
	// The world's event head follows the seeded result so later turns continue the
	// sequence instead of colliding with it.
	if _, err = store.db.Exec(`UPDATE meta SET value=? WHERE key='event_head'`, strconv.FormatInt(seq, 10)); err != nil {
		t.Fatal(err)
	}
	return eventID
}

// Promotion needs a stable identity, inherits only attributed experience, and takes
// effect atomically in the next turn.
func TestPromoteBystanderInheritsOnlyAttributedExperience(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	w, err := a.createFixtureWorld(ctx, "提升", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	logger := &recordingLogger{}
	a.logger = logger
	snapshot, err := a.ReadWorld(ctx, w.WorldID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Definition.BystanderRefs) == 0 || snapshot.Definition.BystanderRefs[0].BystanderID == "" {
		t.Fatalf("fixture has no stable bystander identity: %+v", snapshot.Definition.BystanderRefs)
	}
	target := snapshot.Definition.BystanderRefs[0]
	other := snapshot.Definition.BystanderRefs[1]
	mine := seedBystanderExperience(t, a, w.WorldID, target.BystanderID, 1, "船夫把缆绳递给了玩家。")
	theirs := seedBystanderExperience(t, a, w.WorldID, other.BystanderID, 2, "另一个人接了话。")
	// Being present without an attributed result grants nothing.
	if _, err = a.PreviewCharacterPromotion(ctx, w.WorldID, "bystander:missing", false); !errors.Is(err, ErrContentNotFound) {
		t.Fatalf("unknown passer-by previewed: %v", err)
	}
	playerView, err := a.PreviewCharacterPromotion(ctx, w.WorldID, target.BystanderID, false)
	if err != nil {
		t.Fatal(err)
	}
	if playerView.Experience != 1 || len(playerView.PlayerVisible) != 1 || playerView.PlayerVisible[0].SourceID != mine {
		t.Fatalf("player view: %+v", playerView)
	}
	// The ordinary preview always carries the public facts the player can act on.
	if playerView.Location == "" || playerView.Name == "" || !playerView.InScene {
		t.Fatalf("preview lacks public facts: %+v", playerView)
	}
	authorView, err := a.PreviewCharacterPromotion(ctx, w.WorldID, target.BystanderID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(authorView.PlayerVisible) != 1 || authorView.PlayerVisible[0].Content != "船夫把缆绳递给了玩家。" {
		t.Fatalf("author view: %+v", authorView.PlayerVisible)
	}

	// Sources must belong to this person.
	request := PromotionRequest{
		RequestKey: "promote-1", ExpectedContextEpoch: snapshot.Summary.ContextEpoch, BystanderID: target.BystanderID,
		SourceIDs: []string{theirs},
		Draft:     PromotionDraft{Role: "船夫", Profile: "在栈桥等活的船夫。"},
	}
	if _, err = a.PromoteCharacter(ctx, w.WorldID, request); !errors.Is(err, ErrContentInvalid) {
		t.Fatalf("foreign experience accepted: %v", err)
	}
	request.SourceIDs = []string{mine}
	if _, err = a.PromoteCharacter(ctx, w.WorldID, PromotionRequest{RequestKey: "promote-2", ExpectedContextEpoch: snapshot.Summary.ContextEpoch + 5, BystanderID: target.BystanderID, Draft: request.Draft}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale epoch accepted: %v", err)
	}
	promoted, err := a.PromoteCharacter(ctx, w.WorldID, request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(promoted.EntityID, "npc:") || promoted.Name != target.Name || promoted.Role != "船夫" || promoted.Profile == "" {
		t.Fatalf("promoted character: %+v", promoted)
	}
	after, err := a.ReadWorld(ctx, w.WorldID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if after.Summary.ContextEpoch != snapshot.Summary.ContextEpoch+1 {
		t.Fatalf("promotion must advance the epoch: %d", after.Summary.ContextEpoch)
	}
	for _, bystander := range after.Definition.BystanderRefs {
		if bystander.BystanderID == target.BystanderID {
			t.Fatal("promoted passer-by is still in the passer-by list")
		}
	}
	if after.Definition.BystanderRefs[0].BystanderID != other.BystanderID {
		t.Fatalf("other passers-by must stay: %+v", after.Definition.BystanderRefs)
	}
	// The live roster is the world's authority: the promoted person is in it while the
	// frozen definition keeps its original template.
	if _, found := characterByID(gameDefinition{Characters: after.Characters}, promoted.EntityID); !found {
		t.Fatalf("promoted character is missing from the world roster: %+v", after.Characters)
	}
	if len(after.Characters) == 0 {
		t.Fatalf("roster empty after promotion")
	}
	// The chosen experience now belongs to the promoted identity and nothing else moved.
	experiences, err := readBystanderExperiences(ctx, openWorldForTest(t, a, w.WorldID).db, promoted.EntityID)
	if err != nil {
		t.Fatal(err)
	}
	if len(experiences) != 1 || experiences[0].SourceID != mine {
		t.Fatalf("promoted identity lost its experience: %+v", experiences)
	}
	remaining, err := readBystanderExperiences(ctx, openWorldForTest(t, a, w.WorldID).db, other.BystanderID)
	if err != nil || len(remaining) != 1 {
		t.Fatalf("other passer-by experience moved: %+v %v", remaining, err)
	}
	// Promoting twice returns the same character instead of creating a second one.
	again, err := a.PromoteCharacter(ctx, w.WorldID, PromotionRequest{RequestKey: "promote-3", ExpectedContextEpoch: after.Summary.ContextEpoch, BystanderID: target.BystanderID, Draft: request.Draft})
	if err != nil || again.EntityID != promoted.EntityID {
		t.Fatalf("repeat promotion: %+v %v", again, err)
	}
	// The promoted person joins later turns as an important character.
	run, err := a.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: "after-promotion", Input: "我向船夫点头。"})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a, w.WorldID, run.RunID); done.Status != "completed" {
		t.Fatalf("turn after promotion: %+v\nlog:\n%s", done, logger.String())
	}
}

// Promotion is refused while the world is busy, and it does not fabricate experience
// for a passer-by that has none.
func TestPromotionGuardsAndEmptyExperience(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	w, err := a.createFixtureWorld(ctx, "提升守卫", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := a.ReadWorld(ctx, w.WorldID, 5)
	if err != nil {
		t.Fatal(err)
	}
	target := snapshot.Definition.BystanderRefs[0]
	preview, err := a.PreviewCharacterPromotion(ctx, w.WorldID, target.BystanderID, false)
	if err != nil || preview.Experience != 0 || len(preview.PlayerVisible) != 0 {
		t.Fatalf("fresh passer-by invented experience: %+v %v", preview, err)
	}
	if _, err = a.PromoteCharacter(ctx, w.WorldID, PromotionRequest{RequestKey: "", ExpectedContextEpoch: 1, BystanderID: target.BystanderID, Draft: PromotionDraft{Profile: "资料"}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing request key: %v", err)
	}
	if _, err = a.PromoteCharacter(ctx, w.WorldID, PromotionRequest{RequestKey: "no-profile", ExpectedContextEpoch: snapshot.Summary.ContextEpoch, BystanderID: target.BystanderID, Draft: PromotionDraft{}}); !errors.Is(err, ErrContentInvalid) {
		t.Fatalf("profile-less promotion: %v", err)
	}
	promoted, err := a.PromoteCharacter(ctx, w.WorldID, PromotionRequest{RequestKey: "empty-experience", ExpectedContextEpoch: snapshot.Summary.ContextEpoch, BystanderID: target.BystanderID, Draft: PromotionDraft{Profile: "在栈桥上等活的船夫。"}})
	if err != nil {
		t.Fatal(err)
	}
	store := openWorldForTest(t, a, w.WorldID)
	defer store.db.Close()
	experiences, err := readBystanderExperiences(ctx, store.db, promoted.EntityID)
	if err != nil || len(experiences) != 0 {
		t.Fatalf("promotion invented experience: %+v %v", experiences, err)
	}
}

func openWorldForTest(t *testing.T, a *App, worldID string) *worldStore {
	t.Helper()
	path, _, err := a.worldRecord(context.Background(), worldID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.db.Close() })
	return store
}
