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
/* 左下角浮起来：不遮右栏底部的保存按钮，也不挡队列最后一行的操作 */
.toasts {
  position: fixed;
  left: 20px;
  bottom: 78px;
  z-index: 40;
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 340px;
  max-width: calc(100vw - 40px);
  pointer-events: none;
}

.toast {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  padding: 10px 8px 10px 12px;
  border: 1px solid var(--border-panel);
  border-left: 3px solid var(--accent);
  border-radius: var(--r-3);
  background: var(--panel);
  box-shadow: var(--shadow-pop);
  pointer-events: auto;
}

.toast[data-tone='success'] {
  border-left-color: var(--ok);
}

.toast[data-tone='warning'] {
  border-left-color: var(--warn);
}

.toast[data-tone='danger'] {
  border-left-color: var(--bad);
}

.toast__icon {
  display: grid;
  place-items: center;
  flex: none;
  width: 18px;
  height: 18px;
  margin-top: 1px;
  color: var(--accent);
}

.toast[data-tone='success'] .toast__icon {
  color: var(--ok);
}

.toast[data-tone='warning'] .toast__icon {
  color: var(--warn);
}

.toast[data-tone='danger'] .toast__icon {
  color: var(--bad);
}

.toast__text {
  flex: 1;
  min-width: 0;
  color: var(--ink);
  font-size: var(--fs-2);
  line-height: 1.55;
  overflow-wrap: anywhere;
}

.toast__close {
  display: grid;
  place-items: center;
  flex: none;
  width: 22px;
  height: 22px;
  border: 0;
  border-radius: var(--r-1);
  background: none;
  color: var(--ink-3);
  cursor: pointer;
}

.toast__close:hover {
  background: var(--panel-2);
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

.toast-move {
  transition: transform var(--t-base) var(--ease);
}
</style>
