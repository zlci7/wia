<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import type { useCreationPlay } from '../useCreationPlay';
import { formatWorldClock } from '../worldInformation';
import ActionSuggestionList from './ActionSuggestionList.vue';
import InformationTools from './InformationTools.vue';
import { creationReadingMessages } from '../creationReading';
const props = defineProps<{ creation: ReturnType<typeof useCreationPlay>; streamingSupported: boolean; informationTab?: 'character' | 'inventory' | 'map' | 'people' }>();
const emit = defineEmits<{ information: [tab: 'character' | 'inventory' | 'map' | 'people'] }>();
const messages = computed(() => props.creation.state.active ? creationReadingMessages(props.creation.state.active, props.creation.busy.value) : []);
const viewport = ref<HTMLElement>(), textarea = ref<HTMLTextAreaElement>();
const bottom = ref(props.creation.state.readerBottom);
let top = props.creation.state.readerTop;
function remember() {
  if (!viewport.value) return;
  top = viewport.value.scrollTop;
  bottom.value = viewport.value.scrollHeight - top - viewport.value.clientHeight < 72;
  props.creation.state.readerTop = top; props.creation.state.readerBottom = bottom.value;
}
function restore() {
  if (!viewport.value) return;
  viewport.value.scrollTop = bottom.value ? viewport.value.scrollHeight : top;
}
function latest() { bottom.value = true; restore(); }
function resizeInput() {
  if (!textarea.value) return;
  textarea.value.style.height = 'auto';
  textarea.value.style.height = `${Math.min(textarea.value.scrollHeight, 220)}px`;
}
watch(() => props.creation.state.active?.version, async () => { await nextTick(); restore(); });
watch(() => props.creation.state.draft, async () => { await nextTick(); resizeInput(); });
onMounted(() => { restore(); resizeInput(); });
onUnmounted(remember);
</script>

<template>
  <div v-if="creation.state.active" class="game-layout creation-layout">
    <section class="story-column">
      <div class="reading-heading">
        <div class="world-context">
          <h1 class="visually-hidden">{{ creation.state.active.title }}</h1>
          <strong class="world-clock">{{ formatWorldClock(creation.state.active.clock) }}</strong>
          <span class="world-location">{{ creation.state.active.location }}</span>
        </div>
        <span class="subtle session-lifetime">共创试玩 · 应用退出后会话结束</span>
        <InformationTools :active="informationTab" @choose="emit('information', $event)" />
      </div>
      <div ref="viewport" class="transcript" tabindex="0" aria-label="故事正文" @scroll.passive="remember">
        <div class="reading-flow">
          <div class="history-control"><span class="subtle">故事从这里开始</span></div>
          <article v-for="message in messages" :key="message.seq" :data-message-id="message.message_id" :data-message-seq="message.seq" class="message" :class="message.kind === 'player' ? 'player-message' : 'narrative-message'">
            <div v-if="message.content" class="message-content">{{ message.content }}</div>
            <p v-else class="generation-placeholder" role="status">正在等待正文…</p>
          </article>
          <section class="suggestions" aria-label="行动建议">
            <div class="suggestion-heading"><span title="建议单独调用模型，可关闭。">行动建议<span v-if="creation.state.active.suggestions.status === 'generating'"> · 正在准备</span></span>
              <button type="button" class="quiet-button" :disabled="creation.busy.value || creation.state.suggestionWriting || !!creation.state.pending" @click="creation.toggleSuggestions">{{ creation.state.active.suggestions.enabled ? '关闭建议' : '开启建议' }}</button>
            </div>
            <ActionSuggestionList v-if="creation.state.active.suggestions.status === 'ready' && creation.state.active.suggestions.turn === creation.state.active.turn && !creation.busy.value" :items="creation.state.active.suggestions.items" :disabled="!!creation.state.draft.trim() || !!creation.state.pending || creation.state.sending" @choose="creation.choose" />
            <p v-if="creation.state.draft.trim() && creation.state.active.suggestions.status === 'ready'" class="subtle">保留你的输入；清空后可点选建议。</p>
            <p v-if="creation.state.suggestionError || creation.state.active.suggestions.status === 'failed'" class="subtle" role="status">{{ creation.state.suggestionError || '本轮建议未完成，可以继续自由输入。' }} <button type="button" class="quiet-button" :disabled="creation.state.suggestionWriting || creation.busy.value" @click="creation.prepareSuggestions(true)">重试建议</button></p>
          </section>
          <form class="composer glass" @submit.prevent="creation.send">
            <div class="composer-body">
              <div class="composer-tools">
                <span class="composer-label">你的行动</span>
                <label class="advance-control" title="让模型在正文生成前进行深度思考，可能增加等待时间。"><input v-model="creation.state.reasoning" true-value="low" false-value="off" type="checkbox" :disabled="creation.busy.value || creation.state.sending || !!creation.state.pending" />深度思考</label>
                <label class="advance-control"><input v-model="creation.state.advance" type="checkbox" :disabled="creation.busy.value || creation.state.sending || !!creation.state.pending" />允许主动推进剧情</label>
                <label>传输<select v-model="creation.state.transport" aria-label="传输方式" :disabled="creation.busy.value || creation.state.sending || !!creation.state.pending"><option value="non_stream">非流式</option><option value="stream" :disabled="!streamingSupported">流式</option></select></label>
              </div>
              <textarea ref="textarea" v-model="creation.state.draft" aria-label="你的行动" rows="2" maxlength="4000" placeholder="说出你的想法，或描述接下来要做的事…公开或私下，写在行动里。" :disabled="creation.busy.value || creation.state.sending || !!creation.state.pending" @keydown="creation.inputKeys" @input="resizeInput"></textarea>
              <p v-if="creation.state.fromSuggestion" class="subtle suggestion-origin">来自行动建议，可修改后提交。<button type="button" class="quiet-button" @click="creation.state.fromSuggestion = false">作为自由输入</button></p>
              <p v-if="creation.state.error" class="inline-error" role="alert">{{ creation.state.error }} <button type="button" class="quiet-button" @click="creation.refresh">重试连接</button></p>
              <p v-if="creation.state.pending && !creation.state.sending" class="inline-error" role="status">提交结果待确认，输入已保留。<button type="button" class="quiet-button" @click="creation.send">确认原请求</button></p>
            </div>
            <div class="composer-footer">
              <span v-if="creation.busy.value" role="status">正在创作 · {{ creation.waitingSeconds.value }} 秒</span>
              <span v-else>Ctrl + Enter 提交<span v-if="creation.state.active.run?.status === 'completed'"> · 本轮 {{ (creation.state.active.run.elapsed_ms / 1000).toFixed(1) }} 秒</span></span>
              <button v-if="creation.busy.value" type="button" class="quiet-button" :disabled="creation.state.cancelling" @click="creation.cancel">{{ creation.state.cancelling ? '正在取消…' : '取消生成' }}</button>
              <button v-else type="submit" class="primary-button" :disabled="!creation.canSubmit.value">{{ creation.state.sending ? '正在提交…' : '继续故事' }}</button>
            </div>
          </form>
        </div>
      </div>
      <div v-if="!bottom" class="latest-row"><button type="button" class="secondary-button" @click="latest">回到最新</button></div>
    </section>
  </div>
  <div v-else class="status-banner error" role="alert">{{ creation.state.error || '试玩会话已结束，请返回剧本页面重新开始。' }}</div>
</template>

<style scoped>
.session-lifetime { font-size: 12px; }
.suggestions { margin: 34px 0 22px; }
.suggestion-heading { display: flex; justify-content: space-between; align-items: center; gap: 12px; color: var(--muted); font-size: 13px; margin-bottom: 10px; }
.suggestion-heading button { padding: 4px 10px; }
.suggestions p { font-size: 12px; margin: 8px 0 0; }
.composer-tools .advance-control { display: flex; align-items: center; gap: 7px; margin: 0; }
.advance-control input { width: 16px; height: 16px; accent-color: var(--accent); padding: 0; }
.generation-placeholder { color: var(--muted); font-size: 13px; margin: 0; }
@media (max-width: 720px) { .reading-heading { flex-wrap: wrap; } .session-lifetime { width: 100%; } }
</style>
