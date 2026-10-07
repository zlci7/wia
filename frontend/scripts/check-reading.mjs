import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { parse, compileScript } from "@vue/compiler-sfc";
import { build } from "esbuild";

const bundle = await build({
  stdin: {
    contents: `export { useExperience } from './src/useExperience'; export { formatWorldClock, stateDisplayValue } from './src/worldInformation'; export { default as InformationPanel } from './src/components/InformationPanel.vue'; export { default as AppDialog } from './src/components/AppDialog.vue'; export { createRenderer, nextTick, reactive } from 'vue';`,
    resolveDir: process.cwd(), loader: "ts",
  },
  plugins: [{ name: "vue-setup", setup(builder) {
    builder.onLoad({ filter: /\.vue$/ }, async ({ path }) => {
      const { descriptor } = parse(await readFile(path, "utf8"));
      return { contents: compileScript(descriptor, { id: "reading-test" }).content, loader: "ts" };
    });
  } }],
  bundle: true, write: false, platform: "node", format: "esm",
});
const { useExperience, InformationPanel, AppDialog, formatWorldClock, stateDisplayValue, createRenderer, nextTick, reactive } = await import(
  `data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString("base64")}`,
);
const renderer = createRenderer({ createComment: () => ({}), insert() {}, remove() {}, parentNode() {}, nextSibling() {} });
const response = body => new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
const defaults = { perspective: "second_person", length: "standard", detail: "balanced", player_elaboration: "natural", npc_initiative: "contextual", behavior_policies: { coordination: "", narration: "", npc: "" } };
const state = (id, value, extra = {}) => ({ entity_id: "player", state_id: id, name: id, value: { type: "integer", integer: value }, ...extra });
const location = (id, name, kind = "place", parent = "", connections = []) => ({ id, name, kind, parent, connections });

assert.equal(formatWorldClock("1358-03-29 09:00"), "1358年3月29日 · 09:00");
assert.equal(formatWorldClock("2189-12-31 23:59"), "2189年12月31日 · 23:59");
assert.equal(formatWorldClock("第 2 天 10:30"), "日期未配置 · 10:30");
assert.equal(formatWorldClock("Day 0 09:00"), "日期未配置 · 09:00");
assert.equal(formatWorldClock(""), "日期未配置");
assert.equal(stateDisplayValue({ entity_id: "player", state_id: "zero", name: "正常状态", value: { type: "integer" } }), "0", "Go omitempty preserves a typed integer zero");
assert.equal(stateDisplayValue({ entity_id: "player", state_id: "false", name: "特殊状态", value: { type: "boolean" } }), "否", "Go omitempty preserves a typed boolean false");
assert.equal(stateDisplayValue(state("cash", 0, { display_value: "0信用点" })), "0信用点");

const props = reactive({
  tab: "character", world: { world_id: "A", clock: "2189-12-31 23:59", location: location("bay", "维修工位"), adjacent_locations: [location("office", "值班室")] },
  player: { name: "林", profile: "维修员" },
  states: [state("energy", 4), state("repair", 2, { category: "skill" }), state("cash", 420, { category: "resource", currency: { name: "信用点", denominations: [{ name: "信用点", units: 1 }] }, display_value: "420信用点" }), state("npc_cash", 8, { entity_id: "mechanic", currency: { name: "信用点" }, display_value: "8信用点" })],
  items: [{ instance_id: "tool", name: "扳手", holder_id: "player" }, { instance_id: "part", name: "零件", location_id: "bay" }, { instance_id: "key", name: "钥匙", holder_id: "mechanic" }],
  characters: [{ entity_id: "mechanic", name: "陈", role: "同事", in_scene: true }], bystanders: [], addressee: "mechanic",
  knownLocations: [location("station", "维修站", "region"), location("bay", "维修工位", "place", "station", ["office", "secret"]), location("office", "值班室", "place", "station", ["bay"])],
});
let panel;
const panelApp = renderer.createApp({ setup() { panel = InformationPanel.setup(props, { expose() {}, emit() {} }); return () => null; } });
panelApp.mount({});
assert.deepEqual(panel.inventory.value.map(item => item.instance_id), ["tool"], "nearby and NPC-held items remain outside the inventory");
assert.deepEqual(panel.locationItems.value.map(item => item.instance_id), ["part"], "scene items remain visible at their actual location");
assert.deepEqual(panel.characterItems("mechanic").map(item => item.instance_id), ["key"], "visible NPC possessions remain attached to their actual holder");
assert.deepEqual(panel.balances.value.map(item => item.state_id), ["cash"], "only configured player cash is shown");
assert.deepEqual(panel.conditions.value.map(item => item.state_id), ["energy"]);
assert.deepEqual(panel.skills.value.map(item => item.state_id), ["repair"]);
assert.equal(panel.locationGroups.value[0].name, "维修站");
assert.deepEqual(panel.characterStates("mechanic").map(item => item.state_id), ["npc_cash"], "people use only the server-projected states for that person");
assert.equal(panel.connections(props.knownLocations[1]), "值班室", "unknown connections cannot expose hidden locations");
assert.equal(panel.adjacent(props.knownLocations[2]), true);
props.states = [state("cash", 528, { currency: { name: "复合货币", denominations: [{ name: "大单位", units: 240 }, { name: "小单位", units: 1 }] }, display_value: "2大单位 48小单位" })];
props.items = [{ instance_id: "note", name: "记录本", holder_id: "player" }];
await nextTick();
assert.equal(stateDisplayValue(panel.balances.value[0]), "2大单位 48小单位");
assert.deepEqual(panel.inventory.value.map(item => item.instance_id), ["note"]);
assert.equal(panel.conditions.value.length, 0);
props.states = [];
assert.equal(panel.balances.value.length, 0, "missing currency does not manufacture a zero balance");
panelApp.unmount();

class TestElement {
  closest() { return null; }
}
globalThis.Element = TestElement;
const listeners = new Map();
globalThis.document = {
  activeElement: null,
  body: { style: { overflow: "auto" } },
  addEventListener(type, handler) { listeners.set(type, handler); },
  removeEventListener(type, handler) { if (listeners.get(type) === handler) listeners.delete(type); },
  querySelector() { return null; },
};
let narrow = false;
globalThis.window = { matchMedia: () => ({ matches: narrow, addEventListener() {}, removeEventListener() {} }) };
for (const mobile of [false, true]) {
  narrow = mobile;
  const dialogProps = reactive({ title: "角色资料", drawer: true, explicitClose: true, busy: false, destructive: false });
  let dialog, closed = 0;
  const dialogApp = renderer.createApp({ setup() { dialog = AppDialog.setup(dialogProps, { expose() {}, emit() { closed++; } }); return () => null; } });
  dialogApp.mount({});
  await nextTick();
  assert.equal(dialog.modal.value, mobile, "desktop drawer and narrow modal keep their existing modes");
  listeners.get("pointerdown")({ target: new TestElement() });
  dialog.dismiss();
  listeners.get("keydown")({ key: "Escape", preventDefault() {}, isComposing: false });
  assert.equal(closed, 0, "outside clicks, backdrop and Escape preserve an explicitly closed information panel");
  dialog.close();
  assert.equal(closed, 1, "the close button dismisses the information panel");
  dialogProps.busy = true;
  dialog.close();
  assert.equal(closed, 1, "busy dialogs retain their existing guard");
  dialogProps.busy = false;
  dialogProps.explicitClose = false;
  dialog.dismiss();
  listeners.get("keydown")({ key: "Escape", preventDefault() {}, isComposing: false });
  assert.equal(closed, 3, "ordinary dialogs keep backdrop and Escape dismissal");
  dialogApp.unmount();
  assert.equal(listeners.size, 0, "dialog listeners are removed on unmount");
  assert.equal(document.body.style.overflow, "auto");
}

globalThis.document = { documentElement: { style: { setProperty() {} } }, body: { classList: { toggle() {}, remove() {} } } };
globalThis.window = { location: { hash: "" }, innerHeight: 800, scrollTo() {}, addEventListener() {}, removeEventListener() {}, setInterval() { return 1; }, clearInterval() {}, setTimeout };
const world = { world_id: "A", name: "测试存档", game_id: "demo", context_epoch: 1, message_head: 0, event_head: 0, clock: "2189-12-31 23:59" };
let requests = [], experience;
globalThis.fetch = async (url, init = {}) => {
  requests.push([url, init.method ?? "GET"]);
  if (url === "/api/v1/games") return response({ games: [{ id: "demo", title: "维修站", revision: "v1", mode: "open", player: props.player }] });
  if (url === "/api/v1/model-profiles") return response({ providers: [], model: {} });
  if (url === "/api/v1/status") return response({ status: { active_world: world, active_revision: 1, ready: true, model: {} } });
  if (url === "/api/v1/worlds") return response({ worlds: [world] });
  if (url.includes("/messages")) return response({ messages: [], has_more: false });
  if (url.includes("/runs")) return response({ runs: [] });
  if (url === "/api/v1/worlds/A") return response({ world, player_name: "林", player_profile: "维修员", narrative_settings: defaults, characters: props.characters, states: props.states, items: props.items, known_locations: props.knownLocations });
  throw new Error(`Unexpected HTTP ${url}`);
};
const app = renderer.createApp({ setup() { experience = useExperience(); return () => null; } });
app.mount({});
for (let i = 0; i < 30 && !experience.loaded.value; i++) await new Promise(resolve => setImmediate(resolve));
assert.equal(experience.currentWorld.value.world_id, "A");
experience.session.value.draft = "悄声询问维修进度。";
experience.session.value.addressee = "mechanic";
experience.session.value.bottom = false;
const viewport = { scrollTop: 270, clientHeight: 400, scrollHeight: 1200, querySelectorAll: () => [], getBoundingClientRect: () => ({ top: 0 }) };
experience.viewport.value = viewport;
experience.reader.remember();
const baseline = requests.length;
for (const tab of ["character", "inventory", "map", "people"]) {
  experience.openInformation(tab);
  await nextTick();
  assert.equal(experience.dialog.value, "information");
  assert.equal(experience.informationTab.value, tab);
}
experience.chooseCharacter(props.characters[0]);
assert.equal(experience.dialog.value, "information", "choosing a conversation partner keeps the information panel open");
experience.closeDialog();
await nextTick();
assert.equal(experience.dialog.value, "");
assert.equal(experience.session.value.draft, "悄声询问维修进度。");
assert.equal(experience.session.value.addressee, "mechanic");
assert.equal(viewport.scrollTop, 270);
assert.equal(experience.session.value.top, 270);
assert.equal(experience.currentWorld.value.clock, world.clock);
assert.equal(requests.length, baseline, "viewing information causes no HTTP request or game turn");
app.unmount();
console.log("Reading information, cross-story projection, old-date display and session preservation checks passed");
