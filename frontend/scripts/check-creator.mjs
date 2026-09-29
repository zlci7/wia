import assert from "node:assert/strict";
import { build } from "esbuild";

// Bundle the real composable and exercise its HTTP boundary with a stubbed fetch.
const bundle = await build({
  stdin: {
    contents: `export { useCreator, AUTOSAVE_DEBOUNCE_MS } from './src/useCreator';`,
    resolveDir: process.cwd(),
    loader: "ts",
  },
  bundle: true,
  write: false,
  platform: "node",
  format: "esm",
});
const { useCreator, AUTOSAVE_DEBOUNCE_MS } = await import(
  `data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString("base64")}`
);

const realSetTimeout = globalThis.setTimeout;
const delay = (ms) => new Promise((resolve) => realSetTimeout(resolve, ms));
const response = (body, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

let calls = [];
let hook;
const draft = (overrides = {}) => ({
  draft_id: "draft_1",
  project_id: "project_1",
  base_revision: "",
  version: 1,
  status: "editing",
  updated_at: "now",
  payload: {
    schema_version: 2, game_id: "harbor", mode: "open", title: "港口", description: "", gameplay: "",
    background: "", rules: "", author_facts: "", player: { name: "旅人", profile: "", editable: true },
    opening: "", initial_location: "", clock: "第 1 日 19:00", locations: [], npcs: [], bystanders: [],
  },
  ...overrides,
});

globalThis.fetch = async (url, init = {}) => {
  const method = init.method ?? "GET";
  calls.push(`${method} ${url}`);
  if (hook) {
    const value = hook(method, url, init);
    if (value !== undefined) return value;
  }
  if (url === "/api/v1/content/projects") return response({ projects: [{ project_id: "project_1", game_id: "harbor", title: "港口", current_revision: "", version: 1, created_at: "now", updated_at: "now" }] });
  if (url === "/api/v1/personas") return response({ personas: [] });
  if (url === "/api/v1/content/projects/project_1") return response({ project: { project_id: "project_1", game_id: "harbor", title: "港口", current_revision: "", version: 1, created_at: "now", updated_at: "now" }, drafts: [draft()] });
  if (method === "GET" && url === "/api/v1/content/drafts/draft_1") return response({ draft: draft() });
  if (method === "GET" && url === "/api/v1/content/drafts/draft_1/assets") return response({ assets: [] });
  if (method === "PUT" && url === "/api/v1/content/drafts/draft_1") {
    const body = JSON.parse(init.body);
    return response({ draft: draft({ version: body.expected_version + 1, payload: body.payload }) });
  }
  if (url === "/api/v1/content/drafts/draft_1/preview" || url === "/api/v1/content/drafts/draft_1/preview?view=author") {    return response({ preview: { view: url.includes("author") ? "author" : "player", draft_id: "draft_1", version: 2, title: "港口", mode: "open", description: "", gameplay: "", background: "", opening: "", clock: "", initial_location: "", player: { name: "旅人", profile: "" }, characters: [], bystanders: [], author_facts: url.includes("author") ? "作者事实" : undefined } });
  }
  return response({ error: { code: "not_found", message: `unexpected ${method} ${url}` } }, 404);
};

const tests = {
  async "catalog and draft load through the workspace routes"(c) {
    await c.loadCatalog();
    assert.equal(c.projects.value.length, 1);
    await c.openProject("project_1");
    assert.equal(c.drafts.value.length, 1);
    await c.openDraft("draft_1");
    assert.equal(c.draft.value.version, 1);
    assert.equal(c.form.title, "港口");
    assert.ok(calls.includes("GET /api/v1/content/projects"));
  },

  async "edits autosave once after the debounce window"(c) {
    await c.openProject("project_1");
    await c.openDraft("draft_1");
    const before = calls.filter((line) => line.startsWith("PUT")).length;
    c.form.title = "港口 · 修订";
    c.touch();
    c.form.background = "潮水退去。";
    c.touch();
    assert.equal(calls.filter((line) => line.startsWith("PUT")).length, before, "must not save before the debounce");
    await delay(AUTOSAVE_DEBOUNCE_MS + 250);
    const saved = calls.filter((line) => line.startsWith("PUT"));
    assert.equal(saved.length, before + 1, "one save per burst");
    assert.equal(c.draft.value.version, 2);
    assert.equal(c.conflict.value, false);
  },

  async "a stale version keeps the local edit and offers recovery"(c) {
    await c.openProject("project_1");
    await c.openDraft("draft_1");
    hook = (method, url) => {
      if (method === "PUT" && url.endsWith("/drafts/draft_1")) {
        return response({ error: { code: "version_conflict", message: "version conflict" } }, 409);
      }
    };
    c.form.title = "本地未保存的标题";
    c.touch();
    await delay(AUTOSAVE_DEBOUNCE_MS + 250);
    assert.equal(c.conflict.value, true, "conflict is reported");
    assert.equal(c.form.title, "本地未保存的标题", "local edit survives a conflict");
    const staleSaves = calls.filter((line) => line.startsWith("PUT")).length;
    await delay(AUTOSAVE_DEBOUNCE_MS + 250);
    assert.equal(
      calls.filter((line) => line.startsWith("PUT")).length,
      staleSaves,
      "a conflicted draft is not saved again automatically"
    );
    hook = undefined;
    await c.reloadDraft();
    assert.equal(c.conflict.value, false);
    assert.equal(c.form.title, "港口", "reload adopts the stored draft");
  },

  async "a late response cannot overwrite a newer draft session"(c) {
    await c.openProject("project_1");
    await c.openDraft("draft_1");
    hook = (method, url) => {
      if (method === "PUT" && url.endsWith("/drafts/draft_1")) {
        return delay(400).then(() => response({ draft: draft({ version: 9, payload: { ...draft().payload, title: "过期的服务端标题" } }) }));
      }
    };
    c.form.title = "第一份编辑";
    c.touch();
    await delay(AUTOSAVE_DEBOUNCE_MS + 50);
    // Switch to another draft while the save is still in flight.
    hook = (method, url) => {
      if (method === "GET" && url === "/api/v1/content/drafts/draft_2") {
        return response({ draft: draft({ draft_id: "draft_2", version: 1, payload: { ...draft().payload, title: "另一个草稿" } }) });
      }
    };
    await c.openDraft("draft_2");
    await delay(500);
    hook = undefined;
    assert.equal(c.form.title, "另一个草稿", "the late save must not rewrite a newer session");
    assert.equal(c.draft.value.draft_id, "draft_2");
  },

  async "preview flushes first and author view is explicit"(c) {
    await c.openProject("project_1");
    await c.openDraft("draft_1");
    const before = calls.filter((line) => line.startsWith("PUT")).length;
    c.form.title = "预览前保存";
    c.touch();
    await c.loadPreview("player");
    assert.equal(c.preview.value.view, "player");
    assert.equal(c.preview.value.author_facts, undefined, "player preview carries no author material");
    assert.ok(
      calls.filter((line) => line.startsWith("PUT")).length > before,
      "preview saved pending edits first"
    );
    await c.loadPreview("author");
    assert.equal(c.preview.value.view, "author");
    assert.equal(c.preview.value.author_facts, "作者事实");
  },

  async "new content starts with an adjustable lead"(c) {
    await c.loadCatalog();
    assert.equal(c.form.player.editable, true, "a fresh session defaults to an adjustable lead");
    await c.openProject("project_1");
    assert.equal(c.form.player.editable, true, "opening a project must not lock the lead");
    await c.openDraft("draft_1");
    assert.equal(c.form.player.editable, true);
  },

  async "publishing saves first and reports the operation"(c) {
    await c.openProject("project_1");
    await c.openDraft("draft_1");
    const savesBefore = calls.filter((line) => line.startsWith("PUT")).length;
    let published = 0;
    hook = (method, url, init) => {
      if (method === "POST" && url.endsWith("/publish")) {
        published += 1;
        const body = JSON.parse(init.body);
        return response({ operation: { operation_id: "publish_1", kind: "publish", target_id: "draft_1", stage: "ready", status: "succeeded", expected: body.expected_draft_version } });
      }
      if (method === "GET" && url === "/api/v1/content/projects/project_1") {
        return response({ project: { project_id: "project_1", game_id: "harbor", title: "港口", current_revision: "r-1", version: 2, created_at: "now", updated_at: "now" }, drafts: [draft({ version: 3 })] });
      }
      if (method === "GET" && url === "/api/v1/content/drafts/draft_1") return response({ draft: draft({ version: 3 }) });
    };
    c.form.title = "发布前保存";
    c.touch();
    await c.publish();
    hook = undefined;
    assert.equal(published, 1, "publish runs once");
    assert.ok(calls.filter((line) => line.startsWith("PUT")).length > savesBefore, "publish saved pending edits first");
    assert.equal(c.operation.value.stage, "ready");
    assert.match(c.notice.value, /已发布/);
  },

  async "a failing save blocks publishing"(c) {
    await c.openProject("project_1");
    await c.openDraft("draft_1");
    let published = 0;
    let saveAttempts = 0;
    hook = (method, url) => {
      if (method === "PUT" && url.endsWith("/drafts/draft_1")) {
        saveAttempts += 1;
        return response({ error: { code: "storage_unavailable", message: "外置存储不可用" } }, 503);
      }
      if (method === "POST" && url.endsWith("/publish")) {
        published += 1;
        return response({ operation: { operation_id: "publish_x", stage: "ready", status: "succeeded" } });
      }
    };
    // Autosave will keep retrying; publishing must not accept the older stored version
    // while the local edit has never been confirmed.
    c.form.title = "保存失败也要拦";
    c.touch();
    await c.publish();
    hook = undefined;
    assert.equal(published, 0, "a draft whose save failed is never published");
    assert.ok(saveAttempts >= 1, "the save was actually attempted");
    assert.match(c.error.value, /保存/);
  },

  async "a save still running is awaited before publishing"(c) {    await c.openProject("project_1");
    await c.openDraft("draft_1");
    let published = 0;
    let releaseSave;
    const gate = new Promise((resolve) => {
      releaseSave = resolve;
    });
    hook = (method, url, request) => {
      if (method === "PUT" && url.endsWith("/drafts/draft_1")) {
        return gate.then(() => response({ draft: draft({ version: 4 }) }));
      }
      if (method === "POST" && url.endsWith("/publish")) {
        published += 1;
        const body = JSON.parse(request.body);
        return response({ operation: { operation_id: "publish_y", stage: "ready", status: "succeeded", expected: body.expected_draft_version } });
      }
      if (method === "GET" && url === "/api/v1/content/drafts/draft_1") return response({ draft: draft({ version: 4 }) });
    };
    c.form.title = "保存中也要等";
    c.touch();
    const publishing = c.publish();
    // Let the in-flight save finish only after publishing has started waiting.
    setTimeout(releaseSave, 30);
    await publishing;
    hook = undefined;
    assert.equal(published, 1, "publishing waited for the running save");
    assert.equal(c.operation.value.stage, "ready");
  },

  async "switching drafts does not carry optional fields over"(c) {
    await c.openProject("project_1");
    await c.openDraft("draft_1");
    // Draft A is configured with optional structure the other draft does not have.
    c.form.cover = "assets/cover.png";
    c.form.plot = { nodes: [{ id: "n1", title: "A 的剧情节点" }] };
    c.form.event_generation = { enabled: true };
    assert.equal(c.form.cover, "assets/cover.png");
    hook = (method, url) => {
      if (method === "GET" && url === "/api/v1/content/drafts/draft_2") {
        const withoutOptional = { ...draft().payload, title: "另一份内容" };
        delete withoutOptional.cover;
        delete withoutOptional.plot;
        delete withoutOptional.event_generation;
        return response({ draft: draft({ draft_id: "draft_2", version: 1, payload: withoutOptional }) });
      }
    };
    await c.openDraft("draft_2");
    hook = undefined;
    assert.equal(c.form.title, "另一份内容");
    assert.equal(c.form.cover, undefined, "the previous draft's cover must not follow");
    assert.equal(c.form.plot, undefined, "the previous draft's plot must not follow");
    assert.equal(c.form.event_generation, undefined, "the previous draft's event rules must not follow");
  },

  async "an import stays a preview until it is confirmed"(c) {
    await c.openProject("project_1");
    let confirmed = 0;
    let previewed = 0;
    hook = (method, url) => {
      if (method === "POST" && url === "/api/v1/content/imports/preview") {
        previewed += 1;
        return response({
          import: {
            draft_id: "draft_import", version: 1, project_id: "project_1",
            report: {
              format: "ccv2", format_detail: "Character Card V2", summary: "已映射为一名人物",
              mappings: [{ field: "data.name", source: "card.json", target: "人物姓名", confidence: "high" }],
              unsupported: ["system_prompt 不会被执行"], needs_confirmation: ["情境只作候选"], source_bytes: 128,
            },
          },
        }, 201);
      }
      if (method === "POST" && url === "/api/v1/content/imports/confirm") {
        confirmed += 1;
        const body = JSON.parse(request.body);
        assert.equal(body.draft_id, "draft_import");
        assert.ok(body.request_key, "confirmation carries a request key");
        return response({ draft: draft({ draft_id: "draft_import", version: 2 }) });
      }
      if (method === "GET" && url === "/api/v1/content/projects/project_1") {
        return response({ project: { project_id: "project_1", game_id: "harbor", title: "港口", current_revision: "", version: 1, created_at: "now", updated_at: "now" }, drafts: [draft({ version: 2 })] });
      }
      if (method === "GET" && url === "/api/v1/content/drafts/draft_import") {
        return response({ draft: draft({ draft_id: "draft_import", version: 2 }) });
      }
    };
    const file = new File([JSON.stringify({ spec: "chara_card_v2" })], "card.json", { type: "application/json" });
    await c.importContent(file);
    hook = undefined;
    assert.equal(previewed, 1);
    assert.equal(confirmed, 0, "importing alone must not confirm anything");
    assert.ok(c.importPreview.value, "the report is shown before confirmation");
    assert.equal(c.importPreview.value.report.unsupported.length, 1);

    hook = (method, url) => {
      if (method === "POST" && url === "/api/v1/content/imports/confirm") {
        confirmed += 1;
        return response({ draft: draft({ draft_id: "draft_import", version: 2 }) });
      }
      if (method === "GET" && url.startsWith("/api/v1/content/projects/project_1")) {
        return response({ project: { project_id: "project_1", game_id: "harbor", title: "港口", current_revision: "", version: 1, created_at: "now", updated_at: "now" }, drafts: [draft({ version: 2 })] });
      }
      if (method === "GET" && url === "/api/v1/content/drafts/draft_import") {
        return response({ draft: draft({ draft_id: "draft_import", version: 2 }) });
      }
    };
    await c.confirmImport();
    hook = undefined;
    assert.equal(confirmed, 1, "confirming is what creates the editable draft");
    assert.equal(c.importPreview.value, null);
    assert.equal(c.draftID.value, "draft_import");
  },

  async "publishing twice uses two request identities"(c) {
    await c.openProject("project_1");
    await c.openDraft("draft_1");
    const keys = [];
    let version = 3;
    hook = (method, url, request) => {
      if (method === "POST" && url.endsWith("/publish")) {
        const body = JSON.parse(request.body);
        keys.push(body.request_key);
        const published = { operation_id: "publish_" + keys.length, kind: "publish", target_id: "draft_1", stage: "ready", status: "succeeded" };
        return response({ operation: published });
      }
      if (method === "GET" && url === "/api/v1/content/projects/project_1") {
        return response({ project: { project_id: "project_1", game_id: "harbor", title: "港口", current_revision: "r-1", version: 2, created_at: "now", updated_at: "now" }, drafts: [draft({ version })] });
      }
      if (method === "GET" && url === "/api/v1/content/drafts/draft_1") return response({ draft: draft({ version }) });
    };
    c.form.title = "第一次发布";
    c.touch();
    await c.publish();
    // Publishing reloads the draft, which used to reset the page counter and make the
    // next publish reuse the same identity.
    version = 4;
    c.form.title = "第二次发布";
    c.touch();
    await c.publish();
    hook = undefined;
    assert.equal(keys.length, 2, "two publishes were sent");
    assert.notEqual(keys[0], keys[1], "each publish intent must have its own request key");
    assert.match(keys[0], /^draft_1:publish:/);
    assert.match(keys[1], /^draft_1:publish:/);
  },

  async "an unknown publish outcome keeps its identity for a retry"(c) {
    await c.openProject("project_1");
    await c.openDraft("draft_1");
    const keys = [];
    let attempt = 0;
    hook = (method, url, request) => {
      if (method === "POST" && url.endsWith("/publish")) {
        attempt += 1;
        const body = JSON.parse(request.body);
        keys.push(body.request_key);
        if (attempt === 1) return response({ error: { code: "storage_unavailable", message: "暂时无法完成" } }, 503);
        return response({ operation: { operation_id: "publish_retry", stage: "ready", status: "succeeded" } });
      }
      if (method === "GET" && url === "/api/v1/content/projects/project_1") {
        return response({ project: { project_id: "project_1", game_id: "harbor", title: "港口", current_revision: "r-1", version: 2, created_at: "now", updated_at: "now" }, drafts: [draft({ version: 3 })] });
      }
      if (method === "GET" && url === "/api/v1/content/drafts/draft_1") return response({ draft: draft({ version: 3 }) });
    };
    c.form.title = "重试同一次发布";
    c.touch();
    await c.publish();
    assert.match(c.error.value, /发布失败|暂时/, "the first attempt reported a failure");
    await c.publish();
    hook = undefined;
    assert.equal(keys.length, 2);
    assert.equal(keys[0], keys[1], "a retry of the same intent keeps its request key");
    assert.equal(c.operation.value.stage, "ready");
  },

  async "identifiers the server derives are written back"(c) {
    await c.openProject("project_1");
    await c.openDraft("draft_1");
    let saved = 0;
    hook = (method, url) => {
      if (method === "PUT" && url.endsWith("/drafts/draft_1")) {
        saved += 1;
        // The service derives internal identifiers and returns the normalised payload.
        return response({
          draft: draft({
            version: 4,
            payload: {
              ...draft().payload,
              initial_location: "harbor",
              locations: [{ id: "harbor", name: "港口", connections: [] }],
              npcs: [{ definition_id: "keeper", revision: "v1", entity_id: "npc:keeper", name: "看灯人", role: "港口看灯人", profile: "资料", initial_location: "harbor" }],
              bystanders: [{ bystander_id: "bystander:boatman", name: "船夫", description: "在栈桥等活" }],
            },
          }),
        });
      }
    };
    c.form.locations = [{ id: "", name: "港口", connections: [] }];
    c.form.npcs = [{ definition_id: "", revision: "", entity_id: "", name: "看灯人", role: "港口看灯人", profile: "资料", initial_location: "" }];
    c.form.bystanders = [{ bystander_id: "", name: "船夫", description: "在栈桥等活" }];
    c.touch();
    await c.flush();
    hook = undefined;
    assert.equal(saved, 1, "the save was sent");
    assert.equal(c.form.locations[0].id, "harbor", "the derived place id reaches the form");
    assert.equal(c.form.initial_location, "harbor", "the starting place reaches the form");
    assert.equal(c.form.npcs[0].entity_id, "npc:keeper", "the derived character id reaches the form");
    assert.equal(c.form.bystanders[0].bystander_id, "bystander:boatman", "the derived passer-by id reaches the form");
  },

  async "a conflicting draft is not published"(c) {
    await c.openProject("project_1");
    await c.openDraft("draft_1");
    hook = (method, url) => {
      if (method === "PUT" && url.endsWith("/drafts/draft_1")) {
        return response({ error: { code: "version_conflict", message: "version conflict" } }, 409);
      }
      if (method === "POST" && url.endsWith("/publish")) {
        return response({ operation: { operation_id: "publish_2", stage: "ready", status: "succeeded" } });
      }
    };
    c.form.title = "冲突中的编辑";
    c.touch();
    await c.publish();
    hook = undefined;
    assert.equal(calls.filter((line) => line.includes("/publish")).length, 0, "publish must not run with a conflicting draft");
    assert.equal(c.conflict.value, true);
    assert.match(c.error.value, /版本冲突|过期/);
  },

  async "deleting a draft clears the editing session"(c) {
    await c.openProject("project_1");
    await c.openDraft("draft_1");
    hook = (method, url) => {
      if (method === "DELETE" && url.endsWith("/drafts/draft_1")) return new Response(null, { status: 204 });
    };
    await c.removeDraft("draft_1");
    hook = undefined;
    assert.equal(c.draft.value, null);
    assert.equal(c.drafts.value.length, 0);
    assert.equal(c.form.title, "", "deleting resets the form");
  },
};

let failures = 0;
for (const [name, run] of Object.entries(tests)) {
  const creator = useCreator();
  calls = [];
  hook = undefined;
  try {
    await run(creator);
    console.log(`PASS ${name}`);
  } catch (error) {
    failures += 1;
    console.log(`FAIL ${name}`);
    console.log(error?.stack ?? error);
  }
}
if (failures > 0) {
  console.log(`${failures} creator test(s) failed`);
  process.exit(1);
}
console.log(`${Object.keys(tests).length} creator editing/session tests passed`);
