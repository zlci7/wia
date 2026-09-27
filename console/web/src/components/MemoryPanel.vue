<script lang="ts">
import type { CorrectionRequest } from '../types';
const pendingCorrections = new Map<string, CorrectionRequest>();
</script>
<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue';
import { correctMemory, fetchMemory, rebuildMemory } from '../api';
import { ApiError, type MemoryRecord, type MemoryView } from '../types';
const props = defineProps<{ worldID: string; worldName: string; characters: { entity_id: string; name: string }[] }>();
const emit = defineEmits<{ busy: [value: boolean]; updated: [] }>();
const data = ref<MemoryView>(), author = ref(false), scope = ref('player'), error = ref(''), notice = ref('');
const loading = ref(false), writing = ref(false), editing = ref<MemoryRecord>(), draft = ref('');
const editor = ref<HTMLTextAreaElement>();
const pending = ref(pendingCorrections.get(props.worldID));
let active = true, generation = 0, editingEpoch = 0, timer: ReturnType<typeof setTimeout> | undefined;
const labels: Record<string, string> = { event: '世界事件', character: '人物设定', perception: '个人感知', subjective: '主观状态', digest: '连续回顾' };
const rebuilding = computed(() => !!data.value && !['completed', 'superseded'].includes(data.value.job.status));
function name(id: string) { return id === 'player' ? '我的回顾' : id === 'author' ? '世界事件（剧透）' : props.characters.find(c => c.entity_id === id)?.name ?? id; }
function describe(e: unknown) {
  if (e instanceof ApiError) return ({ version_conflict: '此存档已更新。请刷新资料，再核对并提交纠正。', world_busy: '故事正在生成或另存，请完成后再纠正。', memory_rebuilding: '回顾正在重建，请稍候。', invalid_request: '纠正对象已变化，请刷新后重新选择。' } as Record<string,string>)[e.code] ?? e.message;
  return e instanceof Error ? e.message : String(e);
}
async function load(earlier = false) {
  const ticket = ++generation, id = props.worldID;
  loading.value = true; error.value = ''; clearTimeout(timer);
  try {
    const next = await fetchMemory(id, author.value, scope.value, earlier ? data.value?.next_before_seq : 0);
    if (!active || ticket !== generation || id !== props.worldID) return;
    if (earlier && data.value && next.context_epoch !== data.value.context_epoch) throw new Error('资料已更新，请刷新后重新加载历史。');
    if (earlier && data.value) { next.sources = [...data.value.sources, ...next.sources]; next.records = [...data.value.records, ...next.records.filter(r => !data.value!.records.some(old => old.kind === r.kind && old.target_id === r.target_id))]; }
    data.value = next;
    if (rebuilding.value && next.job.status !== 'failed' && !editing.value) timer = setTimeout(() => void load(), 1500);
  } catch (e) { if (active && ticket === generation) error.value = describe(e); }
  finally { if (active && ticket === generation) loading.value = false; }
}
function choose(item: MemoryRecord) { editing.value = { ...item }; editingEpoch = data.value!.context_epoch; draft.value = item.content; notice.value = ''; clearTimeout(timer); void nextTick(() => { editor.value?.focus(); editor.value?.scrollIntoView({ block: 'center' }); }); }
function cancelEdit() { editing.value = undefined; draft.value = ''; }
async function save() {
  if (writing.value || !data.value || (!pending.value && (!editing.value || !draft.value.trim()))) return;
  const id = props.worldID;
  const payload = pending.value ?? { request_key: crypto.randomUUID(), expected_context_epoch: editingEpoch, kind: editing.value!.kind, scope: editing.value!.scope, target_id: editing.value!.target_id, replacement: draft.value.trim() };
  pending.value = payload; pendingCorrections.set(id, payload); writing.value = true; emit('busy', true); error.value = '';
  try {
    await correctMemory(id, payload);
    pendingCorrections.delete(id);
    if (!active || id !== props.worldID) return;
    pending.value = undefined; cancelEdit(); notice.value = '纠正已记录。原始正文保留，新的回顾将在重建后生效。'; emit('updated'); await load();
  } catch(e) {
    if (!active || id !== props.worldID) return;
    if (e instanceof ApiError && e.status >= 400 && e.status < 500) { pendingCorrections.delete(id); pending.value = undefined; }
    error.value = describe(e);
  } finally { if(active) { writing.value = false; emit('busy', false); } }
}
async function retry() {
  if (!data.value || writing.value) return;
  writing.value = true; emit('busy', true);
  try { await rebuildMemory(props.worldID, data.value.context_epoch); if(active) await load(); }
  catch(e) { if(active) error.value = describe(e); }
  finally { if(active) { writing.value = false; emit('busy', false); } }
}
function changeScope() { cancelEdit(); data.value = undefined; void load(); }
onMounted(() => void load());
onUnmounted(() => { active = false; generation++; clearTimeout(timer); });
</script>

<template>
  <section class="memory-panel">
    <p class="subtle">{{ worldName }} · 原始正文始终保留，纠正影响后续理解。</p>
    <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <p v-if="pending && !writing" class="inline-error">上次提交结果尚未确认。<button class="secondary-button" @click="save">确认上次纠正</button></p>
    <div class="memory-toolbar">
      <label v-if="author">查看范围 <select v-model="scope" :disabled="writing || !!pending" @change="changeScope"><option v-for="id in data?.scopes ?? ['player']" :key="id" :value="id">{{ name(id) }}</option></select></label>
      <button class="secondary-button" :disabled="loading || writing" @click="load()">刷新资料</button>
    </div>
    <p v-if="loading && !data" role="status">正在读取经历…</p>
    <template v-if="data">
      <p v-if="rebuilding" role="status">{{ data.job.status === 'failed' ? '回顾重建未完成，原始记录安全保留。' : '正在按有效来源重建回顾，完成后可以继续故事。' }} <button v-if="data.job.status === 'failed'" class="secondary-button" :disabled="writing" @click="retry">重试重建</button></p>
      <template v-if="scope !== 'author'">
        <h3>{{ name(scope) }}</h3>
        <p class="memory-prose">{{ data.digest.content || '尚未形成连续回顾，近期经历完整保留在下方。' }}</p>
        <p class="subtle">回顾覆盖至个人记录 {{ data.digest.through_seq }} · 版本 {{ data.context_epoch }}</p>
      </template>
      <form v-if="editing" class="memory-editor" @submit.prevent="save">
        <label>{{ labels[editing.kind] }} · {{ name(editing.scope) }}<textarea ref="editor" v-model="draft" rows="6" maxlength="8000" :disabled="writing || !!pending" /></label>
        <p class="subtle">只纠正选中的资料，不重演已发生的故事。世界事件的局部感知会撤回旧解释，不向旁观者补发秘密。</p>
        <div class="modal-actions"><button type="button" class="secondary-button" :disabled="writing || !!pending" @click="cancelEdit">取消</button><button class="primary-button" :disabled="writing || !draft.trim()">{{ writing ? '正在记录…' : '记录纠正' }}</button></div>
      </form>
      <details v-if="data.records.length" class="memory-records">
        <summary>可纠正资料 · {{ data.records.length }} 项</summary>
        <article v-for="item in data.records" :key="`${item.kind}:${item.target_id}`"><div class="memory-record-heading"><strong>{{ labels[item.kind] }}</strong><button class="secondary-button" :disabled="writing || !!pending || rebuilding" @click="choose(item)">纠正</button></div><p class="memory-prose">{{ item.content || '（空）' }}</p><small>{{ item.target_id }}</small></article>
      </details>
      <h3>已提交经历</h3>
      <details v-if="data.corrections?.length" class="memory-records"><summary>当前范围的纠正记录</summary><article v-for="item in data.corrections" :key="item.epoch"><strong>{{ labels[item.kind] }} · 版本 {{ item.epoch }}</strong><p class="memory-prose">{{ item.replacement }}</p><details><summary>原始内容</summary><p class="memory-prose">{{ item.original }}</p></details></article></details>
      <p v-if="!data.sources.length" class="subtle">暂无此范围的记录。</p>
      <article v-for="source in data.sources" :key="source.id" class="memory-source"><p class="memory-prose">{{ source.content }}</p><details><summary class="subtle">来源 · {{ source.seq }}</summary><small>{{ source.id }} · {{ source.actor }} · {{ source.kind }}<br>{{ source.created_at }}</small></details></article>
      <button v-if="data.has_more" class="secondary-button" :disabled="loading || writing" @click="load(true)">{{ loading ? '正在读取…' : '加载更早经历' }}</button>
    </template>
    <details v-if="!author" class="memory-spoiler"><summary>创作与纠正（包含剧透）</summary><p>此视图包含人物私密设定、个人记忆与世界真相，不属于主角已知信息。</p><button class="secondary-button" :disabled="writing || !!pending" @click="author = true; changeScope()">我了解剧透风险，进入创作视图</button></details>
  </section>
</template>

<style scoped>
.memory-toolbar,.memory-record-heading{display:flex;gap:12px;align-items:center;justify-content:space-between;flex-wrap:wrap}
.memory-prose{white-space:pre-wrap;line-height:1.8;overflow-wrap:anywhere}
.memory-source,.memory-records article{padding:16px 0;border-bottom:1px solid var(--line,#ddd4c8)}
.memory-editor{margin:18px 0;padding:16px;background:var(--paper,#faf6ef);border:1px solid var(--line,#ddd4c8);border-radius:12px}
.memory-editor label,.memory-editor textarea{display:block;width:100%}.memory-editor textarea{margin-top:8px;resize:vertical}
.memory-spoiler,.memory-records{margin:20px 0}.memory-panel small{overflow-wrap:anywhere}.memory-panel h3{margin-top:24px}
</style>
