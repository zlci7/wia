<script setup lang="ts">
import type { Character, PublicState } from "../types";

const props = defineProps<{
  states: PublicState[];
  characters: Pick<Character, "entity_id" | "name">[];
}>();

function label(state: PublicState) {
  if (state.entity_id === "player") return state.name;
  const owner = props.characters.find((character) => character.entity_id === state.entity_id);
  return `${owner?.name ?? state.entity_id} · ${state.name}`;
}

function value(state: PublicState) {
  if (state.value.type === "integer") return `${state.value.integer ?? 0}${state.unit ?? ""}`;
  if (state.value.type === "boolean") return state.value.boolean ? "是" : "否";
  return state.value.enum ?? "";
}
</script>

<template>
  <dl v-if="states.length" class="status-list">
    <div v-for="state in states" :key="`${state.entity_id}:${state.state_id}`">
      <dt>{{ label(state) }}</dt>
      <dd>{{ value(state) }}</dd>
    </div>
  </dl>
</template>
