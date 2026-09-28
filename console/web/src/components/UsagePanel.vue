<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { fetchUsage } from '../api';
import type { UsagePage } from '../types';
const props = defineProps<{ worldId?: string; worldName?: string }>();
const scope = ref('all'), data = ref<UsagePage>(), loading = ref(false), error = ref('');
let generation = 0;
const number = (value: number | null | undefined) => value == null ? '未知' : value.toLocaleString('zh-CN');
const totals = computed(() => data.value?.totals);
const purposes: Record<string,string> = { intent:'理解输入', npc:'人物决策', coordination:'场景协调', narration:'故事正文', plot:'世界事件', memory_digest:'记忆整理', suggestions:'行动建议', connection:'连接验证', event_generation:'事件生成' };
async function load(more = false) {
  const ticket = ++generation;
  loading.value = true; error.value = '';
  try {
    const result = await fetchUsage(scope.value === 'world' ? props.worldId : '', more ? data.value?.next_before_id : 0);
    if (ticket !== generation) return;
    data.value = more && data.value ? { ...result, calls: [...data.value.calls, ...result.calls] } : result;
  } catch { if (ticket === generation) error.value = '用量暂时无法读取，请重试。'; }
  finally { if (ticket === generation) loading.value = false; }
}
watch(scope, () => { data.value = undefined; void load(); });
onMounted(() => void load());
onUnmounted(() => { generation++; });
</script>

<template>
  <section class="usage-panel">
    <div class="usage-controls">
      <label>统计范围<select v-model="scope"><option value="all">本账户全部记录</option><option v-if="worldId" value="world">当前存档 · {{ worldName }}</option></select></label>
      <button class="quiet-button" :disabled="loading" @click="load()">{{ loading ? '读取中…' : '刷新' }}</button>
    </div>
    <p class="subtle">从启用用量记录后开始累计，历史费用不回填。包括失败及重试调用；另存不重复计数。这里只展示用量，不是供应商账单。</p>
    <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
    <template v-if="totals">
      <div class="usage-grid">
        <article><span>输入 token · 已知合计</span><strong>{{ totals.input_known ? number(totals.input_tokens) : '未知' }}</strong><small>{{ totals.input_known }} / {{ totals.calls }} 次有数据</small></article>
        <article><span>输出 token · 含推理</span><strong>{{ totals.output_known ? number(totals.output_tokens) : '未知' }}</strong><small>{{ totals.output_known }} / {{ totals.calls }} 次有数据</small></article>
        <article><span>其中推理 token</span><strong>{{ totals.reasoning_known ? number(totals.reasoning_tokens) : '未知' }}</strong><small>{{ totals.reasoning_known }} / {{ totals.calls }} 次有数据</small></article>
        <article><span>已知请求的缓存命中率</span><strong>{{ totals.cache_hit_rate == null ? '未知' : (totals.cache_hit_rate * 100).toFixed(1) + '%' }}</strong><small>{{ totals.cache_known }} / {{ totals.calls }} 次有缓存数据</small></article>
      </div>
      <p class="subtle">{{ totals.calls }} 次调用 · {{ totals.failed }} 次调用失败 · {{ totals.unconfirmed }} 次结果未确认。命中 {{ totals.cache_known ? number(totals.cache_hit_tokens) : '未知' }} / 未命中 {{ totals.cache_known ? number(totals.cache_miss_tokens) : '未知' }} token。命中率按已知命中与未命中量加权，缺失不计为零。</p>
      <p v-if="!totals.calls" class="empty">还没有用量记录。下一次故事或记忆生成后，可在这里查看。</p>
      <div v-else class="usage-table" tabindex="0" aria-label="模型调用明细，可横向滚动">
        <table><thead><tr><th>时间 / 阶段</th><th>模型 / 状态</th><th>输入</th><th>输出</th><th>其中推理</th><th>命中 / 未命中</th><th>耗时</th></tr></thead>
          <tbody><tr v-for="call in data?.calls" :key="call.id">
            <td>{{ new Date(call.started_at).toLocaleString('zh-CN') }}<small>{{ purposes[call.purpose] ?? call.purpose }} · 尝试 {{ call.attempt }}</small><small>{{ call.run_id || '辅助调用' }}</small></td>
            <td>{{ call.model || '未报告模型' }}<small>{{ call.status === 'returned' ? '调用返回' : call.status === 'failed' ? '调用失败' : '结果未确认' }}{{ call.error_code ? ' · ' + call.error_code : '' }}</small></td>
            <td>{{ number(call.input_tokens) }}</td><td>{{ number(call.output_tokens) }}</td><td>{{ number(call.reasoning_tokens) }}</td>
            <td>{{ number(call.cache_hit_tokens) }} / {{ number(call.cache_miss_tokens) }}</td><td>{{ call.status === 'unconfirmed' ? '未知' : (call.elapsed_ms / 1000).toFixed(1) + ' 秒' }}</td>
          </tr></tbody>
        </table>
      </div>
      <p class="subtle">“调用返回”仅表示模型请求返回，不代表故事回合已成功保存。取消或断连时，供应商可能已计费但未返回用量。</p>
      <button v-if="data?.next_before_id" class="quiet-button" :disabled="loading" @click="load(true)">加载更早调用</button>
    </template>
  </section>
</template>

<style scoped>
.usage-controls { display:flex; align-items:end; gap:1rem; }
.usage-controls label { flex:1; }
.usage-grid { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:1rem; margin:1.5rem 0; }
.usage-grid article { border:1px solid var(--border, #dfd4c5); border-radius:12px; padding:1rem; }
.usage-grid strong { display:block; font-size:1.8rem; margin:.4rem 0; }
small { display:block; opacity:.7; overflow-wrap:anywhere; }
.usage-table { overflow:auto; }
table { width:100%; border-collapse:collapse; font-size:13px; }
th,td { text-align:left; padding:.7rem; border-bottom:1px solid #dfd4c5; min-width:5rem; vertical-align:top; }
@media(max-width:480px) { .usage-grid { gap:.5rem; } .usage-grid article { padding:.7rem; } .usage-grid strong { font-size:1.4rem; } }
</style>
