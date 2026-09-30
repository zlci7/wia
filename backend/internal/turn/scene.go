package turn

import (
	"fmt"
	"strings"

	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func initialSceneViews(snapshot Snapshot) []SceneView {
	recipients := []string{"player"}
	for _, c := range snapshot.Characters {
		recipients = append(recipients, c.EntityID)
	}
	var views []SceneView
	for _, id := range recipients {
		view := SceneView{Recipient: id, Version: snapshot.SceneVersion, SourceIDs: []string{}}
		if snapshot.Summary.TurnSeq == 0 {
			view.Content = snapshot.Definition.Scene
			if location := snapshot.Definition.InitialLocations[id]; location != "" {
				for _, loc := range snapshot.Definition.Locations {
					if loc.ID == location {
						view.Content = loc.Name + "：" + loc.Description
					}
				}
			}
			view.SourceIDs = []string{"opening"}
		} else if id == "player" {
			for _, event := range snapshot.Events {
				if event.EventType == "turn_settled" {
					view.Content = event.Content
					view.SourceIDs = []string{event.EventID}
				}
			}
		} else {
			for _, p := range snapshot.Perceptions[id] {
				if strings.HasPrefix(p.SourceType, "action_") {
					view.Content = p.Content
					view.SourceIDs = []string{p.SourceEventID}
				}
			}
		}
		if view.Content == "" {
			view.Content = "当前地点沿用已提交经历；没有足够的获准场景信息，不推定回到开场地点。"
		}
		views = append(views, view)
	}
	return views
}

func applySceneUpdates(snapshot Snapshot, run wiaworld.Run, intent TurnIntent, output Output, host hostResult) ([]SceneView, error) {
	byID := map[string]sceneSource{}
	for _, source := range sceneSources(snapshot, run, intent, output.Events) {
		byID[source.ID] = source
	}
	for i, outcome := range host.Outcomes {
		action, ok := EventByID(output.Events, outcome.ActionID)
		if !ok || action.RunID != run.RunID || action.Stage < 1 || action.Stage > 2 {
			return nil, ErrGenerationFailed
		}
		id := fmt.Sprintf("%s:result:%d", outcome.ActionID, i+1)
		byID[outcome.ActionID] = sceneSource{ID: outcome.ActionID, Content: outcome.Content, Recipients: append(append([]string{}, outcome.Recipients...), action.ActorID), Canonical: []string{id}}
	}
	return mergeSceneUpdates(snapshot.SceneViews, snapshot.SceneVersion+1, byID, host.SceneUpdates)
}

func mergeSceneUpdates(previous []SceneView, version int64, byID map[string]sceneSource, updates []sceneUpdate) ([]SceneView, error) {
	views := append([]SceneView{}, previous...)
	seen := map[string]bool{}
	for _, update := range updates {
		if wire.Clean(update.Content) == "" || len(update.SourceIDs) == 0 || len(update.Recipients) == 0 {
			return nil, fmt.Errorf("%w: incomplete scene update", ErrGenerationFailed)
		}
		for _, recipient := range update.Recipients {
			index := -1
			for i, v := range views {
				if v.Recipient == recipient {
					index = i
					break
				}
			}
			if index < 0 || seen[recipient] {
				return nil, fmt.Errorf("%w: invalid scene recipient", ErrGenerationFailed)
			}
			seen[recipient] = true
			var canonical []string
			for _, id := range update.SourceIDs {
				source, ok := byID[id]
				if !ok || !containsID(source.Recipients, recipient) {
					return nil, fmt.Errorf("%w: scene source is unavailable to recipient", ErrGenerationFailed)
				}
				for _, sid := range source.Canonical {
					if !containsID(canonical, sid) {
						canonical = append(canonical, sid)
					}
				}
			}
			views[index] = SceneView{Recipient: recipient, Content: wire.Clean(update.Content), SourceIDs: canonical, Version: version}
		}
	}
	return views, nil
}

// Later stages read only committed views and the projections already granted in
// this workspace. An author's plot result is never a scene source for a player.
func plotSceneSources(output Output, visible []wiaworld.Event) map[string]sceneSource {
	sources := map[string]sceneSource{}
	for _, view := range output.SceneViews {
		sources["view:"+view.Recipient] = sceneSource{ID: "view:" + view.Recipient, Content: view.Content, Recipients: []string{view.Recipient}, Canonical: view.SourceIDs}
	}
	for _, p := range output.Perceptions {
		if p.Stage < 4 {
			continue
		}
		s := sources[p.SourceEventID]
		s.ID, s.Content, s.Canonical = p.SourceEventID, p.Content, []string{p.SourceEventID}
		if !containsID(s.Recipients, p.RecipientID) {
			s.Recipients = append(s.Recipients, p.RecipientID)
		}
		sources[p.SourceEventID] = s
	}
	for _, e := range visible {
		s := sources[e.EventID]
		s.ID, s.Content, s.Canonical = e.EventID, e.Content, []string{e.EventID}
		if !containsID(s.Recipients, "player") {
			s.Recipients = append(s.Recipients, "player")
		}
		sources[e.EventID] = s
	}
	return sources
}

func applyPlotSceneUpdates(output *Output, sources map[string]sceneSource, updates []sceneUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	views, err := mergeSceneUpdates(output.SceneViews, output.SceneVersion+1, sources, updates)
	if err != nil {
		return err
	}
	output.SceneVersion++
	output.SceneViews = views
	output.Scene = SceneFor(Snapshot{SceneViews: views}, "player")
	return nil
}

func containsID(ids []string, id string) bool {
	for _, value := range ids {
		if value == id {
			return true
		}
	}
	return false
}

func validateSceneViews(snapshot Snapshot) error {
	valid := map[string]bool{"player": true}
	for _, c := range snapshot.Characters {
		valid[c.EntityID] = true
	}
	seen := map[string]bool{}
	for _, view := range snapshot.SceneViews {
		if !valid[view.Recipient] || seen[view.Recipient] || wire.Clean(view.Content) == "" || view.Version > snapshot.SceneVersion || view.Version < 1 {
			return fmt.Errorf("%w: invalid stored scene view", ErrContextSourceMissing)
		}
		seen[view.Recipient] = true
	}
	if len(seen) != len(valid) {
		return fmt.Errorf("%w: incomplete stored scene views", ErrContextSourceMissing)
	}
	return nil
}
