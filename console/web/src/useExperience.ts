import {
  computed,
  nextTick,
  onMounted,
  onUnmounted,
  reactive,
  ref,
  watch,
} from "vue";
import {
  activateWorld,
  cancelRun,
  createWorld,
  deleteWorld,
  exchangeBootstrapToken,
  fetchCopyOperation,
  fetchGames,
  fetchModel,
  fetchRuns,
  fetchStatus,
  fetchWorld,
  fetchWorlds,
  retryRun,
  saveAgentSettings,
  saveAs,
  saveModel,
  submitRun,
} from "./api";
import {
  ApiError,
  type Character,
  type BehaviorPolicyCatalog,
  type GameSummary,
  type NarrativeSettings,
  type Run,
  type Status,
  type WorldSummary,
} from "./types";
import { useStoryReader } from "./useStoryReader";

type Dialog =
  | ""
  | "model"
  | "settings"
  | "saves"
  | "new"
  | "copy"
  | "delete"
  | "scene";
export function useExperience() {
  const status = ref<Status | null>(null),
    games = ref<GameSummary[]>([]),
    worlds = ref<WorldSummary[]>([]);
  const currentWorld = ref<WorldSummary | null>(null),
    characters = ref<Character[]>([]);
  const view = ref<"home" | "story" | "play">("home"),
    gameID = ref(""),
    loaded = ref(false);
  const connectionError = ref(""),
    notice = ref(""),
    dialog = ref<Dialog>(""),
    dialogError = ref("");
  const dialogBusy = ref(false),
    navigating = ref(false),
    moreOpen = ref(false),
    modelReturn = ref<Dialog>("");
  const deleteCandidate = ref<WorldSummary | null>(null),
    formWorldID = ref("");
  const newWorld = reactive({
    name: "",
    mode: "guided",
    player_name: "旅人",
    player_profile: "一个正在寻找答案的旅人。",
  });
  type PendingCopy = { key: string; source: string; sourceName: string; name: string; revision: number; operationID?: string };
  const copies = reactive<Record<string, PendingCopy | undefined>>({});
  const pendingCopy = computed(() => copies[formWorldID.value]);
  let dialogSession = 0;
  const modelForm = reactive({
    provider: "deepseek",
    model: "",
    base_url: "",
    api_key: "",
  });
  const providers = ref<{ provider: string; model: string }[]>([]),
    modelAdvanced = ref(false);
  const defaults = (): NarrativeSettings => ({
    perspective: "second_person",
    length: "standard",
    detail: "balanced",
    player_elaboration: "natural",
    npc_initiative: "contextual",
    behavior_policies: { coordination: "", narration: "", npc: "" },
  });
  const policyDefaults = ref<BehaviorPolicyCatalog>();
  const settings = ref(defaults()),
    settingsForm = reactive(defaults()),
    settingsWorldName = ref(""),
    detailsOpen = ref(false);
  let settingsEdit:
    | { worldID: string; name: string; epoch: number; valid: boolean }
    | undefined;
  const textarea = ref<HTMLTextAreaElement>(),
    runs = reactive<Record<string, Run | undefined>>({});
  const seenFailures = new Map<string, string>();
  type PendingSubmission = { key: string; input: string; retryID?: string; payload?: Parameters<typeof submitRun>[1] };
  const submissions = reactive<Record<string, PendingSubmission | undefined>>({});
  const pendingSubmission = computed(() => submissions[currentWorld.value?.world_id ?? ""]);
  const now = ref(Date.now());
  const waitingSeconds = computed(() => activeRun.value ? Math.max(0, Math.floor((now.value - Date.parse(activeRun.value.created_at)) / 1000)) : 0);
  const runRevisions = new Map<string, number>();
  const reader = useStoryReader(missingWorld),
    { session, viewport } = reader;
  const game = computed(
    () =>
      games.value.find((item) => item.id === gameID.value) ?? games.value[0],
  );
  const storyWorlds = computed(() =>
    worlds.value.filter((item) => item.game_id === game.value?.id),
  );
  const recentWorld = computed(
    () =>
      storyWorlds.value.find(
        (item) => item.world_id === status.value?.active_world?.world_id,
      ) ?? storyWorlds.value[0],
  );
  const run = computed(() => runs[currentWorld.value?.world_id ?? ""]);
  const activeRun = computed(() =>
    run.value && ["accepted", "running"].includes(run.value.status)
      ? run.value
      : undefined,
  );
  const failedRun = computed(() =>
    run.value &&
    ["failed", "cancelled", "interrupted"].includes(run.value.status)
      ? run.value
      : undefined,
  );
  const saved = computed(
    () =>
      !pendingSubmission.value &&
      run.value?.status === "completed" &&
      !!run.value.message_seq &&
      (currentWorld.value?.message_head ?? 0) >= run.value.message_seq &&
      session.value.messages.some(
        (message) => message.seq === run.value?.message_seq,
      ),
  );
  const addresseeName = computed(
    () =>
      characters.value.find(
        (item) => item.entity_id === session.value.addressee,
      )?.name,
  );
  const canSubmit = computed(
    () =>
      !!session.value.draft.trim() &&
      !!currentWorld.value &&
      !currentWorld.value.story_ended &&
      !session.value.sending &&
      !activeRun.value &&
      !pendingSubmission.value &&
      !navigating.value,
  );
  const presets = [
    {
      label: "克制扮演",
      note: "忠实转述，人物以回应为主",
      player_elaboration: "restrained",
      npc_initiative: "responsive",
    },
    {
      label: "自然共创",
      note: "适度补全表达，人物按情境主动",
      player_elaboration: "natural",
      npc_initiative: "contextual",
    },
    {
      label: "小说共创",
      note: "更充分演绎，人物积极互动",
      player_elaboration: "expressive",
      npc_initiative: "proactive",
    },
  ] as const;
  const lengths = [
    { value: "concise", label: "简短", note: "直接呈现关键回应和变化" },
    { value: "standard", label: "标准", note: "按本轮新增内容适度展开" },
    { value: "detailed", label: "细致", note: "充分描写有信息量的内容" },
  ] as const;
  const players = [
    { value: "restrained", label: "克制", note: "间接转述，只补必要衔接" },
    { value: "natural", label: "自然", note: "补充等价的简短台词和动作" },
    { value: "expressive", label: "充分", note: "在已选方向内完整表现主角" },
  ] as const;
  const initiatives = [
    { value: "responsive", label: "回应为主", note: "少插话，仍可处理必要事务" },
    {
      value: "contextual",
      label: "按情境主动",
      note: "结合职责、关切与机会行动",
    },
    { value: "proactive", label: "积极互动", note: "主动提问、试探和相关行动" },
  ] as const;
  const presetName = computed(
    () =>
      presets.find(
        (item) =>
          item.player_elaboration === settingsForm.player_elaboration &&
          item.npc_initiative === settingsForm.npc_initiative,
      )?.label ?? "自定义",
  );
  const dialogTitle = computed(
    () =>
      ({
        model: "连接模型",
        settings: "故事设置",
        saves: "存档与故事",
        new: "确认你的主角",
        copy: "另存当前进度",
        delete: "删除存档",
        scene: "场景与人物",
        "": "",
      })[dialog.value],
  );
  let generation = 0,
    timer: number | undefined,
    refreshing: Promise<void> | undefined,
    stopped = false;

  function describe(error: unknown): string {
    if (error instanceof ApiError)
      return (
        (
          {
            model_not_configured: "请先连接模型，再继续故事。",
            world_busy: "这个存档正在处理上一项操作，请稍候。",
            story_ended: "这段流程故事已结束，可以阅读、另存，或从剧本页开始新的故事。",
            version_conflict: "故事状态已更新，请重新打开此操作后重试。",
            storage_unavailable: "存档暂时无法读写，请稍后重试。",
            save_failed: "另存没有完成，原存档没有受到影响。",
          } as Record<string, string>
        )[error.code] ?? error.message
      );
    return error instanceof Error ? error.message : String(error);
  }
  function failureText(item: Run): string {
    if (item.status === "cancelled") return "本轮已取消，输入仍保留。";
    if (item.status === "interrupted")
      return "本轮被中断，输入仍保留，可以重试。";
    return (
      (
        {
          model_connection_failed: "模型连接失败，本轮未保存，输入已保留，可以重试。",
          model_service_failed: "模型服务未能处理请求，本轮未保存，输入已保留；请检查连接或稍后重试。",
          model_empty_response: "模型返回了空内容，本轮未保存，输入已保留，可以重试。",
          model_output_incomplete: "模型输出未完整结束，本轮未保存，输入已保留；可以重试，持续发生时检查模型输出限制。",
          model_invalid_response: "模型响应格式不可用，本轮未保存，输入已保留，可以重试。",
          narration_generation_failed:
            "故事正文没有成功生成，输入仍保留，可以重试。",
          npc_generation_failed: "有角色未完成回应，输入仍保留，可以重试。",
          coordination_generation_failed:
            "场景结果未能确定，输入仍保留，可以重试。",
          intent_generation_failed: "未能理解这次输入，输入仍保留，可以重试。",
          generation_timeout: "模型响应超时，输入仍保留，可以重试。",
          context_capacity_exceeded: "本轮必需内容超出模型可用容量。输入已保留，请缩短本轮输入或在模型设置中选择容量足够的模型。",
          context_source_missing: "故事所需的来源记录不完整，本轮未保存。输入已保留，请检查存档或恢复完整备份。",
        } as Record<string, string>
      )[item.reason ?? ""] ??
      item.error ??
      "这一轮没有完成，输入仍保留，可以重试。"
    );
  }
  function formatDate(value: string) {
    const d = new Date(value);
    return Number.isNaN(d.getTime())
      ? value
      : d.toLocaleString([], {
          month: "short",
          day: "numeric",
          hour: "2-digit",
          minute: "2-digit",
        });
  }
  function dateName() {
    const d = new Date(),
      pad = (n: number) => String(n).padStart(2, "0");
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
  }
  function showDialog(value: Dialog) {
    ++dialogSession;
    if (value !== "settings") settingsEdit = undefined;
    moreOpen.value = false;
    dialogError.value = "";
    dialog.value = value;
  }
  function closeDialog() {
    if (dialogBusy.value) return;
    const back = dialog.value === "model" ? modelReturn.value : "";
    modelReturn.value = "";
    showDialog(back);
  }
  function openModel(returnTo: Dialog = "") {
    modelReturn.value = returnTo;
    modelForm.api_key = "";
    if (status.value?.model.configured && status.value.model.provider) {
      modelForm.provider = status.value.model.provider;
      modelForm.model = status.value.model.model ?? "";
    }
    modelAdvanced.value = false;
    showDialog("model");
  }
  function changeProvider() {
    modelForm.model =
      providers.value.find((item) => item.provider === modelForm.provider)
        ?.model ?? "";
    modelForm.base_url = "";
  }
  function missingWorld(id: string) {
    reader.forget(id);
    delete runs[id];
    delete submissions[id];
    if (currentWorld.value?.world_id !== id) return;
    invalidateSettings();
    generation++;
    currentWorld.value = null;
    characters.value = [];
    view.value = "story";
    void reader.select("");
    notice.value = "该存档已被删除或不可访问，请选择其他进度。";
  }
  async function loadCurrent(id: string, epoch: number) {
    if (!id) {
      if (epoch === generation && currentWorld.value)
        missingWorld(currentWorld.value.world_id);
      return;
    }
    try {
      const snapshot = await fetchWorld(id);
      if (epoch !== generation || status.value?.active_world?.world_id !== id)
        return;
      if (currentWorld.value?.world_id !== id) {
        reader.remember();
        invalidateSettings();
      }
      if (
        currentWorld.value?.world_id === id &&
        snapshot.world.context_epoch < currentWorld.value.context_epoch
      )
        return;
      currentWorld.value = snapshot.world;
      if (view.value === "play" || !gameID.value)
        gameID.value = snapshot.world.game_id;
      characters.value = snapshot.characters.filter((item) => item.in_scene);
      settings.value = snapshot.narrative_settings;
      policyDefaults.value = snapshot.behavior_policy_defaults;
      await reader.select(id);
      if (epoch !== generation || currentWorld.value?.world_id !== id) return;
      if (
        !characters.value.some(
          (item) => item.entity_id === session.value.addressee,
        )
      )
        session.value.addressee = "";
    } catch (error) {
      if (epoch !== generation) return;
      if (error instanceof ApiError && error.status === 404) missingWorld(id);
      else throw error;
    }
  }
  async function refreshNow() {
    const epoch = generation;
    try {
      const [next, list] = await Promise.all([fetchStatus(), fetchWorlds()]);
      if (epoch !== generation || stopped) return;
      status.value = next;
      if (settingsEdit && next.active_world?.world_id !== settingsEdit.worldID)
        invalidateSettings();
      worlds.value = list;
      connectionError.value = "";
      if (!games.value.length) {
        const [catalog, options] = await Promise.all([
          fetchGames(),
          fetchModel(),
        ]);
        if (epoch !== generation) return;
        games.value = catalog;
        providers.value = options.providers;
        if (!gameID.value) gameID.value = catalog[0]?.id ?? "";
        if (!modelForm.model) changeProvider();
      }
      const id = next.active_world?.world_id ?? "",
        previous = currentWorld.value;
      if (
        !id ||
        previous?.world_id !== id ||
        previous.context_epoch !== next.active_world?.context_epoch ||
        previous.message_head !== next.active_world?.message_head
      )
        await loadCurrent(id, epoch);
      if (epoch !== generation || !id || currentWorld.value?.world_id !== id)
        return;
      const runRevision = runRevisions.get(id) ?? 0;
      const pending = submissions[id];
      if (pending && !session.value.sending) {
        const found = (await fetchRuns(id, pending.key))[0];
        if (epoch !== generation || currentWorld.value?.world_id !== id || runRevision !== (runRevisions.get(id) ?? 0)) return;
        if (found) settleSubmission(id, pending, found);
      }
      const latest = (await fetchRuns(id))[0];
      if (epoch !== generation || currentWorld.value?.world_id !== id) return;
      if (session.value.sending || runRevision !== (runRevisions.get(id) ?? 0))
        return;
      reader.remember();
      runs[id] = latest;
      await nextTick();
      if (epoch !== generation || currentWorld.value?.world_id !== id) return;
      reader.restore();
      if (
        latest &&
        ["failed", "cancelled", "interrupted"].includes(latest.status) &&
        seenFailures.get(id) !== `${latest.run_id}:${latest.attempt}`
      ) {
        if (!session.value.draft.trim()) session.value.draft = latest.input;
        seenFailures.set(id, `${latest.run_id}:${latest.attempt}`);
      }
      if (
        !session.value.initialized ||
        session.value.error ||
        currentWorld.value.message_head >
          (session.value.messages.at(-1)?.seq ?? 0)
      )
        await reader.sync(id);
    } catch (error) {
      if (epoch === generation && !stopped)
        connectionError.value = describe(error);
    } finally {
      loaded.value = true;
    }
  }
  function refresh() {
    if (refreshing) return refreshing;
    refreshing = refreshNow().finally(() => {
      refreshing = undefined;
    });
    return refreshing;
  }
  async function freshRefresh() {
    await refreshing;
    await refresh();
  }
  function returnHome() {
    reader.remember();
    view.value = "home";
    moreOpen.value = false;
    if (!dialogBusy.value) showDialog("");
  }
  function openStory(item: GameSummary) {
    reader.remember();
    gameID.value = item.id;
    view.value = "story";
  }
  async function switchWorld(world: WorldSummary) {
    if (navigating.value) return;
    reader.remember();
    navigating.value = true;
    const epoch = ++generation,
      fromDialog = dialog.value === "saves";
    dialogError.value = "";
    if (fromDialog) dialogBusy.value = true;
    try {
      if (status.value?.active_world?.world_id !== world.world_id)
        await activateWorld(world.world_id, status.value?.active_revision ?? 0);
      if (epoch !== generation) return;
      await freshRefresh();
      if (currentWorld.value?.world_id !== world.world_id)
        throw new Error("无法读取所选存档，请重试。");
      gameID.value = world.game_id;
      view.value = "play";
      showDialog("");
      await nextTick();
      reader.restore();
    } catch (error) {
      if (epoch === generation) {
        if (fromDialog) dialogError.value = describe(error);
        else notice.value = describe(error);
      }
    } finally {
      navigating.value = false;
      if (fromDialog) dialogBusy.value = false;
    }
  }
  function openNewWorld() {
    newWorld.name = `${game.value?.title ?? "新的故事"} · ${dateName()}`;
    newWorld.mode = game.value?.default_mode ?? "guided";
    newWorld.player_name = "旅人";
    newWorld.player_profile = "一个正在寻找答案的旅人。";
    showDialog("new");
  }
  function openCopy() {
    if (!currentWorld.value) return;
    formWorldID.value = currentWorld.value.world_id;
    newWorld.name = pendingCopy.value?.name ?? `${currentWorld.value.name} · 分支 ${dateName()}`;
    showDialog("copy");
  }
  function openSettings() {
    if (!currentWorld.value || dialogBusy.value) return;
    settingsEdit = {
      worldID: currentWorld.value.world_id,
      name: currentWorld.value.name,
      epoch: currentWorld.value.context_epoch,
      valid: true,
    };
    settingsWorldName.value = currentWorld.value.name;
    Object.assign(settingsForm, settings.value, {
      behavior_policies: { ...settings.value.behavior_policies },
    });
    detailsOpen.value = false;
    showDialog("settings");
  }
  function invalidateSettings() {
    if (!settingsEdit) return;
    settingsEdit.valid = false;
    if (!dialogBusy.value) {
      showDialog("");
      notice.value = "活动存档已切换，请重新打开故事设置。";
    }
  }
  async function configureModel() {
    if (dialogBusy.value) return;
    if (!modelForm.api_key.trim()) {
      dialogError.value = "请输入模型 API Key。";
      return;
    }
    dialogBusy.value = true;
    dialogError.value = "";
    try {
      status.value = await saveModel({ ...modelForm });
      modelForm.api_key = "";
      await freshRefresh();
      const back = modelReturn.value;
      modelReturn.value = "";
      showDialog(back);
      notice.value = "模型已连接，可以继续刚才的操作。";
    } catch (error) {
      dialogError.value = describe(error);
    } finally {
      dialogBusy.value = false;
    }
  }
  async function configureSettings() {
    const editing = settingsEdit;
    if (
      dialogBusy.value ||
      activeRun.value ||
      !editing ||
      dialog.value !== "settings"
    )
      return;
    const ownsDialog = () =>
      !stopped && settingsEdit === editing && dialog.value === "settings";
    dialogBusy.value = true;
    dialogError.value = "";
    try {
      if (!editing.valid || currentWorld.value?.world_id !== editing.worldID)
        throw new Error("活动存档已切换，请重新打开故事设置。");
      const result = await saveAgentSettings(
        editing.worldID,
        { ...settingsForm, behavior_policies: { ...settingsForm.behavior_policies } },
        editing.epoch,
      );
      if (!ownsDialog()) return;
      const superseded =
        currentWorld.value?.world_id === editing.worldID &&
        result.world.context_epoch < currentWorld.value.context_epoch;
      if (
        editing.valid &&
        currentWorld.value?.world_id === editing.worldID &&
        result.world.context_epoch >= currentWorld.value.context_epoch
      ) {
        settings.value = result.settings;
        currentWorld.value = result.world;
      }
      dialogBusy.value = false;
      showDialog("");
      notice.value = superseded
        ? `“${editing.name}”的设置提交已完成，当前设置已有更新。`
        : `“${editing.name}”的故事设置已保存，从下一轮生效。`;
      await freshRefresh();
    } catch (error) {
      if (!ownsDialog()) return;
      if (editing.valid) dialogError.value = describe(error);
      else
        notice.value = `“${editing.name}”的故事设置未保存：${describe(error)}`;
    } finally {
      if (ownsDialog()) {
        dialogBusy.value = false;
        if (!editing.valid) showDialog("");
      }
    }
  }
  async function createOrCopy() {
    if (dialogBusy.value) return;
    const copying = dialog.value === "copy";
    if (copying) { await resumeCopy(); return; }
    if (!copying && !status.value?.ready) {
      if (!status.value || connectionError.value) {
        dialogError.value = "暂时无法确认连接状态，请重试连接后继续。";
        return;
      }
      openModel("new");
      return;
    }
    dialogBusy.value = true;
    dialogError.value = "";
    try {
      {
        ++generation;
        const world = await createWorld({ ...newWorld });
        await freshRefresh();
        if (currentWorld.value?.world_id !== world.world_id)
          throw new Error("存档已创建，暂时无法读取，请从存档列表继续。");
        gameID.value = world.game_id;
        view.value = "play";
        showDialog("");
        await nextTick();
        await reader.latest();
      }
      await freshRefresh();
    } catch (error) {
      dialogError.value = describe(error);
    } finally {
      dialogBusy.value = false;
    }
  }
  async function resumeCopy() {
    const source = formWorldID.value;
    if (!source || dialogBusy.value) return;
    const editing = dialogSession;
    const ownsDialog = () => !stopped && dialogSession === editing && dialog.value === "copy";
    const operation = copies[source] ?? (copies[source] = {
      key: crypto.randomUUID(), source,
      sourceName: currentWorld.value?.name ?? source,
      name: newWorld.name.trim(), revision: status.value?.active_revision ?? 0,
    });
    dialogBusy.value = true;
    dialogError.value = "";
    try {
      let result = operation.operationID
        ? await fetchCopyOperation(operation.operationID)
        : await saveAs(operation.source, operation.name, operation.revision, operation.key);
      operation.operationID = result.operation_id;
      const deadline = Date.now() + 300000;
      while (!["ready", "failed"].includes(result.status) && Date.now() < deadline && !stopped) {
        await new Promise(resolve => window.setTimeout(resolve, 500));
        result = await fetchCopyOperation(operation.operationID!);
      }
      if (!["ready", "failed"].includes(result.status))
        throw new Error("另存结果尚未确认，请点击继续确认原操作。");
      delete copies[source];
      if (result.status === "failed") throw new Error(result.error || "另存失败，原存档未受影响。");
      if (!ownsDialog()) return;
      dialogBusy.value = false;
      const stillHere = currentWorld.value?.world_id === source;
      showDialog(stillHere ? "saves" : "");
      notice.value = `“${operation.sourceName}”已另存为“${result.target_name}”` + (stillHere ? "，当前仍在原存档。" : "，当前存档未改变。");
      await freshRefresh();
    } catch (error) {
      // A definitive rejection before task creation permits a new logical request.
      if (!operation.operationID && error instanceof ApiError &&
        ["version_conflict", "world_busy", "world_not_found", "invalid_request"].includes(error.code)) delete copies[source];
      if (ownsDialog()) dialogError.value = describe(error) + (copies[source] ? " 原另存操作已保留，继续确认不会创建第二份副本。" : "");
    } finally {
      if (ownsDialog()) dialogBusy.value = false;
    }
  }
  function confirmDelete(world: WorldSummary) {
    deleteCandidate.value = world;
    showDialog("delete");
  }
  async function removeWorld() {
    const world = deleteCandidate.value;
    if (!world || dialogBusy.value) return;
    dialogBusy.value = true;
    dialogError.value = "";
    try {
      await deleteWorld(world.world_id, status.value?.active_revision ?? 0);
      if (currentWorld.value?.world_id === world.world_id)
        missingWorld(world.world_id);
      else reader.forget(world.world_id);
      worlds.value = worlds.value.filter(
        (item) => item.world_id !== world.world_id,
      );
      showDialog("");
      notice.value = `已删除“${world.name}”，其他存档不受影响。`;
      await freshRefresh();
    } catch (error) {
      dialogError.value = describe(error);
    } finally {
      dialogBusy.value = false;
    }
  }
  function settleSubmission(id: string, pending: PendingSubmission, result: Run) {
    if (stopped || submissions[id] !== pending) return;
    delete submissions[id];
    const state = reader.sessions[id];
    if (state) {
      if (state.draft.trim() === pending.input.trim()) state.draft = "";
      state.sendError = "";
    }
    if (!runs[id] || Date.parse(result.created_at) >= Date.parse(runs[id]!.created_at)) runs[id] = result;
  }
  async function sendInput(retry = false) {
    const world = currentWorld.value, state = session.value, failed = failedRun.value;
    if (!world || state.sending || navigating.value) return;
    const id = world.world_id;
    if (!submissions[id] && (activeRun.value || (!retry && !state.draft.trim()))) return;
    if (!status.value?.ready && !submissions[id]) {
      if (!status.value || connectionError.value) {
        state.sendError = "暂时无法确认连接状态，请重试连接后继续。"; return;
      }
      openModel(); return;
    }
    if (!submissions[id]) {
      if (retry && !failed) return;
      const key = crypto.randomUUID();
      submissions[id] = { key, input: retry ? failed!.input : state.draft.trim(),
        retryID: retry ? failed!.run_id : undefined,
        payload: retry ? undefined : {
          request_key: key, input: state.draft.trim(), addressee_id: state.addressee || undefined,
          expected_active_revision: status.value!.active_revision,
          expected_message_head: world.message_head, expected_event_head: world.event_head,
          expected_context_epoch: world.context_epoch,
        },
      };
    }
    const pending = submissions[id]!;
    runRevisions.set(id, (runRevisions.get(id) ?? 0) + 1);
    state.sending = true;
    state.sendError = "";
    try {
      // Query first. Reusing the exact request key and baseline also covers a
      // submission that arrives at the server after this lookup returns empty.
      let result = (await fetchRuns(id, pending.key))[0];
      if (stopped || currentWorld.value?.world_id !== id) return;
      if (!result) result = pending.retryID
        ? await retryRun(id, pending.retryID, pending.key)
        : await submitRun(id, pending.payload!);
      settleSubmission(id, pending, result);
      if (currentWorld.value?.world_id === id) await reader.latest();
    } catch (error) {
      if (submissions[id] !== pending || stopped) return;
      if (error instanceof ApiError && error.status >= 400 && error.status < 500) {
        delete submissions[id];
        state.sendError = describe(error);
      } else {
        state.sendError = "暂时无法确认提交结果，输入已保留。系统会继续查询；再次确认时只使用同一份原请求。";
      }
    } finally {
      state.sending = false;
      runRevisions.set(id, (runRevisions.get(id) ?? 0) + 1);
    }
  }
  async function stopRun() {
    const id = currentWorld.value?.world_id,
      item = activeRun.value,
      state = session.value;
    if (!id || !item || state.sending) return;
    state.sending = true;
    runRevisions.set(id, (runRevisions.get(id) ?? 0) + 1);
    try {
      await cancelRun(id, item.run_id);
    } catch (error) {
      state.sendError = describe(error);
    } finally {
      state.sending = false;
      runRevisions.set(id, (runRevisions.get(id) ?? 0) + 1);
      await freshRefresh();
    }
  }
  function inputKeys(event: KeyboardEvent) {
    if (
      !pendingSubmission.value &&
      event.ctrlKey &&
      event.key === "Enter" &&
      !event.isComposing &&
      event.keyCode !== 229
    ) {
      event.preventDefault();
      void sendInput();
    }
  }
  function chooseCharacter(item: Character) {
    session.value.addressee = item.entity_id;
    if (dialog.value === "scene") closeDialog();
    void nextTick(() => textarea.value?.focus());
  }
  function resizeInput() {
    if (textarea.value) {
      textarea.value.style.height = "auto";
      textarea.value.style.height = `${textarea.value.scrollHeight}px`;
    }
  }
  function resizeViewport() {
    document.documentElement.style.setProperty(
      "--viewport-height",
      `${window.visualViewport?.height ?? window.innerHeight}px`,
    );
    resizeInput();
    if (session.value.bottom) reader.restore();
  }
  watch(
    () => session.value.draft,
    () => void nextTick(resizeInput),
  );
  watch(view, async (value) => {
    document.body.classList.toggle("playing", value === "play");
    await nextTick();
    if (value === "play") {
      reader.restore();
      resizeInput();
    }
  });
  onMounted(async () => {
    resizeViewport();
    window.visualViewport?.addEventListener("resize", resizeViewport);
    window.addEventListener("resize", resizeViewport);
    try {
      await exchangeBootstrapToken();
      const [availableGames, availableModels] = await Promise.all([
        fetchGames(),
        fetchModel(),
      ]);
      games.value = availableGames;
      gameID.value = games.value[0]?.id ?? "";
      providers.value = availableModels.providers;
      modelForm.provider = providers.value[0]?.provider ?? "deepseek";
      changeProvider();
      await refresh();
    } catch (error) {
      connectionError.value = describe(error);
      loaded.value = true;
    }
    timer = window.setInterval(() => {
      now.value = Date.now();
      if (!navigating.value) void refresh();
    }, 1500);
  });
  onUnmounted(() => {
    stopped = true;
    generation++;
    window.clearInterval(timer);
    document.body.classList.remove("playing");
    window.removeEventListener("resize", resizeViewport);
    window.visualViewport?.removeEventListener("resize", resizeViewport);
  });
  return {
    status,
    games,
    worlds,
    currentWorld,
    characters,
    view,
    game,
    gameID,
    loaded,
    connectionError,
    notice,
    dialog,
    dialogError,
    dialogBusy,
    navigating,
    moreOpen,
    deleteCandidate,
    newWorld,
    modelForm,
    providers,
    modelAdvanced,
    settingsForm,
    settingsWorldName,
    policyDefaults,
    detailsOpen,
    textarea,
    reader,
    session,
    viewport,
    storyWorlds,
    recentWorld,
    activeRun,
    pendingSubmission,
    waitingSeconds,
    failedRun,
    saved,
    addresseeName,
    canSubmit,
    presets,
    lengths,
    players,
    initiatives,
    presetName,
    dialogTitle,
    formatDate,
    failureText,
    showDialog,
    closeDialog,
    openModel,
    changeProvider,
    freshRefresh,
    returnHome,
    openStory,
    switchWorld,
    openNewWorld,
    openCopy,
    pendingCopy,
    openSettings,
    configureModel,
    configureSettings,
    createOrCopy,
    confirmDelete,
    removeWorld,
    sendInput,
    stopRun,
    inputKeys,
    chooseCharacter,
    resizeInput,
  };
}
