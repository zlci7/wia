<script setup lang="ts">
import AppDialog from "./components/AppDialog.vue";
import CreatorView from "./components/CreatorView.vue";
import MemoryPanel from "./components/MemoryPanel.vue";
import UsagePanel from "./components/UsagePanel.vue";
import SuggestionPanel from "./components/SuggestionPanel.vue";
import { useExperience } from "./useExperience";
import "./style.css";
const {
  storyEntries,
  packIssues,
  newGame,
  newRevisionConflict,
  pendingCreate,
  refreshNewRevision,
  status,
  games,
  currentWorld,
  characters,
  view,
  game,
  loaded,
  connectionError,
  notice,
  dialog,
  dialogError,
  dialogBusy,
  navigating,
  moreOpen,
  deleteCandidate,
  newWorld,
  modelForm,
  providers,
  modelAdvanced,
  settingsForm,
  settingsWorldName,
  policyDefaults,
  detailsOpen,
  textarea,
  reader,
  session,
  viewport,
  storyWorlds,
  recentWorld,
  activeRun,
  pendingSubmission,
  waitingSeconds,
  failedRun,
  saved,
  addresseeName,
  canSubmit,
  presets,
  lengths,
  players,
  initiatives,
  presetName,
  dialogTitle,
  formatDate,
  failureText,
  showDialog,
  closeDialog,
  openModel,
  changeProvider,
  freshRefresh,
  returnHome,
  openStory,
  switchWorld,
  openNewWorld,
  openCopy,
  pendingCopy,
  openSettings,
  configureModel,
  configureSettings,
  createOrCopy,
  confirmDelete,
  removeWorld,
  sendInput,
  stopRun,
  inputKeys,
  chooseCharacter,
  chooseSuggestion,
  openCreator,
  resizeInput,
} = useExperience();
const policyOptions = [
  {
    key: "coordination",
    label: "场景协调策略",
    note: "行动结果、冲突处理与玩家反应机会。",
  },
  { key: "narration", label: "正文表达策略", note: "叙事节奏、描写与收尾。" },
  {
    key: "npc",
    label: "NPC 共用决策策略",
    note: "发送给所有参与决策的 NPC，请勿填写任何人物的秘密或专属背景。",
  },
] as const;
</script>

<template>
  <main class="shell" :class="{ 'play-shell': view === 'play' }">
    <div class="app-header">
      <header class="topbar">
        <div class="brand">
          <span class="brand-mark">W</span><strong>World Is Agent</strong>
        </div>
        <nav class="topbar-actions" aria-label="主导航">
          <span
            class="connection-state"
            :class="{
              online: status?.ready && !connectionError && !session.error,
            }"
            >{{
              connectionError || (view === "play" && session.error)
                ? "连接异常"
                : status?.ready
                  ? "已连接"
                  : "未连接模型"
            }}</span
          >
          <button
            v-if="view !== 'home'"
            class="quiet-button"
            @click="returnHome"
          >
            返回首页
          </button>
          <button
            v-if="currentWorld && view === 'play'"
            class="quiet-button"
            @click="showDialog('saves')"
          >
            存档与故事
          </button>
          <button
            v-if="currentWorld && view === 'play'"
            class="quiet-button desktop-setting"
            @click="openSettings"
          >
            故事设置
          </button>
          <button class="quiet-button desktop-setting" @click="openModel()">
            模型设置
          </button>
          <button class="quiet-button desktop-setting" @click="showDialog('usage')">模型用量</button>
          <button
            class="quiet-button desktop-setting"
            :class="{ current: view === 'creator' }"
            @click="openCreator"
          >
            创作
          </button>
          <button
            v-if="currentWorld && view === 'play'"
            class="quiet-button desktop-setting"
            @click="showDialog('memory')"
          >
            回顾与纠正
          </button>
          <div class="more-menu">
            <button
              id="wia-more-menu"
              class="quiet-button"
              :aria-expanded="moreOpen"
              @click="moreOpen = !moreOpen"
            >
              更多
            </button>
            <div v-if="moreOpen" class="menu-panel">
              <button @click="showDialog('usage')">模型用量</button>
              <button @click="openCreator">创作</button>
              <button
                v-if="currentWorld && view === 'play'"
                @click="showDialog('memory')"
              >
                回顾与纠正
              </button>
              <button
                v-if="currentWorld && view === 'play'"
                @click="openSettings"
              >
                故事设置</button
              ><button @click="openModel()">模型设置</button>
            </div>
          </div>
        </nav>
      </header>
      <div
        v-if="connectionError || (view === 'play' && session.error)"
        class="status-banner error"
        role="alert"
      >
        {{ connectionError || session.error }}
        <button @click="freshRefresh">重试连接</button>
      </div>
      <div v-if="notice" class="status-banner" role="status">
        {{ notice
        }}<button aria-label="关闭提示" @click="notice = ''">×</button>
      </div>
    </div>
    <div v-if="!loaded" class="loading-page">正在打开故事书…</div>

    <section v-else-if="view === 'creator'" class="page-content">
      <CreatorView />
    </section>

    <section v-else-if="view === 'home'" class="page-content">
      <div class="page-heading">
        <span class="eyebrow">故事首页</span>
        <h1>今晚，故事从哪里继续？</h1>
        <p>继续上次旅程，或选择一个故事重新开始。</p>
      </div>
      <section v-if="currentWorld" class="continue-panel">
        <div>
          <span class="eyebrow">继续上次故事</span>
          <h2>{{ currentWorld.name }}</h2>
          <p>{{ currentWorld.scene }} · 游戏内 {{ currentWorld.clock }}</p>
        </div>
        <button
          class="primary-button"
          :disabled="navigating"
          @click="switchWorld(currentWorld)"
        >
          继续故事
        </button>
      </section>
      <h2>选择一个剧本</h2>
      <details v-if="packIssues.length" class="pack-issues">
        <summary>
          有 {{ packIssues.length }} 个剧本加载问题；已有存档可继续
        </summary>
        <p v-for="issue in packIssues" :key="issue.file">
          {{ issue.file }}：{{ issue.message }}
        </p>
      </details>
      <p v-if="!storyEntries.length" class="subtle">
        暂无可用剧本，请检查本地剧本目录。
      </p>
      <div class="story-grid">
        <article v-for="item in storyEntries" :key="item.id" class="story-card">
          <div class="story-art">
            <img
              v-if="item.cover_url"
              class="story-cover"
              :src="item.cover_url"
              :alt="item.cover_alt || item.title"
            />
            <span v-else class="cover-placeholder" aria-hidden="true">{{
              item.title.slice(0, 1)
            }}</span>
          </div>
          <div class="story-card-body">
            <span class="eyebrow">{{
              item.mode === "guided" ? "流程型" : "开放型"
            }}</span>
            <h2>{{ item.title }}</h2>
            <p>{{ item.description }}</p>
            <button class="primary-button" @click="openStory(item)">
              进入这个故事
            </button>
          </div>
        </article>
      </div>
      <p v-if="!status?.ready" class="subtle">
        可以先看看故事，开始游玩前再连接模型。
      </p>
    </section>

    <section v-else-if="view === 'story' && game" class="page-content">
      <div class="story-detail">
        <div>
          <span class="eyebrow">选择进入方式</span>
          <h1>{{ game.title }}</h1>
          <p>{{ game.description }}</p>
          <p class="eyebrow">
            {{ game.mode === "guided" ? "流程型" : "开放型" }} ·
            {{ game.revision }}
          </p>
          <p v-if="game.background">{{ game.background }}</p>
          <p v-if="game.gameplay">{{ game.gameplay }}</p>
          <p v-if="game.player.requirements" class="subtle">
            {{ game.player.requirements }}
          </p>
          <div class="button-row">
            <button
              v-if="recentWorld"
              class="primary-button"
              :disabled="navigating"
              @click="switchWorld(recentWorld)"
            >
              继续最近进度</button
            ><button
              v-if="game.available !== false"
              :class="recentWorld ? 'secondary-button' : 'primary-button'"
              @click="openNewWorld"
            >
              开始新的故事
            </button>
          </div>
        </div>
        <div class="story-art">
          <img
            v-if="game.cover_url"
            class="story-cover"
            :src="game.cover_url"
            :alt="game.cover_alt || game.title"
          />
          <span v-else class="cover-placeholder" aria-hidden="true">{{
            game.title.slice(0, 1)
          }}</span>
        </div>
      </div>
      <section v-if="storyWorlds.length" class="story-saves">
        <h2>
          选择存档 <small>{{ storyWorlds.length }} 个</small>
        </h2>
        <div
          v-for="world in storyWorlds"
          :key="world.world_id"
          class="save-item"
          :class="{ active: world.world_id === status?.active_world?.world_id }"
        >
          <button
            class="save-open"
            :disabled="navigating"
            @click="switchWorld(world)"
          >
            <strong
              >{{ world.name }}
              <small v-if="world.world_id === status?.active_world?.world_id"
                >当前</small
              ></strong
            ><span>{{ world.scene }}</span
            ><small
              >更新于 {{ formatDate(world.updated_at) }} · 游戏内
              {{ world.clock }} ·
              {{ world.mode === "guided" ? "流程型" : "开放型" }}</small
            ></button
          ><button
            class="delete-button"
            :aria-label="`删除存档 ${world.name}`"
            @click="confirmDelete(world)"
          >
            删除
          </button>
        </div>
      </section>
    </section>

    <div v-else-if="currentWorld && view === 'play'" class="game-layout">
      <section class="story-column">
        <div class="reading-heading">
          <div>
            <h1>{{ currentWorld.name }}</h1>
            <span class="subtle"
              >{{ currentWorld.scene }} · 游戏内 {{ currentWorld.clock }}</span
            >
          </div>
          <button
            class="quiet-button scene-toggle"
            @click="showDialog('scene')"
          >
            场景与人物
          </button>
        </div>
        <div
          ref="viewport"
          class="transcript"
          tabindex="0"
          aria-label="故事正文"
          @scroll.passive="reader.remember"
        >
          <div class="history-control">
            <button
              v-if="session.before !== undefined"
              class="quiet-button"
              :disabled="session.loading || session.historyLoading"
              @click="reader.earlier"
            >
              {{
                session.historyLoading ? "正在读取…" : "加载更早内容"
              }}</button
            ><span v-else-if="session.initialized" class="subtle"
              >故事从这里开始</span
            ><span v-else class="subtle">正在读取故事…</span>
          </div>
          <div v-if="session.historyError" class="inline-error" role="alert">
            {{ session.historyError
            }}<button @click="reader.earlier()">重试历史</button>
          </div>
          <article
            v-for="message in session.messages"
            :key="message.message_id"
            :data-message-id="message.message_id"
            class="message"
            :class="
              message.kind === 'player' ? 'player-message' : 'narrative-message'
            "
          >
            <div class="message-content">{{ message.content }}</div>
            <time :datetime="message.created_at">{{
              formatDate(message.created_at)
            }}</time>
          </article>
          <div v-if="activeRun" class="run-card" role="status">
            <p class="pending-input">{{ activeRun.input }}</p>
            <div class="button-row">
              <span class="pulse-dot"></span><span>正在组织回应</span
              ><button
                class="quiet-button"
                :disabled="session.sending"
                @click="stopRun"
              >
                取消
              </button>
            </div>
          </div>
          <div
            v-if="failedRun && !pendingSubmission"
            class="run-card failed-card"
          >
            <p>{{ failureText(failedRun) }}</p>
            <button
              class="secondary-button"
              :disabled="session.sending"
              @click="sendInput(true)"
            >
              重试本轮
            </button>
          </div>
        </div>
        <div v-if="!session.bottom" class="latest-row">
          <button class="secondary-button" @click="reader.latest">
            {{ session.unread ? "有新内容 · 回到最新" : "回到最新" }}
          </button>
        </div>
        <form class="composer" @submit.prevent="sendInput()">
          <SuggestionPanel v-if="currentWorld" :world="currentWorld" :active-revision="status?.active_revision ?? 0" :ready="!!status?.ready" :busy="!!activeRun || session.sending || !!pendingSubmission" :has-draft="!!session.draft.trim()" @choose="chooseSuggestion" />
          <div class="composer-tools">
            <label
              >对谁说
              <select v-model="session.addressee" aria-label="交谈对象">
                <option value="">自动判断</option>
                <option
                  v-for="character in characters"
                  :key="character.entity_id"
                  :value="character.entity_id"
                >
                  {{ character.name }}
                </option>
              </select></label
            ><button
              v-if="addresseeName"
              type="button"
              class="recipient-chip"
              @click="session.addressee = ''"
            >
              对{{ addresseeName }}说 ×</button
            ><span class="save-status">{{
              session.sending
                ? "正在处理"
                : pendingSubmission
                  ? "提交结果待确认"
                  : activeRun
                    ? `正在生成 · 已等待 ${waitingSeconds} 秒`
                    : failedRun
                      ? failedRun.status === "cancelled"
                        ? "本轮已取消"
                        : "本轮未完成"
                      : saved
                        ? "本轮已保存"
                        : "进度自动保存"
            }}</span>
          </div>
          <textarea
            ref="textarea"
            v-model="session.draft"
            aria-label="你的行动"
            rows="2"
            placeholder="你想做什么？"
            :disabled="!!activeRun || session.sending"
            @keydown="inputKeys"
            @input="resizeInput"
          ></textarea>
          <p v-if="session.suggestionBasis" class="subtle suggestion-origin">来自行动建议，可修改后提交。
            <button type="button" class="quiet-button" @click="session.suggestionBasis = undefined; session.sendError = ''">作为自由输入</button>
          </p>
          <p
            v-if="session.sendError && !pendingSubmission"
            class="inline-error"
            role="alert"
          >
            {{ session.sendError }}
          </p>
          <div
            v-if="pendingSubmission && !session.sending"
            class="inline-error"
            role="status"
          >
            提交结果待确认，输入已保留。系统会继续查询，确认前不会发送新的行动。
            <button type="button" class="secondary-button" @click="sendInput()">
              确认或重发原请求
            </button>
          </div>
          <div class="composer-footer">
            <span>{{
              currentWorld?.story_ended
                ? "本段故事已结束，可另存或开始新故事"
                : "Ctrl + Enter 提交"
            }}</span
            ><button
              class="primary-button"
              type="submit"
              :disabled="!canSubmit"
            >
              {{
                session.sending
                  ? "正在提交…"
                  : activeRun
                    ? "等待回应"
                    : currentWorld?.story_ended
                      ? "故事已结束"
                      : status?.ready
                        ? "继续故事"
                        : "连接模型并继续"
              }}
            </button>
          </div>
        </form>
      </section>
      <aside class="side-column">
        <section class="side-panel">
          <span class="eyebrow">当前场景</span>
          <h2>{{ currentWorld.scene }}</h2>
          <span class="subtle">游戏内时间</span>
          <p class="clock-value">{{ currentWorld.clock }}</p>
        </section>
        <section class="side-panel">
          <h2>眼前的人</h2>
          <button
            v-for="character in characters"
            :key="character.entity_id"
            class="character-row"
            :class="{ selected: session.addressee === character.entity_id }"
            @click="chooseCharacter(character)"
          >
            <span class="avatar">{{ character.name.slice(0, 1) }}</span
            ><span
              ><strong>{{ character.name }}</strong
              ><small>{{ character.role }}</small></span
            >
          </button>
          <p v-if="!characters.length" class="subtle">
            眼前暂时没有可交谈的人物。
          </p>
        </section>
      </aside>
    </div>

    <AppDialog
      v-if="dialog"
      :key="dialog"
      :title="dialogTitle"
      :busy="dialogBusy"
      :destructive="dialog === 'delete'"
      :drawer="dialog === 'scene'"
      return-focus-to="#wia-more-menu"
      @close="closeDialog"
    >
      <p v-if="dialogError" class="inline-error" role="alert">
        {{ dialogError }}
      </p>
      <MemoryPanel
        v-if="dialog === 'memory' && currentWorld"
        :key="currentWorld.world_id"
        :world-i-d="currentWorld.world_id"
        :world-name="currentWorld.name"
        :characters="characters"
        @busy="dialogBusy = $event"
        @updated="freshRefresh"
      />
      <UsagePanel v-if="dialog === 'usage'" :key="currentWorld?.world_id ?? ''" :world-id="currentWorld?.world_id" :world-name="currentWorld?.name" />
      <template v-if="dialog === 'model'">
        <p class="subtle">
          凭据仅保存在本机。{{
            status?.model.configured
              ? `当前：${status.model.provider} · ${status.model.model}`
              : "选择服务商并填写密钥，即可开始故事。"
          }}
        </p>
        <form @submit.prevent="configureModel">
          <fieldset :disabled="dialogBusy">
            <label
              >服务商<select
                v-model="modelForm.provider"
                @change="changeProvider"
              >
                <option
                  v-for="option in providers"
                  :key="option.provider"
                  :value="option.provider"
                >
                  {{ option.provider }}
                </option>
              </select></label
            >
            <p>
              推荐模型：{{
                providers.find(
                  (option) => option.provider === modelForm.provider,
                )?.model
              }}
            </p>
            <label
              >API Key<input
                v-model="modelForm.api_key"
                type="password"
                autocomplete="off"
                placeholder="填写服务商提供的密钥"
            /></label>
            <details
              :open="modelAdvanced"
              @toggle="
                modelAdvanced = ($event.target as HTMLDetailsElement).open
              "
            >
              <summary>高级连接设置</summary>
              <label>模型名称<input v-model="modelForm.model" /></label
              ><label
                >自定义地址 <small>可选</small
                ><input
                  v-model="modelForm.base_url"
                  placeholder="留空使用服务商默认地址"
              /></label>
            </details>
          </fieldset>
          <div class="modal-actions">
            <button
              type="button"
              class="secondary-button"
              :disabled="dialogBusy"
              @click="closeDialog"
            >
              取消</button
            ><button class="primary-button" :disabled="dialogBusy">
              {{ dialogBusy ? "正在验证…" : "验证并保存" }}
            </button>
          </div>
        </form>
      </template>
      <template v-else-if="dialog === 'settings'">
        <p class="subtle">
          属于“{{ settingsWorldName }}”，从下一轮生效；另存会继承设置。
        </p>
        <form @submit.prevent="configureSettings">
          <fieldset :disabled="dialogBusy || !!activeRun">
            <span class="field-label"
              >互动风格 <small>{{ presetName }}</small></span
            >
            <div class="setting-options">
              <button
                v-for="option in presets"
                :key="option.label"
                type="button"
                :class="{ selected: presetName === option.label }"
                @click="
                  settingsForm.player_elaboration = option.player_elaboration;
                  settingsForm.npc_initiative = option.npc_initiative;
                "
              >
                <strong
                  >{{ option.label
                  }}{{
                    option.player_elaboration === "natural" ? " · 默认" : ""
                  }}</strong
                ><span>{{ option.note }}</span>
              </button>
            </div>
            <span class="field-label">回复长度</span>
            <div class="setting-options">
              <button
                v-for="option in lengths"
                :key="option.value"
                type="button"
                :class="{ selected: settingsForm.length === option.value }"
                @click="settingsForm.length = option.value"
              >
                <strong>{{ option.label }}</strong
                ><span>{{ option.note }}</span>
              </button>
            </div>
            <details
              :open="detailsOpen"
              @toggle="detailsOpen = ($event.target as HTMLDetailsElement).open"
            >
              <summary>详细设置</summary>
              <label
                >叙事视角<select v-model="settingsForm.perspective">
                  <option value="second_person">第二人称 · 你</option>
                  <option value="first_person">第一人称 · 我</option>
                  <option value="third_person">第三人称 · 主角姓名</option>
                </select></label
              ><label
                >描写密度<select v-model="settingsForm.detail">
                  <option value="restrained">克制</option>
                  <option value="balanced">平衡</option>
                  <option value="rich">丰富</option>
                </select></label
              ><span class="field-label">主角表现</span>
              <div class="setting-options">
                <button
                  v-for="option in players"
                  :key="option.value"
                  type="button"
                  :class="{
                    selected: settingsForm.player_elaboration === option.value,
                  }"
                  @click="settingsForm.player_elaboration = option.value"
                >
                  <strong>{{ option.label }}</strong
                  ><span>{{ option.note }}</span>
                </button>
              </div>
              <span class="field-label">人物主动程度</span>
              <div class="setting-options">
                <button
                  v-for="option in initiatives"
                  :key="option.value"
                  type="button"
                  :class="{
                    selected: settingsForm.npc_initiative === option.value,
                  }"
                  @click="settingsForm.npc_initiative = option.value"
                >
                  <strong>{{ option.label }}</strong
                  ><span>{{ option.note }}</span>
                </button>
              </div>
              <details v-if="policyDefaults" class="behavior-settings">
                <summary>高级行为策略</summary>
                <p class="guardrail-note">
                  只影响当前存档，从下一轮生效。上方的人称、篇幅、主角表现和主动程度优先；信息范围与关键选择边界保持有效。
                </p>
                <div
                  v-for="option in policyOptions"
                  :key="option.key"
                  class="policy-editor"
                >
                  <label :for="'policy-' + option.key"
                    >{{ option.label }}
                    <small>{{
                      settingsForm.behavior_policies[option.key]
                        ? "自定义"
                        : "默认 · " + policyDefaults.version
                    }}</small>
                  </label>
                  <p class="policy-note">{{ option.note }}</p>
                  <textarea
                    :id="'policy-' + option.key"
                    :value="
                      settingsForm.behavior_policies[option.key] ||
                      policyDefaults[option.key]
                    "
                    :maxlength="policyDefaults.max_chars"
                    rows="6"
                    @input="
                      settingsForm.behavior_policies[option.key] = (
                        $event.target as HTMLTextAreaElement
                      ).value
                    "
                  ></textarea>
                  <div class="policy-footer">
                    <small
                      >{{
                        [
                          ...(settingsForm.behavior_policies[option.key] ||
                            policyDefaults[option.key]),
                        ].length
                      }}
                      / {{ policyDefaults.max_chars }} 字</small
                    >
                    <button
                      type="button"
                      class="secondary-button"
                      :disabled="!settingsForm.behavior_policies[option.key]"
                      @click="settingsForm.behavior_policies[option.key] = ''"
                    >
                      恢复默认
                    </button>
                  </div>
                  <small
                    >自定义文本替换本项默认策略；清空后使用默认。默认策略随版本更新，自定义内容保留。</small
                  >
                </div>
              </details>
            </details>
          </fieldset>
          <p class="guardrail-note">
            关键选择留给你，人物依据自己的经历作出回应。
          </p>
          <div class="modal-actions">
            <button
              type="button"
              class="secondary-button"
              :disabled="dialogBusy"
              @click="closeDialog"
            >
              取消</button
            ><button
              class="primary-button"
              :disabled="dialogBusy || !!activeRun"
            >
              {{
                activeRun
                  ? "请等待当前回合完成"
                  : dialogBusy
                    ? "正在保存…"
                    : "保存设置"
              }}
            </button>
          </div>
        </form>
      </template>
      <template v-else-if="dialog === 'saves'">
        <p class="subtle">
          {{ game?.title }} · 读取后继续更新所选存档；另存保留独立进度。
        </p>
        <p v-if="notice" class="success-note" role="status">{{ notice }}</p>
        <div
          v-for="world in storyWorlds"
          :key="world.world_id"
          class="save-item"
          :class="{ active: world.world_id === currentWorld?.world_id }"
        >
          <button
            class="save-open"
            :disabled="dialogBusy"
            @click="switchWorld(world)"
          >
            <strong
              >{{ world.name }}
              <small v-if="world.world_id === currentWorld?.world_id"
                >当前</small
              ></strong
            ><span>{{ world.scene }}</span
            ><small
              >更新于 {{ formatDate(world.updated_at) }} · 游戏内
              {{ world.clock }} ·
              {{ world.mode === "guided" ? "流程型" : "开放型" }}</small
            ></button
          ><button
            class="delete-button"
            :disabled="dialogBusy"
            :aria-label="`删除存档 ${world.name}`"
            @click="confirmDelete(world)"
          >
            删除
          </button>
        </div>
        <div class="modal-actions">
          <button
            class="secondary-button"
            :disabled="
              dialogBusy ||
              !games.some((item) => item.id === currentWorld?.game_id)
            "
            @click="openNewWorld"
          >
            新开一局</button
          ><button
            class="primary-button"
            :disabled="dialogBusy || !!activeRun"
            @click="openCopy"
          >
            另存当前进度
          </button>
        </div>
        <p v-if="activeRun" class="subtle">本轮完成后可以另存。</p>
      </template>
      <template v-else-if="dialog === 'new' || dialog === 'copy'">
        <form @submit.prevent="createOrCopy">
          <fieldset
            :disabled="dialogBusy || (dialog === 'new' && !!pendingCreate)"
          >
            <label
              >存档名称 <small>可直接使用默认名称</small
              ><input
                v-model="newWorld.name"
                :disabled="dialog === 'copy' && !!pendingCopy" /></label
            ><template v-if="dialog === 'new'"
              ><label
                >主角名字<input
                  v-model="newWorld.player_name"
                  :readonly="newGame?.player.editable === false" /></label
              ><label
                >主角简介<textarea
                  v-model="newWorld.player_profile"
                  :readonly="newGame?.player.editable === false"
                  rows="3"
                ></textarea>
              </label>
              <p class="subtle">
                {{ newGame?.title }} ·
                {{ newGame?.mode === "guided" ? "流程型" : "开放型" }} ·
                {{ newWorld.expected_revision }}
              </p>
              <p>{{ newGame?.gameplay }}</p>
              <p class="subtle">{{ newGame?.player.requirements }}</p></template
            >
          </fieldset>
          <button
            v-if="dialog === 'new' && newRevisionConflict"
            type="button"
            class="secondary-button"
            :disabled="dialogBusy"
            @click="refreshNewRevision"
          >
            刷新剧本版本并确认
          </button>
          <p v-if="dialog === 'new' && pendingCreate" class="subtle">
            正在确认原开局请求，请勿重复创建。
          </p>
          <p v-if="dialog === 'copy' && pendingCopy" class="subtle">
            正在确认原另存操作，名称和源存档已固定；不会新建第二份副本。
          </p>
          <div class="modal-actions">
            <button
              type="button"
              class="secondary-button"
              :disabled="dialogBusy"
              @click="closeDialog"
            >
              取消</button
            ><button
              class="primary-button"
              :disabled="
                dialogBusy || (dialog === 'new' && newRevisionConflict)
              "
            >
              {{
                dialogBusy
                  ? "正在处理…"
                  : dialog === "copy"
                    ? pendingCopy
                      ? "继续确认另存"
                      : "创建独立存档"
                    : pendingCreate
                      ? "继续确认开局"
                      : status?.ready
                        ? "开始游玩"
                        : "连接模型并开始"
              }}
            </button>
          </div>
        </form>
      </template>
      <template v-else-if="dialog === 'delete'"
        ><p>确认删除“{{ deleteCandidate?.name }}”？</p>
        <p class="subtle">此存档及其独立进度会从本机删除，其他存档不受影响。</p>
        <div class="modal-actions">
          <button
            class="secondary-button"
            :disabled="dialogBusy"
            @click="closeDialog"
          >
            保留存档</button
          ><button
            class="danger-button"
            :disabled="dialogBusy"
            @click="removeWorld"
          >
            {{ dialogBusy ? "正在删除…" : "确认删除" }}
          </button>
        </div></template
      >
      <template v-else-if="dialog === 'scene'"
        ><span class="eyebrow">当前场景</span>
        <h3>{{ currentWorld?.scene }}</h3>
        <p class="subtle">游戏内时间 · {{ currentWorld?.clock }}</p>
        <h3>眼前的人</h3>
        <button
          v-for="character in characters"
          :key="character.entity_id"
          class="character-row"
          :class="{ selected: session.addressee === character.entity_id }"
          @click="chooseCharacter(character)"
        >
          <span class="avatar">{{ character.name.slice(0, 1) }}</span
          ><span
            ><strong>{{ character.name }}</strong
            ><small>{{ character.role }}</small></span
          >
        </button>
        <p v-if="!characters.length" class="subtle">
          眼前暂时没有可交谈的人物。
        </p></template
      >
    </AppDialog>
  </main>
</template>
