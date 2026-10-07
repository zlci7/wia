import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { parse, compileScript } from '@vue/compiler-sfc';
import { build } from 'esbuild';

const bundle = await build({
  stdin: { contents: `export { default as RunProgress } from './src/components/RunProgress.vue'; export { createRenderer, nextTick, reactive } from 'vue';`, resolveDir: process.cwd(), loader: 'ts' },
  plugins: [{ name: 'vue-setup', setup(builder) { builder.onLoad({ filter: /\.vue$/ }, async ({ path }) => {
    const { descriptor } = parse(await readFile(path, 'utf8'));
    return { contents: compileScript(descriptor, { id: 'progress-test' }).content, loader: 'ts' };
  }); } }],
  bundle: true, write: false, platform: 'node', format: 'esm',
});
const { RunProgress, createRenderer, nextTick, reactive } = await import(`data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString('base64')}`);
const renderer = createRenderer({ createComment: () => ({}), insert() {}, remove() {}, parentNode() {}, nextSibling() {} });
const props = reactive({ worldID: 'A', run: { run_id: 'r1', status: 'running' }, characters: [] });
const response = body => new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } });
const calls = [], deferred = [];
let delay = false;
globalThis.fetch = async url => {
  calls.push(url);
  const value = { run_id: props.run.run_id, status: 'running', budget_seconds: 300, calls: [{ id: 1, purpose: 'scene', phase: 'thinking', reasoning_chars: 4, output_chars: 0, ...(url.includes('include_thinking=1') ? { reasoning: '幕后内容' } : {}) }] };
  if (delay) return new Promise(resolve => deferred.push(() => resolve(response(value))));
  return response(value);
};
let panel;
const app = renderer.createApp({ setup() { panel = RunProgress.setup(props, { expose() {}, emit() {} }); return () => null; } });
app.mount({});
const settle = async () => { await new Promise(resolve => setImmediate(resolve)); await nextTick(); };
await settle();
assert.equal(calls.length, 1);
assert(!calls[0].includes('include_thinking'), 'default progress excludes reasoning');
assert.equal(panel.progress.value.calls[0].reasoning, undefined);
assert.equal(panel.statusText.value, '组织场景：思考中');
panel.toggleThinking({ target: { open: true } });
await settle();
assert(calls.at(-1).includes('include_thinking=1'));
assert.equal(panel.progress.value.calls[0].reasoning, '幕后内容');
panel.toggleThinking({ target: { open: false } });
assert.equal(panel.progress.value.calls[0].reasoning, undefined, 'collapse clears received raw text');
await settle();
delay = true;
panel.toggleThinking({ target: { open: true } });
panel.toggleThinking({ target: { open: false } });
deferred.shift()();
await settle();
assert.equal(panel.progress.value.calls[0].reasoning, undefined, 'late expanded response cannot reveal reasoning after collapse');
props.worldID = 'B'; props.run = { run_id: 'r2', status: 'running' };
await nextTick();
deferred.shift()();
await settle();
assert.equal(panel.progress.value, undefined, 'old-world response cannot update the new run');
app.unmount();
for (const resolve of deferred) resolve();
await settle();
console.log('Run progress disclosure, stale-response and teardown checks passed');
