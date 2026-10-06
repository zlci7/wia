import { execFileSync } from "node:child_process";
import assert from "node:assert/strict";

const browser = process.env.WIA_BROWSE_BIN;
const base = process.env.WIA_FIXTURE_URL;
if (!browser || !/^http:\/\/127\.0\.0\.1:\d+$/.test(base ?? ""))
  throw new Error("Use WIA_BROWSE_BIN and a disposable TestBrowserFixture URL");

const call = (...args) => execFileSync(browser, args, { encoding: "utf8", timeout: 20000 }).trim();
const js = expression => JSON.parse(call("js", `(async()=>JSON.stringify(await (${expression})))()`));
const click = selector => call("click", selector);
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
async function wait(expression) {
  const deadline = Date.now() + 18000;
  while (Date.now() < deadline) {
    if (js(expression)) return;
    await pause(250);
  }
  throw new Error(`Timed out: ${expression}`);
}
const readWorld = id => js(`fetch('/api/v1/worlds/'+${JSON.stringify(id)}).then(r=>r.json())`);
const heads = snapshot => ({
  world_id: snapshot.world.world_id,
  clock: snapshot.world.clock,
  context_epoch: snapshot.world.context_epoch,
  message_head: snapshot.world.message_head,
  event_head: snapshot.world.event_head,
  turn_seq: snapshot.world.turn_seq,
});
const anchor = `(()=>{const box=document.querySelector('.transcript'),top=box.getBoundingClientRect().top;const item=[...box.querySelectorAll('[data-message-id]')].find(item=>item.getBoundingClientRect().bottom>top);return {id:item?.dataset.messageId,offset:item?.getBoundingClientRect().top-top,scrollTop:box.scrollTop}})()`;
function assertSession(draft, addressee, before, label) {
  assert.equal(js(`document.querySelector('textarea[aria-label="你的行动"]').value`), draft, `${label}: draft`);
  assert.equal(js(`document.querySelector('select[aria-label="交谈对象"]').value`), addressee, `${label}: addressee`);
  const after = js(anchor);
  assert.equal(after.id, before.id, `${label}: visible message`);
  assert(Math.abs(after.offset - before.offset) < 3, `${label}: anchor shifted ${after.offset - before.offset}px`);
  assert(Math.abs(after.scrollTop - before.scrollTop) < 3, `${label}: scroll position`);
}
function assertLayout(size) {
  const layout = js(`(()=>{const submit=document.querySelector('.composer button[type=submit]'),box=submit.getBoundingClientRect(),hit=document.elementFromPoint(box.left+box.width/2,box.top+box.height/2);return {width:innerWidth,height:innerHeight,bodyWidth:document.body.scrollWidth,documentWidth:document.documentElement.scrollWidth,readerHeight:document.querySelector('.transcript').clientHeight,composerBottom:document.querySelector('.composer').getBoundingClientRect().bottom,submit:{left:box.left,right:box.right,top:box.top,bottom:box.bottom,width:box.width,height:box.height,disabled:submit.disabled,reachable:hit===submit||submit.contains(hit)}}})()`);
  assert(layout.bodyWidth <= layout.width && layout.documentWidth <= layout.width, `${size}: page overflow ${JSON.stringify(layout)}`);
  assert(layout.readerHeight > 0, `${size}: visible reader`);
  assert(layout.composerBottom <= layout.height + 1, `${size}: composer reaches outside the viewport`);
  assert(layout.submit.left >= 0 && layout.submit.right <= layout.width + 1 && layout.submit.top >= 0 && layout.submit.bottom <= layout.height + 1, `${size}: submit outside viewport`);
  assert(layout.submit.width > 0 && layout.submit.height > 0 && layout.submit.reachable && !layout.submit.disabled, `${size}: enabled submit is reachable`);
}
function assertSuggestionRows(size) {
  const layout = js(`(()=>{const list=document.querySelector('.suggestion-items');return {width:list.clientWidth,scrollWidth:list.scrollWidth,rows:[...list.querySelectorAll('button')].map(button=>{const box=button.getBoundingClientRect();return {left:box.left,right:box.right,top:box.top,bottom:box.bottom,width:button.clientWidth,scrollWidth:button.scrollWidth,height:button.clientHeight,scrollHeight:button.scrollHeight}})}})()`);
  assert.equal(layout.rows.length, 3, `${size}: three suggestions`);
  assert(layout.scrollWidth <= layout.width, `${size}: suggestions have no horizontal scrolling`);
  for (const [index, row] of layout.rows.entries()) {
    assert(Math.abs(row.left - layout.rows[0].left) < 1 && Math.abs(row.right - layout.rows[0].right) < 1, `${size}: each suggestion occupies its own full-width row`);
    if (index) assert(row.top >= layout.rows[index - 1].bottom, `${size}: suggestions appear in vertical order`);
    assert(row.scrollWidth <= row.width && row.scrollHeight <= row.height + 1, `${size}: suggestion text fits its button`);
  }
}

let opened = false;
try {
  call("newtab", base);
  opened = true;
  call("viewport", "1280x800");
  await wait(`!!document.querySelector('.continue-panel')`);
  const fixture = js(`fetch('/fixture/control',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'}).then(async r=>({status:r.status,body:await r.json()}))`);
  assert.equal(fixture.status, 200, "fixture route is required before operating this service");
  assert.equal(fixture.body.ok, true, "fixture control must be recognized");
  const worlds = js(`fetch('/api/v1/worlds').then(r=>r.json()).then(value=>value.worlds)`);
  const world = worlds.find(value => value.name === "雨夜的第一晚" && value.message_head >= 100);
  assert(world, "the disposable fixture must provide the seeded reading world");
  const status = js(`fetch('/api/v1/status').then(r=>r.json()).then(value=>value.status)`);
  if (status.active_world.world_id !== world.world_id) {
    const activated = js(`fetch('/api/v1/worlds/'+${JSON.stringify(world.world_id)}+'/activate',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({request_key:crypto.randomUUID(),expected_active_revision:${status.active_revision}})}).then(r=>r.status)`);
    assert.equal(activated, 200);
    call("reload");
    await wait(`!!document.querySelector('.continue-panel')`);
  }
  click(".continue-panel button");
  await wait(`document.querySelectorAll('[data-message-id]').length===100`);
  await wait(`(()=>{const box=document.querySelector('.transcript');return box.scrollHeight-box.scrollTop-box.clientHeight<3})()`);
  assert(js(`(()=>{const box=document.querySelector('.transcript');return box.scrollHeight-box.scrollTop-box.clientHeight<3})()`));
  await wait(`!document.querySelector('.suggestion-heading')?.textContent.includes('正在准备')`);
  await wait(`document.querySelectorAll('.suggestion-items button').length===3`);
  for (const size of ["1440x900", "390x844", "1440x600"]) {
    call("viewport", size);
    await pause(300);
    assertSuggestionRows(size);
    // Exercise the permitted text-length boundary without changing fixture data.
    const texts = js(`[...document.querySelectorAll('.suggestion-items button')].map(button=>button.innerHTML)`);
    js(`(()=>{document.querySelectorAll('.suggestion-items button').forEach(button=>button.textContent='观察'.repeat(60));return true})()`);
    assertSuggestionRows(`${size} / 120 characters`);
    await wait(`(()=>{const box=document.querySelector('.transcript');return box.scrollHeight-box.scrollTop-box.clientHeight<3})()`);
    js(`(()=>{document.querySelectorAll('.suggestion-items button').forEach((button,index)=>button.innerHTML=${JSON.stringify(texts)}[index]);return true})()`);
  }
  call("viewport", "1280x800");
  const suggestedText = js(`document.querySelector('.suggestion-items button').textContent.replace(/^1/,'')`);
  const suggestionBaseline = heads(readWorld(world.world_id));
  click('.suggestion-items button:first-child');
  assert.equal(js(`document.querySelector('textarea[aria-label="你的行动"]').value`), suggestedText, "selection fills the editable draft");
  assert.deepEqual(heads(readWorld(world.world_id)), suggestionBaseline, "selection does not execute an action");
  click('textarea[aria-label="你的行动"]');
  call("press", "Control+A");
  call("press", "Backspace");
  console.log("PASS suggestions use three full-width rows at desktop, mobile and short viewports, wrap 120-character text, and fill without submitting");

  const snapshot = readWorld(world.world_id);
  const character = snapshot.characters.find(value => value.in_scene);
  assert(character, "the fixture must provide an in-scene addressee");
  click('.information-entry button:has-text("人物")');
  click(`.modal .character-row:has-text(${JSON.stringify(character.name)})`);
  await wait(`!document.querySelector('[role=dialog]')`);
  const draft = "我低声询问渡口的消息，等对方回答。";
  call("fill", 'textarea[aria-label="你的行动"]', draft);
  js(`(()=>{const box=document.querySelector('.transcript');box.scrollTop=Math.min(3000,box.scrollHeight-box.clientHeight-300);return true})()`);
  await pause(200);
  const before = js(anchor);
  assert(before.id && before.scrollTop > 0, "the anchor must be in scrollable history");
  const baseline = heads(readWorld(world.world_id));
  js(`(()=>{window.wiaReadingOriginalFetch=window.fetch;window.wiaReadingRequests=[];window.fetch=(...args)=>{window.wiaReadingRequests.push({url:String(args[0]),method:args[1]?.method??'GET'});return window.wiaReadingOriginalFetch(...args);};return true})()`);

  const columns = ["角色", "背包", "地图", "人物"];
  click('.information-entry button:has-text("角色")');
  for (const column of columns) {
    click(`.information-tabs button:has-text(${JSON.stringify(column)})`);
    await wait(`document.querySelector('.information-content')?.getAttribute('aria-label')===${JSON.stringify(column)}`);
    assert.equal(js(`document.querySelectorAll('[role=dialog]').length`), 1, "one shared information surface");
    assertSession(draft, character.entity_id, before, column);
    if (column === "角色") assert(js(`document.querySelector('.information-content').textContent.includes(${JSON.stringify(snapshot.player_name)})`));
    if (column === "背包") {
      const actual = js(`[...document.querySelectorAll('.information-content .item-list > li > strong')].map(item=>item.textContent)`);
      const expected = (snapshot.items ?? []).filter(item => item.holder_id === "player").map(item => item.name);
      assert.deepEqual(actual.sort(), expected.sort(), "inventory matches current player ownership");
      const amounts = js(`[...document.querySelectorAll('.information-content .balance-list dd')].map(item=>item.textContent)`);
      const expectedAmounts = (snapshot.states ?? []).filter(state => state.entity_id === "player" && state.currency).map(state => state.display_value);
      assert.deepEqual(amounts, expectedAmounts, "cash matches the server projection");
    }
    if (column === "地图") assert(js(`document.querySelector('.information-content').textContent.includes(${JSON.stringify(snapshot.world.location?.name ?? snapshot.world.scene)})`));
    if (column === "人物") assert(js(`document.querySelector('.information-content').textContent.includes(${JSON.stringify(character.name)})`));
  }
  call("press", "Escape");
  await wait(`!document.querySelector('[role=dialog]')`);
  assert(js(`document.activeElement===document.querySelector('#wia-information-character')`), "Escape returns focus to the information entry");
  assertSession(draft, character.entity_id, before, "closed");
  assert.deepEqual(heads(readWorld(world.world_id)), baseline, "viewing information preserves date and committed heads");
  assert.deepEqual(js(`window.wiaReadingRequests.filter(request=>request.method!=='GET')`), [], "viewing information causes no model request or mutation");
  console.log("PASS shared read-only columns preserve the draft, addressee, history anchor, focus and world time");

  for (const size of ["390x844", "1440x600"]) {
    call("viewport", size);
    await pause(500);
    assertLayout(size);
    assert(js(`document.querySelector('select[aria-label="交谈对象"]').getBoundingClientRect().height>0`), `${size}: visible explicit addressee`);
    const resizedAnchor = js(anchor);
    click('.information-entry button:has-text("背包")');
    await wait(`!!document.querySelector('[role=dialog]')`);
    assert(js(`(()=>{const panel=document.querySelector('[role=dialog]'),box=panel.getBoundingClientRect();return panel.scrollWidth<=panel.clientWidth&&box.left>=0&&box.right<=innerWidth+1&&box.top>=0&&box.bottom<=innerHeight+1})()`), `${size}: information drawer fits the viewport`);
    call("press", "Escape");
    await wait(`!document.querySelector('[role=dialog]')`);
    assertSession(draft, character.entity_id, resizedAnchor, `${size} return`);
    assertLayout(size);
  }
  assert.deepEqual(heads(readWorld(world.world_id)), baseline, "responsive viewing preserves the same committed world");
  assert.deepEqual(js(`window.wiaReadingRequests.filter(request=>request.method!=='GET')`), [], "responsive viewing causes no mutation");
  console.log("PASS 390x844 and 1440x600 fit the page and retain an enabled, reachable submit control (simulated viewports)");
  console.log("Browser reading checks passed");
} finally {
  if (opened) {
    try { js(`(()=>{if(window.wiaReadingOriginalFetch)window.fetch=window.wiaReadingOriginalFetch;return true})()`); } finally { call("closetab"); }
  }
}
