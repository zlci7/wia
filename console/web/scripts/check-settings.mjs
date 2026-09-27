import assert from "node:assert/strict";
import { build } from "esbuild";

// Bundle the real composable and Vue together; exercise its HTTP boundary.
const bundle = await build({
  stdin: {
    contents: `export { useExperience } from './src/useExperience'; export { createRenderer, nextTick, watch } from 'vue';`,
    resolveDir: process.cwd(),
    loader: "ts",
  },
  bundle: true,
  write: false,
  platform: "node",
  format: "esm",
});
const { useExperience, createRenderer, nextTick, watch } = await import(
  `data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString("base64")}`
);
globalThis.document = {
  documentElement: { style: { setProperty() {} } },
  body: { classList: { toggle() {}, remove() {} } },
};
globalThis.window = {
  location: { hash: "" },
  innerHeight: 800,
  addEventListener() {},
  removeEventListener() {},
  setInterval() {
    return 1;
  },
  clearInterval() {},
};
const renderer = createRenderer({
  createComment: () => ({}),
  insert() {},
  remove() {},
  parentNode() {},
  nextSibling() {},
});
const defaults = {
  perspective: "second_person",
  length: "standard",
  detail: "balanced",
  player_elaboration: "natural",
  npc_initiative: "contextual",
  custom_instruction: "",
};
const world = (id, epoch = 1) => ({
  world_id: id,
  name: `存档${id}`,
  game_id: "demo",
  context_epoch: epoch,
  message_head: 0,
  event_head: 0,
});
let active, records, pending, sent, experience;
function response(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
globalThis.fetch = async (url, init = {}) => {
  if (url === "/api/v1/games") return response({ games: [{ id: "demo" }] });
  if (url === "/api/v1/model-profiles")
    return response({ providers: [], model: {} });
  if (url === "/api/v1/status")
    return response({
      status: { active_world: records[active].world, ready: true, model: {} },
    });
  if (url === "/api/v1/worlds")
    return response({ worlds: Object.values(records).map((r) => r.world) });
  const [, id, resource = ""] =
    url.match(/^\/api\/v1\/worlds\/([^/]+)(.*)$/) ?? [];
  if (resource === "/agent-settings") {
    sent = { id, body: JSON.parse(init.body) };
    return new Promise((resolve) => {
      pending = resolve;
    });
  }
  if (resource === "/runs") return response({ runs: [] });
  if (resource.startsWith("/messages"))
    return response({ messages: [], has_more: false });
  if (records[id]) return response(structuredClone(records[id]));
  throw new Error(`Unexpected HTTP ${url}`);
};
async function setup() {
  active = "A";
  records = Object.fromEntries(
    ["A", "B"].map((id) => [
      id,
      { world: world(id), narrative_settings: { ...defaults }, characters: [] },
    ]),
  );
  pending = sent = undefined;
  const app = renderer.createApp({
    setup() {
      experience = useExperience();
      return () => null;
    },
  });
  app.mount({});
  for (let i = 0; i < 30 && !experience.loaded.value; i++)
    await new Promise((resolve) => setImmediate(resolve));
  assert.equal(experience.currentWorld.value.world_id, "A");
  return app;
}
const tests = {
  async "polling preserves draft epoch and conflict"(x) {
    x.openSettings();
    x.settingsForm.length = "concise";
    records.A.world.context_epoch = 2;
    await x.freshRefresh();
    const saving = x.configureSettings();
    assert.equal(sent.body.expected_context_epoch, 1);
    pending(response({ error: { code: "version_conflict" } }, 409));
    await saving;
    assert.match(x.dialogError.value, /重新打开/);
    assert.equal(x.dialog.value, "settings");
  },
  async "late success cannot downgrade newer world or settings"(x) {
    x.openSettings();
    const saving = x.configureSettings();
    records.A.world.context_epoch = 3;
    records.A.narrative_settings.length = "detailed";
    await x.freshRefresh();
    const seen = [];
    const stop = watch(
      () => x.currentWorld.value.context_epoch,
      (value) => seen.push(value),
      { flush: "sync" },
    );
    pending(response({ world: world("A", 2), settings: defaults }));
    await saving;
    stop();
    assert(!seen.includes(2), "late response temporarily downgraded the world");
    assert.equal(x.currentWorld.value.context_epoch, 3);
    x.openSettings();
    assert.equal(x.settingsForm.length, "detailed");
  },
  async "world switch closes an idle editor"(x) {
    x.openSettings();
    active = "B";
    await x.freshRefresh();
    assert.equal(x.dialog.value, "");
  },
  async "world switch protects pending write and identifies original save"(x) {
    x.openSettings();
    const saving = x.configureSettings();
    active = "B";
    await x.freshRefresh();
    x.closeDialog();
    assert.equal(x.dialog.value, "settings");
    assert.equal(x.dialogBusy.value, true);
    pending(response({ world: world("A", 2), settings: defaults }));
    await saving;
    assert.equal(x.currentWorld.value.world_id, "B");
    assert.equal(x.dialog.value, "");
    assert.match(x.notice.value, /存档A/);
  },
  async "late failure on switched world releases invalid editor with scoped feedback"(
    x,
  ) {
    x.openSettings();
    const saving = x.configureSettings();
    active = "B";
    await x.freshRefresh();
    pending(response({ error: { code: "world_busy", message: "busy" } }, 409));
    await saving;
    assert.equal(x.dialog.value, "");
    assert.equal(x.dialogBusy.value, false);
    assert.match(x.notice.value, /存档A/);
    assert.equal(x.dialogError.value, "");
  },
  async "detached response cannot close or change a newer dialog"(x) {
    x.openSettings();
    const saving = x.configureSettings();
    // A detached component/navigation may replace the editor despite UI guards.
    x.showDialog("model");
    x.dialogError.value = "新表单错误";
    x.dialogBusy.value = true;
    pending(response({ world: world("A", 2), settings: defaults }));
    await saving;
    assert.equal(x.dialog.value, "model");
    assert.equal(x.dialogError.value, "新表单错误");
    assert.equal(x.dialogBusy.value, true);
    assert.equal(x.notice.value, "");
  },
  async "late failure cannot overwrite a newer editing session"(x) {
    x.openSettings();
    const saving = x.configureSettings();
    x.showDialog("");
    x.dialogBusy.value = false;
    x.openSettings();
    x.settingsForm.length = "detailed";
    x.dialogError.value = "新会话提示";
    x.dialogBusy.value = true;
    pending(response({ error: { code: "world_busy" } }, 409));
    await saving;
    assert.equal(x.dialog.value, "settings");
    assert.equal(x.settingsForm.length, "detailed");
    assert.equal(x.dialogError.value, "新会话提示");
    assert.equal(x.dialogBusy.value, true);
  },
  async "switching back does not revalidate an in-flight old editor"(x) {
    x.openSettings();
    const saving = x.configureSettings();
    active = "B";
    await x.freshRefresh();
    active = "A";
    await x.freshRefresh();
    pending(response({ error: { code: "version_conflict" } }, 409));
    await saving;
    assert.equal(x.dialog.value, "");
    assert.match(x.notice.value, /存档A.*未保存/);
    assert.equal(x.dialogBusy.value, false);
  },
};
let failures = 0;
for (const [name, test] of Object.entries(tests)) {
  const app = await setup();
  try {
    await test(experience);
    console.log(`PASS ${name}`);
  } catch (error) {
    failures++;
    console.error(`FAIL ${name}: ${error.message}`);
  } finally {
    app.unmount();
    await nextTick();
  }
}
if (failures) process.exitCode = 1;
