package turn

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"gameagent/backend/internal/story"
	"gameagent/backend/internal/tokenestimate"
)

// Material authorization applies equally to the directory and the full text.
func materialAuthorized(snapshot Snapshot, purpose, recipient string, m story.Material) bool {
	if purpose == "scene" {
		if m.Purpose == "npc_plan" {
			return false
		}
		if m.Visibility == "owner" {
			return slices.Contains(snapshot.sceneEntities, m.OwnerID)
		}
		return true
	}
	author := purpose == "coordination" || purpose == "plot" || purpose == "plot_actions" || purpose == "event_generation"
	if m.Purpose == "npc_plan" {
		return false // The initial file initializes a persisted plan, rather than restoring it on every call.
	}
	if author {
		return m.Visibility != "owner"
	}
	if m.Visibility == "author" {
		return false
	}
	if m.Visibility == "owner" {
		return m.OwnerID == recipient && (purpose == "npc" || recipient == "player")
	}
	if m.Delivery == "core" || slices.Contains(m.KnownTo, recipient) || snapshot.PerceivedSources[recipient][m.SourceID(snapshot.Definition.Revision)] {
		return true
	}
	return m.Purpose == "location_lore" && materialAtLocation(snapshot, m, snapshot.Positions[recipient])
}

func materialAtLocation(snapshot Snapshot, m story.Material, place string) bool {
	for _, id := range m.LocationIDs {
		if id == place {
			return true
		}
		for _, location := range snapshot.Definition.Locations {
			if location.ID == place && location.Parent == id {
				return true
			}
		}
	}
	return false
}

func materialSection(m story.Material, revision string) Section {
	return Section{Name: "material:" + m.ID, Text: fmt.Sprintf("冻结材料 %s（来源 %s）：\n%s", m.ID, m.SourceID(revision), m.Body), Sources: []string{m.SourceID(revision)}}
}

// Required additions survive the existing recent-memory budget rebuild.
func appendRequiredMaterial(material Material, section Section) Material {
	bounded := material.Bounded
	material.Required += "\n" + section.Text
	material.RequiredSources = append(slices.Clone(material.RequiredSources), section.Sources...)
	optional := []Section{}
	for _, candidate := range material.Optional {
		if candidate.Name != section.Name {
			optional = append(optional, candidate)
		}
	}
	material.Optional = optional
	if bounded != nil {
		material.Bounded = func(limit int, system string) (Material, bool) {
			next, changed := bounded(max(0, limit-tokenestimate.EstimateText(section.Text)-1), system)
			return appendRequiredMaterial(next, section), changed
		}
	}
	return material
}

func appendOptionalMaterial(material Material, section Section) Material {
	bounded := material.Bounded
	material.Optional = append(slices.Clone(material.Optional), section)
	if bounded != nil {
		material.Bounded = func(limit int, system string) (Material, bool) {
			next, changed := bounded(limit, system)
			return appendOptionalMaterial(next, section), changed
		}
	}
	return material
}

func selectStoryMaterials(snapshot Snapshot, purpose, recipient string, material Material) (Material, map[string]story.Material) {
	available := map[string]story.Material{}
	if purpose == "npc" {
		material = personalPlanContext(snapshot, recipient, material)
	}
	switch purpose {
	case "npc", "intent", "coordination", "plot", "plot_actions", "event_generation", "narration", "scene":
	default:
		return material, available
	}
	type candidate struct {
		m     story.Material
		score int
	}
	var candidates []candidate
	for _, m := range snapshot.Definition.Materials {
		if !materialAuthorized(snapshot, purpose, recipient, m) {
			continue
		}
		if m.Delivery == "core" {
			if slices.Contains(material.RequiredSources, m.SourceID(snapshot.Definition.Revision)) {
				continue
			}
			if purpose == "scene" {
				material.Prefix = append(material.Prefix, materialSection(m, snapshot.Definition.Revision))
			} else {
				material = appendRequiredMaterial(material, materialSection(m, snapshot.Definition.Revision))
			}
			continue
		}
		score := 0
		place := snapshot.Positions[recipient]
		if place == "" {
			place = snapshot.SceneLocation
		}
		if materialAtLocation(snapshot, m, place) {
			score += 4
		}
		if m.OwnerID != "" && (m.OwnerID == recipient || purpose == "scene" && slices.Contains(snapshot.sceneEntities, m.OwnerID)) {
			score += 6
		}
		for _, id := range append(slices.Clone(m.EntityIDs), m.ItemIDs...) {
			if id == recipient || strings.Contains(material.Required, id) {
				score += 2
			}
		}
		if strings.Contains(material.Required, m.ID) {
			score += 8
		}
		candidates = append(candidates, candidate{m, score})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].m.ID < candidates[j].m.ID
	})
	var directory []string
	selected := 0
	for i, c := range candidates {
		if i >= 16 {
			break
		}
		available[c.m.ID] = c.m
		directory = append(directory, c.m.ID+": "+c.m.Summary)
		if c.score > 0 && selected < 4 && !slices.Contains(material.RequiredSources, c.m.SourceID(snapshot.Definition.Revision)) {
			section := materialSection(c.m, snapshot.Definition.Revision)
			if purpose == "scene" {
				section.Priority = 50
			}
			material = appendOptionalMaterial(material, section)
			selected++
		}
	}
	if len(directory) > 0 {
		material = appendRequiredMaterial(material, Section{Name: "material_directory", Text: "获准材料目录（摘要只描述资料，不代表正文已提供）：\n" + strings.Join(directory, "\n")})
		if purpose == "scene" {
			material.System += "\n目录只说明获准文件；正文未提供时不能引用它为事实。一次补充上下文机会与目的地人物扩展共享，最多读取四个目录文件。读取没有世界后果。"
		} else {
			material.System += "\n若最终输出前必须读取目录中尚未提供的正文，只返回 {\"needs_material\":[\"材料ID\"]}，每个用途一次、最多四份，整轮最多两次。只请求目录中的ID；否则直接返回最终输出。读取不产生行动、决定或经历。"
		}
	}
	if purpose == "coordination" || purpose == "plot" || purpose == "plot_actions" || purpose == "event_generation" || purpose == "scene" {
		sources := currentFactSources(snapshot)
		material = appendRequiredMaterial(material, Section{Name: "current_fact_sources", Text: "本次权威位置、状态、关系、物品的因果来源（只作当前事实依据）：" + strings.Join(sources, ","), Sources: sources})
	}
	return material, available
}

func currentFactSources(snapshot Snapshot) []string {
	seen := map[string]bool{}
	for _, id := range snapshot.PositionSources {
		if id != "" {
			seen[id] = true
		}
	}
	for _, values := range snapshot.States {
		for _, state := range values {
			if state.SourceEvent != "" {
				seen[state.SourceEvent] = true
			}
		}
	}
	for _, relation := range snapshot.Relationships {
		if relation.SourceEvent != "" {
			seen[relation.SourceEvent] = true
		}
	}
	for _, item := range snapshot.Items {
		if item.SourceEvent != "" {
			seen[item.SourceEvent] = true
		}
	}
	sources := []string{}
	for id := range seen {
		sources = append(sources, id)
	}
	sort.Strings(sources)
	return sources
}

func materialRequest(text string) ([]string, bool, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &object) != nil {
		return nil, false, nil
	}
	if _, ok := object["needs_material"]; !ok {
		return nil, false, nil
	}
	var request struct {
		IDs []string `json:"needs_material"`
	}
	if err := DecodeGeneratedJSON(text, &request, nil, []string{"needs_material"}); err != nil {
		return nil, true, err
	}
	seen := map[string]bool{}
	if len(request.IDs) == 0 || len(request.IDs) > 4 {
		return nil, true, coordinationInvalid("material_request_limit", "needs_material", "one-to-four-unique-material-ids")
	}
	for _, id := range request.IDs {
		if id == "" || seen[id] {
			return nil, true, coordinationInvalid("material_request_invalid", "needs_material", "unique-listed-material-ids")
		}
		seen[id] = true
	}
	return request.IDs, true, nil
}

type materialReadBudget struct {
	mu        sync.Mutex
	used      int
	requested map[string]bool
}

func newMaterialReadBudget() *materialReadBudget {
	return &materialReadBudget{requested: map[string]bool{}}
}

type materialReadGroup struct {
	mu       sync.Mutex
	readers  []string
	ready    map[string]bool
	requests map[string]bool
	closed   bool
	done     chan struct{}
	grants   map[string]bool
	attempts map[string]bool
}

func newMaterialReadGroup(readers []string) *materialReadGroup {
	return &materialReadGroup{readers: slices.Clone(readers), ready: map[string]bool{}, requests: map[string]bool{}, done: make(chan struct{}), grants: map[string]bool{}, attempts: map[string]bool{}}
}

func (g *materialReadGroup) mark(id string, request bool) {
	g.ready[id] = true
	if request {
		g.requests[id] = true
	}
	if !g.closed && len(g.ready) == len(g.readers) {
		g.closed = true
		close(g.done)
	}
}

func (g *materialReadGroup) finish(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.mark(id, false)
}

func (b *materialReadBudget) request(ctx context.Context, scope ContextScope, group *materialReadGroup) (bool, error) {
	if b == nil {
		b = newMaterialReadBudget()
	}
	key := fmt.Sprintf("%s:%s:%d", scope.Purpose, scope.Recipient, scope.Stage)
	if group == nil {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.requested[key] {
			return false, coordinationInvalid("material_request_limit", "needs_material", "one-request-per-purpose")
		}
		b.requested[key] = true
		if b.used >= 2 {
			return false, nil
		}
		b.used++
		return true, nil
	}
	group.mu.Lock()
	if group.attempts[scope.Recipient] {
		group.mu.Unlock()
		return false, coordinationInvalid("material_request_limit", "needs_material", "one-request-per-purpose")
	}
	group.attempts[scope.Recipient] = true
	group.mark(scope.Recipient, true)
	group.mu.Unlock()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-group.done:
	}
	group.mu.Lock()
	defer group.mu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(group.grants) == 0 {
		ids := slices.Clone(group.readers)
		sort.Strings(ids)
		for _, id := range ids {
			if !group.requests[id] {
				continue
			}
			k := fmt.Sprintf("%s:%s:%d", scope.Purpose, id, scope.Stage)
			if b.requested[k] {
				return false, coordinationInvalid("material_request_limit", "needs_material", "one-request-per-purpose")
			}
			b.requested[k] = true
			granted := b.used < 2
			if granted {
				b.used++
			}
			group.grants[id] = granted
		}
	}
	return group.grants[scope.Recipient], nil
}
