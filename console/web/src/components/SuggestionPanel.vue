<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue';
import { fetchSuggestions, requestSuggestions } from '../api';
import type { SuggestionBasis, SuggestionSet, WorldSummary } from '../types';
const props = defineProps<{ world: WorldSummary; activeRevision: number; busy: boolean; ready: boolean; hasDraft: boolean }>();
const emit = defineEmits<{ choose: [text: string, basis: SuggestionBasis] }>();
const set = ref<SuggestionSet>(), error = ref(''), writing = ref(false);
let generation = 0, timer: ReturnType<typeof setTimeout> | undefined;
function same(b: SuggestionBasis) {
  const w = props.world;
  return b.world_id === w.world_id && b.message_head === w.message_head && b.event_head === w.event_head && b.context_epoch === w.context_epoch && b.revision === w.revision;
}
async function refresh(ticket: number, start = true) {
  try {
    let result = await fetchSuggestions(props.world.world_id);
    if (ticket !== generation || !same(result.basis)) return;
    set.value = result; error.value = '';
    if (start && result.enabled && result.status === 'empty' && props.ready && !props.busy) {
      result = await requestSuggestions(props.world.world_id, result.basis, props.activeRevision);
      if (ticket !== generation || !same(result.basis)) return;
      set.value = result;
    }
    if (result.status === 'generating') timer = setTimeout(() => void refresh(ticket, false), 1500);
  } catch { if (ticket === generation) error.value = '建议暂时不可用，自由输入仍可使用。'; }
}
async function toggle() {
  if (!set.value || writing.value || props.busy) return;
  const ticket = ++generation;
  clearTimeout(timer); writing.value = true;
  try {
    const result = await requestSuggestions(props.world.world_id, set.value.basis, props.activeRevision, !set.value.enabled);
    if (ticket !== generation || !same(result.basis)) return;
    set.value = result;
    await refresh(ticket);
  } catch { if (ticket === generation) error.value = '设置结果暂未确认，请刷新建议状态。'; }
  finally { if (ticket === generation) writing.value = false; }
}
function pick(item: string) {
  if (!set.value || set.value.status !== 'ready' || !same(set.value.basis) || props.busy || props.hasDraft) return;
  emit('choose', item, set.value.basis);
}
watch(() => [props.world.world_id, props.world.message_head, props.world.event_head, props.world.context_epoch, props.world.revision, props.activeRevision, props.busy, props.ready], () => {
  const ticket = ++generation; clearTimeout(timer); set.value = undefined; error.value = ''; writing.value = false;
  void refresh(ticket);
}, { immediate: true });
onUnmounted(() => { generation++; clearTimeout(timer); });
</script>

<template>
  <section class="suggestions" aria-label="行动建议">
    <div class="suggestion-heading"><span title="建议单独调用模型，用量计入模型用量页；关闭后仍可自由输入。">行动建议<span v-if="set?.status === 'generating'"> · 正在准备</span></span>
      <button type="button" class="quiet-button" :disabled="!set || writing || busy" @click="toggle">{{ set?.enabled === false ? '开启建议' : '关闭建议' }}</button>
    </div>
    <div v-if="set?.status === 'ready' && same(set.basis) && !busy" class="suggestion-items">
      <button v-for="(item,index) in set.items" :key="index" type="button" :disabled="hasDraft" @click="pick(item)"><span>{{ index+1 }}</span>{{ item }}</button>
    </div>
    <p v-if="hasDraft && set?.status === 'ready'" class="subtle">保留你的输入；清空后可点选建议。</p>
    <p v-if="error" class="subtle" role="status">{{ error }} <button type="button" class="quiet-button" @click="refresh(generation)">刷新状态</button></p>
    <p v-else-if="set && ['failed','interrupted'].includes(set.status)" class="subtle">本轮建议未完成，可继续自由输入。</p>
  </section>
</template>

<style scoped>
.suggestions { margin-bottom:.65rem; }
.suggestion-heading { display:flex; justify-content:space-between; align-items:center; color:var(--muted,#766f65); font-size:.8rem; }
.suggestion-heading button { padding:.25rem .5rem; }
.suggestion-items { display:flex; gap:.4rem; overflow-x:auto; max-height:6rem; }
.suggestion-items button { flex:1; min-width:130px; text-align:left; font:inherit; font-size:.85rem; color:inherit; background:var(--paper,#fffdf8); border:1px solid var(--border,#ddd2c4); border-radius:8px; padding:.5rem; cursor:pointer; overflow:auto; }
.suggestion-items button:disabled { cursor:default; opacity:.55; }
.suggestion-items span { color:var(--accent,#ae5440); margin-right:.5rem; }
.suggestions p { font-size:.8rem; margin:.2rem 0 0; }
</style>
