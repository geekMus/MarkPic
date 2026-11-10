<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import type { Toast, ToastTone } from '../composables/useToasts'
import { useI18n } from '../i18n'

defineProps<{ toasts: Toast[] }>()

const emit = defineEmits<{ dismiss: [id: number] }>()

const { t } = useI18n()

const ICON_BY_TONE: Record<ToastTone, 'info' | 'check' | 'alert'> = {
  info: 'info',
  success: 'check',
  warning: 'alert',
  danger: 'alert',
}
</script>

<template>
  <aside class="toasts" :aria-label="t('toast.region')" aria-live="polite">
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
          :aria-label="t('toast.dismiss')"
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
  gap: 6px;
  width: 320px;
  max-width: calc(100vw - 40px);
  pointer-events: none;
}

.toast {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  padding: 9px 8px 9px 11px;
  border: 1px solid var(--line-2);
  border-radius: 8px;
  background: var(--panel);
  box-shadow: var(--shadow-pop);
  pointer-events: auto;
}

.toast__icon {
  display: grid;
  place-items: center;
  flex: none;
  width: 16px;
  height: 16px;
  margin-top: 2px;
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
