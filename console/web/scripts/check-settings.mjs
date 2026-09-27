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
  setTimeout(fn) { return realSetTimeout(fn, 0); },
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
  behavior_policies: { coordination: "", narration: "", npc: "" },
};
const world = (id, epoch = 1) => ({
  world_id: id,
  name: `存档${id}`,
  game_id: "demo",
  context_epoch: epoch,
  message_head: 0,
  event_head: 0,
});
let active, records, pending, sent, experience, networkHook;
const realSetTimeout = globalThis.setTimeout;
const delay = (ms) => new Promise(resolve => realSetTimeout(resolve, ms));
function response(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
globalThis.fetch = async (url, init = {}) => {
  if (networkHook) { const value = networkHook(url, init); if (value !== undefined) return value; }
  if (url === "/api/v1/games") return response({ games: [{ id: "demo" }] });
  if (url === "/api/v1/model-profiles")
    return response({ providers: [], model: {} });
  if (url === "/api/v1/status")
    return response({
      status: { active_world: records[active].world, active_revision: 1, ready: true, model: {} },
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
  if (resource.startsWith("/runs")) return response({ runs: [] });
  if (resource.startsWith("/messages"))
    return response({ messages: [], has_more: false });
  if (records[id]) return response(structuredClone(records[id]));
  throw new Error(`Unexpected HTTP ${url}`);
};
async function setup() {
  active = "A";
  networkHook = undefined;
  records = Object.fromEntries(
    ["A", "B"].map((id) => [
      id,
      { world: world(id), narrative_settings: structuredClone(defaults), characters: [] },
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
  async "ended guided story remains readable and blocks another input"(x) {
    records.A.world.story_ended = true;
    records.A.world.message_head++;
    await x.freshRefresh();
    x.session.value.draft = '继续看看';
    assert.equal(x.canSubmit.value, false);
    assert.equal(x.currentWorld.value.world_id, 'A');
  },
  async "save-as terminal failure releases identity for a new operation"(x) {
    const keys = [];
    networkHook = (url, init) => {
      if (!url.endsWith('/save-as')) return;
      keys.push(JSON.parse(init.body).request_key);
      return response({operation:{operation_id:keys.at(-1),status:keys.length === 1 ? 'failed':'ready',target_name:'新副本'}});
    };
    x.openCopy();
    await x.createOrCopy();
    assert.equal(x.pendingCopy.value, undefined);
    await x.createOrCopy();
    assert.notEqual(keys[0], keys[1]);
  },
  async "late copy result cannot close another dialog or claim it saved B"(x) {
    networkHook = (url) => {
      if (url.endsWith('/save-as')) return new Promise(resolve => { pending = resolve; });
    };
    x.openCopy();
    const saving = x.createOrCopy();
    active = 'B';
    await x.freshRefresh();
    x.showDialog('settings');
    x.dialogError.value = '新表单错误';
    pending(response({operation:{operation_id:'copy-A',status:'ready',target_name:'A副本'}}));
    await saving;
    assert.equal(x.dialog.value,'settings');
    assert.equal(x.dialogError.value,'新表单错误');
    assert.equal(x.notice.value.includes('A副本'),false);
  },
  async "lost save-as response resumes one frozen operation"(x) {
    const requests = [], copies = new Map();
    networkHook = (url, init) => {
      if (!url.endsWith('/save-as')) return;
      const body = JSON.parse(init.body);
      requests.push(body);
      if (!copies.has(body.request_key)) copies.set(body.request_key, {
        operation_id: `copy-${copies.size}`, source_world_id: 'A', target_name: body.name, status: 'ready',
      });
      if (requests.length === 1) throw new TypeError('accepted but response lost');
      return response({operation: copies.get(body.request_key)});
    };
    x.openCopy();
    x.newWorld.name = '分支一';
    await x.createOrCopy();
    assert(x.dialogError.value);
    x.showDialog('');
    x.openCopy();
    x.newWorld.name = '不应改变恢复载荷';
    await x.createOrCopy();
    assert.deepEqual(requests[1], requests[0]);
    assert.equal(copies.size, 1);
    assert.match(x.notice.value, /分支一/);
  },
  async "save-as polling failure resumes known task without another POST"(x) {
    let posts = 0, reads = 0;
    networkHook = (url) => {
      if (url.endsWith('/save-as')) {
        posts++;
        return response({operation: {operation_id:'copy-one', status:'copying'}});
      }
      if (url === '/api/v1/world-copy-operations/copy-one') {
        if (++reads === 1) throw new TypeError('poll lost');
        return response({operation:{operation_id:'copy-one',status:'ready',target_name:'唯一副本'}});
      }
    };
    x.openCopy();
    await x.createOrCopy();
    await x.createOrCopy();
    assert.equal(posts, 1);
    assert.equal(reads, 2);
    assert.match(x.notice.value, /唯一副本/);
  },
  async "pending input never inherits previous saved indicator"(x) {
    const done = {run_id:"done", status:"completed", message_seq:1, created_at:new Date().toISOString()};
    records.A.world.message_head = 1;
    networkHook = (url, init) => {
      if (url.includes("/runs?")) return response({runs:[]});
      if (url.endsWith("/runs") && init.method === "POST") throw new TypeError("connection lost");
      if (url.endsWith("/runs")) return response({runs:[done]});
      if (url.includes("/messages")) return response({messages:[{seq:1,kind:"narrative",content:"已保存正文"}],has_more:false});
    };
    await x.freshRefresh();
    assert.equal(x.saved.value,true);
    x.session.value.draft="本次输入";
    await x.sendInput();
    assert(x.pendingSubmission.value);
    assert.equal(x.saved.value,false);
  },
  async "hung polling expires and later polling recovers without late overwrite"(x) {
    globalThis.setTimeout = (fn, ms, ...args) => realSetTimeout(fn, ms === 15000 ? 20 : ms, ...args);
    let release;
    networkHook = (url) => url === "/api/v1/status" ? new Promise(resolve => { release = resolve; }) : undefined;
    await x.freshRefresh();
    assert.match(x.connectionError.value, /超时/);
    networkHook = undefined;
    active = "B";
    await x.freshRefresh();
    release(response({status:{active_world: records.A.world}}));
    await delay(5);
    assert.equal(x.currentWorld.value.world_id, "B");
    assert.equal(x.connectionError.value, "");
  },
  async "accepted submission with lost response is reconciled without replay"(x) {
    let accepted, posts = 0;
    networkHook = (url, init) => {
      if (url.includes("/runs?")) return response({runs: accepted ? [accepted] : []});
      if (url.endsWith("/runs") && init.method === "POST") {
        posts++;
        const body = JSON.parse(init.body);
        accepted = {run_id:"r1",request_key:body.request_key,input:body.input,status:"completed",created_at:new Date().toISOString()};
        throw new TypeError("connection lost");
      }
      if (url.endsWith("/runs")) return response({runs:accepted ? [accepted] : []});
    };
    x.session.value.draft = "问候";
    await x.sendInput();
    assert.equal(x.pendingSubmission.value.input, "问候");
    assert.equal(x.session.value.draft, "问候");
    await x.freshRefresh();
    assert.equal(x.pendingSubmission.value, undefined);
    assert.equal(x.session.value.draft, "");
    assert.equal(posts, 1);
  },
  async "uncertain resubmission preserves original key payload and edited draft"(x) {
    const bodies = [];
    networkHook = (url, init) => {
      if (url.includes("/runs?")) return response({runs:[]});
      if (url.endsWith("/runs") && init.method === "POST") {
        bodies.push(JSON.parse(init.body));
        if (bodies.length === 1) throw new TypeError("connection lost");
        return response({run:{run_id:"r1",status:"running",request_key:bodies[1].request_key,input:bodies[1].input,created_at:new Date().toISOString()}},202);
      }
    };
    x.session.value.draft = "原来的问候";
    await x.sendInput();
    x.session.value.draft = "下一句话";
    assert.equal(x.canSubmit.value,false);
    await x.sendInput();
    assert.deepEqual(bodies[0],bodies[1]);
    assert.equal(x.session.value.draft,"下一句话");
    assert.equal(x.pendingSubmission.value,undefined);
  },
  async "late submission response never clears another world draft"(x) {
    let release;
    networkHook = (url,init) => {
      if (url.includes("/runs?")) return response({runs:[]});
      if (init.method === "POST" && url.endsWith("/runs")) return new Promise(resolve=>{release=resolve;});
    };
    x.session.value.draft = "A的行动";
    const sending=x.sendInput();
    while(!release) await delay(1);
    active="B"; await x.freshRefresh();
    x.session.value.draft="B的行动";
    release(response({run:{run_id:"a1",status:"running",input:"A的行动",created_at:new Date().toISOString()}},202));
    await sending;
    assert.equal(x.currentWorld.value.world_id,"B");
    assert.equal(x.session.value.draft,"B的行动");
    assert.equal(x.activeRun.value,undefined);
  },
  async "retry with lost response reuses same retry request key"(x) {
    const failed={run_id:"failed1",status:"failed",input:"重试输入",created_at:new Date().toISOString()};
    let bodies=[];
    networkHook=(url,init)=>{
      if(url.includes("/runs?")) return response({runs:[]});
      if(url.endsWith("/retry")){
        bodies.push(JSON.parse(init.body));
        if(bodies.length===1) throw new TypeError("lost");
        return response({run:{...failed,run_id:"retry1",status:"running"}},202);
      }
      if(url.endsWith("/runs")) return response({runs:[failed]});
    };
    await x.freshRefresh();
    await x.sendInput(true);
    await x.sendInput();
    assert.equal(bodies.length,2);
    assert.deepEqual(bodies[0],bodies[1]);
  },
  async "cancel response loss is reconciled from actual terminal run status"(x) {
    const run={run_id:"r1",status:"running",input:"取消输入",created_at:new Date().toISOString()};
    networkHook=(url,init)=>{
      if(url.endsWith("/cancel")) {run.status="cancelled"; throw new TypeError("lost");}
      if(url.endsWith("/runs")) return response({runs:[run]});
    };
    await x.freshRefresh();
    await x.stopRun();
    assert.equal(x.activeRun.value,undefined);
    assert.equal(x.failedRun.value.status,"cancelled");
    assert.equal(x.session.value.draft,"取消输入");
  },
  async "policy draft is detached from saved settings and polling"(x) {
    x.openSettings();
    x.settingsForm.behavior_policies.npc = "自定义决定";
    records.A.narrative_settings.behavior_policies.npc = "远端更新";
    records.A.world.context_epoch = 2;
    await x.freshRefresh();
    assert.equal(x.settingsForm.behavior_policies.npc, "自定义决定");
    const saving = x.configureSettings();
    assert.equal(sent.body.behavior_policies.npc, "自定义决定");
    assert.equal(sent.body.expected_context_epoch, 1);
    x.settingsForm.behavior_policies.npc = "更晚的编辑";
    assert.equal(sent.body.behavior_policies.npc, "自定义决定");
    pending(response({ error: { code: "version_conflict" } }, 409));
    await saving;
    assert.equal(x.settingsForm.behavior_policies.npc, "更晚的编辑");
  },
  async "cancelled policy edit does not change saved settings"(x) {
    x.openSettings();
    x.settingsForm.behavior_policies.npc = "未保存";
    x.closeDialog();
    x.openSettings();
    assert.equal(x.settingsForm.behavior_policies.npc, "");
  },
  async "resetting policy submits default marker only for that policy"(x) {
    records.A.narrative_settings.behavior_policies = { npc: "主动", coordination: "协调", narration: "叙事" };
    records.A.world.context_epoch = 2;
    await x.freshRefresh();
    x.openSettings();
    x.settingsForm.behavior_policies.npc = "";
    const saving = x.configureSettings();
    assert.deepEqual(sent.body.behavior_policies, { npc: "", coordination: "协调", narration: "叙事" });
    pending(response({ world: world("A", 3), settings: sent.body }));
    await saving;
  },
  async "context failures give actionable feedback without altering drafts"(x) {
    x.session.value.draft = "保留这段输入";
    const draft = x.session.value.draft;
    assert.match(x.failureText({ status: "failed", reason: "context_capacity_exceeded" }), /容量.*输入已保留/);
    assert.match(x.failureText({ status: "failed", reason: "context_source_missing" }), /来源记录不完整.*本轮未保存/);
    assert.equal(x.session.value.draft, draft);
  },
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
    globalThis.setTimeout = realSetTimeout;
    app.unmount();
    await nextTick();
  }
}
if (failures) process.exitCode = 1;
