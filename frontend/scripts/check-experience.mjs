import { execFileSync } from "node:child_process";
import assert from "node:assert/strict";

// Run against TestBrowserFixture, never against a personal data root.
const browser = process.env.WIA_BROWSE_BIN;
const base = process.env.WIA_FIXTURE_URL;
if (!browser || !base || !/^http:\/\/127\.0\.0\.1:\d+$/.test(base))
  throw new Error(
    "Set WIA_BROWSE_BIN and WIA_FIXTURE_URL to the disposable browser fixture",
  );
const call = (...args) =>
  execFileSync(browser, args, { encoding: "utf8", timeout: 20000 }).trim();
const js = (expression) =>
  JSON.parse(
    call(
      "js",
      `Promise.resolve(${expression}).then(value=>JSON.stringify(value))`,
    ),
  );
const click = (selector) => call("click", selector);
const fill = (selector, value) => call("fill", selector, value);
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
async function wait(expression) {
  const deadline = Date.now() + 18000;
  while (Date.now() < deadline) {
    if (js(expression)) return;
    await pause(250);
  }
  throw new Error(`Timed out: ${expression}`);
}
async function control(value) {
  return js(
    `fetch('/fixture/control',{method:'POST',headers:{'Content-Type':'application/json'},body:${JSON.stringify(JSON.stringify(value))}}).then(r=>r.json())`,
  );
}
const anchor = `(()=>{const box=document.querySelector('.transcript'),top=box.getBoundingClientRect().top;const e=[...box.querySelectorAll('[data-message-id]')].find(e=>e.getBoundingClientRect().bottom>top);return {id:e?.dataset.messageId,offset:e?.getBoundingClientRect().top-top}})()`;
const offset = (id) =>
  js(
    `document.querySelector('[data-message-id="${id}"]').getBoundingClientRect().top-document.querySelector('.transcript').getBoundingClientRect().top`,
  );
let opened = false;
try {
  call("newtab", base);
  opened = true;
  call("viewport", "1280x800");
  await wait(`!!document.querySelector('.continue-panel')`);
  click(".continue-panel button");
  await wait(`document.querySelectorAll('[data-message-id]').length===100`);
  const id = js(
    `fetch('/api/v1/status').then(r=>r.json()).then(v=>v.status.active_world.world_id)`,
  );
  assert(
    js(
      `(()=>{const e=document.querySelector('.transcript');return e.scrollHeight-e.scrollTop-e.clientHeight<3})()`,
    ),
  );
  assert(
    js(
      `document.querySelector('.composer').getBoundingClientRect().bottom<=innerHeight`,
    ),
  );
  js(`document.querySelector('.transcript').scrollTop=0`);
  await pause(100);
  await control({ fail_path: `GET /api/v1/worlds/${id}/messages` });
  click(".history-control button");
  await wait(`!!document.querySelector('.transcript .inline-error')`);
  assert.equal(
    js(`document.querySelectorAll('[data-message-id]').length`),
    100,
  );
  click('.inline-error button:has-text("重试历史")');
  await wait(`document.querySelectorAll('[data-message-id]').length===200`);
  js(`document.querySelector('.transcript').scrollTop=0`);
  await pause(100);
  const before = js(anchor);
  click(".history-control button");
  await wait(`document.querySelectorAll('[data-message-id]').length===251`);
  assert(Math.abs(offset(before.id) - before.offset) < 3);
  console.log(
    "PASS history pages retain visible message anchors and retry failed reads",
  );

  click('.character-row:has-text("沈岚")');
  fill('textarea[aria-label="你的行动"]', "留在 A 的草稿");
  js(`document.querySelector('.transcript').scrollTop=3000`);
  await pause(100);
  click('button:has-text("存档与故事")');
  click('.save-open:has-text("另一段旅程")');
  await wait(
    `document.querySelector('.reading-heading h1')?.textContent==='另一段旅程'`,
  );
  assert.equal(js(`document.querySelector('textarea').value`), "");
  assert.equal(js(`document.querySelector('select').value`), "");
  fill('textarea[aria-label="你的行动"]', "留在 B 的草稿");
  click('button:has-text("存档与故事")');
  click('.save-open:has-text("雨夜的第一晚")');
  await wait(
    `document.querySelector('.reading-heading h1')?.textContent==='雨夜的第一晚'`,
  );
  assert.equal(js(`document.querySelector('textarea').value`), "留在 A 的草稿");
  assert.equal(js(`document.querySelector('select').value`), "npc:innkeeper");
  assert(
    Math.abs(js(`document.querySelector('.transcript').scrollTop`) - 3000) < 3,
  );
  console.log(
    "PASS world switching preserves isolated drafts, recipients and positions",
  );

  const oldAnchor = js(anchor);
  await control({ append: 230, world_id: id });
  await wait(`document.querySelectorAll('[data-message-id]').length===481`);
  assert.equal(js(anchor).id, oldAnchor.id);
  assert(
    js(
      `document.querySelector('.latest-row').textContent.includes('有新内容')`,
    ),
  );
  click(".latest-row button");
  await wait(`!document.querySelector('.latest-row')`);
  console.log(
    "PASS multi-page forward catch-up and non-disruptive new-content notice",
  );

  click('button:has-text("故事设置")');
  assert.equal(js(`document.querySelector('.modal details').open`), false);
  call("press", "Shift+Tab");
  assert(js(`document.activeElement.textContent.includes('保存设置')`));
  call("press", "Tab");
  assert.equal(js(`document.activeElement.getAttribute('aria-label')`), "关闭");
  await control({ fail_path: `PUT /api/v1/worlds/${id}/agent-settings` });
  click('.modal button:has-text("保存设置")');
  await wait(`!!document.querySelector('.modal .inline-error')`);
  assert(
    js(
      `document.querySelector('.modal .inline-error').textContent.includes('测试连接失败')`,
    ),
  );
  click('.modal button:has-text("保存设置")');
  await wait(`!document.querySelector('[role=dialog]')`);
  console.log(
    "PASS settings progressive disclosure, focus trap, local error and retry",
  );

  fill('textarea[aria-label="你的行动"]', "跟老板打招呼");
  js(
    `document.querySelector('textarea').dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',ctrlKey:true,isComposing:true,bubbles:true}))`,
  );
  assert.equal(js(`document.querySelector('textarea').value`), "跟老板打招呼");
  await control({
    fail_path: `POST /api/v1/worlds/${id}/runs`,
    delay_path: `POST /api/v1/worlds/${id}/runs`,
  });
  call("press", "Control+Enter");
  await wait(
    `document.querySelector('.composer button[type=submit]').disabled`,
  );
  call("press", "Control+Enter");
  await wait(`!!document.querySelector('.composer .inline-error')`);
  assert.equal(js(`document.querySelector('textarea').value`), "跟老板打招呼");
  click('.composer .inline-error button:has-text("确认或重发原请求")');
  await wait(`!!document.querySelector('.run-card')`);
  js(`document.querySelector('.transcript').scrollTop=2000`);
  await wait(
    `document.querySelector('.save-status').textContent==='本轮已保存'`,
  );
  assert(
    Math.abs(js(`document.querySelector('.transcript').scrollTop`) - 2000) < 3,
  );
  console.log(
    "PASS IME guard, pending-submit guard, retained failed input and saved-state evidence",
  );

  fill('textarea[aria-label="你的行动"]', "属于 A 的迟到提交");
  await control({
    fail_path: `POST /api/v1/worlds/${id}/runs`,
    delay_path: `POST /api/v1/worlds/${id}/runs`,
    delay_ms: 6000,
  });
  click(".composer button[type=submit]");
  click('button:has-text("存档与故事")');
  click('.save-open:has-text("另一段旅程")');
  await wait(
    `document.querySelector('.reading-heading h1')?.textContent==='另一段旅程'`,
  );
  await pause(6500);
  assert.equal(js(`document.querySelector('textarea').value`), "留在 B 的草稿");
  assert.equal(
    js(`!!document.querySelector('.composer .inline-error')`),
    false,
  );
  assert.equal(
    js(`document.querySelectorAll('[data-message-id]').length`),
    100,
  );
  click('button:has-text("存档与故事")');
  click('.save-open:has-text("雨夜的第一晚")');
  await wait(
    `document.querySelector('.reading-heading h1')?.textContent==='雨夜的第一晚'`,
  );
  assert.equal(
    js(`document.querySelector('textarea').value`),
    "属于 A 的迟到提交",
  );
  assert(js(`!!document.querySelector('.composer .inline-error')`));
  console.log(
    "PASS delayed submission responses remain scoped to their original world",
  );

  for (const size of ["768x1024", "390x844", "390x420"]) {
    call("viewport", size);
    await pause(500);
    const layout = js(`(()=>{const box=document.querySelector('.composer').getBoundingClientRect();return {bottom:box.bottom,top:box.top,height:box.height,viewport:innerHeight,scrollY}})()`);
    assert(js(`document.body.scrollWidth<=innerWidth`));
    assert(
      js(`document.querySelector('.topbar').getBoundingClientRect().top===0`),
    );
    assert(layout.bottom <= layout.viewport + 1, `${size}: ${JSON.stringify(layout)}`);
    assert(js(`document.querySelector('.transcript').clientHeight>0`));
  }
  call("viewport", "390x844");
  click('button:has-text("场景与人物")');
  click('.modal .character-row:has-text("铁杉")');
  assert.equal(js(`document.querySelector('select').value`), "npc:mercenary");
  assert.equal(
    js(`document.activeElement.getAttribute('aria-label')`),
    "你的行动",
  );
  click('button:has-text("返回首页")');
  click('.story-card:has-text("雾都余烬") button');
  await wait(
    `document.querySelector('.story-detail h1')?.textContent==='雾都余烬'`,
  );
  click('button:has-text("继续最近进度")');
  await wait(
    `document.querySelector('.reading-heading h1')?.textContent==='雾都机制验收'`,
  );
  click('button:has-text("场景与人物")');
  assert(
    js(`document.querySelector('[role=dialog]').textContent.includes('调查事务所')`),
  );
  assert(
    js(`document.querySelector('[role=dialog]').textContent.includes('街角咖啡馆')`),
  );
  assert(
    js(`document.querySelector('[role=dialog]').textContent.includes('疲劳')`),
  );
  assert(
    js(`document.querySelector('[role=dialog]').textContent.includes('裂纹银镜')`),
  );
  assert(
    js(`document.querySelector('[role=dialog]').textContent.includes('由裁缝店老板持有')`),
  );
  assert(
    js(`document.querySelector('[role=dialog]').scrollWidth<=document.querySelector('[role=dialog]').clientWidth`),
  );
  click('button[aria-label="关闭"]');
  click('button:has-text("返回首页")');
  click('.story-card:has-text("暮灯镇的失踪信使") button');
  await wait(
    `document.querySelector('.story-detail h1')?.textContent==='暮灯镇的失踪信使'`,
  );
  click('button:has-text("继续最近进度")');
  await wait(
    `document.querySelector('.reading-heading h1')?.textContent==='雨夜的第一晚'`,
  );
  click('button:has-text("更多")');
  click('.menu-panel button:has-text("故事设置")');
  call("press", "Escape");
  assert(js(`!document.querySelector('[role=dialog]')`));
  console.log(
    "PASS responsive layout, scene drawer and keyboard dismissal (simulated viewports)",
  );

  call("viewport", "1280x800");
  click('button:has-text("存档与故事")');
  click('.modal button:has-text("另存当前进度")');
  fill(".modal input", "验收分支");
  await control({
    fail_path: `POST /api/v1/worlds/${id}/save-as`,
    delay_path: `POST /api/v1/worlds/${id}/save-as`,
  });
  click('.modal button:has-text("创建独立存档")');
  call("press", "Escape");
  assert(js(`!!document.querySelector('[role=dialog]')`));
  await wait(`!!document.querySelector('.modal .inline-error')`);
  assert.equal(js(`document.querySelector('.modal input').value`), "验收分支");
  click('.modal button:has-text("继续确认另存")');
  await wait(
    `document.querySelector('.modal .success-note')?.textContent.includes('验收分支')`,
  );
  assert.equal(
    js(`document.querySelector('.reading-heading h1').textContent`),
    "雨夜的第一晚",
  );
  click('button[aria-label="删除存档 验收分支"]');
  js(
    `document.querySelector('.modal-backdrop').dispatchEvent(new MouseEvent('click',{bubbles:true}))`,
  );
  assert(
    js(
      `document.querySelector('[role=dialog]').getAttribute('aria-label')==='删除存档'`,
    ),
  );
  const copyID = js(
    `fetch('/api/v1/worlds').then(r=>r.json()).then(v=>v.worlds.find(w=>w.name==='验收分支').world_id)`,
  );
  await control({ fail_path: `DELETE /api/v1/worlds/${copyID}` });
  click('.modal button:has-text("确认删除")');
  await wait(`!!document.querySelector('.modal .inline-error')`);
  click('.modal button:has-text("确认删除")');
  await wait(`!document.querySelector('[role=dialog]')`);
  console.log(
    "PASS save-as retains source and delete requires explicit action",
  );

  await control({ ready: false });
  call("reload");
  await wait(`!!document.querySelector('.story-card')`);
  click('.story-card:has-text("暮灯镇的失踪信使") button');
  click('button:has-text("开始新的故事")');
  assert(js(`document.querySelector('.modal input').value.length>10`));
  fill('.modal label:has-text("主角名字") input', "测试旅人");
  click('.modal button:has-text("连接模型并开始")');
  assert.equal(js(`document.querySelector('.modal details').open`), false);
  await control({ fail_path: "POST /api/v1/model-profiles" });
  fill(".modal input[type=password]", "fixture-key");
  click('.modal button:has-text("验证并保存")');
  await wait(`!!document.querySelector('.modal .inline-error')`);
  click('.modal button:has-text("验证并保存")');
  await wait(
    `document.querySelector('[role=dialog]')?.getAttribute('aria-label')==='确认你的主角'`,
  );
  assert.equal(
    js(
      `document.querySelector('.modal label:has(input)').textContent.includes('存档名称')`,
    ),
    true,
  );
  assert.equal(
    js(`document.querySelectorAll('.modal input')[1].value`),
    "测试旅人",
  );
  await control({ fail_path: "POST /api/v1/worlds" });
  click('.modal button:has-text("开始游玩")');
  await wait(`!!document.querySelector('.modal .inline-error')`);
  assert.equal(
    js(`document.querySelectorAll('.modal input')[1].value`),
    "测试旅人",
  );
  click('.modal button:has-text("继续确认开局")');
  await wait(
    `!!document.querySelector('.transcript') && !document.querySelector('[role=dialog]')`,
  );
  assert.equal(js(`document.querySelectorAll('[data-message-id]').length`), 1);
  console.log(
    "PASS unconfigured browsing, deferred connection, form preservation and default-named start",
  );

  js(
    `fetch('/api/v1/status').then(r=>r.json()).then(v=>fetch('/api/v1/worlds/'+v.status.active_world.world_id+'?expected_active_revision='+v.status.active_revision,{method:'DELETE'})).then(r=>r.ok)`,
  );
  await wait(
    `!document.querySelector('.transcript') && document.querySelector('.status-banner')?.textContent.includes('已被删除')`,
  );
  console.log(
    "PASS externally deleted active worlds exit instead of showing empty history",
  );
  console.log("Browser experience checks passed");
} finally {
  if (opened) call("closetab");
}
