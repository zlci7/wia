<script setup lang="ts">
import { computed } from 'vue';
import type { GameSummary } from '../types';
const props = defineProps<{ options: NonNullable<GameSummary['starting_options']>; disabled: boolean }>();
const selected = defineModel<string>({ required: true });
const profile = computed(() => props.options.find(option => option.id === selected.value)?.profile);
</script>

<template>
  <fieldset class="starting-options" :disabled="disabled">
    <legend>选择你的开场身份</legend>
    <label v-for="option in options" :key="option.id" class="starting-option" :class="{ selected: selected === option.id }">
      <input v-model="selected" type="radio" name="starting-option" :value="option.id" />
      <span><strong>{{ option.title }}</strong><span class="option-description">{{ option.description }}</span></span>
    </label>
    <p v-if="profile" class="starting-profile">{{ profile }}</p>
  </fieldset>
</template>

<style scoped>
.starting-options { border: 0; padding: 0; margin: 28px 0; min-width: 0; }
legend { margin-bottom: 12px; font-weight: 600; }
.starting-option { display: flex; align-items: flex-start; gap: 12px; padding: 15px 18px; margin-bottom: 9px; border: 1px solid var(--border); border-radius: 22px; background: rgba(255,255,255,.28); cursor: pointer; }
.starting-option.selected { border-color: var(--accent); background: rgba(255,255,255,.55); }
.starting-option input { flex: 0 0 auto; width: 17px; height: 17px; margin: 3px 0 0; accent-color: var(--accent); }
.starting-option:focus-within { outline: 2px solid var(--accent); outline-offset: 3px; }
.option-description { display: block; margin-top: 5px; color: var(--muted); font-size: 13px; line-height: 1.6; }
.starting-profile { color: var(--muted); font-size: 14px; white-space: pre-line; line-height: 1.8; }
.starting-options:disabled { opacity: .65; }
</style>
