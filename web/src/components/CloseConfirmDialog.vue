<script setup lang="ts">
import { nextTick, onMounted, ref } from 'vue'
import AppIcon from './AppIcon.vue'
import { useI18n } from '../i18n'

/**
 * 点窗口叉号时的确认框。
 *
 * - 没有任务、设置是「每次询问」（也是第一次用的默认值）：给两条路，
 *   「最小化到托盘」把窗口收进通知区、进程和任务都还在，「退出程序」结束进程。
 *   勾了「不再询问」就按这次点的那个记住，以后直接执行、不再弹。
 * - 还有任务：正文写清退出会中止几个任务，只有「取消」和「退出程序」，
 *   这里永远不提供「不再询问」，带着任务退出必须每次确认。
 *
 * 后端只在需要确认时发这个弹窗（cmd/markpic/chrome_windows.go 的 WM_CLOSE）。
 */
defineProps<{ count: number; ask: boolean }>()

const emit = defineEmits<{
  cancel: []
  minimize: [remember: boolean]
  quit: [remember: boolean]
}>()

const { t } = useI18n()
const panel = ref<HTMLElement | null>(null)
const dontAskAgain = ref(false)

onMounted(() => {
  void nextTick(() => panel.value?.focus())
})
</script>

<template>
  <div class="dlg" @mousedown.self="emit('cancel')">
    <section
      ref="panel"
      tabindex="-1"
      class="dlg__panel dlg__panel--close"
      :role="ask ? 'dialog' : 'alertdialog'"
      aria-modal="true"
      aria-labelledby="close-title"
      aria-describedby="close-body"
      @keydown.esc.stop="emit('cancel')"
    >
      <header class="panel__head">
        <div class="panel__title">
          <h2 id="close-title">{{ t('close.title') }}</h2>
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

      <div class="dlg__body close">
        <p id="close-body" class="close__text">
          {{ ask ? t('close.idleBody') : t('close.quitBody', { count }) }}
        </p>
        <label v-if="ask" class="close__dontask">
          <input v-model="dontAskAgain" type="checkbox" />
          <span>{{ t('close.dontAskAgain') }}</span>
        </label>
      </div>

      <footer class="panel__foot">
        <span class="close__spacer" />
        <button
          v-if="ask"
          type="button"
          class="btn btn--secondary"
          @click="emit('minimize', dontAskAgain)"
        >
          {{ t('close.minimize') }}
        </button>
        <button v-else type="button" class="btn btn--ghost" @click="emit('cancel')">
          {{ t('common.cancel') }}
        </button>
        <button
          type="button"
          class="btn btn--danger"
          @click="emit('quit', ask && dontAskAgain)"
        >
          {{ t('close.quitConfirm') }}
        </button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.dlg__panel--close {
  width: min(420px, 100%);
}

.close {
  padding: 18px 22px 20px;
}

.close__text {
  margin: 0;
  color: var(--ink);
  font-size: var(--fs-3);
  line-height: 1.6;
}

.close__dontask {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 14px;
  color: var(--ink-2);
  font-size: var(--fs-2);
  line-height: 1.4;
  cursor: pointer;
  user-select: none;
}

.close__dontask input {
  width: 14px;
  height: 14px;
  margin: 0;
  accent-color: var(--accent);
  flex: none;
}

.close__spacer {
  flex: 1;
}

@media (max-width: 460px) {
  .panel__foot {
    height: auto;
    flex-wrap: wrap;
    padding: 10px 14px;
  }
}
</style>
