<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import type { Toast, ToastTone } from '../composables/useToasts'

defineProps<{ toasts: Toast[] }>()

const emit = defineEmits<{ dismiss: [id: number] }>()

const ICON_BY_TONE: Record<ToastTone, 'info' | 'check' | 'alert'> = {
  info: 'info',
  success: 'check',
  warning: 'alert',
  danger: 'alert',
}
</script>

<template>
  <aside class="toasts" aria-label="通知" aria-live="polite">
    <TransitionGroup name="toast">
      <div
        v-for="toast in toasts"
        :key="toast.id"
        class="toast"
        :data-tone="toast.tone"
        role="status"
      >
        <span class="toast__icon" aria-hidden="true">
          <AppIcon :name="ICON_BY_TONE[toast.tone]" :size="15" />
        </span>
        <p class="toast__text">{{ toast.message }}</p>
        <button
          type="button"
          class="toast__close"
          aria-label="关闭提示"
          @click="emit('dismiss', toast.id)"
        >
          <AppIcon name="x" :size="13" />
        </button>
      </div>
    </TransitionGroup>
  </aside>
</template>

<style scoped>
.toasts {
  position: fixed;
  left: 16px;
  bottom: 56px;
  z-index: 60;
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: min(392px, calc(100vw - 32px));
  pointer-events: none;
}

.toast {
  --tone: var(--accent);

  display: flex;
  align-items: flex-start;
  gap: 9px;
  padding: 9px 8px 9px 11px;
  border: 1px solid var(--line);
  border-left: 2px solid var(--tone);
  border-radius: var(--r-2);
  background: var(--surface);
  box-shadow: var(--shadow-pop);
  pointer-events: auto;
}

.toast[data-tone='success'] {
  --tone: var(--ok);
}

.toast[data-tone='warning'] {
  --tone: var(--warn);
}

.toast[data-tone='danger'] {
  --tone: var(--bad);
}

.toast__icon {
  display: grid;
  place-items: center;
  width: 18px;
  height: 18px;
  flex: none;
  margin-top: 1px;
  color: var(--tone);
}

.toast__text {
  flex: 1;
  min-width: 0;
  color: var(--ink);
  font-size: var(--fs-data);
  line-height: 1.5;
  overflow-wrap: anywhere;
}

.toast__close {
  display: grid;
  place-items: center;
  width: 22px;
  height: 22px;
  flex: none;
  padding: 0;
  border: none;
  border-radius: var(--r-1);
  background: transparent;
  color: var(--ink-3);
  cursor: pointer;
  transition:
    background-color var(--t-fast) var(--ease),
    color var(--t-fast) var(--ease);
}

.toast__close:hover {
  background: var(--sunk);
  color: var(--ink);
}

.toast-enter-active,
.toast-leave-active {
  transition:
    opacity var(--t-base) var(--ease),
    transform var(--t-base) var(--ease);
}

.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateY(6px);
}
</style>
