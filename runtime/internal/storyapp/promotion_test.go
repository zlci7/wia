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
func seedBystanderExperience(t *testing.T, a *App, worldID, bystanderID string, index int, content string, playerWitnessed bool) string {
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
	// Only when the player received the same result is it player-visible.
	if playerWitnessed {
		if _, err = store.db.Exec(`INSERT INTO perceptions(recipient_id,source_event_id,source_type,content,stage,scene_version,created_at) VALUES('player',?,'action_succeeded',?,3,1,?)`,
			eventID, content, nowText()); err != nil {
			t.Fatal(err)
		}
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
	mine := seedBystanderExperience(t, a, w.WorldID, target.BystanderID, 1, "船夫把缆绳递给了玩家。", true)
	theirs := seedBystanderExperience(t, a, w.WorldID, other.BystanderID, 2, "另一个人接了话。", false)
	// A result the person lived through but the player never learned stays out of the
	// ordinary preview.
	secret := seedBystanderExperience(t, a, w.WorldID, target.BystanderID, 3, "船夫在玩家没看见时收了别人的钱。", false)
	// Being present without an attributed result grants nothing.
	if _, err = a.PreviewCharacterPromotion(ctx, w.WorldID, "bystander:missing", false); !errors.Is(err, ErrContentNotFound) {
		t.Fatalf("unknown passer-by previewed: %v", err)
	}
	playerView, err := a.PreviewCharacterPromotion(ctx, w.WorldID, target.BystanderID, false)
	if err != nil {
		t.Fatal(err)
	}
	// The ordinary preview states how much the person lived through but does not claim to
	// know what the player was told: a committed turn records no comparable player record,
	// so listing content here would be a guess.
	if playerView.Experience != 2 || len(playerView.PlayerVisible) != 0 {
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
	// The author view carries the private record too, so the author can decide.
	if len(authorView.PlayerVisible) != 2 {
		t.Fatalf("author view: %+v", authorView.PlayerVisible)
	}
	foundSecret := false
	for _, record := range authorView.PlayerVisible {
		if record.SourceID == secret {
			foundSecret = true
		}
	}
	if !foundSecret {
		t.Fatalf("author view hides the private record: %+v", authorView.PlayerVisible)
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
	request.SourceIDs = []string{mine, secret}
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
	if len(experiences) != 2 || experiences[0].SourceID != mine || experiences[1].SourceID != secret {
		t.Fatalf("promoted identity lost its experience: %+v", experiences)
	}
	remaining, err := readBystanderExperiences(ctx, openWorldForTest(t, a, w.WorldID).db, other.BystanderID)
	if err != nil || len(remaining) != 1 {
		t.Fatalf("other passer-by experience moved: %+v %v", remaining, err)
	}
	// Repeating the same request returns the same character, while a different request
	// key for the same person is a conflict instead of a second promotion.
	again, err := a.PromoteCharacter(ctx, w.WorldID, request)
	if err != nil || again.EntityID != promoted.EntityID {
		t.Fatalf("repeat promotion: %+v %v", again, err)
	}
	if _, err = a.PromoteCharacter(ctx, w.WorldID, PromotionRequest{RequestKey: "promote-other", ExpectedContextEpoch: after.Summary.ContextEpoch, BystanderID: target.BystanderID, Draft: request.Draft}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("second promotion under a new key: %v", err)
	}
	// The early return must also refuse a changed payload under the same key: the same
	// request key with a different draft is not a repeat.
	changedDraft := request.Draft
	changedDraft.Profile = "换过的资料。"
	if _, err = a.PromoteCharacter(ctx, w.WorldID, PromotionRequest{RequestKey: request.RequestKey, ExpectedContextEpoch: after.Summary.ContextEpoch, BystanderID: target.BystanderID, Draft: changedDraft}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed payload under the same key: %v", err)
	}
	// A repeat that only differs by blanks is still the same request.
	spaced := request
	spaced.SourceIDs = append([]string{"  "}, request.SourceIDs...)
	if _, err = a.PromoteCharacter(ctx, w.WorldID, spaced); err != nil {
		t.Fatalf("a whitespace-only difference was treated as a conflict: %v", err)
	}
	origins, err := readCharacterOrigins(ctx, openWorldForTest(t, a, w.WorldID).db, promoted.EntityID)
	if err != nil || len(origins) != 3 {
		t.Fatalf("promotion origin record: %+v %v", origins, err)
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
	experiences, err := readBystanderExperiences(ctx, store.db, promoted.EntityID)
	if err != nil || len(experiences) != 0 {
		t.Fatalf("promotion invented experience: %+v %v", experiences, err)
	}
	store.db.Close()
	// Deleting the world means a late promotion cannot revive anything.
	status, err := a.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.DeleteWorld(ctx, w.WorldID, status.ActiveRevision); err != nil {
		t.Fatalf("delete world: %v", err)
	}
	if _, err = a.PromoteCharacter(ctx, w.WorldID, PromotionRequest{RequestKey: "late", ExpectedContextEpoch: 1, BystanderID: target.BystanderID, Draft: PromotionDraft{Profile: "资料"}}); err == nil {
		t.Fatal("a promotion revived a deleted world")
	}
}

// A rewritten scene description must not move a person out of the room.
func TestBystanderStaysInSceneWhenTheSceneTextIsRewritten(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	w, err := a.createFixtureWorld(ctx, "情境改写", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := a.ReadWorld(ctx, w.WorldID, 1)
	if err != nil {
		t.Fatal(err)
	}
	target := snapshot.Definition.BystanderRefs[0]
	before, err := a.PreviewCharacterPromotion(ctx, w.WorldID, target.BystanderID, false)
	if err != nil {
		t.Fatal(err)
	}
	if !before.InScene {
		t.Fatalf("fixture passer-by starts out of scene: %+v", before)
	}
	// The narrator may describe the same place in more detail.
	path, _, err := a.worldRecord(ctx, w.WorldID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`UPDATE meta SET value=value || '，灯光昏黄，玩家坐在门边。' WHERE key='scene'`); err != nil {
		store.db.Close()
		t.Fatal(err)
	}
	store.db.Close()
	after, err := a.PreviewCharacterPromotion(ctx, w.WorldID, target.BystanderID, false)
	if err != nil {
		t.Fatal(err)
	}
	if !after.InScene {
		t.Fatalf("a rewritten scene description moved the person out of the room: %+v", after)
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
