export interface ModelInfo {
  provider?: string
  model?: string
  configured: boolean
  source?: string
}
export interface Status {
  ready: boolean
  model: ModelInfo
  model_error?: string
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
  scene: string
  status: string
  story_ended?: boolean
  updated_at: string
}

export interface Character {
  entity_id: string
  definition_id: string
  name: string
  role: string
  in_scene: boolean
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
