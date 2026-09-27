import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { parse, compileScript } from '@vue/compiler-sfc';
import { build } from 'esbuild';

const source = await readFile('src/components/MemoryPanel.vue', 'utf8');
const { descriptor } = parse(source);
const script = compileScript(descriptor, { id: 'memory-test' });
const bundle = await build({ stdin: { contents: script.content + `\nexport { createRenderer, nextTick } from 'vue';`, resolveDir: `${process.cwd()}/src/components`, loader: 'ts' }, bundle: true, write: false, platform: 'node', format: 'esm' });
const { default: component, createRenderer, nextTick } = await import(`data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString('base64')}`);
const renderer = createRenderer({ createComment: () => ({}), insert() {}, remove() {}, parentNode() {}, nextSibling() {} });
const sleep = () => new Promise(resolve => setTimeout(resolve, 0));
const response = (data, status = 200) => new Response(JSON.stringify(data), { status });
const record = { kind: 'digest', scope: 'player', target_id: '1', content: '旧回顾' };
const view = epoch => ({ world_id: 'A', context_epoch: epoch, scope: 'player', scopes: ['player'], digest: { revision: 1, through_seq: 4, content: '旧回顾', states: [], source_ids: [] }, sources: [], records: [record], job: { status: 'completed' }, has_more: false });
let state, requests, events, hook, epoch;
globalThis.fetch = async (url, init = {}) => {
  if (hook) { const value = hook(url, init); if (value !== undefined) return value; }
  if (init.method === 'POST') { requests.push(JSON.parse(init.body)); return response({ correction: {} }, 202); }
  return response(view(epoch));
};
async function mount() {
  epoch = 1; requests = []; events = []; hook = undefined;
  const app = renderer.createApp({ setup() { state = component.setup({ worldID: 'A', worldName: '测试存档', characters: [] }, { expose() {}, emit: (...e) => events.push(e) }); return () => null; } });
  app.mount({}); await sleep(); await nextTick(); return app;
}

{
  const app = await mount(); state.choose(record); state.draft.value = '纠正草稿'; epoch = 2; await state.load(); await state.save();
  assert.equal(requests[0].expected_context_epoch, 1, 'refresh must not rebase an old draft'); app.unmount();
}
{
  const app = await mount(); state.choose(record); state.draft.value = '第一次内容'; let first;
  hook = (url, init) => { if (init.method === 'POST') { first = JSON.parse(init.body); return Promise.reject(new Error('response lost')); } };
  await state.save(); assert.ok(state.pending.value); state.draft.value = '后来编辑'; hook = undefined; await state.save();
  assert.deepEqual(requests[0], first, 'recovery must retain original identity and payload'); app.unmount();
}
{
  const app = await mount(); let late;
  hook = () => new Promise(resolve => { late = resolve; }); const first = state.load(); hook = undefined; epoch = 3; await state.load();
  late(response(view(1))); await first; assert.equal(state.data.value.context_epoch, 3); app.unmount();
}
{
  const app = await mount(); state.choose(record); state.draft.value = '纠正'; let late;
  hook = (url, init) => init.method === 'POST' ? new Promise(resolve => { late = resolve; }) : undefined;
  const pending = state.save(); app.unmount(); late(response({}, 202)); await pending;
  assert.equal(events.some(e => e[0] === 'updated'), false, 'late result must not refresh another world');
}
console.log('4 memory editing/session/recovery tests passed');
