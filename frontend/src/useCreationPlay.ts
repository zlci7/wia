import { computed, onUnmounted, reactive } from 'vue';
import { createPlay, fetchPlay, submitPlay, cancelPlay, requestPlaySuggestions } from './api';
import { ApiError, type GameSummary, type PlayRequest, type PlaySession } from './types';

const storageKey = 'wia.creation-session';

export function useCreationPlay() {
  const state = reactive({
    active: undefined as PlaySession | undefined,
    draft: '', advance: false, transport: 'non_stream' as 'stream' | 'non_stream',
    reasoning: 'low' as 'low' | 'off',
    starting: false, sending: false, cancelling: false, suggestionWriting: false,
    error: '', suggestionError: '', fromSuggestion: false,
    pending: undefined as PlayRequest | undefined, now: Date.now(),
    readerTop: 0, readerBottom: true,
  });
  let pendingCreate: Parameters<typeof createPlay>[0] | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let disposed = false, ticket = 0, refreshing = false, suggestionAttempt = -1;
  let settledRun = '';
  const busy = computed(() => state.active?.run?.status === 'running');
  const canSubmit = computed(() => !!state.active && !!state.draft.trim() && !busy.value && !state.sending && !state.pending);
  const waitingSeconds = computed(() => state.active?.run?.status === 'running'
    ? Math.max(0, Math.floor((state.now - Date.parse(state.active.run.started_at)) / 1000)) : 0);
  function rememberID(id?: string) {
    try { if (id) window.sessionStorage.setItem(storageKey, id); else window.sessionStorage.removeItem(storageKey); } catch { /* A live session also works without browser storage. */ }
  }
  function describe(error: unknown) {
    if (error instanceof ApiError) {
      if (error.code === 'play_session_not_found') return '试玩会话已结束，可以重新开始。';
      if (error.code === 'model_not_configured') return '请先连接模型，再开始试玩。';
      if (error.code === 'version_conflict') return '情境或剧本版本已变化，请刷新后再试。';
      if (error.code === 'world_busy') return '当前操作尚未结束，请稍后再试。';
      if (error.status < 500) return '本次请求未被接受，输入已保留。';
    }
    return '连接暂时中断，输入已保留。请重试连接。';
  }
  function accept(view: PlaySession) {
    if (state.active?.id === view.id && state.active.version > view.version) return;
    state.active = view;
    const pending = state.pending;
    if (pending && view.run?.request_key === pending.request_key) {
      state.pending = undefined;
      if (view.run.status === 'completed') {
        state.draft = ''; state.fromSuggestion = false;
      }
    }
    if (view.run?.status === 'completed' && settledRun !== view.run.id) {
      // Clear only the submitted draft; a draft typed while suggestions load is retained.
      if (state.draft === view.run.input) { state.draft = ''; state.fromSuggestion = false; }
      settledRun = view.run.id;
    }
    if (view.run && ['failed', 'cancelled'].includes(view.run.status)) {
      state.error = view.run.error ?? '本轮未完成，输入已保留。';
      if (!state.draft.trim()) state.draft = view.run.input;
    }
  }
  function schedule() {
    clearTimeout(timer);
    if (disposed || !state.active) return;
    timer = setTimeout(() => void refresh(), busy.value || state.active.suggestions.status === 'generating' ? 300 : 1500);
  }
  async function prepareSuggestions(force = false) {
    const current = state.active;
    if (!current || busy.value || state.pending || state.sending || !current.suggestions.enabled || state.suggestionWriting) return;
    if (!force && (suggestionAttempt === current.turn || current.suggestions.status !== 'empty')) return;
    const id = current.id, epoch = ticket;
    suggestionAttempt = current.turn;
    state.suggestionWriting = true;
    try {
      const view = await requestPlaySuggestions(id, current.turn, true);
      if (ticket !== epoch || state.active?.id !== id) return;
      accept(view); state.suggestionError = '';
    } catch { if (ticket === epoch) state.suggestionError = '建议暂时不可用，可以继续自由输入。'; }
    finally { if (ticket === epoch) state.suggestionWriting = false; schedule(); }
  }
  async function refresh() {
    if (refreshing || disposed || !state.active) return;
    const id = state.active.id, epoch = ticket;
    refreshing = true;
    state.now = Date.now();
    try {
      const view = await fetchPlay(id);
      if (epoch !== ticket || state.active?.id !== id) return;
      state.error = ''; accept(view);
      // Resolve an uncertain submission with its original key and frozen options.
      if (state.pending && !busy.value && !state.sending) await send();
      else await prepareSuggestions();
    } catch (error) {
      if (epoch === ticket) {
        state.error = describe(error);
        if (error instanceof ApiError && error.code === 'play_session_not_found') {
          rememberID(); clearTimeout(timer); state.active = undefined;
        }
      }
    } finally { refreshing = false; schedule(); }
  }
  async function restore() {
    let id: string | null = null;
    try { id = window.sessionStorage.getItem(storageKey); } catch { return false; }
    if (!id) return false;
    try {
      accept(await fetchPlay(id));
      if (busy.value && state.active?.run) {
        state.draft = state.active.run.input;
        state.advance = state.active.run.allow_plot_advance;
        state.transport = state.active.run.transport;
        state.reasoning = state.active.run.reasoning ?? 'low';
      }
      schedule(); void prepareSuggestions(); return true;
    } catch (error) {
      state.error = describe(error);
      if (error instanceof ApiError && error.code === 'play_session_not_found') rememberID();
      return false;
    }
  }
  async function start(game: GameSummary, startingOptionID = '', fresh = false) {
    if (state.starting) return false;
    if (!fresh && state.active?.game_id === game.id && state.active.revision === game.revision) { schedule(); return true; }
    if (busy.value || state.pending) { state.error = '当前试玩尚在处理，请先继续当前会话或取消生成。'; return false; }
    state.starting = true; state.error = '';
    try {
      // Keep an uncertain creation's original key and identity until it is resolved.
      pendingCreate ??= { game_id: game.id, expected_revision: game.revision, request_key: crypto.randomUUID(), starting_option_id: startingOptionID };
      const view = await createPlay(pendingCreate);
      ticket++; clearTimeout(timer);
      pendingCreate = undefined; suggestionAttempt = -1;
      state.suggestionWriting = false;
      state.draft = ''; state.fromSuggestion = false; state.pending = undefined;
      state.reasoning = 'low';
      state.advance = false;
      state.readerTop = 0; state.readerBottom = true;
      state.suggestionError = ''; accept(view); rememberID(view.id); schedule();
      void prepareSuggestions(); return true;
    } catch (error) {
      if (error instanceof ApiError && error.status >= 400 && error.status < 500) pendingCreate = undefined;
      state.error = describe(error); return false;
    }
    finally { state.starting = false; }
  }
  async function send() {
    const current = state.active;
    if (!current || busy.value || state.sending || (!state.pending && !canSubmit.value)) return;
    const epoch = ticket;
    state.pending ??= { request_key: crypto.randomUUID(), expected_turn: current.turn,
      input: state.draft, allow_plot_advance: state.advance, transport: state.transport, reasoning: state.reasoning };
    state.sending = true; state.error = '';
    try {
      const view = await submitPlay(current.id, { ...state.pending });
      if (ticket === epoch && state.active?.id === current.id) accept(view);
    } catch (error) {
      if (ticket === epoch) {
        state.error = describe(error);
        if (error instanceof ApiError && error.status >= 400 && error.status < 500) state.pending = undefined;
      }
    } finally { if (ticket === epoch) state.sending = false; schedule(); }
  }
  async function cancel() {
    const current = state.active;
    if (!current?.run || !busy.value || state.cancelling) return;
    state.cancelling = true;
    try { accept(await cancelPlay(current.id, current.run.id)); }
    catch (error) { state.error = describe(error); }
    finally { state.cancelling = false; schedule(); }
  }
  async function toggleSuggestions() {
    const current = state.active;
    if (!current || busy.value || state.suggestionWriting || state.pending) return;
    state.suggestionWriting = true;
    try { accept(await requestPlaySuggestions(current.id, current.turn, !current.suggestions.enabled)); state.suggestionError = ''; }
    catch { state.suggestionError = '建议设置尚未确认，请重试。'; }
    finally { state.suggestionWriting = false; schedule(); }
  }
  function choose(text: string) {
    const current = state.active;
    if (!current || current.suggestions.turn !== current.turn || current.suggestions.status !== 'ready' || state.draft.trim() || busy.value || state.pending || state.sending || !current.suggestions.items.includes(text)) return;
    state.draft = text; state.fromSuggestion = true;
  }
  function inputKeys(event: KeyboardEvent) {
    if (event.key === 'Enter' && (event.ctrlKey || event.metaKey) && !event.isComposing) { event.preventDefault(); void send(); }
  }
  onUnmounted(() => { disposed = true; ticket++; clearTimeout(timer); });
  return { state, busy, canSubmit, waitingSeconds, start, restore, refresh, send, cancel, choose, inputKeys, toggleSuggestions, prepareSuggestions };
}
