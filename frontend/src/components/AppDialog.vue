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
const modal = ref(true);
let previous: HTMLElement | null = null;
let previousOverflow = "";
let narrow: MediaQueryList;
function syncMode() {
  modal.value = !props.drawer || narrow.matches;
  document.body.style.overflow = modal.value ? "hidden" : previousOverflow;
  if (modal.value && panel.value && !panel.value.contains(document.activeElement))
    panel.value.focus({ preventScroll: true });
}
function close() {
  if (!props.busy) emit("close");
}
function outside(event: PointerEvent) {
  if (modal.value || props.destructive || !(event.target instanceof Element)) return;
  if (!panel.value?.contains(event.target) && !event.target.closest(".information-entry")) close();
}
function keys(event: KeyboardEvent) {
  if (event.isComposing) return;
  if (event.key === "Escape") {
    event.preventDefault();
    close();
    return;
  }
  if (event.key !== "Tab" || !modal.value || !panel.value) return;
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
    modal.value &&
    panel.value &&
    event.target instanceof Node &&
    !panel.value.contains(event.target)
  )
    panel.value.focus({ preventScroll: true });
}
onMounted(async () => {
  previous = document.activeElement as HTMLElement;
  previousOverflow = document.body.style.overflow;
  narrow = window.matchMedia("(max-width: 720px)");
  narrow.addEventListener("change", syncMode);
  syncMode();
  document.addEventListener("keydown", keys);
  document.addEventListener("focusin", containFocus);
  document.addEventListener("pointerdown", outside);
  await nextTick();
  panel.value?.focus({ preventScroll: true });
});
onUnmounted(() => {
  document.body.style.overflow = previousOverflow;
  narrow?.removeEventListener("change", syncMode);
  document.removeEventListener("keydown", keys);
  document.removeEventListener("focusin", containFocus);
  document.removeEventListener("pointerdown", outside);
  if (previous?.isConnected && previous !== document.body && previous.getClientRects().length) previous.focus({ preventScroll: true });
  else if (props.returnFocusTo) {
    const target = document.querySelector<HTMLElement>(props.returnFocusTo);
    if (target?.getClientRects().length) target.focus({ preventScroll: true });
  }
});
</script>

<template>
  <Teleport to="body">
    <div
      class="modal-backdrop"
      :class="{ 'drawer-backdrop': drawer, 'nonmodal-backdrop': !modal }"
      @click.self="!destructive && close()"
    >
      <section
        ref="panel"
        :id="drawer ? 'wia-information-panel' : undefined"
        class="modal"
        :class="{ 'scene-drawer': drawer }"
        role="dialog"
        :aria-modal="modal ? 'true' : undefined"
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
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" /></svg>
          </button>
        </header>
        <slot />
      </section>
    </div>
  </Teleport>
</template>
