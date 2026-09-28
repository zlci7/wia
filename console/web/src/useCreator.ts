import { computed, reactive, ref } from "vue";
import {
  createContentDraft,
  createContentProject,
  deleteContentDraft,
  deleteContentDraftAsset,
  fetchContentDraft,
  fetchContentDraftAssets,
  fetchContentPreview,
  fetchContentProject,
  fetchContentProjects,
  fetchPersonas,
  publishContentDraft,
  saveContentDraft,
  uploadContentDraftAsset,
} from "./api";
import {
  ApiError,
  type ContentDraft,
  type ContentDraftAsset,
  type ContentDraftPayload,
  type ContentDraftPreview,
  type ContentDraftSummary,
  type ContentOperation,
  type ContentProject,
  type Persona,
} from "./types";

// Authoring sessions are independent of the play page: switching project, draft or
// page keeps a late response from overwriting newer input.
export const AUTOSAVE_DEBOUNCE_MS = 750;

export function useCreator() {
  const personas = ref<Persona[]>([]);
  const projects = ref<ContentProject[]>([]);
  const project = ref<ContentProject | null>(null);
  const drafts = ref<ContentDraftSummary[]>([]);
  const draft = ref<ContentDraft | null>(null);
  const form = reactive<ContentDraftPayload>(emptyPayload());
  const preview = ref<ContentDraftPreview | null>(null);
  const previewView = ref<"player" | "author">("player");
  const error = ref("");
  const notice = ref("");
  const busy = ref(false);
  const saving = ref(false);
  const dirty = ref(false);
  const conflict = ref(false);
  const loaded = ref(false);
  const assets = ref<ContentDraftAsset[]>([]);
  const operation = ref<ContentOperation | null>(null);
  const publishing = ref(false);
  let session = 0;
  let previewGeneration = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let inflight = false;

  const draftID = computed(() => draft.value?.draft_id ?? "");
  const statusLine = computed(() => {
    if (conflict.value) return "草稿已在别处更新，请重新载入或复制为新草稿。";
    if (saving.value) return "正在保存草稿…";
    if (dirty.value) return "有未保存的修改，停止输入后会保存。";
    if (draft.value) return `草稿版本 ${draft.value.version} 已保存`;
    return "尚未打开草稿";
  });

  function emptyPayload(): ContentDraftPayload {
    return {
      schema_version: 2, game_id: "", mode: "open", title: "", description: "", gameplay: "",
      background: "", rules: "", author_facts: "", player: { name: "", profile: "" },
      opening: "", initial_location: "", clock: "", locations: [], npcs: [], bystanders: [],
    };
  }

  function applyPayload(payload: ContentDraftPayload) {
    const merged = { ...emptyPayload(), ...payload };
    merged.player = { name: payload?.player?.name ?? "", profile: payload?.player?.profile ?? "" };
    merged.locations = Array.isArray(payload?.locations) ? payload.locations : [];
    merged.npcs = Array.isArray(payload?.npcs) ? payload.npcs : [];
    merged.bystanders = Array.isArray(payload?.bystanders) ? payload.bystanders : [];
    Object.assign(form, merged);
    dirty.value = false;
  }

  async function loadPersonas() {
    try {
      personas.value = await fetchPersonas();
    } catch (e) {
      error.value = describe(e, "暂时无法读取主角模板。");
    }
  }

  async function loadCatalog() {
    const ticket = ++session;
    busy.value = true;
    error.value = "";
    try {
      const [list] = await Promise.all([fetchContentProjects(), loadPersonas()]);
      if (ticket !== session) return;
      projects.value = list;
      loaded.value = true;
    } catch (e) {
      if (ticket === session) error.value = describe(e, "暂时无法读取剧本列表。");
    } finally {
      if (ticket === session) busy.value = false;
    }
  }

  async function openProject(projectID: string) {
    const ticket = ++session;
    busy.value = true;
    error.value = "";
    notice.value = "";
    try {
      const result = await fetchContentProject(projectID);
      if (ticket !== session) return;
      project.value = result.project;
      drafts.value = result.drafts ?? [];
      draft.value = null;
      conflict.value = false;
      applyPayload(emptyPayload());
    } catch (e) {
      if (ticket === session) error.value = describe(e, "暂时无法打开这个剧本。");
    } finally {
      if (ticket === session) busy.value = false;
    }
  }

  async function createProject(gameID: string, title: string) {
    const ticket = ++session;
    busy.value = true;
    error.value = "";
    try {
      const created = await createContentProject(gameID, title);
      if (ticket !== session) return;
      projects.value = [created, ...projects.value];
      await openProject(created.project_id);
    } catch (e) {
      if (ticket === session) error.value = describe(e, "创建剧本失败，请检查标识与标题。");
    } finally {
      if (ticket === session) busy.value = false;
    }
  }

  async function openDraft(draftIDToOpen: string) {
    const ticket = ++session;
    busy.value = true;
    error.value = "";
    try {
      const opened = await fetchContentDraft(draftIDToOpen);
      if (ticket !== session) return;
      draft.value = opened;
      conflict.value = false;
      applyPayload(opened.payload);
      preview.value = null;
      await loadAssets();
    } catch (e) {
      if (ticket === session) error.value = describe(e, "暂时无法打开这份草稿。");
    } finally {
      if (ticket === session) busy.value = false;
    }
  }

  async function startDraft(baseRevision = "") {
    if (!project.value) return;
    const ticket = ++session;
    busy.value = true;
    error.value = "";
    try {
      const created = await createContentDraft(project.value.project_id, baseRevision);
      if (ticket !== session) return;
      drafts.value = [{ ...created }, ...drafts.value];
      draft.value = created;
      conflict.value = false;
      applyPayload(created.payload);
      preview.value = null;
    } catch (e) {
      if (ticket === session) error.value = describe(e, "新建草稿失败。");
    } finally {
      if (ticket === session) busy.value = false;
    }
  }

  function touch() {
    dirty.value = true;
    conflict.value = false;
    schedule();
  }

  function schedule() {
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => void flush(), AUTOSAVE_DEBOUNCE_MS);
  }

  async function flush(): Promise<void> {
    if (timer) {
      clearTimeout(timer);
      timer = undefined;
    }
    const current = draft.value;
    if (!current || inflight) {
      if (inflight) schedule();
      return;
    }
    const ticket = session;
    inflight = true;
    saving.value = true;
    try {
      const saved = await saveContentDraft(current.draft_id, form, current.version);
      if (ticket !== session) return;
      draft.value = { ...current, ...saved, payload: form };
      drafts.value = drafts.value.map(d => (d.draft_id === saved.draft_id ? { ...d, ...saved } : d));
      dirty.value = false;
      conflict.value = false;
      error.value = "";
    } catch (e) {
      if (ticket !== session) return;
      if (e instanceof ApiError && (e.code === "version_conflict" || e.code === "draft_version_conflict")) {
        // The stored draft is untouched; keep the local edit and let the author choose.
        conflict.value = true;
      } else {
        error.value = describe(e, "草稿保存失败，修改仍保留在本地。");
      }
    } finally {
      inflight = false;
      if (ticket === session) saving.value = false;
      // A conflicting or failed save waits for the author's decision instead of
      // hammering the same stale version.
      if (dirty.value && !conflict.value && ticket === session) schedule();
    }
  }

  async function reloadDraft() {
    const current = draft.value;
    if (current) await openDraft(current.draft_id);
  }
  async function copyAsNewDraft() {
    if (!project.value) return;
    const ticket = ++session;
    busy.value = true;
    try {
      const created = await createContentDraft(project.value.project_id, "");
      if (ticket !== session) return;
      const saved = await saveContentDraft(created.draft_id, form, created.version);
      if (ticket !== session) return;
      drafts.value = [{ ...saved }, ...drafts.value];
      draft.value = saved;
      conflict.value = false;
      dirty.value = false;
      notice.value = "已复制为新草稿，原草稿未被覆盖。";
    } catch (e) {
      if (ticket === session) error.value = describe(e, "复制为新草稿失败。");
    } finally {
      if (ticket === session) busy.value = false;
    }
  }

  async function removeDraft(draftIDToRemove: string) {
    const ticket = ++session;
    busy.value = true;
    try {
      await deleteContentDraft(draftIDToRemove);
      if (ticket !== session) return;
      drafts.value = drafts.value.filter(d => d.draft_id !== draftIDToRemove);
      if (draft.value?.draft_id === draftIDToRemove) {
        draft.value = null;
        applyPayload(emptyPayload());
        preview.value = null;
      }
    } catch (e) {
      if (ticket === session) error.value = describe(e, "删除草稿失败。");
    } finally {
      if (ticket === session) busy.value = false;
    }
  }

  async function loadPreview(view: "player" | "author") {
    const current = draft.value;
    if (!current) return;
    // A preview does not switch the editing session: pending edits are saved first
    // and a late preview response is dropped by its own generation counter.
    const generation = ++previewGeneration;
    busy.value = true;
    error.value = "";
    try {
      await flush();
      const result = await fetchContentPreview(current.draft_id, view === "author");
      if (generation !== previewGeneration || draft.value?.draft_id !== current.draft_id) return;
      preview.value = result;
      previewView.value = view;
    } catch (e) {
      if (generation === previewGeneration) error.value = describe(e, "预览失败。");
    } finally {
      if (generation === previewGeneration) busy.value = false;
    }
  }

  function addLocation() {
    form.locations = [...form.locations, { id: "", name: "", description: "", connections: [] }];
    touch();
  }
  function removeLocation(index: number) {
    form.locations = form.locations.filter((_, i) => i !== index);
    touch();
  }
  function addNPC() {
    form.npcs = [...form.npcs, {
      definition_id: "", revision: "v1", entity_id: "", name: "", role: "", profile: "",
      knowledge: "", initial_concerns: "", initial_location: form.initial_location || "",
    }];
    touch();
  }
  function removeNPC(index: number) {
    form.npcs = form.npcs.filter((_, i) => i !== index);
    touch();
  }
  function addBystander() {
    form.bystanders = [...form.bystanders, { bystander_id: "", name: "" }];
    touch();
  }
  function removeBystander(index: number) {
    form.bystanders = form.bystanders.filter((_, i) => i !== index);
    touch();
  }

  function reset() {
    session++;
    if (timer) clearTimeout(timer);
    timer = undefined;
    project.value = null;
    drafts.value = [];
    draft.value = null;
    preview.value = null;
    assets.value = [];
    publishing.value = false;
    operation.value = null;
    conflict.value = false;
    dirty.value = false;
    notice.value = "";
    applyPayload(emptyPayload());
  }

  async function loadAssets() {
    const current = draft.value;
    if (!current) return;
    try {
      assets.value = await fetchContentDraftAssets(current.draft_id);
    } catch (e) {
      error.value = describe(e, "暂时无法读取草稿资源。");
    }
  }

  // Uploading needs a stable version: save first so the asset belongs to the version
  // that will be published.
  async function uploadAsset(name: string, file: File) {
    const current = draft.value;
    if (!current) return;
    busy.value = true;
    error.value = "";
    try {
      await flush();
      const asset = await uploadContentDraftAsset(current.draft_id, name, file);
      assets.value = [...assets.value.filter(a => a.relative_name !== asset.relative_name), asset];
      notice.value = `已上传 ${asset.relative_name}`;
    } catch (e) {
      error.value = describe(e, "资源上传失败。");
    } finally {
      busy.value = false;
    }
  }

  async function removeAsset(assetID: string) {
    const current = draft.value;
    if (!current) return;
    busy.value = true;
    try {
      await deleteContentDraftAsset(current.draft_id, assetID);
      assets.value = assets.value.filter(a => a.asset_id !== assetID);
    } catch (e) {
      error.value = describe(e, "删除资源失败。");
    } finally {
      busy.value = false;
    }
  }

  // Publishing always saves first, then acts on the version the server confirmed, so
  // an accepted publication cannot contain a newer unsaved edit.
  async function publish() {
    const current = draft.value;
    if (!current || !project.value) return;
    busy.value = true;
    error.value = "";
    notice.value = "";
    try {
      await flush();
      if (conflict.value) {
        error.value = "草稿已过期，请先处理版本冲突再发布。";
        return;
      }
      const fresh = await fetchContentDraft(current.draft_id);
      if (fresh.draft_id !== draft.value?.draft_id) return;
      draft.value = fresh;
      publishing.value = true;
      const result = await publishContentDraft(fresh.draft_id, crypto.randomUUID(), fresh.version, project.value.version);
      operation.value = result;
      if (result.status === "succeeded") {
        const revision = result.stage === "ready" ? "已发布不可变修订，新开局将使用它。" : "";
        await openProject(fresh.project_id);
        await openDraft(fresh.draft_id);
        notice.value = revision;
      } else {
        error.value = result.safe_error || "发布未完成。";
      }
    } catch (e) {
      if (e instanceof ApiError && e.code === "version_conflict") {
        conflict.value = true;
        error.value = "草稿或剧本版本已变化，请重新载入后再发布。";
      } else {
        error.value = describe(e, "发布失败。");
      }
    } finally {
      publishing.value = false;
      busy.value = false;
    }
  }

  return {
    personas, projects, project, drafts, draft, form, preview, previewView,
    error, notice, busy, saving, dirty, conflict, loaded, draftID, statusLine,
    assets, operation, publishing,
    loadCatalog, openProject, createProject, openDraft, startDraft, touch, flush,
    reloadDraft, copyAsNewDraft, removeDraft, loadPreview, loadAssets, uploadAsset, removeAsset, publish,
    addLocation, removeLocation, addNPC, removeNPC, addBystander, removeBystander, reset,
  };
}

function describe(e: unknown, fallback: string): string {
  if (e instanceof ApiError) {
    switch (e.code) {
      case "content_invalid": return "内容校验未通过：" + e.message;
      case "content_not_found": return "找不到这份内容，可能已被删除。";
      case "content_busy": return "这个标识已被占用，请换一个。";
      case "version_conflict": return "版本已变化，请重新载入。";
      default: return e.message || fallback;
    }
  }
  return fallback;
}
