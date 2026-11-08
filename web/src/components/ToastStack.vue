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
      <div v-for="toast in toasts" :key="toast.id" class="toast" :data-tone="toast.tone" role="status">
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
  right: 16px;
  bottom: 68px;
  z-index: 60;
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: min(384px, calc(100vw - 32px));
  pointer-events: none;
}

.toast {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  padding: 9px 8px 9px 11px;
  border: 1px solid var(--border);
  border-left: 2px solid var(--text-3);
  border-radius: var(--r-md);
  background: var(--surface);
  box-shadow: var(--shadow-pop);
  pointer-events: auto;
}

.toast__icon {
  display: grid;
  place-items: center;
  width: 18px;
  height: 18px;
  flex: none;
  margin-top: 1px;
  color: var(--text-3);
}

.toast__text {
  flex: 1;
  min-width: 0;
  color: var(--text-1);
  font-size: 12.5px;
  line-height: 1.45;
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
  border-radius: var(--r-sm);
  background: transparent;
  color: var(--text-3);
  cursor: pointer;
  transition: background-color var(--dur-hover) var(--ease), color var(--dur-hover) var(--ease);
}

.toast__close:hover {
  background: var(--surface-3);
  color: var(--text-1);
}

.toast[data-tone='success'] {
  border-left-color: var(--success);
}

.toast[data-tone='success'] .toast__icon {
  color: var(--success);
}

.toast[data-tone='warning'] {
  border-left-color: var(--warning);
}

.toast[data-tone='warning'] .toast__icon {
  color: var(--warning);
}

.toast[data-tone='danger'] {
  border-left-color: var(--danger);
}

.toast[data-tone='danger'] .toast__icon {
  color: var(--danger);
}

.toast[data-tone='info'] .toast__icon {
  color: var(--accent-text);
}

.toast-enter-active,
.toast-leave-active {
  transition: opacity var(--dur-enter) var(--ease), transform var(--dur-enter) var(--ease);
}

.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateY(6px);
}
</style>
