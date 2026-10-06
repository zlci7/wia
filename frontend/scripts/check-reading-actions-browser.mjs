import { execFileSync } from "node:child_process";
import assert from "node:assert/strict";

const browser = process.env.WIA_BROWSE_BIN, base = process.env.WIA_FIXTURE_URL;
if (!browser || !/^http:\/\/127\.0\.0\.1:\d+$/.test(base ?? ""))
  throw new Error("Use WIA_BROWSE_BIN and a disposable TestBrowserFixture URL");
const call = (...args) => execFileSync(browser, args, { encoding: "utf8", timeout: 20000 }).trim();
const js = expression => JSON.parse(call("js", `(async()=>JSON.stringify(await (${expression})))()`));
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
async function wait(expression) {
  const deadline = Date.now() + 24000;
  while (Date.now() < deadline) {
    if (js(expression)) return;
    await pause(250);
  }
  throw new Error(`Timed out: ${expression}`);
}
const read = id => js(`fetch('/api/v1/worlds/'+${JSON.stringify(id)}).then(r=>r.json())`);
const anchor = `(()=>{const box=document.querySelector('.transcript'),top=box.getBoundingClientRect().top,item=[...box.querySelectorAll('[data-message-id]')].find(item=>item.getBoundingClientRect().bottom>top);return {id:item?.dataset.messageId,offset:item?.getBoundingClientRect().top-top}})()`;
let opened = false;
try {
  call("newtab", base); opened = true;
  call("viewport", "1440x900");
  await wait(`!!document.querySelector('.continue-panel')`);
  assert.equal(js(`fetch('/fixture/control',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'}).then(r=>r.json()).then(v=>v.ok)`), true);
  const worlds = js(`fetch('/api/v1/worlds').then(r=>r.json()).then(v=>v.worlds)`);
  const world = worlds.find(w => w.name === "雨夜的第一晚" && w.message_head >= 251);
  assert(world, "seeded disposable history is required");
  const status = js(`fetch('/api/v1/status').then(r=>r.json()).then(v=>v.status)`);
  if (status.active_world.world_id !== world.world_id) {
    assert.equal(js(`fetch('/api/v1/worlds/'+${JSON.stringify(world.world_id)}+'/activate',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({request_key:crypto.randomUUID(),expected_active_revision:${status.active_revision}})}).then(r=>r.status)`), 200);
    call("reload"); await wait(`!!document.querySelector('.continue-panel')`);
  }
  call("click", ".continue-panel button");
  await wait(`document.querySelectorAll('[data-message-id]').length===100`);
  js(`(()=>{document.querySelector('.transcript').scrollTop=0;return true})()`);
  await pause(250);
  const before = js(anchor);
  call("click", '.history-control button');
  await wait(`document.querySelectorAll('[data-message-id]').length===200`);
  const after = js(`(()=>{const box=document.querySelector('.transcript'),item=box.querySelector('[data-message-id="'+${JSON.stringify(before.id)}+'"]');return {id:item?.dataset.messageId,offset:item?.getBoundingClientRect().top-box.getBoundingClientRect().top}})()`);
  assert.equal(after.id, before.id, "history insertion preserves the existing paragraph");
  assert(Math.abs(after.offset-before.offset)<3, "history insertion preserves the paragraph offset");

  const append = js(`fetch('/fixture/control',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({append:2,world_id:${JSON.stringify(world.world_id)}})}).then(r=>r.json())`);
  assert.equal(append.ok, true);
  await wait(`document.querySelector('.latest-row button')?.textContent.includes('有新内容')`);
  assert(Math.abs(js(`(()=>{const box=document.querySelector('.transcript'),item=box.querySelector('[data-message-id="'+${JSON.stringify(before.id)}+'"]');return item.getBoundingClientRect().top-box.getBoundingClientRect().top})()`)-after.offset)<3, "new content does not pull the reader out of history");
  call("click", ".latest-row button");
  await wait(`(()=>{const box=document.querySelector('.transcript');return box.scrollHeight-box.scrollTop-box.clientHeight<3})()`);
  console.log("PASS real history paging and unseen-content recovery preserve the paragraph anchor");

  const cancelledInput = "我向老板询问渡口今晚的情况。";
  call("fill", 'textarea[aria-label="你的行动"]', cancelledInput);
  call("press", "Control+Enter");
  await wait(`!!document.querySelector('.run-card[role=status]')`);
  call("click", '.run-card button:has-text("取消")');
  await wait(`!!document.querySelector('.failed-card') && document.querySelector('textarea[aria-label="你的行动"]').value===${JSON.stringify(cancelledInput)}`);
  assert.equal(read(world.world_id).world.message_head, world.message_head+2, "cancelled generation does not commit another turn");
  call("click", '.failed-card button:has-text("重试本轮")');
  await wait(`!document.querySelector('.run-card') && document.querySelector('.save-status')?.textContent.includes('本轮已保存')`);
  assert.equal(read(world.world_id).world.message_head, world.message_head+4, "retry publishes one player and one narrative message");
  console.log("PASS keyboard submit, cancellation, retained input and retry use the existing run flow");

  js(`(()=>{window.wiaActionFetch=window.fetch;window.wiaActionPosts=[];window.wiaLostAction=false;window.fetch=async(url,init={})=>{const path=String(url);if(window.wiaLostAction && path.includes('/runs?request_key='))await new Promise(r=>setTimeout(r,1200));const response=await window.wiaActionFetch(url,init);if(init.method==='POST' && path.endsWith('/runs')){window.wiaActionPosts.push(JSON.parse(init.body));if(!window.wiaLostAction && response.ok){window.wiaLostAction=true;throw new TypeError('Controlled response loss after acceptance');}}return response};return true})()`);
  const pendingInput = "我继续核对老板刚才说的消息。";
  call("fill", 'textarea[aria-label="你的行动"]', pendingInput);
  call("click", '.composer button[type=submit]');
  await wait(`window.wiaLostAction && document.querySelector('.save-status')?.textContent.includes('提交结果待确认')`);
  assert.equal(js(`document.querySelector('textarea[aria-label="你的行动"]').value`), pendingInput, "unknown submission retains the original input");
  assert(js(`document.querySelector('.composer button[type=submit]').disabled`), "unknown submission blocks a new action");
  await wait(`document.querySelector('.save-status')?.textContent.includes('本轮已保存') && !document.querySelector('.run-card')`);
  assert.equal(js(`window.wiaActionPosts.length`), 1, "accepted response loss is reconciled without another POST");
  assert.equal(read(world.world_id).world.message_head, world.message_head+6, "recovery commits one turn");
  console.log("PASS accepted response loss preserves input and reconciles one request without replay");
} finally {
  if (opened) {
    try { js(`(()=>{if(window.wiaActionFetch)window.fetch=window.wiaActionFetch;return true})()`); }
    finally { call("closetab"); }
  }
}
