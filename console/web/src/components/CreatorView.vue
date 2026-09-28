<script setup lang="ts">
import { onMounted, ref } from "vue";
import { useCreator } from "../useCreator";
const {
  personas, projects, project, drafts, draft, form, preview, previewView,
  error, notice, busy, saving, conflict, draftID, statusLine,
  loadCatalog, openProject, createProject, openDraft, startDraft, touch, flush,
  reloadDraft, copyAsNewDraft, removeDraft, loadPreview,
  addLocation, removeLocation, addNPC, removeNPC, addBystander, removeBystander,
} = useCreator();

const newGameID = ref(""), newTitle = ref("");
onMounted(() => { void loadCatalog(); });

function createProjectForm() {
  if (!newGameID.value.trim() || !newTitle.value.trim()) return;
  void createProject(newGameID.value.trim(), newTitle.value.trim());
  newGameID.value = ""; newTitle.value = "";
}
function speaking(value: string, index: number) {
  form.npcs[index].speaking_examples = value.split("\n").map(line => line.trim()).filter(Boolean);
  touch();
}
</script>

<template>
  <section class="creator" aria-label="创作">
    <div class="creator-head">
      <h1>创作</h1>
      <p class="subtle">草稿只保存在你的内容库中，不会改动任何已有存档；发布后的修订从新开局使用。</p>
    </div>
    <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
    <p v-if="notice" class="subtle" role="status">{{ notice }}</p>

    <div class="creator-columns">
      <aside class="creator-list">
        <h2>我的剧本</h2>
        <ul v-if="projects.length" class="project-list">
          <li v-for="item in projects" :key="item.project_id">
            <button
              type="button"
              class="quiet-button project-open"
              :class="{ current: project?.project_id === item.project_id }"
              @click="openProject(item.project_id)"
            >
              {{ item.title }}<span class="subtle"> · {{ item.game_id }}<template v-if="item.current_revision"> · {{ item.current_revision }}</template></span>
            </button>
          </li>
        </ul>
        <p v-else class="subtle">还没有内容项目。用下面的表单新建一个。</p>

        <form class="new-project" @submit.prevent="createProjectForm">
          <label>标识<input v-model="newGameID" placeholder="harbor-lights" autocomplete="off" /></label>
          <label>标题<input v-model="newTitle" placeholder="港口的灯" autocomplete="off" /></label>
          <button type="submit" class="quiet-button" :disabled="busy">新建内容项目</button>
        </form>

        <h2 v-if="personas.length">主角模板</h2>
        <ul v-if="personas.length" class="project-list">
          <li v-for="item in personas" :key="item.persona_id">
            <span class="subtle">{{ item.name }} · {{ item.profile }}</span>
          </li>
        </ul>
      </aside>

      <div class="creator-main">
        <template v-if="project">
          <header class="project-head">
            <h2>{{ project.title }}</h2>
            <span class="subtle">{{ project.game_id }}<template v-if="project.current_revision"> · 已发布 {{ project.current_revision }}</template></span>
          </header>

          <div class="draft-bar">
            <button type="button" class="quiet-button" :disabled="busy" @click="startDraft('')">新建空白草稿</button>
            <button v-if="project.current_revision" type="button" class="quiet-button" :disabled="busy" @click="startDraft(project.current_revision)">从已发布修订起稿</button>
            <span class="subtle">{{ statusLine }}</span>
          </div>

          <ul v-if="drafts.length" class="draft-list">
            <li v-for="item in drafts" :key="item.draft_id">
              <button type="button" class="quiet-button draft-open" :class="{ current: draftID === item.draft_id }" @click="openDraft(item.draft_id)">
                草稿 {{ item.draft_id.slice(-6) }} · v{{ item.version }} · {{ item.status }}
              </button>
              <button type="button" class="quiet-button" @click="removeDraft(item.draft_id)">删除</button>
            </li>
          </ul>
          <p v-else class="subtle">这个项目还没有草稿。</p>

          <div v-if="conflict" class="conflict" role="alert">
            <p>草稿已在别处更新。你的修改仍保留在本地，选择一种处理方式：</p>
            <button type="button" class="quiet-button" @click="reloadDraft()">重新载入远端草稿</button>
            <button type="button" class="quiet-button" @click="copyAsNewDraft()">复制为新草稿</button>
          </div>

          <form v-if="draft" class="editor" @submit.prevent="flush()">
            <fieldset>
              <legend>基本信息</legend>
              <label>标题<input v-model="form.title" @input="touch" /></label>
              <label>类型
                <select v-model="form.mode" @change="touch">
                  <option value="open">开放型</option>
                  <option value="guided">流程型</option>
                </select>
              </label>
              <label>简介<textarea v-model="form.description" rows="2" @input="touch"></textarea></label>
              <label>玩法<textarea v-model="form.gameplay" rows="2" @input="touch"></textarea></label>
              <label>开场<textarea v-model="form.opening" rows="3" @input="touch"></textarea></label>
              <label>起始时间<input v-model="form.clock" placeholder="第 1 日 19:00" @input="touch" /></label>
              <div class="pair">
                <label>主角姓名<input v-model="form.player.name" @input="touch" /></label>
                <label>主角简介<textarea v-model="form.player.profile" rows="2" @input="touch"></textarea></label>
              </div>
            </fieldset>

            <fieldset>
              <legend>世界设定</legend>
              <label>公开背景<textarea v-model="form.background" rows="3" @input="touch"></textarea></label>
              <label>世界规则<textarea v-model="form.rules" rows="3" @input="touch"></textarea></label>
              <label>作者事实（仅作者视图可见）<textarea v-model="form.author_facts" rows="3" @input="touch"></textarea></label>
            </fieldset>

            <fieldset>
              <legend>地点</legend>
              <div v-for="(location, index) in form.locations" :key="index" class="row">
                <input v-model="location.id" placeholder="id" @input="touch" />
                <input v-model="location.name" placeholder="名称" @input="touch" />
                <input v-model="location.description" placeholder="描述" @input="touch" />
                <button type="button" class="quiet-button" @click="removeLocation(index)">移除</button>
              </div>
              <label>起始地点
                <select v-model="form.initial_location" @change="touch">
                  <option value="">未选择</option>
                  <option v-for="location in form.locations" :key="location.id" :value="location.id">{{ location.name || location.id }}</option>
                </select>
              </label>
              <button type="button" class="quiet-button" @click="addLocation()">添加地点</button>
            </fieldset>

            <fieldset>
              <legend>人物</legend>
              <div v-for="(npc, index) in form.npcs" :key="index" class="npc-card">
                <div class="row">
                  <input v-model="npc.definition_id" placeholder="definition_id" @input="touch" />
                  <input v-model="npc.revision" placeholder="版本" @input="touch" />
                  <input v-model="npc.entity_id" placeholder="npc:xxx" @input="touch" />
                  <button type="button" class="quiet-button" @click="removeNPC(index)">移除</button>
                </div>
                <div class="pair">
                  <label>姓名<input v-model="npc.name" @input="touch" /></label>
                  <label>身份<input v-model="npc.role" @input="touch" /></label>
                </div>
                <label>公开外观<input v-model="npc.appearance" @input="touch" /></label>
                <label>头像资源<input v-model="npc.avatar" placeholder="assets/keeper.png" @input="touch" /></label>
                <label>人物资料<textarea v-model="npc.profile" rows="2" @input="touch"></textarea></label>
                <label>初始知情（作者私有）<textarea v-model="npc.knowledge" rows="2" @input="touch"></textarea></label>
                <label>初始关切<textarea v-model="npc.initial_concerns" rows="2" @input="touch"></textarea></label>
                <label>初始地点
                  <select v-model="npc.initial_location" @change="touch">
                    <option value="">未选择</option>
                    <option v-for="location in form.locations" :key="location.id" :value="location.id">{{ location.name || location.id }}</option>
                  </select>
                </label>
                <label>说话示例（每行一条）<textarea :value="(npc.speaking_examples ?? []).join('\n')" rows="2" @input="speaking(($event.target as HTMLTextAreaElement).value, index)"></textarea></label>
              </div>
              <button type="button" class="quiet-button" @click="addNPC()">添加人物</button>
            </fieldset>

            <fieldset>
              <legend>路人</legend>
              <div v-for="(bystander, index) in form.bystanders" :key="index" class="row">
                <input v-model="bystander.bystander_id" placeholder="bystander:xxx" @input="touch" />
                <input v-model="bystander.name" placeholder="名称" @input="touch" />
                <input v-model="bystander.description" placeholder="公开描述" @input="touch" />
                <button type="button" class="quiet-button" @click="removeBystander(index)">移除</button>
              </div>
              <button type="button" class="quiet-button" @click="addBystander()">添加路人</button>
            </fieldset>

            <div class="editor-actions">
              <button type="submit" class="quiet-button" :disabled="saving">立即保存</button>
              <button type="button" class="quiet-button" @click="loadPreview('player')">玩家预览</button>
              <button type="button" class="quiet-button" @click="loadPreview('author')">作者预览（含剧透）</button>
            </div>
          </form>

          <section v-if="preview" class="preview" aria-label="预览">
            <p v-if="preview.spoiler_warning" class="spoiler">{{ preview.spoiler_warning }}</p>
            <h3>{{ preview.title }}</h3>
            <p class="subtle">{{ previewView === 'author' ? '作者视图' : '玩家视图' }} · {{ preview.mode === 'guided' ? '流程型' : '开放型' }} · {{ preview.clock }} · {{ preview.initial_location }}</p>
            <p>{{ preview.description }}</p>
            <p>{{ preview.opening }}</p>
            <h4>人物</h4>
            <ul>
              <li v-for="npc in preview.characters" :key="npc.entity_id">{{ npc.name }} · {{ npc.role }}</li>
            </ul>
            <template v-if="preview.view === 'author'">
              <h4>作者事实</h4>
              <p>{{ preview.author_facts }}</p>
              <h4>人物私有资料</h4>
              <ul>
                <li v-for="npc in preview.author_characters ?? []" :key="npc.entity_id">{{ npc.name }} · 知情：{{ npc.knowledge }}</li>
              </ul>
            </template>
          </section>
        </template>
        <p v-else class="subtle">从左侧选择一个内容项目，或新建一个。</p>
      </div>
    </div>
  </section>
</template>

<style scoped>
.creator { padding: 1rem 1.25rem 3rem; }
.creator-head h1 { margin: 0 0 .25rem; font-size: 1.3rem; }
.creator-columns { display: grid; grid-template-columns: 260px 1fr; gap: 1.25rem; margin-top: 1rem; }
.creator-list h2, .project-head h2 { font-size: 1rem; margin: .75rem 0 .5rem; }
.project-list, .draft-list { list-style: none; margin: 0; padding: 0; }
.project-list li, .draft-list li { display: flex; gap: .25rem; align-items: center; margin-bottom: .25rem; }
.project-open, .draft-open { flex: 1; text-align: left; }
.project-open.current, .draft-open.current { border-color: var(--accent, #ae5440); color: var(--accent, #ae5440); }
.new-project { display: flex; flex-direction: column; gap: .35rem; margin-top: .75rem; }
.new-project label, .editor label { display: flex; flex-direction: column; gap: .15rem; font-size: .8rem; color: var(--muted, #766f65); }
.new-project input, .editor input, .editor textarea, .editor select { font: inherit; padding: .35rem; border: 1px solid var(--border, #ddd2c4); border-radius: 6px; background: var(--paper, #fffdf8); color: inherit; }
.draft-bar { display: flex; gap: .5rem; align-items: center; flex-wrap: wrap; margin-bottom: .5rem; }
.editor { display: flex; flex-direction: column; gap: 1rem; }
.editor fieldset { border: 1px solid var(--border, #ddd2c4); border-radius: 8px; padding: .6rem .75rem .8rem; display: flex; flex-direction: column; gap: .45rem; }
.editor legend { font-size: .85rem; color: var(--muted, #766f65); padding: 0 .3rem; }
.editor .pair { display: grid; grid-template-columns: 1fr 1fr; gap: .5rem; }
.editor .row { display: grid; grid-template-columns: 1fr 1fr 2fr auto; gap: .35rem; align-items: center; }
.npc-card { border: 1px dashed var(--border, #ddd2c4); border-radius: 8px; padding: .5rem; display: flex; flex-direction: column; gap: .35rem; }
.npc-card .row { grid-template-columns: 1fr 1fr 1fr auto; }
.editor-actions { display: flex; gap: .5rem; flex-wrap: wrap; }
.conflict { border: 1px solid var(--accent, #ae5440); border-radius: 8px; padding: .6rem .75rem; margin-bottom: .75rem; display: flex; flex-direction: column; gap: .4rem; }
.preview { border: 1px solid var(--border, #ddd2c4); border-radius: 8px; padding: .75rem; margin-top: 1rem; }
.preview h3, .preview h4 { margin: .5rem 0 .25rem; }
.spoiler { color: var(--accent, #ae5440); font-size: .8rem; margin: 0; }
@media (max-width: 900px) {
  .creator-columns { grid-template-columns: 1fr; }
  .editor .pair { grid-template-columns: 1fr; }
  .editor .row, .npc-card .row { grid-template-columns: 1fr; }
}
</style>
