<script setup lang="ts">
import { nextTick, onMounted, ref } from 'vue'
import AppIcon from './AppIcon.vue'
import { useI18n } from '../i18n'

/**
 * 一个通用的是/否确认框。
 *
 * 用在小而明确的操作上（比如「停止扫描？」）：标题、正文、确认按钮文案由调用方给，
 * 关掉的方式只有两个 —— 点确认、或者点取消/按 Esc。不做「不再询问」那种带记忆的
 * 选项，避免把一次性的决定变成长期设置。
 */
const props = withDefaults(
  defineProps<{
    title: string
    body: string
    confirmLabel: string
    /** 确认按钮的样式：破坏性操作用 danger。 */
    tone?: 'primary' | 'danger'
  }>(),
  { tone: 'primary' },
)

const emit = defineEmits<{ confirm: []; cancel: [] }>()

const { t } = useI18n()
const panel = ref<HTMLElement | null>(null)

onMounted(() => {
  void nextTick(() => panel.value?.focus())
})
</script>

<template>
  <div class="dlg" @mousedown.self="emit('cancel')">
    <section
      ref="panel"
      tabindex="-1"
      class="dlg__panel dlg__panel--confirm"
      role="alertdialog"
      aria-modal="true"
      aria-labelledby="confirm-title"
      aria-describedby="confirm-body"
      @keydown.esc.stop="emit('cancel')"
    >
      <header class="panel__head">
        <div class="panel__title">
          <h2 id="confirm-title">{{ props.title }}</h2>
        </div>
        <div class="panel__actions">
          <button
            type="button"
            class="icon-btn"
            :title="t('common.cancel')"
            :aria-label="t('common.cancel')"
            @click="emit('cancel')"
          >
            <AppIcon name="x" :size="16" />
          </button>
        </div>
      </header>

      <div class="dlg__body confirm">
        <p id="confirm-body" class="confirm__text">{{ props.body }}</p>
      </div>

      <footer class="panel__foot">
        <span class="confirm__spacer" />
        <button type="button" class="btn btn--ghost" @click="emit('cancel')">
          {{ t('common.cancel') }}
        </button>
        <button
          type="button"
          :class="['btn', props.tone === 'danger' ? 'btn--danger' : 'btn--primary']"
          @click="emit('confirm')"
        >
          {{ props.confirmLabel }}
        </button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.dlg__panel--confirm {
  width: min(440px, 100%);
}

.confirm {
  padding: 18px 22px 20px;
}

.confirm__text {
  margin: 0;
  color: var(--ink);
  font-size: var(--fs-3);
  line-height: 1.6;
}

.confirm__spacer {
  flex: 1;
}
</style>
