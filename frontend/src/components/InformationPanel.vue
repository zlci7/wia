<script setup lang="ts">
import { computed } from "vue";
import type { Character, KnownLocation, PackBystander, PublicItem, PublicState, WorldSummary } from "../types";
import { formatWorldClock, stateDisplayValue } from "../worldInformation";
import StatePanel from "./StatePanel.vue";

const props = defineProps<{
  tab: "character" | "inventory" | "map" | "people";
  world: WorldSummary;
  player: { name: string; profile: string };
  knownLocations: KnownLocation[];
  states: PublicState[];
  items: PublicItem[];
  characters: Character[];
  bystanders: PackBystander[];
  addressee: string;
}>();
const emit = defineEmits<{
  "update:tab": [tab: typeof props.tab];
  "choose-character": [character: Character];
}>();
const tabs = [
  { id: "character", label: "角色" },
  { id: "inventory", label: "背包" },
  { id: "map", label: "地图" },
  { id: "people", label: "人物" },
] as const;
const playerStates = computed(() => props.states.filter(state => state.entity_id === "player" && !state.currency));
const conditions = computed(() => playerStates.value.filter(state => state.category !== "skill"));
const skills = computed(() => playerStates.value.filter(state => state.category === "skill"));
const balances = computed(() => props.states.filter(state => state.entity_id === "player" && state.currency));
const inventory = computed(() => props.items.filter(item => item.holder_id === "player"));
const locationItems = computed(() => props.world.location?.id ? props.items.filter(item => item.location_id === props.world.location!.id) : []);
const locations = computed<KnownLocation[]>(() => {
  if (props.knownLocations.length) return props.knownLocations;
  return [
    ...(props.world.location ? [props.world.location] : []),
    ...(props.world.adjacent_locations ?? []),
  ].filter((location, index, all) => all.findIndex(item => item.id === location.id) === index)
    .map(location => ({ ...location, kind: "place", connections: [] }));
});
const locationGroups = computed(() => {
  const regions = locations.value.filter(location => location.kind === "region");
  const groups = regions.map(region => ({
    id: region.id,
    name: region.name,
    description: region.description,
    places: locations.value.filter(location => location.kind === "place" && location.parent === region.id),
  }));
  const remaining = locations.value.filter(location => location.kind === "place" && !regions.some(region => region.id === location.parent));
  if (remaining.length) groups.push({ id: "", name: "已知地点", description: undefined, places: remaining });
  return groups;
});
function connections(location: KnownLocation) {
  return (location.connections ?? []).map(id => locations.value.find(item => item.id === id)?.name).filter(Boolean).join("、");
}
function characterItems(entityID: string) {
  return props.items.filter(item => item.holder_id === entityID);
}
function characterStates(entityID: string) {
  return props.states.filter(state => state.entity_id === entityID);
}
function adjacent(location: KnownLocation) {
  return props.world.adjacent_locations?.some(item => item.id === location.id) ?? false;
}
</script>

<template>
  <nav class="information-tabs" aria-label="资料栏目">
    <button v-for="entry in tabs" :key="entry.id" type="button" :aria-pressed="tab === entry.id" :class="{ selected: tab === entry.id }" @click="emit('update:tab', entry.id)">{{ entry.label }}</button>
  </nav>
  <section class="information-content" :aria-label="tabs.find(entry => entry.id === tab)?.label">
    <template v-if="tab === 'character'">
      <h3>{{ player.name || '角色资料' }}</h3>
      <p v-if="player.profile" class="player-profile">{{ player.profile }}</p>
      <template v-if="conditions.length">
        <h4>当前状态</h4>
        <StatePanel :states="conditions" :characters="characters" />
      </template>
      <template v-if="skills.length">
        <h4>已知技能</h4>
        <StatePanel :states="skills" :characters="characters" />
      </template>
      <p v-if="!playerStates.length" class="subtle">当前没有可查看的状态记录。</p>
    </template>
    <template v-else-if="tab === 'inventory'">
      <template v-if="balances.length">
        <h3>现金</h3>
        <dl class="status-list balance-list">
          <div v-for="balance in balances" :key="balance.state_id">
            <dt>{{ balance.name }}<small v-if="balance.currency?.name">{{ balance.currency.name }}</small></dt>
            <dd>{{ stateDisplayValue(balance) }}</dd>
          </div>
        </dl>
      </template>
      <h3>随身物品</h3>
      <ul v-if="inventory.length" class="item-list">
        <li v-for="item in inventory" :key="item.instance_id">
          <strong>{{ item.name }}</strong>
          <span v-if="item.description">{{ item.description }}</span>
        </li>
      </ul>
      <p v-else class="subtle">你当前没有持有物品。</p>
    </template>
    <template v-else-if="tab === 'map'">
      <h3>{{ world.location?.name ?? world.scene }}</h3>
      <p class="subtle world-clock">{{ world.calendar?.era ? `${world.calendar.era} · ` : '' }}{{ formatWorldClock(world.clock) }}</p>
      <p v-if="world.location?.description" class="subtle">{{ world.location.description }}</p>
      <section v-for="group in locationGroups" :key="group.id" class="location-group">
        <h4>{{ group.name }}</h4>
        <p v-if="group.description" class="subtle">{{ group.description }}</p>
        <ul class="location-list">
          <li v-for="location in group.places" :key="location.id" :class="{ 'current-location': location.id === world.location?.id }">
            <strong>{{ location.name }} <small v-if="location.id === world.location?.id">当前位置</small></strong>
            <span v-if="location.description" class="location-description">{{ location.description }}</span>
            <small v-if="adjacent(location)">可从当前位置前往</small>
            <small v-if="connections(location)">通往：{{ connections(location) }}</small>
            <div v-if="location.id === world.location?.id && locationItems.length" class="location-items">
              <h5>眼前物品</h5>
              <ul class="item-list">
                <li v-for="item in locationItems" :key="item.instance_id">
                  <strong>{{ item.name }}</strong>
                  <span v-if="item.description">{{ item.description }}</span>
                  <small>位于{{ location.name }}</small>
                </li>
              </ul>
            </div>
          </li>
        </ul>
      </section>
      <p v-if="!locations.length" class="subtle">当前没有可查看的地点资料。</p>
    </template>
    <template v-else-if="tab === 'people'">
      <h3>眼前的人</h3>
      <article v-for="character in characters" :key="character.entity_id" class="person-information">
        <button type="button" class="character-row" :class="{ selected: addressee === character.entity_id }" :aria-pressed="addressee === character.entity_id" @click="emit('choose-character', character)">
          <span class="avatar">{{ character.name.slice(0, 1) }}</span>
          <span><strong>{{ character.name }}</strong><small>{{ character.role }}</small><small>选择交谈对象</small></span>
        </button>
        <StatePanel :states="characterStates(character.entity_id)" :characters="characters" />
        <ul v-if="characterItems(character.entity_id).length" class="item-list">
          <li v-for="item in characterItems(character.entity_id)" :key="item.instance_id">
            <strong>{{ item.name }}</strong>
            <span v-if="item.description">{{ item.description }}</span>
            <small>由{{ character.name }}持有</small>
          </li>
        </ul>
      </article>
      <p v-if="!characters.length" class="subtle">眼前暂时没有可交谈的人物。</p>
      <template v-if="bystanders.length">
        <h4>场景里的路人</h4>
        <div v-for="bystander in bystanders" :key="bystander.bystander_id" class="bystander-row">
          <strong>{{ bystander.name }}</strong><p v-if="bystander.description" class="subtle">{{ bystander.description }}</p>
        </div>
      </template>
    </template>
  </section>
</template>
