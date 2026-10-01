import { computed, reactive, ref } from "vue";
import {
  confirmContentImport,
  contentExportURL,
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
  importContentPreview,
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
  type ImportPreview,
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
  // editGeneration counts local edits; confirmedGeneration counts the ones the server
  // has acknowledged. Publishing requires them to be equal.
  let editGeneration = 0;
  let confirmedGeneration = 0;
  let pending: Promise<void> = Promise.resolve();
  // The publication identity of an attempt whose result we could not observe. It is only
  // reused for a retry of that same attempt.
  let pendingPublish: { draftID: string; key: string; state: "unknown" } | null = null;

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
      background: "", rules: "", author_facts: "",
      // New content starts with a lead the player may adjust; the editor can lock it.
      player: { name: "", profile: "", editable: true },
      opening: "", initial_location: "", clock: "", locations: [], npcs: [], bystanders: [],
    };
  }

  function applyPayload(payload: ContentDraftPayload) {
    const fallback = emptyPayload();
    // The form is replaced wholesale, not merged: a field the new draft does not carry
    // must not keep the previous draft's value.
    const merged = { ...fallback, ...stripUndefined(payload) };
    merged.player = {
      name: payload?.player?.name ?? fallback.player.name,
      profile: payload?.player?.profile ?? fallback.player.profile,
      requirements: payload?.player?.requirements,
      editable: payload?.player?.editable ?? fallback.player.editable,
      initial_state: payload?.player?.initial_state,
    };
    for (const key of Object.keys(form) as (keyof typeof form)[]) {
      if (!(key in merged)) {
        delete (form as Record<string, unknown>)[key];
      }
    }
    Object.assign(form, merged);
    form.npcs = (merged.npcs ?? []).map(npc => ({ ...npc, speaking_examples: npc.speaking_examples ?? [] }));
    form.locations = (merged.locations ?? []).map(location => ({ ...location, connections: location.connections ?? [] }));
    form.bystanders = (merged.bystanders ?? []).map(bystander => ({ ...bystander }));
    dirty.value = false;
    // A new draft starts with a clean confirmation history.
    editGeneration = 0;
    confirmedGeneration = 0;
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
    // Every edit gets a generation, so a caller can wait until the server has
    // confirmed exactly the content it is about to act on.
    editGeneration += 1;
    schedule();
  }

  function schedule() {
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => void flush(), AUTOSAVE_DEBOUNCE_MS);
  }

  // savePending returns after the draft stored on the server matches the current
  // form: it waits for an in-flight save, then saves again if edits arrived in the
  // meantime. An unconfirmed save is reported, never silently treated as success.
  async function savePending(): Promise<"saved" | "unsaved" | "conflict"> {
    if (timer) {
      clearTimeout(timer);
      timer = undefined;
    }
    if (!draft.value) return "unsaved";
    await pending;
    while (confirmedGeneration < editGeneration && !conflict.value) {
      if (inflight) {
        await pending;
        continue;
      }
      await flush();
      if (saving.value) await pending;
      if (conflict.value) return "conflict";
      if (!dirty.value && confirmedGeneration >= editGeneration) break;
      if (!inflight && confirmedGeneration < editGeneration) {
        // The save failed and did not reschedule; do not pretend it succeeded.
        if (error.value) return "unsaved";
        break;
      }
    }
    if (conflict.value) return "conflict";
    if (confirmedGeneration < editGeneration) return "unsaved";
    return "saved";
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
    const generation = editGeneration;
    inflight = true;
    saving.value = true;
    pending = (async () => {
      try {
        // Remember what the save carried, so the identifiers the server derives can be
        // written back without replacing anything the author changed meanwhile.
        const sent = JSON.parse(JSON.stringify(form)) as ContentDraftPayload;
        const saved = await saveContentDraft(current.draft_id, form, current.version);
        if (ticket !== session) return;
        // The server owns internal identifiers, so its normalised payload is written back
        // into the form; a field the author touched during the save keeps their value.
        const merged = mergeNormalizedPayload(sent, saved.payload);
        for (let index = 0; index < form.locations.length && index < merged.locations.length; index++) {
          if (!form.locations[index].id) form.locations[index].id = merged.locations[index].id;
        }
        for (let index = 0; index < form.npcs.length && index < merged.npcs.length; index++) {
          if (!form.npcs[index].definition_id) form.npcs[index].definition_id = merged.npcs[index].definition_id;
          if (!form.npcs[index].revision) form.npcs[index].revision = merged.npcs[index].revision;
          if (!form.npcs[index].entity_id) form.npcs[index].entity_id = merged.npcs[index].entity_id;
        }
        for (let index = 0; index < form.bystanders.length && index < merged.bystanders.length; index++) {
          if (!form.bystanders[index].bystander_id) form.bystanders[index].bystander_id = merged.bystanders[index].bystander_id;
        }
        if (!form.initial_location && merged.initial_location) form.initial_location = merged.initial_location;
        draft.value = { ...current, ...saved, payload: form };
        drafts.value = drafts.value.map(d => (d.draft_id === saved.draft_id ? { ...d, ...saved } : d));
        // Only the edit generation this save carried counts as confirmed; anything the
        // author typed while it was in flight is still pending.
        confirmedGeneration = Math.max(confirmedGeneration, generation);
        if (confirmedGeneration >= editGeneration) dirty.value = false;
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
    })();
    return pending;
  }

  // Importing is a two-step path in the interface too: the file produces a report the
  // author reads, and only confirming turns the preview into an editable draft.
  const importPreview = ref<ImportPreview | null>(null);
  async function importContent(file: File) {
    const current = project.value;
    if (!current) {
      error.value = "请先打开或新建一个内容项目，再导入。";
      return;
    }
    busy.value = true;
    error.value = "";
    notice.value = "";
    try {
      const preview = await importContentPreview(current.project_id, file);
      if (project.value?.project_id !== current.project_id) return;
      importPreview.value = preview;
      notice.value = "已生成导入预览，确认后才会成为草稿。";
      await loadCatalog();
    } catch (e) {
      error.value = describe(e, "导入失败。");
    } finally {
      busy.value = false;
    }
  }
  async function confirmImport() {
    const preview = importPreview.value;
    if (!preview) return;
    busy.value = true;
    error.value = "";
    try {
      await confirmContentImport(preview.draft_id, `import:${preview.draft_id}`, preview.version);
      importPreview.value = null;
      notice.value = "导入内容已变成可编辑草稿。";
      await openProject(preview.project_id);
      await openDraft(preview.draft_id);
    } catch (e) {
      error.value = describe(e, "确认导入失败。");
    } finally {
      busy.value = false;
    }
  }
  function dismissImport() {
    importPreview.value = null;
    notice.value = "";
  }

  // Export hands the browser the archive the server rebuilt from the published revision.
  function exportRevision() {
    const current = project.value;
    if (!current?.current_revision) return;
    const link = document.createElement("a");
    link.href = contentExportURL(current.game_id, current.current_revision);
    link.download = `${current.game_id}-${current.current_revision}.wia-story.zip`;
    document.body.appendChild(link);
    link.click();
    link.remove();
    notice.value = "已开始下载导出的故事包。";
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

  // Publishing only acts on content the server has confirmed, so an accepted
  // publication can never contain a newer unsaved edit or an older stored version.
  async function publish() {
    const current = draft.value;
    if (!current || !project.value) return;
    busy.value = true;
    error.value = "";
    notice.value = "";
    try {
      const state = await savePending();
      if (state === "conflict") {
        error.value = "草稿已过期，请先处理版本冲突再发布。";
        return;
      }
      if (state === "unsaved") {
        error.value = "还有修改没有保存成功，请先解决保存问题再发布。";
        return;
      }
      const fresh = await fetchContentDraft(current.draft_id);
      if (fresh.draft_id !== draft.value?.draft_id) return;
      draft.value = fresh;
      publishing.value = true;
      // Each publication intent gets its own request identity. Only an outcome we could
      // not observe is retried under the same key and the same payload, so a second,
      // deliberate publish is never mistaken for a repeat of the first.
      const requestKey = pendingPublish?.draftID === fresh.draft_id && pendingPublish.state === "unknown"
        ? pendingPublish.key
        : `${fresh.draft_id}:publish:${crypto.randomUUID()}`;
      pendingPublish = { draftID: fresh.draft_id, key: requestKey, state: "unknown" };
      const result = await publishContentDraft(fresh.draft_id, requestKey, fresh.version, project.value.version);
      pendingPublish = null;
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
        pendingPublish = null;
        conflict.value = true;
        error.value = "草稿或剧本版本已变化，请重新载入后再发布。";
      } else {
        // The outcome is unknown, so the same key and payload stay available for a retry.
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
    importPreview, importContent, confirmImport, dismissImport, exportRevision,
  };
}

// mergeNormalizedPayload takes the identifiers the server derived and writes them into
// the entries the save carried, matched by position. An entry the author added or edited
// during the save is left alone.
function mergeNormalizedPayload(sent: ContentDraftPayload, saved: ContentDraftPayload): Pick<ContentDraftPayload, "locations" | "npcs" | "bystanders" | "initial_location"> {
  return {
    locations: mergeByIndex(sent.locations ?? [], saved.locations ?? [], (item, normalized) => {
      item.id = normalized.id;
      return item;
    }),
    npcs: mergeByIndex(sent.npcs ?? [], saved.npcs ?? [], (item, normalized) => {
      item.definition_id = normalized.definition_id;
      item.revision = normalized.revision;
      item.entity_id = normalized.entity_id;
      return item;
    }),
    bystanders: mergeByIndex(sent.bystanders ?? [], saved.bystanders ?? [], (item, normalized) => {
      item.bystander_id = normalized.bystander_id;
      return item;
    }),
    initial_location: saved.initial_location,
  };
}

function mergeByIndex<T, N>(sent: T[], saved: N[], apply: (item: T, normalized: N) => T): T[] {
  return sent.map((item, index) => (index < saved.length ? apply(item, saved[index]) : item));
}

function stripUndefined<T extends object>(value: T | undefined | null): Partial<T> {  if (!value) return {};
  const out: Record<string, unknown> = {};
  for (const [key, item] of Object.entries(value)) {
    if (item !== undefined) out[key] = item;
  }
  return out as Partial<T>;
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
