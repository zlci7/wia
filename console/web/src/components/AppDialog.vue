<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref } from "vue";

const props = withDefaults(
  defineProps<{
    title: string;
    busy?: boolean;
    destructive?: boolean;
    drawer?: boolean;
    returnFocusTo?: string;
  }>(),
  { busy: false, destructive: false, drawer: false },
);
const emit = defineEmits<{ close: [] }>();
const panel = ref<HTMLElement>();
let previous: HTMLElement | null = null;
let previousOverflow = "";
function close() {
  if (!props.busy) emit("close");
}
function keys(event: KeyboardEvent) {
  if (event.isComposing) return;
  if (event.key === "Escape") {
    event.preventDefault();
    close();
    return;
  }
  if (event.key !== "Tab" || !panel.value) return;
  const items = [
    ...panel.value.querySelectorAll<HTMLElement>(
      'button, input, textarea, select, summary, [tabindex="0"]',
    ),
  ].filter(
    (item) => !item.matches(":disabled") && item.getClientRects().length,
  );
  const first = items[0],
    last = items[items.length - 1];
  if (!first) {
    event.preventDefault();
    panel.value.focus();
    return;
  }
  if (
    event.shiftKey &&
    (document.activeElement === first || document.activeElement === panel.value)
  ) {
    event.preventDefault();
    last?.focus();
  } else if (
    !event.shiftKey &&
    (document.activeElement === last ||
      !panel.value.contains(document.activeElement))
  ) {
    event.preventDefault();
    first.focus();
  }
}
function containFocus(event: FocusEvent) {
  if (
    panel.value &&
    event.target instanceof Node &&
    !panel.value.contains(event.target)
  )
    panel.value.focus();
}
onMounted(async () => {
  previous = document.activeElement as HTMLElement;
  previousOverflow = document.body.style.overflow;
  document.body.style.overflow = "hidden";
  document.addEventListener("keydown", keys);
  document.addEventListener("focusin", containFocus);
  await nextTick();
  panel.value?.focus();
});
onUnmounted(() => {
  document.body.style.overflow = previousOverflow;
  document.removeEventListener("keydown", keys);
  document.removeEventListener("focusin", containFocus);
  if (previous?.isConnected && previous !== document.body && previous.getClientRects().length) previous.focus();
  else if (props.returnFocusTo) {
    const target = document.querySelector<HTMLElement>(props.returnFocusTo);
    if (target?.getClientRects().length) target.focus();
  }
});
</script>

<template>
  <Teleport to="body">
    <div
      class="modal-backdrop"
      :class="{ 'drawer-backdrop': drawer }"
      @click.self="!destructive && close()"
    >
      <section
        ref="panel"
        class="modal"
        :class="{ 'scene-drawer': drawer }"
        role="dialog"
        aria-modal="true"
        :aria-label="title"
        tabindex="-1"
        :aria-busy="busy"
      >
        <header class="modal-header">
          <h2>{{ title }}</h2>
          <button
            class="icon-button"
            type="button"
            aria-label="关闭"
            :disabled="busy"
            @click="close"
          >
            ×
          </button>
        </header>
        <slot />
      </section>
    </div>
  </Teleport>
</template>
