<script setup lang="ts">
defineProps<{ items: string[]; disabled: boolean }>();
defineEmits<{ choose: [text: string] }>();
</script>

<template>
  <div class="suggestion-items">
    <button v-for="(item,index) in items" :key="index" type="button" :disabled="disabled" @click="$emit('choose', item)">
      <span class="suggestion-copy">{{ item }}</span><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12h14m-5-5 5 5-5 5"/></svg>
    </button>
  </div>
</template>

<style scoped>
.suggestion-items { display: grid; grid-template-columns: minmax(0, 1fr); gap: 8px; }
.suggestion-items button { display: flex; align-items: center; gap: 14px; min-width: 0; width: 100%; min-height: 56px; text-align: left; font: inherit; font-size: 14px; line-height: 1.75; white-space: normal; overflow-wrap: anywhere; color: var(--ink); background: var(--soft-panel); border: 0; border-radius: 20px; padding: 12px 16px; }
.suggestion-items button:hover:not(:disabled) { background: var(--paper-deep); }
.suggestion-items button:disabled { cursor: default; opacity: .55; }
.suggestion-copy { flex: 1; min-width: 0; }
.suggestion-items svg { width: 17px; height: 17px; flex-shrink: 0; fill: none; stroke: var(--accent); stroke-width: 1.7; stroke-linecap: round; stroke-linejoin: round; }
@media (max-width: 720px) { .suggestion-items button { padding: 12px 14px; gap: 9px; font-size: 13px; } }
</style>
