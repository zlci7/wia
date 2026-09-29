import { computed, nextTick, reactive, ref } from "vue";
import { fetchMessages } from "./api";
import { ApiError, type Message } from "./types";

type Anchor = { id: string; offset: number };
export type ReadingSession = {
  suggestionBasis?: import('./types').SuggestionBasis;
  messages: Message[];
  draft: string;
  addressee: string;
  initialized: boolean;
  before?: number;
  loading: boolean;
  historyLoading: boolean;
  error: string;
  historyError: string;
  sending: boolean;
  sendError: string;
  bottom: boolean;
  unread: boolean;
  anchor?: Anchor;
  top: number;
};

export function useStoryReader(onMissing: (id: string) => void) {
  const sessions = reactive<Record<string, ReadingSession>>({});
  const worldID = ref("");
  const viewport = ref<HTMLElement>();
  const pending = new Map<string, Promise<void>>();
  const empty = makeSession();
  function makeSession(): ReadingSession {
    return {
      messages: [],
      draft: "",
      addressee: "",
      initialized: false,
      loading: false,
      historyLoading: false,
      error: "",
      historyError: "",
      sending: false,
      sendError: "",
      bottom: true,
      unread: false,
      top: 0,
    };
  }
  const session = computed(() => sessions[worldID.value] ?? empty);
  function anchor(): Anchor | undefined {
    const box = viewport.value;
    if (!box) return;
    const top = box.getBoundingClientRect().top;
    const element = [
      ...box.querySelectorAll<HTMLElement>("[data-message-id]"),
    ].find((item) => item.getBoundingClientRect().bottom > top);
    return element
      ? {
          id: element.dataset.messageId!,
          offset: element.getBoundingClientRect().top - top,
        }
      : undefined;
  }
  function remember() {
    if (!viewport.value || !worldID.value) return;
    const state = session.value;
    state.top = viewport.value.scrollTop;
    state.anchor = anchor();
    state.bottom =
      viewport.value.scrollHeight -
        viewport.value.clientHeight -
        viewport.value.scrollTop <=
      80;
    if (state.bottom) state.unread = false;
  }
  function restore() {
    const box = viewport.value,
      state = session.value;
    if (!box) return;
    if (state.bottom) box.scrollTop = box.scrollHeight;
    else {
      const element = [
        ...box.querySelectorAll<HTMLElement>("[data-message-id]"),
      ].find((item) => item.dataset.messageId === state.anchor?.id);
      if (element && state.anchor)
        box.scrollTop +=
          element.getBoundingClientRect().top -
          box.getBoundingClientRect().top -
          state.anchor.offset;
      else box.scrollTop = state.top;
    }
  }
  async function latest() {
    session.value.bottom = true;
    session.value.unread = false;
    await nextTick();
    restore();
  }
  async function merge(id: string, items: Message[], newContent: boolean) {
    const state = sessions[id];
    if (!state) return;
    if (worldID.value === id) remember();
    const count = state.messages.length;
    state.messages = [
      ...new Map(
        [...state.messages, ...items].map((message) => [message.seq, message]),
      ).values(),
    ].sort((a, b) => a.seq - b.seq);
    if (newContent && state.messages.length > count && !state.bottom)
      state.unread = true;
    await nextTick();
    if (worldID.value === id) restore();
  }
  async function handleError(id: string, error: unknown, history = false) {
    if (error instanceof ApiError && error.status === 404) {
      onMissing(id);
      return;
    }
    if (worldID.value === id) remember();
    if (sessions[id])
      sessions[id][history ? "historyError" : "error"] =
        error instanceof Error ? error.message : "正文暂时无法读取，请重试。";
    await nextTick();
    if (worldID.value === id) restore();
  }
  async function sync(id = worldID.value) {
    if (!id || !sessions[id]) return;
    if (pending.has(id)) return pending.get(id);
    const state = sessions[id];
    const job = (async () => {
      try {
        if (!state.initialized) {
          state.loading = true;
          const page = await fetchMessages(id);
          if (sessions[id] !== state) return;
          state.error = "";
          state.before = page.next_before_seq;
          await merge(id, page.messages, false);
          state.initialized = true;
        } else {
          let after = state.messages.at(-1)?.seq ?? 0;
          for (;;) {
            const page = await fetchMessages(id, { after_seq: after });
            if (sessions[id] !== state) return;
            state.error = "";
            await merge(id, page.messages, true);
            if (!page.has_more || page.next_after_seq === undefined) break;
            after = page.next_after_seq;
          }
        }
      } catch (error) {
        await handleError(id, error);
      } finally {
        state.loading = false;
      }
    })();
    pending.set(id, job);
    try {
      await job;
    } finally {
      if (pending.get(id) === job) pending.delete(id);
    }
  }
  async function select(id: string) {
    if (id !== worldID.value) {
      remember();
      worldID.value = id;
      if (id) sessions[id] ??= makeSession();
    }
    await nextTick();
    restore();
    if (id) await sync(id);
  }
  async function earlier() {
    const id = worldID.value,
      state = session.value;
    if (state.loading || state.historyLoading || state.before === undefined)
      return;
    state.historyLoading = true;
    try {
      const page = await fetchMessages(id, { before_seq: state.before });
      if (sessions[id] !== state) return;
      state.historyError = "";
      state.before = page.next_before_seq;
      await merge(id, page.messages, false);
    } catch (error) {
      await handleError(id, error, true);
    } finally {
      state.historyLoading = false;
    }
  }
  function forget(id: string) {
    delete sessions[id];
  }
  return {
    sessions,
    worldID,
    session,
    viewport,
    select,
    sync,
    earlier,
    remember,
    restore,
    latest,
    forget,
  };
}
