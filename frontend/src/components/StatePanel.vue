<script setup lang="ts">
import type { Character, PublicState } from "../types";
import { stateDisplayValue } from "../worldInformation";

const props = defineProps<{
  states: PublicState[];
  characters: Pick<Character, "entity_id" | "name">[];
}>();

function label(state: PublicState) {
  if (state.entity_id === "player") return state.name;
  const owner = props.characters.find((character) => character.entity_id === state.entity_id);
  return `${owner?.name ?? state.entity_id} · ${state.name}`;
}

</script>

<template>
  <dl v-if="states.length" class="status-list">
    <div v-for="state in states" :key="`${state.entity_id}:${state.state_id}`">
      <dt>{{ label(state) }}<small v-if="state.category === 'skill' && state.description">{{ state.description }}</small></dt>
      <dd>{{ stateDisplayValue(state) }}</dd>
    </div>
  </dl>
</template>
