import assert from 'node:assert/strict';
import { build } from 'esbuild';
const bundle = await build({ stdin: { contents: `export { useCreationPlay } from './src/useCreationPlay'; export { creationReadingMessages } from './src/creationReading'; export { createRenderer, nextTick } from 'vue';`, resolveDir: process.cwd(), loader: 'ts' }, bundle: true, write: false, platform: 'node', format: 'esm' });
const { useCreationPlay, creationReadingMessages, createRenderer, nextTick } = await import(`data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString('base64')}`);
const renderer = createRenderer({ createComment: () => ({}), insert() {}, remove() {}, parentNode() {}, nextSibling() {} });
const stored = new Map();
globalThis.window = { sessionStorage: { getItem: key => stored.get(key), setItem: (key, value) => stored.set(key, value), removeItem: key => stored.delete(key) } };
const json = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
async function until(check) { for (let i = 0; i < 100; i++) { if (check()) return; await new Promise(resolve => setTimeout(resolve, 1)); } throw new Error('state did not settle'); }
const initial = { id: 'P', version: 1, game_id: 'demo', title: '船坞', revision: 'v1', turn: 0, clock: '2040-06-12 23:58', location: '船坞', messages: [{ seq: 1, message_id: 'opening', kind: 'narrative', content: '开场' }], characters: [], suggestions: { turn: 0, enabled: true, status: 'empty', items: [] } };
const streaming = { ...initial, run: { id: 'R', input: '我接过信', candidate: '工匠递过纸信。', started_at: '' } };
const pendingReading = creationReadingMessages(streaming, true);
assert.deepEqual(pendingReading.map(message => message.seq), [1, 2, 3]);
const completedReading = creationReadingMessages({ ...streaming, messages: pendingReading }, false);
assert.deepEqual(completedReading, pendingReading, 'acceptance preserves message sequence, kind and prose');
assert.deepEqual(creationReadingMessages(streaming, false), initial.messages, 'cancelled or failed candidates leave accepted history');
let version = 1, current = structuredClone(initial), posts = [], creates = [], failReply = false, failCreate = false;
globalThis.fetch = async (url, init = {}) => {
  if (url === '/api/v1/play-sessions') {
    creates.push(JSON.parse(init.body));
    if (failCreate) { failCreate = false; throw new Error('lost create response'); }
    return json(current, 201);
  }
  if (url.endsWith('/suggestions')) {
    const request = JSON.parse(init.body);
    current = { ...current, version: ++version, suggestions: { turn: current.turn, enabled: request.enabled, status: request.enabled ? 'ready' : 'empty', items: request.enabled ? ['观察核实', '交谈试探', '主动行动'] : [] } };
    return json(current, 202);
  }
  if (url.endsWith('/turns') && init.method === 'POST') {
    const request = JSON.parse(init.body); posts.push(request);
    if (!current.run || current.run.request_key !== request.request_key) current = { ...current, version: ++version, run: { id: 'R', request_key: request.request_key, input: request.input, status: 'running', started_at: new Date().toISOString() }, suggestions: { ...current.suggestions, status: 'empty', items: [] } };
    if (failReply) { failReply = false; throw new Error('lost accepted response'); }
    return json(current, 202);
  }
  if (url.endsWith('/cancel')) { current = { ...current, version: ++version, run: { ...current.run, status: 'cancelled', error: '已取消' } }; return json(current, 202); }
  return json(current);
};
let play;
const vm = renderer.createApp({ setup() { play = useCreationPlay(); return () => null; } }); vm.mount({});
try {
  assert.equal(await play.start({ id: 'demo', revision: 'v1' }), true);
  assert.equal(play.state.reasoning, 'low', 'new play enables deep thinking');
  await until(() => !play.state.suggestionWriting);
  assert.equal(play.state.active.suggestions.items.length, 3);
  play.choose('交谈试探'); assert.equal(play.state.draft, '交谈试探');
  play.choose('观察核实'); assert.equal(play.state.draft, '交谈试探', 'suggestion cannot overwrite a draft');
  play.state.advance = true; play.state.transport = 'stream'; failReply = true;
  await play.send();
  assert.ok(play.state.pending, 'uncertain response keeps the original request');
  const original = { ...play.state.pending };
  play.state.advance = false; play.state.transport = 'non_stream'; play.state.reasoning = 'off';
  await play.refresh();
  assert.equal(play.state.pending, undefined, 'read resolves the already accepted request');
  assert.equal(posts.length, 1, 'resolving an accepted request costs no second submission');
  assert.equal(posts[0].allow_plot_advance, true); assert.equal(posts[0].transport, 'stream');
  assert.equal(posts[0].reasoning, 'low', 'reasoning is frozen with the submitted action');
  assert.equal(await play.start({ id: 'demo', revision: 'v1' }), true, 'a running session can be reopened from its story');
  await play.cancel();
  assert.equal(play.state.draft, '交谈试探'); assert.equal(play.state.active.turn, 0);
  // A definitive failed run can be retried with a new key and the retained input.
  await play.send(); assert.notEqual(posts[1].request_key, original.request_key);
  assert.equal(posts[1].reasoning, 'off', 'explicitly disabled thinking is submitted');
  current = { ...current, version: ++version, turn: 1, messages: [...current.messages, { message_id: 'input', kind: 'player', content: '交谈试探' }, { message_id: 'narrative', kind: 'narrative', content: '回应' }], run: { ...current.run, status: 'completed' }, suggestions: { turn: 1, enabled: true, status: 'empty', items: [] } };
  await play.refresh(); assert.equal(play.state.draft, '');
  play.state.draft = '交谈试探'; await play.refresh();
  assert.equal(play.state.draft, '交谈试探', 'a new identical draft remains after a later poll');
  await play.toggleSuggestions(); assert.equal(play.state.active.suggestions.enabled, false);
  assert.equal(play.state.draft, '交谈试探');
  // A late response from a previous presentation revision cannot replace a newer view.
  const saved = current;
  current = { ...initial, version: 1 }; await play.refresh();
  assert.equal(play.state.active.turn, 1); current = saved;
  let restored;
  const second = renderer.createApp({ setup() { restored = useCreationPlay(); return () => null; } }); second.mount({});
  try { assert.equal(await restored.restore(), true); assert.equal(restored.state.active.turn, 1); } finally { second.unmount(); }
  // A new identity is frozen during an uncertain creation, and the old scene stays available.
  const previous = play.state.active;
  failCreate = true;
  assert.equal(await play.start({ id: 'demo', revision: 'v1' }, 'worker', true), false);
  assert.equal(play.state.active.id, previous.id); assert.equal(play.state.draft, '交谈试探');
  const uncertainCreate = creates.at(-1);
  current = { ...initial, id: 'P2', starting_option_id: 'worker' };
  assert.equal(await play.start({ id: 'demo', revision: 'v1' }, 'visitor', true), true);
  assert.deepEqual(creates.at(-1), uncertainCreate, 'retry retains original identity and key');
  assert.equal(play.state.active.starting_option_id, 'worker'); assert.equal(play.state.reasoning, 'low');
  assert.equal(play.state.draft, '', 'accepted new session begins with a clean draft');
  console.log('Creation play request recovery, frozen options, cancellation, suggestion basis, draft preservation and refresh checks passed');
} finally { vm.unmount(); }
