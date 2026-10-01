import {
  ApiError,
  type Character,
  type BehaviorPolicyCatalog,
  type GameSummary,
  type ModelCandidate,
  type ModelInfo,
  type Message,
  type MessagePage,
  type NarrativeSettings,
  type Run,
  type SaveOperation,
  type Status,
  type WorldSummary,
  type PublicState,
  type PublicItem,
} from './types'
import type { MemoryView, CorrectionRequest } from './types'

export function fetchSuggestions(world: string): Promise<import('./types').SuggestionSet> {
  return request(`/api/v1/worlds/${encodeURIComponent(world)}/suggestions`);
}
export function requestSuggestions(world: string, basis: import('./types').SuggestionBasis, revision: number, enabled?: boolean): Promise<import('./types').SuggestionSet> {
  return request(`/api/v1/worlds/${encodeURIComponent(world)}/suggestions`, {
    method: enabled === undefined ? 'POST' : 'PUT', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ basis, expected_active_revision: revision, enabled }),
  });
}

export function fetchPersonas(): Promise<import('./types').Persona[]> {
  return request<{ personas: import('./types').Persona[] }>('/api/v1/personas').then(r => r.personas ?? []);
}
export function createPersona(name: string, profile: string): Promise<import('./types').Persona> {
  return request<{ persona: import('./types').Persona }>('/api/v1/personas', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name, profile }),
  }).then(r => r.persona);
}
export function updatePersona(personaID: string, name: string, profile: string, expectedVersion: number): Promise<import('./types').Persona> {
  return request<{ persona: import('./types').Persona }>(`/api/v1/personas/${encodeURIComponent(personaID)}`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name, profile, expected_version: expectedVersion }),
  }).then(r => r.persona);
}
export function deletePersona(personaID: string): Promise<void> {
  return request<void>(`/api/v1/personas/${encodeURIComponent(personaID)}`, { method: 'DELETE' });
}
export function savePlayerProfile(world: string, playerName: string, playerProfile: string, expectedContextEpoch: number): Promise<WorldSummary> {
  return request<{ world: WorldSummary }>(`/api/v1/worlds/${encodeURIComponent(world)}/player-profile`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ player_name: playerName, player_profile: playerProfile, expected_context_epoch: expectedContextEpoch }),
  }).then(r => r.world);
}

export function fetchContentProjects(): Promise<import('./types').ContentProject[]> {
  return request<{ projects: import('./types').ContentProject[] }>('/api/v1/content/projects').then(r => r.projects ?? []);
}
export function createContentProject(gameID: string, title: string): Promise<import('./types').ContentProject> {
  return request<{ project: import('./types').ContentProject }>('/api/v1/content/projects', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ game_id: gameID, title }),
  }).then(r => r.project);
}
export function fetchContentProject(projectID: string): Promise<{ project: import('./types').ContentProject; drafts: import('./types').ContentDraftSummary[] }> {
  return request(`/api/v1/content/projects/${encodeURIComponent(projectID)}`);
}
export function createContentDraft(projectID: string, baseRevision = ''): Promise<import('./types').ContentDraft> {
  return request<{ draft: import('./types').ContentDraft }>(`/api/v1/content/projects/${encodeURIComponent(projectID)}/drafts`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ base_revision: baseRevision }),
  }).then(r => r.draft);
}
export function fetchContentDraft(draftID: string): Promise<import('./types').ContentDraft> {
  return request<{ draft: import('./types').ContentDraft }>(`/api/v1/content/drafts/${encodeURIComponent(draftID)}`).then(r => r.draft);
}
export function saveContentDraft(draftID: string, payload: import('./types').ContentDraftPayload, expectedVersion: number): Promise<import('./types').ContentDraft> {
  return request<{ draft: import('./types').ContentDraft }>(`/api/v1/content/drafts/${encodeURIComponent(draftID)}`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ payload, expected_version: expectedVersion }),
  }).then(r => r.draft);
}
export function deleteContentDraft(draftID: string): Promise<void> {
  return request<void>(`/api/v1/content/drafts/${encodeURIComponent(draftID)}`, { method: 'DELETE' });
}
export function fetchContentPreview(draftID: string, author = false): Promise<import('./types').ContentDraftPreview> {
  return request<{ preview: import('./types').ContentDraftPreview }>(`/api/v1/content/drafts/${encodeURIComponent(draftID)}/preview${author ? '?view=author' : ''}`).then(r => r.preview);
}
export function publishContentDraft(draftID: string, requestKey: string, expectedDraftVersion: number, expectedProjectVersion: number): Promise<import('./types').ContentOperation> {
  return request<{ operation: import('./types').ContentOperation }>(`/api/v1/content/drafts/${encodeURIComponent(draftID)}/publish`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ request_key: requestKey, expected_draft_version: expectedDraftVersion, expected_project_version: expectedProjectVersion }),
  }).then(r => r.operation);
}
export function fetchContentDraftAssets(draftID: string): Promise<import('./types').ContentDraftAsset[]> {
  return request<{ assets: import('./types').ContentDraftAsset[] }>(`/api/v1/content/drafts/${encodeURIComponent(draftID)}/assets`).then(r => r.assets ?? []);
}
export function uploadContentDraftAsset(draftID: string, name: string, file: File): Promise<import('./types').ContentDraftAsset> {
  const form = new FormData();
  form.append('name', name);
  form.append('file', file);
  return request<{ asset: import('./types').ContentDraftAsset }>(`/api/v1/content/drafts/${encodeURIComponent(draftID)}/assets`, {
    method: 'POST', body: form,
  }, 60000).then(r => r.asset);
}
export function deleteContentDraftAsset(draftID: string, assetID: string): Promise<void> {
  return request<void>(`/api/v1/content/drafts/${encodeURIComponent(draftID)}/assets?asset_id=${encodeURIComponent(assetID)}`, { method: 'DELETE' });
}

export function fetchPromotion(world: string, bystanderID: string, author = false): Promise<import('./types').PromotionPreview> {
  const query = new URLSearchParams({ bystander_id: bystanderID });
  if (author) query.set('view', 'author');
  return request<{ promotion: import('./types').PromotionPreview }>(`/api/v1/worlds/${encodeURIComponent(world)}/character-promotions?${query}`).then(r => r.promotion);
}
export function promoteCharacter(world: string, payload: import('./types').PromotionRequest): Promise<import('./types').Character> {
  return request<{ character: import('./types').Character }>(`/api/v1/worlds/${encodeURIComponent(world)}/character-promotions`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload),
  }).then(r => r.character);
}
export function importContentPreview(projectID: string, file: File): Promise<import('./types').ImportPreview> {
  const form = new FormData();
  form.append('project_id', projectID);
  form.append('file', file);
  return request<{ import: import('./types').ImportPreview }>('/api/v1/content/imports/preview', { method: 'POST', body: form }, 60000).then(r => r.import);
}
export function confirmContentImport(draftID: string, requestKey: string, expectedVersion: number): Promise<import('./types').ContentDraft> {
  return request<{ draft: import('./types').ContentDraft }>('/api/v1/content/imports/confirm', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ draft_id: draftID, request_key: requestKey, expected_version: expectedVersion }),
  }).then(r => r.draft);
}
export function contentExportURL(gameID: string, revision: string): string {
  return `/api/v1/content/revisions/${encodeURIComponent(gameID)}/${encodeURIComponent(revision)}/export`;
}

export function fetchUsage(world = '', before = 0): Promise<import('./types').UsagePage> {  const query = new URLSearchParams();
  if (world) query.set('world_id', world);
  if (before) query.set('before_id', String(before));
  return request(`/api/v1/usage?${query}`);
}

export function fetchMemory(world: string, author = false, scope = 'player', before = 0): Promise<MemoryView> {
  const query = new URLSearchParams({ scope }); if (before) query.set('before_seq', String(before));
  return request(`/api/v1/worlds/${encodeURIComponent(world)}/${author ? 'author-memory' : 'memory'}?${query}`)
}
export function correctMemory(world: string, payload: CorrectionRequest): Promise<unknown> {
  return request(`/api/v1/worlds/${encodeURIComponent(world)}/corrections`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) })
}
export function rebuildMemory(world: string, epoch: number): Promise<unknown> {
  return request(`/api/v1/worlds/${encodeURIComponent(world)}/memory/rebuild`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ expected_context_epoch: epoch }) })
}

export async function exchangeBootstrapToken(): Promise<void> {
  const fragment = new URLSearchParams(window.location.hash.replace(/^#/, ''))
  const token = fragment.get('token')
  if (!token) return
  try {
    await request<void>('/api/session', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token }),
    })
  } finally {
    window.history.replaceState(null, '', window.location.pathname + window.location.search)
  }
}

export const REQUEST_TIMEOUT_MS = 15000
async function request<T>(url: string, init?: RequestInit, deadlineMS = REQUEST_TIMEOUT_MS): Promise<T> {
  const controller = new AbortController()
  let timeout: ReturnType<typeof setTimeout> | undefined
  const expired = new Promise<never>((_, reject) => {
    timeout = setTimeout(() => {
      reject(new ApiError(0, 'request_timeout', '连接等待超时，暂时无法确认操作结果。'))
      controller.abort()
    }, deadlineMS)
  })
  try {
    return await Promise.race([expired, (async () => {
      const response = await fetch(url, {
        ...init, signal: controller.signal,
        headers: { Accept: 'application/json', ...(init?.headers ?? {}) },
      })
      if (!response.ok) throw await toApiError(response)
      if (response.status === 204) return undefined as T
      return await response.json() as T
    })()])
  } finally { clearTimeout(timeout) }
}

export async function fetchStatus(): Promise<Status> {
  const result = await request<{ status: Status }>('/api/v1/status')
  return result.status
}

export async function fetchGames(): Promise<{ games: GameSummary[]; issues: { file: string; field?: string; code: string; message: string }[] }> {
  return request('/api/v1/games')
}

export async function fetchGame(id: string): Promise<GameSummary> {
  const result = await request<{ game: GameSummary }>(`/api/v1/games/${encodeURIComponent(id)}`)
  return result.game
}
export async function fetchWorldGame(world: string): Promise<GameSummary> {
  const result = await request<{ game: GameSummary }>(`/api/v1/worlds/${encodeURIComponent(world)}/game`)
  return result.game
}

export async function fetchWorlds(): Promise<WorldSummary[]> {
  const result = await request<{ worlds: WorldSummary[] }>('/api/v1/worlds')
  return result.worlds
}

export async function createWorld(input: { name: string; game_id: string; expected_revision: string; request_key: string; player_name: string; player_profile: string }): Promise<WorldSummary> {
  const result = await request<{ world: WorldSummary }>('/api/v1/worlds', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ...input, activate: true }),
  })
  return result.world
}

export async function fetchWorld(worldID: string): Promise<{ world: WorldSummary; player_name: string; player_profile: string; narrative_settings: NarrativeSettings; behavior_policy_defaults: BehaviorPolicyCatalog; messages: Message[]; characters: Character[]; states: PublicState[]; items: PublicItem[] }> {
  return request(`/api/v1/worlds/${encodeURIComponent(worldID)}`)
}

export async function fetchMessages(worldID: string, cursor: { before_seq?: number; after_seq?: number } = {}): Promise<MessagePage> {
  const query = new URLSearchParams({ limit: '100' })
  if (cursor.before_seq !== undefined) query.set('before_seq', String(cursor.before_seq))
  if (cursor.after_seq !== undefined) query.set('after_seq', String(cursor.after_seq))
  return request(`/api/v1/worlds/${encodeURIComponent(worldID)}/messages?${query}`)
}

export async function saveAgentSettings(worldID: string, settings: NarrativeSettings, expectedContextEpoch: number): Promise<{ settings: NarrativeSettings; world: WorldSummary }> {
  return request(`/api/v1/worlds/${encodeURIComponent(worldID)}/agent-settings`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ...settings, expected_context_epoch: expectedContextEpoch }),
  })
}

export async function activateWorld(worldID: string, expectedRevision: number, requestKey = crypto.randomUUID()): Promise<Status> {
  return request(`/api/v1/worlds/${encodeURIComponent(worldID)}/activate`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ expected_active_revision: expectedRevision, request_key: requestKey }),
  })
}

export async function deleteWorld(worldID: string, expectedRevision: number): Promise<void> {
  const query = new URLSearchParams({ expected_active_revision: String(expectedRevision) })
  await request(`/api/v1/worlds/${encodeURIComponent(worldID)}?${query}`, { method: 'DELETE' })
}

export async function submitRun(worldID: string, input: { request_key: string; input: string; addressee_id?: string; expected_active_revision: number; expected_message_head: number; expected_event_head: number; expected_context_epoch: number }): Promise<Run> {
  const result = await request<{ run: Run }>(`/api/v1/worlds/${encodeURIComponent(worldID)}/runs`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input),
  })
  return result.run
}

export async function fetchRun(worldID: string, runID: string): Promise<Run> {
  const result = await request<{ run: Run }>(`/api/v1/worlds/${encodeURIComponent(worldID)}/runs/${encodeURIComponent(runID)}`)
  return result.run
}

export async function fetchRuns(worldID: string, requestKey?: string): Promise<Run[]> {
  const result = await request<{ runs: Run[] }>(`/api/v1/worlds/${encodeURIComponent(worldID)}/runs${requestKey ? "?request_key=" + encodeURIComponent(requestKey) : ""}`)
  return Array.isArray(result.runs) ? result.runs : []
}

export async function cancelRun(worldID: string, runID: string): Promise<void> {
  await request(`/api/v1/worlds/${encodeURIComponent(worldID)}/runs/${encodeURIComponent(runID)}/cancel`, { method: 'POST' })
}

export async function retryRun(worldID: string, runID: string, requestKey: string = crypto.randomUUID()): Promise<Run> {
  const result = await request<{ run: Run }>(`/api/v1/worlds/${encodeURIComponent(worldID)}/runs/${encodeURIComponent(runID)}/retry`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ request_key: requestKey }),
  })
  return result.run
}

export async function saveAs(worldID: string, name: string, expectedRevision: number, requestKey: string): Promise<SaveOperation> {
  const result = await request<{ operation: SaveOperation }>(`/api/v1/worlds/${encodeURIComponent(worldID)}/save-as`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, request_key: requestKey, expected_active_revision: expectedRevision }),
  })
  return result.operation
}

export async function fetchCopyOperation(operationID: string): Promise<SaveOperation> {
  const result = await request<{ operation: SaveOperation }>(`/api/v1/world-copy-operations/${encodeURIComponent(operationID)}`)
  return result.operation
}

export async function fetchModel(): Promise<{ model: ModelInfo; model_error?: string; providers: { provider: string; model: string }[] }> {
  return request('/api/v1/model-profiles')
}

export async function saveModel(candidate: ModelCandidate): Promise<Status> {
  return request('/api/v1/model-profiles', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(candidate),
  }, 70000)
}

async function toApiError(response: Response): Promise<ApiError> {
  try {
    const body = await response.json() as { error?: { code?: string; message?: string } }
    return new ApiError(response.status, body.error?.code ?? 'unknown', body.error?.message ?? response.statusText)
  } catch {
    return new ApiError(response.status, 'unknown', response.statusText)
  }
}
