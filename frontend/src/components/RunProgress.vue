<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { fetchRunProgress } from '../api';
import type { Character, Run, RunProgress } from '../types';

const props = defineProps<{ worldID: string; run: Run; characters: Character[] }>();
const progress = ref<RunProgress>();
const thinkingOpen = ref(false);
const error = ref('');
let generation = 0, timer: ReturnType<typeof setTimeout> | undefined;
const purposes: Record<string, string> = {
  intent: '理解你的行动', npc: '人物回应', scene: '组织场景', coordination: '衔接行动',
  narration: '撰写正文', plot: '世界变化', memory_digest: '整理经历', event_generation: '世界变化',
};
const phases: Record<string, string> = {
  waiting: '等待模型响应', thinking: '思考中', writing: '生成回应', received: '响应已返回', failed: '响应未完成',
};
function callTitle(call: RunProgress['calls'][number]) {
  const name = props.characters.find(character => character.entity_id === call.recipient)?.name;
  return `${purposes[call.purpose] ?? '组织回应'}${name ? ` · ${name}` : ''}`;
}
const active = computed(() => progress.value?.calls.filter(call => ['waiting', 'thinking', 'writing'].includes(call.phase)) ?? []);
const thinkingCalls = computed(() => progress.value?.calls.filter(call => call.reasoning_chars > 0 || ['waiting', 'thinking', 'writing'].includes(call.phase)) ?? []);
const statusText = computed(() => active.value.length
  ? active.value.map(call => `${callTitle(call)}：${phases[call.phase]}`).join('；')
  : progress.value?.calls.length ? '整理与校验本轮回应' : '准备本轮回应');
const outputChars = computed(() => progress.value?.calls.reduce((total, call) => total + call.output_chars, 0) ?? 0);

async function read(ticket: number) {
  const includeThinking = thinkingOpen.value;
  try {
    const next = await fetchRunProgress(props.worldID, props.run.run_id, includeThinking);
    if (ticket !== generation || includeThinking !== thinkingOpen.value) return;
    progress.value = next;
    error.value = '';
  } catch {
    if (ticket === generation) error.value = '进度暂时无法更新，正在确认本轮状态。';
  } finally {
    if (ticket === generation) timer = setTimeout(() => void read(ticket), 1500);
  }
}
function toggleThinking(event: Event) {
  const open = (event.target as HTMLDetailsElement).open;
  if (thinkingOpen.value === open) return;
  thinkingOpen.value = open;
  if (!open && progress.value) {
    progress.value = { ...progress.value, calls: progress.value.calls.map(call => ({ ...call, reasoning: undefined })) };
  }
  clearTimeout(timer);
  void read(++generation);
}
watch(() => `${props.worldID}:${props.run.run_id}`, () => {
  clearTimeout(timer);
  progress.value = undefined;
  error.value = '';
  void read(++generation);
}, { immediate: true });
onUnmounted(() => { generation++; clearTimeout(timer); });
</script>

<template>
  <div class="run-progress">
    <p class="run-phase" role="status">{{ statusText }}</p>
    <p class="subtle">本轮最多等待 {{ progress?.budget_seconds ?? 300 }} 秒<template v-if="outputChars"> · 已收到 {{ outputChars }} 字响应</template></p>
    <p v-if="progress?.transport === 'non_stream'" class="subtle">非流式：思考文本随完整响应返回。</p>
    <p v-if="error" class="subtle" role="status">{{ error }}</p>
    <details class="run-thinking" @toggle="toggleThinking">
      <summary>模型思考 · 可能剧透</summary>
      <template v-if="thinkingOpen">
        <p class="subtle">原始思考可能包含人物秘密和幕后设定；生成中的内容尚未成为游戏事实。</p>
        <section v-for="call in thinkingCalls" :key="call.id" class="thinking-call">
          <p class="thinking-heading">{{ callTitle(call) }} · {{ phases[call.phase] }}</p>
          <pre v-if="call.reasoning" class="thinking-text">{{ call.reasoning }}</pre>
          <p v-else class="subtle">{{ call.phase === 'waiting' ? '等待服务返回内容。' : call.phase === 'thinking' ? '正在接收思考内容。' : '服务尚未提供思考文本。' }}</p>
          <p v-if="call.reasoning_limited" class="subtle">思考文本已达到展示长度上限，生成继续进行。</p>
        </section>
        <p v-if="!thinkingCalls.length" class="subtle">服务尚未提供思考文本。</p>
      </template>
    </details>
  </div>
</template>

<style scoped>
.run-progress { margin-top: 12px; min-width: 0; }
.run-phase { font-size: .92rem; }
.run-thinking { margin-top: 12px; border-top: 1px solid var(--line); padding-top: 10px; }
.run-thinking summary { cursor: pointer; color: var(--muted); font-size: .88rem; }
.run-thinking summary:focus-visible { outline: 2px solid var(--accent); outline-offset: 4px; border-radius: 4px; }
.thinking-call { margin-top: 12px; }
.thinking-heading { font-size: .85rem; color: var(--muted); }
.thinking-text { margin: 0; max-height: 260px; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; font: inherit; font-size: .85rem; line-height: 1.65; color: var(--muted); }
</style>
