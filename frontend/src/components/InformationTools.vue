<script setup lang="ts">
defineProps<{ active?: 'character' | 'inventory' | 'map' | 'people' }>();
const emit = defineEmits<{ choose: [tab: 'character' | 'inventory' | 'map' | 'people'] }>();
</script>

<template>
  <nav class="information-entry glass" aria-label="世界与角色资料">
    <button id="wia-information-character" type="button" class="information-tool" :aria-expanded="active === 'character'" aria-controls="wia-information-panel" @click="emit('choose', 'character')">
      <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="8" r="3.5"/><path d="M5 20v-2a7 7 0 0 1 14 0v2"/></svg><span>角色</span>
    </button>
    <button type="button" class="information-tool" :aria-expanded="active === 'inventory'" aria-controls="wia-information-panel" @click="emit('choose', 'inventory')">
      <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="5" y="6" width="14" height="15" rx="4"/><path d="M9 6V4a3 3 0 0 1 6 0v2M8 14h8v4H8z"/></svg><span>背包</span>
    </button>
    <button type="button" class="information-tool" :aria-expanded="active === 'map'" aria-controls="wia-information-panel" @click="emit('choose', 'map')">
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m3 6 6-3 6 3 6-3v15l-6 3-6-3-6 3zM9 3v15M15 6v15"/></svg><span>地图</span>
    </button>
    <button type="button" class="information-tool" :aria-expanded="active === 'people'" aria-controls="wia-information-panel" @click="emit('choose', 'people')">
      <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="9" cy="8" r="3"/><path d="M3 20v-2a6 6 0 0 1 12 0v2M16 5a3 3 0 0 1 0 6M18 14a5 5 0 0 1 3 4v2"/></svg><span>人物</span>
    </button>
  </nav>
</template>
