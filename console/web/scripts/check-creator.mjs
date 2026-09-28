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
    background: "", rules: "", author_facts: "", player: { name: "旅人", profile: "" },
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
  if (method === "PUT" && url === "/api/v1/content/drafts/draft_1") {
    const body = JSON.parse(init.body);
    return response({ draft: draft({ version: body.expected_version + 1, payload: body.payload }) });
  }
  if (url === "/api/v1/content/drafts/draft_1/preview" || url === "/api/v1/content/drafts/draft_1/preview?view=author") {
    return response({ preview: { view: url.includes("author") ? "author" : "player", draft_id: "draft_1", version: 2, title: "港口", mode: "open", description: "", gameplay: "", background: "", opening: "", clock: "", initial_location: "", player: { name: "旅人", profile: "" }, characters: [], bystanders: [], author_facts: url.includes("author") ? "作者事实" : undefined } });
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

  async "draft deletion clears the editing session"(c) {
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
