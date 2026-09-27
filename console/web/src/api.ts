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
} from './types'
import type { MemoryView, CorrectionRequest } from './types'

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

export async function fetchGames(): Promise<GameSummary[]> {
  const result = await request<{ games: GameSummary[] }>('/api/v1/games')
  return result.games
}

export async function fetchWorlds(): Promise<WorldSummary[]> {
  const result = await request<{ worlds: WorldSummary[] }>('/api/v1/worlds')
  return result.worlds
}

export async function createWorld(input: { name: string; mode: string; player_name: string; player_profile: string }): Promise<WorldSummary> {
  const result = await request<{ world: WorldSummary }>('/api/v1/worlds', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ...input, activate: true }),
  })
  return result.world
}

export async function fetchWorld(worldID: string): Promise<{ world: WorldSummary; player_name: string; player_profile: string; narrative_settings: NarrativeSettings; behavior_policy_defaults: BehaviorPolicyCatalog; messages: Message[]; characters: Character[] }> {
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
