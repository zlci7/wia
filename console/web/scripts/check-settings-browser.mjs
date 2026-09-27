import { execFileSync } from "node:child_process";
import assert from "node:assert/strict";

const browser = process.env.WIA_BROWSE_BIN,
  base = process.env.WIA_FIXTURE_URL;
if (!browser || !/^http:\/\/127\.0\.0\.1:\d+$/.test(base ?? ""))
  throw new Error("Use a disposable TestBrowserFixture");
const call = (...args) =>
  execFileSync(browser, args, { encoding: "utf8", timeout: 20000 }).trim();
const js = (expression) =>
  JSON.parse(call("js", `(async()=>JSON.stringify(await (${expression})))()`));
const click = (selector) => call("click", selector);
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
async function wait(expression) {
  for (let i = 0; i < 60; i++) {
    if (js(expression)) return;
    await pause(250);
  }
  throw new Error(`Timed out: ${expression}`);
}
let opened = false;
try {
  call("newtab", base);
  opened = true;
  call("viewport", "1280x800");
  await wait(`!!document.querySelector('.continue-panel')`);
  click(".continue-panel button");
  await wait(`!!document.querySelector('.reading-heading')`);
  const id = js(
    `fetch('/api/v1/status').then(r=>r.json()).then(v=>v.status.active_world.world_id)`,
  );
  const url = `/api/v1/worlds/${id}/agent-settings`;
  const mutate = (length) => {
    const w = js(`fetch('/api/v1/worlds/${id}').then(r=>r.json())`);
    const body = JSON.stringify({
      ...w.narrative_settings,
      length,
      expected_context_epoch: w.world.context_epoch,
    });
    return js(
      `fetch('${url}',{method:'PUT',headers:{'Content-Type':'application/json'},body:${JSON.stringify(body)}}).then(r=>r.status)`,
    );
  };
  click('button:has-text("故事设置")');
  click('.modal .setting-options button:has(strong:text-is("简短"))');
  assert.equal(mutate("detailed"), 200);
  await pause(1900);
  click('.modal button:has-text("保存设置")');
  await wait(
    `document.querySelector('.modal .inline-error')?.textContent.includes('重新打开')`,
  );
  assert.equal(
    js(
      `fetch('/api/v1/worlds/${id}').then(r=>r.json()).then(v=>v.narrative_settings.length)`,
    ),
    "detailed",
  );
  call("press", "Escape");
  console.log(
    "PASS real API rejects old draft after another client updates same world",
  );

  click('button:has-text("故事设置")');
  js(
    `(()=>{window.originalFetch=window.fetch;window.held=false;window.fetch=async(...args)=>{const r=await window.originalFetch(...args);if(args[0]==='${url}'&&args[1]?.method==='PUT'){window.held=true;await new Promise(resolve=>window.releaseSettings=resolve);}return r;};return true})()`,
  );
  click('.modal .setting-options button:has(strong:text-is("简短"))');
  click('.modal button:has-text("保存设置")');
  await wait("window.held");
  // The write has committed but its client response is held. Another client advances the epoch.
  js(`(()=>{window.fetch=window.originalFetch;return true})()`);
  assert.equal(mutate("detailed"), 200);
  await pause(1900);
  call("press", "Escape");
  assert(js(`!!document.querySelector('[role=dialog]')`));
  js(`(()=>{window.releaseSettings();return true})()`);
  await wait(`!document.querySelector('[role=dialog]')`);
  click('button:has-text("故事设置")');
  assert(
    js(
      `document.querySelector('.modal .setting-options:nth-of-type(2) .selected')?.textContent.includes('细致')`,
    ),
  );
  call("press", "Escape");
  console.log(
    "PASS delayed committed response retains newer settings and modal write lock",
  );

  click('button:has-text("故事设置")');
  const other = js(
    `fetch('/api/v1/worlds').then(r=>r.json()).then(v=>v.worlds.find(w=>w.world_id!=='${id}').world_id)`,
  );
  const revision = js(
    `fetch('/api/v1/status').then(r=>r.json()).then(v=>v.status.active_revision)`,
  );
  assert.equal(
    js(
      `fetch('/api/v1/worlds/${other}/activate',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({request_key:crypto.randomUUID(),expected_active_revision:${revision}})}).then(r=>r.status)`,
    ),
    200,
  );
  await wait(
    `!document.querySelector('[role=dialog]')&&document.querySelector('.reading-heading h1')?.textContent==='另一段旅程'`,
  );
  console.log("PASS active world change invalidates settings editor");
  console.log("Browser settings checks passed");
} finally {
  if (opened) call("closetab");
}
