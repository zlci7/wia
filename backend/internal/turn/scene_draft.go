package turn

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unicode"

	wiaworld "gameagent/backend/internal/world"
)

const sceneDraftRevision = "scene-draft.v1"
const sceneBeatLimit = 24

// SceneDraft is a complete, uncommitted scene candidate. Identifiers and world
// changes are compiled by the turn before the application can publish an Output.
type SceneDraft struct {
	SchemaRevision  string                `json:"schema_revision"`
	InputMap        []sceneInput          `json:"input_map"`
	Beats           []sceneBeat           `json:"beats"`
	NarrativeBlocks []sceneNarrativeBlock `json:"narrative_blocks"`
	ElapsedMinutes  int                   `json:"elapsed_minutes"`
	Stop            sceneStop             `json:"stop"`
	ProgressUpdates []sceneProgress       `json:"progress_updates"`
	EventOffer      *sceneEventOffer      `json:"event_offer,omitempty"`
}

type sceneInput struct {
	Text             string   `json:"text"`
	IntentType       string   `json:"intent_type"`
	AddresseeID      string   `json:"addressee_id"`
	Visibility       string   `json:"visibility"`
	BeatIDs          []string `json:"beat_ids"`
	Status           string   `json:"status"`
	UnexecutedReason string   `json:"unexecuted_reason"`
	WaitMinutes      int      `json:"wait_minutes,omitempty"`
	ActionRuleID     string   `json:"action_rule_id,omitempty"`
	Start, End       int      `json:"-"`
}

type sceneBeat struct {
	LocalID       string             `json:"local_id"`
	Kind          string             `json:"kind"`
	ActorID       string             `json:"actor_id"`
	TargetID      string             `json:"target_id,omitempty"`
	OffsetMinutes int                `json:"offset_minutes"`
	Basis         []string           `json:"basis"`
	Content       string             `json:"content"`
	Recipients    []string           `json:"recipients"`
	Bystanders    []string           `json:"bystanders"`
	Projections   []actionProjection `json:"projections"`
	Effects       sceneEffects       `json:"effects"`
	Scope         *string            `json:"scope,omitempty"`
	Status        *string            `json:"status,omitempty"`
	Attempt       *sceneAttempt      `json:"attempt,omitempty"`
}

type sceneAttempt struct {
	Content            string `json:"content"`
	InputFragmentIndex *int   `json:"input_fragment_index,omitempty"`
}

// Scene effects carry no model-chosen action IDs. The compiler binds every
// effect to its validated node and converts it to the existing effect types.
type sceneEffects struct {
	Movements           []sceneMovement     `json:"movements,omitempty"`
	StateEffects        []sceneStateEffect  `json:"state_effects,omitempty"`
	RelationshipEffects []sceneRelation     `json:"relationship_effects,omitempty"`
	ItemTransfers       []sceneItemTransfer `json:"item_transfers,omitempty"`
	PlanUpdates         []scenePlanUpdate   `json:"plan_updates,omitempty"`
}

type sceneMovement struct {
	EntityID string   `json:"entity_id"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Route    []string `json:"route"`
}

type sceneStateEffect struct {
	EntityID string               `json:"entity_id"`
	StateID  string               `json:"state_id"`
	Delta    int                  `json:"delta,omitempty"`
	Value    *wiaworld.StateValue `json:"value,omitempty"`
}

type sceneRelation struct {
	SubjectID    string   `json:"subject_id"`
	TargetID     string   `json:"target_id"`
	RelationType string   `json:"relation_type"`
	Delta        int      `json:"delta"`
	Basis        []string `json:"basis"`
}

type sceneItemTransfer struct {
	InstanceID     string `json:"instance_id"`
	FromHolderID   string `json:"from_holder_id,omitempty"`
	FromLocationID string `json:"from_location_id,omitempty"`
	ToHolderID     string `json:"to_holder_id,omitempty"`
	ToLocationID   string `json:"to_location_id,omitempty"`
}

type scenePlanUpdate struct {
	OwnerID            string   `json:"owner_id"`
	ID                 string   `json:"id,omitempty"`
	LocalPlanID        string   `json:"local_plan_id,omitempty"`
	Content            string   `json:"content"`
	Status             string   `json:"status"`
	ReviewAfterMinutes int      `json:"review_after_minutes"`
	Basis              []string `json:"basis"`
}

type sceneNarrativeBlock struct {
	Text    string   `json:"text"`
	BeatIDs []string `json:"beat_ids"`
}

type sceneStop struct {
	Reason  string `json:"reason"`
	Content string `json:"content"`
}

type sceneProgress struct {
	Type    string   `json:"type"`
	ID      string   `json:"id"`
	Status  string   `json:"status"`
	Basis   []string `json:"basis"`
	BeatIDs []string `json:"beat_ids"`
}

type sceneEventOffer struct {
	TriggerBeatID  string   `json:"trigger_beat_id"`
	Kind           string   `json:"kind"`
	Location       string   `json:"location"`
	Condition      string   `json:"condition"`
	Development    string   `json:"development"`
	AfterMinutes   int      `json:"after_minutes"`
	InitialBeatIDs []string `json:"initial_beat_ids"`
}

type sceneResolutionRequest struct {
	RuleID               string      `json:"rule_id"`
	InputFragmentIndex   int         `json:"input_fragment_index"`
	PrefixBeats          []sceneBeat `json:"prefix_beats"`
	PrefixElapsedMinutes int         `json:"prefix_elapsed_minutes"`
}

type sceneResponse struct {
	Draft      *SceneDraft
	Material   []string
	Resolution *sceneResolutionRequest
}

// The same strict decoder owns syntax, duplicate keys and unknown fields for
// every response. Supplemental responses cannot carry partial scene effects.
func decodeSceneResponse(text string) (sceneResponse, error) {
	if err := ValidateStrictJSON([]byte(text)); err != nil {
		return sceneResponse{}, &GenerationError{Code: "json_syntax_invalid", Cause: err}
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &shape); err != nil || shape == nil {
		return sceneResponse{}, coordinationInvalid("json_object_required", "scene", "object")
	}
	if _, exists := shape["needs_material"]; exists {
		var request struct {
			IDs []string `json:"needs_material"`
		}
		if err := decodeSceneJSON(text, &request); err != nil {
			return sceneResponse{}, err
		}
		if len(request.IDs) < 1 || len(request.IDs) > 4 || !uniqueSceneIDs(request.IDs) {
			return sceneResponse{}, coordinationInvalid("scene_material_invalid", "needs_material", "one-to-four-unique-listed-material-ids")
		}
		return sceneResponse{Material: request.IDs}, nil
	}
	if _, exists := shape["needs_resolution"]; exists {
		var request struct {
			Resolution sceneResolutionRequest `json:"needs_resolution"`
		}
		if err := decodeSceneJSON(text, &request); err != nil {
			return sceneResponse{}, err
		}
		return sceneResponse{Resolution: &request.Resolution}, nil
	}
	var draft SceneDraft
	if err := decodeSceneJSON(text, &draft); err != nil {
		return sceneResponse{}, err
	}
	return sceneResponse{Draft: &draft}, nil
}

func (b *sceneBeat) UnmarshalJSON(data []byte) error {
	type plain sceneBeat
	var value plain
	fields := []string{"local_id", "kind", "actor_id", "offset_minutes", "basis", "content", "recipients", "bystanders", "projections", "effects"}
	if err := DecodeGeneratedJSON(string(data), &value, nil, fields); err != nil {
		return err
	}
	*b = sceneBeat(value)
	return b.validateGeneratedFields()
}

func (b *sceneBeat) validateGeneratedFields() error {
	if strings.TrimSpace(b.LocalID) == "" || strings.ContainsAny(b.LocalID, ": \t\r\n") || len(b.LocalID) > 64 || strings.TrimSpace(b.ActorID) == "" || strings.TrimSpace(b.Content) == "" || b.OffsetMinutes < 0 || b.OffsetMinutes > 120 || len(b.Basis) == 0 || !uniqueSceneIDs(b.Basis) || !uniqueSceneIDs(b.Recipients) || !uniqueSceneIDs(b.Bystanders) {
		return coordinationInvalid("scene_beat_invalid", "beats", "bounded-node-with-unique-bases-and-recipients")
	}
	switch b.Kind {
	case "dialogue":
		if b.Scope == nil || !slices.Contains([]string{"public", "private"}, *b.Scope) || b.Status != nil || b.Attempt != nil || len(b.Projections) != 0 || len(b.Bystanders) != 0 || len(b.Effects.Movements)+len(b.Effects.StateEffects)+len(b.Effects.ItemTransfers) != 0 {
			return coordinationInvalid("scene_dialogue_invalid", "beats", "speech-scope-and-program-derived-projections")
		}
		if *b.Scope == "public" && len(b.Recipients) != 0 || *b.Scope == "private" && (len(b.Recipients) < 1 || len(b.Recipients) > 4 || slices.Contains(b.Recipients, b.ActorID)) {
			return coordinationInvalid("scene_dialogue_audience_invalid", "beats.recipients", "empty-public-or-one-to-four-private-listeners")
		}
	case "action_result":
		if b.Scope != nil || b.Status == nil || !sceneStatus(*b.Status) || b.Attempt == nil || strings.TrimSpace(b.Attempt.Content) == "" || b.Attempt.InputFragmentIndex != nil && (*b.Attempt.InputFragmentIndex < 0 || *b.Attempt.InputFragmentIndex > 3) {
			return coordinationInvalid("scene_action_invalid", "beats", "attempt-and-result-status")
		}
		if b.ActorID == "player" && b.Attempt.InputFragmentIndex == nil || b.ActorID != "player" && b.Attempt.InputFragmentIndex != nil {
			return coordinationInvalid("scene_attempt_binding_invalid", "beats.attempt", "only-player-attempts-reference-original-input")
		}
	case "observation", "world_change":
		if b.Scope != nil || b.Status != nil || b.Attempt != nil || len(b.Projections) == 0 || b.Kind == "observation" && len(b.Effects.Movements)+len(b.Effects.StateEffects)+len(b.Effects.ItemTransfers) != 0 {
			return coordinationInvalid("scene_observation_invalid", "beats", "explicit-personal-projections-with-kind-appropriate-effects")
		}
	default:
		return coordinationInvalid("scene_kind_invalid", "beats.kind", "dialogue|action_result|observation|world_change")
	}
	return nil
}

func (p *sceneInput) UnmarshalJSON(data []byte) error {
	type plain sceneInput
	var value plain
	if err := DecodeGeneratedJSON(string(data), &value, nil, []string{"text", "intent_type", "addressee_id", "visibility", "beat_ids", "status", "unexecuted_reason"}); err != nil {
		return err
	}
	*p = sceneInput(value)
	return nil
}

func (p *sceneResolutionRequest) UnmarshalJSON(data []byte) error {
	type plain sceneResolutionRequest
	var value plain
	if err := DecodeGeneratedJSON(string(data), &value, nil, []string{"rule_id", "input_fragment_index", "prefix_beats", "prefix_elapsed_minutes"}); err != nil {
		return err
	}
	if value.RuleID == "" || value.InputFragmentIndex < 0 || value.InputFragmentIndex > 3 {
		return coordinationInvalid("scene_resolution_invalid", "needs_resolution", "selected-rule-and-original-fragment")
	}
	if err := validateSceneBeats(value.PrefixBeats, value.PrefixElapsedMinutes); err != nil {
		return err
	}
	*p = sceneResolutionRequest(value)
	return nil
}

func (d *SceneDraft) validateGeneratedFields() error {
	if d.SchemaRevision != sceneDraftRevision || len(d.InputMap) < 1 || len(d.InputMap) > 4 || len(d.NarrativeBlocks) == 0 || d.ProgressUpdates == nil || !slices.Contains([]string{"completed", "player_choice", "interrupted", "time_limit"}, d.Stop.Reason) || strings.TrimSpace(d.Stop.Content) == "" {
		return coordinationInvalid("scene_draft_invalid", "scene", "complete-scene-draft.v1")
	}
	if err := validateSceneBeats(d.Beats, d.ElapsedMinutes); err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, b := range d.Beats {
		ids[b.LocalID] = true
	}
	for _, part := range d.InputMap {
		if strings.TrimSpace(part.Text) == "" || !slices.Contains([]string{"speak", "observe", "act"}, part.IntentType) || !slices.Contains([]string{"public", "private"}, part.Visibility) || part.Visibility == "private" && part.AddresseeID == "" || !sceneStatus(part.Status) || part.BeatIDs == nil || !uniqueSceneIDs(part.BeatIDs) || part.WaitMinutes < 0 || part.WaitMinutes > 120 || part.Status == "not_executed" && strings.TrimSpace(part.UnexecutedReason) == "" || part.Status == "succeeded" && part.UnexecutedReason != "" {
			return coordinationInvalid("scene_input_invalid", "input_map", "original-input-scope-and-execution-status")
		}
		for _, id := range part.BeatIDs {
			if !ids[id] {
				return coordinationInvalid("scene_input_beat_invalid", "input_map.beat_ids", "existing-node")
			}
		}
		if len(part.BeatIDs) == 0 && part.UnexecutedReason == "" {
			return coordinationInvalid("scene_input_unanswered", "input_map", "response-node-or-explicit-unexecuted-reason")
		}
	}
	for _, block := range d.NarrativeBlocks {
		if strings.TrimSpace(block.Text) == "" || block.BeatIDs == nil || !uniqueSceneIDs(block.BeatIDs) {
			return coordinationInvalid("scene_narrative_invalid", "narrative_blocks", "nonempty-paragraph-with-node-references")
		}
		for _, id := range block.BeatIDs {
			if !ids[id] {
				return coordinationInvalid("scene_narrative_beat_invalid", "narrative_blocks.beat_ids", "existing-node")
			}
		}
	}
	return nil
}

func validateSceneBeats(beats []sceneBeat, elapsed int) error {
	if beats == nil || len(beats) > sceneBeatLimit || elapsed < 0 || elapsed > 120 {
		return coordinationInvalid("scene_span_invalid", "beats", "at-most-24-nodes-and-120-minutes")
	}
	seen, offset := map[string]bool{}, 0
	for _, b := range beats {
		if err := b.validateGeneratedFields(); err != nil {
			return err
		}
		if seen[b.LocalID] || b.OffsetMinutes < offset || b.OffsetMinutes > elapsed {
			return coordinationInvalid("scene_sequence_invalid", "beats", "unique-ids-with-monotone-offsets")
		}
		for _, basis := range b.Basis {
			if id, ok := strings.CutPrefix(basis, "beat:"); ok && !seen[id] {
				return coordinationInvalid("scene_basis_order_invalid", "beats.basis", "earlier-node-only")
			}
		}
		seen[b.LocalID], offset = true, b.OffsetMinutes
	}
	if len(beats) > 0 && offset != elapsed {
		return coordinationInvalid("scene_elapsed_invalid", "elapsed_minutes", "last-node-offset")
	}
	return nil
}

func validateSceneInput(d *SceneDraft, run wiaworld.Run) error {
	raw, cursor := []rune(run.Input), 0
	for i := range d.InputMap {
		part := &d.InputMap[i]
		text := []rune(part.Text)
		if len(text) == 0 {
			return coordinationInvalid("scene_input_span_invalid", "input_map.text", "nonempty-original-input")
		}
		for cursor < len(raw) && unicode.IsSpace(raw[cursor]) && raw[cursor] != text[0] {
			cursor++
		}
		if cursor+len(text) > len(raw) || string(raw[cursor:cursor+len(text)]) != part.Text {
			return coordinationInvalid("scene_input_span_invalid", fmt.Sprintf("input_map[%d].text", i), "ordered-contiguous-original-input")
		}
		part.Start, part.End, cursor = cursor, cursor+len(text), cursor+len(text)
	}
	for cursor < len(raw) && unicode.IsSpace(raw[cursor]) {
		cursor++
	}
	if cursor != len(raw) {
		return coordinationInvalid("scene_input_coverage_invalid", "input_map", "cover-all-non-whitespace-input")
	}
	return nil
}

func uniqueSceneIDs(ids []string) bool {
	seen := map[string]bool{}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func sceneStatus(status string) bool {
	return slices.Contains([]string{"succeeded", "failed", "partial", "not_executed"}, status)
}

// Presence is checked recursively because zero is a valid time, delta and input
// index. A missing nested field must not silently become an intentional zero.
func decodeSceneJSON(text string, target any) error {
	if err := requireSceneFields(json.RawMessage(text), reflect.TypeOf(target).Elem(), "scene"); err != nil {
		return err
	}
	return DecodeGeneratedJSON(text, target, nil, nil)
}

func requireSceneFields(data json.RawMessage, kind reflect.Type, path string) error {
	if strings.TrimSpace(string(data)) == "null" {
		return coordinationInvalid("json_required_field_null", path, "non-null-field")
	}
	if kind.Kind() == reflect.Pointer {
		return requireSceneFields(data, kind.Elem(), path)
	}
	switch kind.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil || object == nil {
			return coordinationInvalid("json_object_required", path, "object")
		}
		for i := 0; i < kind.NumField(); i++ {
			field := kind.Field(i)
			tag := strings.Split(field.Tag.Get("json"), ",")
			if tag[0] == "-" || tag[0] == "" {
				continue
			}
			value, exists := object[tag[0]]
			if !exists {
				if !slices.Contains(tag, "omitempty") {
					return coordinationInvalid("json_required_field_missing", path+"."+tag[0], "explicit-field")
				}
				continue
			}
			if err := requireSceneFields(value, field.Type, path+"."+tag[0]); err != nil {
				return err
			}
		}
	case reflect.Slice:
		var array []json.RawMessage
		if err := json.Unmarshal(data, &array); err != nil {
			return coordinationInvalid("json_array_required", path, "array")
		}
		for i, value := range array {
			if err := requireSceneFields(value, kind.Elem(), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}
