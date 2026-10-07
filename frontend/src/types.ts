export interface ModelInfo {
  provider?: string
  model?: string
  configured: boolean
  source?: string
}

export interface PlayRequest {
  request_key: string; expected_turn: number; input: string;
  allow_plot_advance: boolean; transport: 'stream' | 'non_stream';
}
export interface PlaySession {
	version: number;
  id: string; game_id: string; title: string; revision: string; turn: number;
  clock: string; location: string; messages: Message[]; characters: Character[];
  run?: { id: string; request_key: string; input: string; status: string;
    transport: 'stream' | 'non_stream'; allow_plot_advance: boolean; started_at: string;
    elapsed_ms: number; candidate?: string; calls: number; repairs: number;
    error?: string; error_code?: string };
  suggestions: { turn: number; enabled: boolean; status: string; items: string[] };
}
export interface Status {
  ready: boolean
  model: ModelInfo
  model_error?: string
  generation_transport?: { mode: 'stream' | 'non_stream'; streaming_supported: boolean }
  user_id: string
  active_world: WorldSummary | null
  active_revision: number
  data_root: string
  model_config_path: string
}

export interface GameSummary {
  id: string
  title: string
  description: string
  modes: string[]
  default_mode: string
	mode: 'guided' | 'open'
	revision: string
	gameplay: string
	background: string
	cover_url?: string
	cover_alt?: string
	player: { name: string; profile: string; requirements: string; editable: boolean }
	available?: boolean
}

export interface WorldSummary {
	game_title: string
	revision: string
  game_id: string
  world_id: string
  name: string
  mode: string
  turn_seq: number
  message_head: number
  event_head: number
  context_epoch: number
  clock: string
  calendar?: { kind: 'gregorian'; era?: string }
  scene: string
  scene_location?: string
  location?: { id: string; name: string; description?: string }
  adjacent_locations?: { id: string; name: string; description?: string }[]
  status: string
  story_ended?: boolean
  updated_at: string
}

export interface KnownLocation {
  id: string
  name: string
  description?: string
  kind: 'region' | 'place'
  parent?: string
  connections: string[]
}

export interface Character {
  entity_id: string
  definition_id: string
  name: string
  role: string
  in_scene: boolean
}

export interface StateValue {
  type: 'integer' | 'boolean' | 'enum'
  integer?: number
  boolean?: boolean
  enum?: string
}

export interface PublicState {
  entity_id: string
  state_id: string
  name: string
  description?: string
  value: StateValue
  unit?: string
  category?: 'condition' | 'skill' | 'resource'
  currency?: { name: string; denominations: { name: string; units: number }[] }
  display_value?: string
}

export interface PublicItem {
  instance_id: string
  definition_id: string
  name: string
  description?: string
  holder_id?: string
  location_id?: string
}

export type NarrativePerspective = 'first_person' | 'second_person' | 'third_person'
export type NarrativeLength = 'concise' | 'standard' | 'detailed'
export type NarrativeDetail = 'restrained' | 'balanced' | 'rich'
export type PlayerElaboration = 'restrained' | 'natural' | 'expressive'
export type NPCInitiative = 'responsive' | 'contextual' | 'proactive'

export interface BehaviorPolicies {
  coordination: string
  narration: string
  npc: string
}
export interface BehaviorPolicyCatalog extends BehaviorPolicies {
  version: string
  max_chars: number
}
export interface NarrativeSettings {
  perspective: NarrativePerspective
  length: NarrativeLength
  detail: NarrativeDetail
  player_elaboration: PlayerElaboration
  npc_initiative: NPCInitiative
  behavior_policies: BehaviorPolicies
}

export interface Message {
  seq: number
  message_id: string
  kind: 'narrative' | 'player'
  content: string
  run_id?: string
  created_at: string
}

export interface MessagePage {
  messages: Message[]
  has_more: boolean
  next_before_seq?: number
  next_after_seq?: number
}

export interface Run {
  run_id: string
  request_key: string
  request_hash: string
  input: string
  addressee_id?: string
  attempt: number
  status: string
  reason?: string
  error?: string
  message_seq?: number
  created_at: string
  updated_at: string
}

export interface RunProgress {
  run_id: string
  status: string
  budget_seconds: number
  transport?: 'stream' | 'non_stream'
  calls: {
    id: number
    purpose: string
    recipient?: string
    phase: 'waiting' | 'thinking' | 'writing' | 'received' | 'failed'
    started_at: string
    reasoning?: string
    reasoning_chars: number
    reasoning_limited?: boolean
    output_chars: number
  }[]
}

export interface SaveOperation {
  operation_id: string
  request_key: string
  source_world_id: string
  target_world_id: string
  target_name: string
  status: string
  error?: string
  created_at: string
  updated_at: string
}

export interface ModelCandidate {
  provider: string
  model: string
  base_url?: string
  api_key: string
}

export class ApiError extends Error {
  constructor(readonly status: number, readonly code: string, message: string) {
    super(message)
  }
}

export interface MemoryRecord { kind: string; scope: string; target_id: string; content: string }
export interface MemorySource { scope: string; seq: number; id: string; event_id: string; run_id: string; actor: string; kind: string; content: string; created_at: string }
export interface MemoryView {
	corrections: { epoch: number; kind: string; scope: string; target_id: string; original: string; replacement: string }[];
  world_id: string; context_epoch: number; scope: string; scopes: string[];
  digest: { revision: number; through_seq: number; content: string; states: { kind: string; content: string; source_ids: string[] }[]; source_ids: string[] };
  sources: MemorySource[]; records: MemoryRecord[];
  job: { epoch: number; status: string; completed: number; scopes: string[] | null; error: string };
  has_more: boolean; next_before_seq?: number;
}
export interface CorrectionRequest { request_key: string; expected_context_epoch: number; kind: string; scope: string; target_id: string; replacement: string }
export interface UsageCall {
  id: number; world_id: string; run_id: string; attempt: number; purpose: string;
  stage: number; started_at: string; status: string; elapsed_ms: number;
  provider: string; model: string; input_tokens: number | null; output_tokens: number | null;
  reasoning_tokens: number | null; cache_hit_tokens: number | null; cache_miss_tokens: number | null;
  estimated_input_tokens: number; output_limit: number; error_code: string;
}
export interface UsagePage {
  totals: {
    calls: number; unconfirmed: number; failed: number;
    input_known: number; output_known: number; reasoning_known: number; cache_known: number;
    input_tokens: number; output_tokens: number; reasoning_tokens: number;
    cache_hit_tokens: number; cache_miss_tokens: number; cache_hit_rate: number | null;
  };
  calls: UsageCall[]; next_before_id?: number;
}
export interface SuggestionBasis {
  world_id: string; message_head: number; event_head: number; context_epoch: number; revision: string;
}
export interface SuggestionSet {
  id: string; basis: SuggestionBasis; enabled: boolean; status: string; items: string[];
}
export interface Persona {
  persona_id: string; name: string; profile: string; version: number; created_at: string; updated_at: string;
}
export interface ContentProject {
  project_id: string; game_id: string; title: string; current_revision: string; version: number;
  created_at: string; updated_at: string;
}
export interface ContentDraftSummary {
  draft_id: string; project_id: string; base_revision: string; version: number; status: string; updated_at: string;
}
export interface PackBystander {
  bystander_id: string; name: string; description?: string; initial_location?: string; avatar?: string;
}
export interface ContentDraftNPC {
  definition_id: string; revision: string; entity_id: string; name: string; role: string;
  appearance?: string; profile: string; knowledge?: string; initial_concerns?: string;
  initial_location: string; avatar?: string; speaking_examples?: string[]; initial_state?: Record<string, number | boolean | string>;
}
export interface DraftStateDefinition { id: string; name: string; type: 'integer' | 'boolean' | 'enum'; minimum?: number; maximum?: number; enum_values?: string[]; default: number | boolean | string; scope: 'player' | 'npc' | 'all'; projection: 'self' | 'public' | 'hidden'; knowledge: 'owner' | 'public' | 'host_only'; update_policy: { kind: 'readonly' | 'bounded_proposal' | 'rule_only'; max_change_per_turn?: number }; description?: string; unit?: string }
export interface DraftRelationDefinition { id: string; name: string; minimum: number; maximum: number; default: number; max_change_per_turn: number; projection: 'self' | 'public' | 'hidden'; description?: string }
export interface DraftInitialRelation { subject_id: string; target_id: string; relation_type: string; value: number }
export interface DraftItemDefinition { id: string; name: string; description?: string; projection: 'public' | 'holder' | 'hidden' }
export interface DraftItemInstance { instance_id: string; definition_id: string; holder_id?: string; location_id?: string }
export interface DraftFactCondition { kind: 'location_is' | 'state_at_least' | 'state_at_most' | 'relation_at_least' | 'relation_at_most' | 'item_held' | 'item_at' | 'rule_result' | 'rule_result_absent'; entity_id?: string; target_id?: string; fact_id?: string; location_id?: string; value?: number; status?: 'succeeded' | 'failed' }
export interface DraftRuleEffect { kind: 'state_delta'; entity_id: 'actor' | 'player'; state_id: string; delta: number }
export interface DraftActionRule { id: string; name: string; guidance: string; conditions?: DraftFactCondition[]; risk?: { base_target: number; minimum: number; maximum: number; modifiers?: { label: string; amount: number; condition: DraftFactCondition }[] }; success_text: string; failure_text?: string; success_effects?: DraftRuleEffect[]; failure_effects?: DraftRuleEffect[] }
export interface ContentDraftPayload {
  schema_version: number; requires?: Record<string, number>; game_id: string; mode: string; title: string; description: string; gameplay: string;
  background: string; rules: string; author_facts: string; cover?: string; cover_alt?: string;
  player: { name: string; profile: string; requirements?: string; editable?: boolean; initial_state?: Record<string, number | boolean | string> };
  opening: string; initial_location: string; clock: string;  locations: { id: string; kind?: 'region' | 'place'; parent?: string; name: string; description?: string; connections: string[]; public?: boolean }[];
  npcs: ContentDraftNPC[]; bystanders: PackBystander[];
  plot?: unknown; event_generation?: unknown; defaults?: unknown;
  state_definitions?: DraftStateDefinition[]; relation_definitions?: DraftRelationDefinition[]; initial_relations?: DraftInitialRelation[]; item_definitions?: DraftItemDefinition[]; item_instances?: DraftItemInstance[];
  action_rules?: DraftActionRule[];
}
export interface ContentDraft extends ContentDraftSummary { payload: ContentDraftPayload }
export interface ContentOperation {
  operation_id: string; kind: string; target_id: string; stage: string; status: string; safe_error?: string;
}
export interface ContentDraftAsset {
  asset_id: string; relative_name: string; media_type: string; byte_size: number; width: number; height: number;
}
export interface ContentPreviewNPC {
  entity_id: string; definition_id: string; name: string; role: string; appearance?: string; avatar?: string; initial_location?: string;
}
export interface ContentPreviewAuthorNPC extends ContentPreviewNPC {
  revision: string; profile: string; knowledge: string; initial_concerns: string; speaking_examples?: string[];
}
export interface ContentDraftPreview {
  view: string; draft_id: string; version: number; title: string; mode: string; description: string;
  gameplay: string; background: string; opening: string; clock: string; initial_location: string;
  player: { name: string; profile: string }; characters: ContentPreviewNPC[]; bystanders: PackBystander[];
  spoiler_warning?: string; author_rules?: string; author_facts?: string;
  author_characters?: ContentPreviewAuthorNPC[]; author_plot?: unknown; author_event_generation?: unknown;
}
export interface ImportMapping {
  field: string; source: string; target: string; confidence: string; note?: string;
}
export interface ImportReport {
  format: string; format_detail: string; summary: string; mappings: ImportMapping[];
  unsupported: string[]; needs_confirmation: string[]; source_bytes: number;
}
export interface ImportPreview {
  draft_id: string; version: number; project_id: string; report: ImportReport;
}
export interface PromotionSource {
  source_id: string; kind: string; content: string; player_visible: boolean;
}
export interface PromotionDraft {
  role: string; appearance: string; profile: string; knowledge: string;
  initial_concerns: string; speaking_examples?: string[];
}
export interface PromotionPreview {
  bystander_id: string; name: string; description?: string; avatar?: string;
  location: string; in_scene: boolean; experience_count: number;
  player_visible_experiences: PromotionSource[]; draft: PromotionDraft;
}
export interface PromotionRequest {
  request_key: string; expected_context_epoch: number; bystander_id: string;
  source_ids: string[]; draft: PromotionDraft;
}
