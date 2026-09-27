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
    custom_instruction: "",
  });
  const settings = ref(defaults()),
    settingsForm = reactive(defaults()),
    settingsEpoch = ref(0),
    detailsOpen = ref(false);
  const textarea = ref<HTMLTextAreaElement>(),
    runs = reactive<Record<string, Run | undefined>>({});
  const seenFailures = new Map<string, string>();
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
      !session.value.sending &&
      !activeRun.value &&
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
    { value: "concise", label: "简短", note: "约 120–300 字" },
    { value: "standard", label: "标准", note: "约 300–600 字" },
    { value: "detailed", label: "细致", note: "约 600–1200 字" },
  ] as const;
  const players = [
    { value: "restrained", label: "克制", note: "间接转述，只补必要衔接" },
    { value: "natural", label: "自然", note: "补充等价的简短台词和动作" },
    { value: "expressive", label: "充分", note: "在已选方向内完整表现主角" },
  ] as const;
  const initiatives = [
    { value: "responsive", label: "回应为主", note: "没有直接刺激时倾向沉默" },
    {
      value: "contextual",
      label: "按情境主动",
      note: "职责或关切被触及时介入",
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
          narration_generation_failed:
            "故事正文没有成功生成，输入仍保留，可以重试。",
          npc_generation_failed: "有角色未完成回应，输入仍保留，可以重试。",
          coordination_generation_failed:
            "场景结果未能确定，输入仍保留，可以重试。",
          intent_generation_failed: "未能理解这次输入，输入仍保留，可以重试。",
          generation_timeout: "模型响应超时，输入仍保留，可以重试。",
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
    if (currentWorld.value?.world_id !== id) return;
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
        if (dialog.value === "settings" && !dialogBusy.value) {
          showDialog("");
          notice.value = "活动存档已切换，请重新打开故事设置。";
        }
      }
      currentWorld.value = snapshot.world;
      if (view.value === "play" || !gameID.value)
        gameID.value = snapshot.world.game_id;
      characters.value = snapshot.characters.filter((item) => item.in_scene);
      settings.value = snapshot.narrative_settings;
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
    newWorld.name = `${currentWorld.value.name} · 分支 ${dateName()}`;
    showDialog("copy");
  }
  function openSettings() {
    if (!currentWorld.value) return;
    formWorldID.value = currentWorld.value.world_id;
    settingsEpoch.value = currentWorld.value.context_epoch;
    Object.assign(settingsForm, settings.value);
    detailsOpen.value = false;
    showDialog("settings");
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
    if (dialogBusy.value || activeRun.value) return;
    dialogBusy.value = true;
    dialogError.value = "";
    const id = formWorldID.value;
    try {
      if (currentWorld.value?.world_id !== id)
        throw new Error("活动存档已切换，请重新打开故事设置。");
      const result = await saveAgentSettings(
        id,
        { ...settingsForm },
        settingsEpoch.value,
      );
      if (currentWorld.value?.world_id === id) {
        settings.value = result.settings;
        currentWorld.value = result.world;
      }
      showDialog("");
      notice.value = "故事设置已保存，从下一轮生效。";
      await freshRefresh();
    } catch (error) {
      dialogError.value = describe(error);
    } finally {
      dialogBusy.value = false;
    }
  }
  async function createOrCopy() {
    if (dialogBusy.value) return;
    const copying = dialog.value === "copy";
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
      if (copying) {
        const operation = await saveAs(
          formWorldID.value,
          newWorld.name.trim(),
          status.value?.active_revision ?? 0,
        );
        let result = operation;
        const deadline = Date.now() + 300000;
        while (
          !["ready", "failed"].includes(result.status) &&
          Date.now() < deadline &&
          !stopped
        ) {
          await new Promise((resolve) => window.setTimeout(resolve, 500));
          result = await fetchCopyOperation(operation.operation_id);
        }
        if (result.status === "failed")
          throw new Error(result.error || "另存失败，原存档未受影响。");
        if (result.status !== "ready")
          throw new Error("另存仍在进行，请稍后到存档列表查看。");
        showDialog("saves");
        notice.value = `已另存为“${result.target_name}”，当前仍在原存档。`;
      } else {
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
  async function sendInput(retry = false) {
    const world = currentWorld.value,
      state = session.value,
      failed = failedRun.value;
    if (
      !world ||
      state.sending ||
      activeRun.value ||
      navigating.value ||
      (!retry && !state.draft.trim())
    )
      return;
    if (!status.value?.ready) {
      if (!status.value || connectionError.value) {
        state.sendError = "暂时无法确认连接状态，请重试连接后继续。";
        return;
      }
      openModel();
      return;
    }
    const id = world.world_id,
      text = state.draft.trim();
    runRevisions.set(id, (runRevisions.get(id) ?? 0) + 1);
    state.sending = true;
    state.sendError = "";
    try {
      if (retry && !failed) return;
      const result = retry
        ? await retryRun(id, failed!.run_id)
        : await submitRun(id, {
            request_key: crypto.randomUUID(),
            input: text,
            addressee_id: state.addressee || undefined,
            expected_active_revision: status.value.active_revision,
            expected_message_head: world.message_head,
            expected_event_head: world.event_head,
            expected_context_epoch: world.context_epoch,
          });
      runs[id] = result;
      if (state.draft.trim() === (retry ? failed!.input.trim() : text))
        state.draft = "";
      if (currentWorld.value?.world_id === id) await reader.latest();
    } catch (error) {
      state.sendError = describe(error);
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
    detailsOpen,
    textarea,
    reader,
    session,
    viewport,
    storyWorlds,
    recentWorld,
    activeRun,
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
