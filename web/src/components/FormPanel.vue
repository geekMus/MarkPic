<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import PageHead from './PageHead.vue'
import { nextTick, onMounted, ref } from 'vue'
import { useI18n, type MessageKey } from '../i18n'
import type { IconName } from './AppIcon.vue'

/**
 * 「输出」和「设置」两页共用的外壳：标题 + 「未保存」、滚动区、底部的还原 / 保存。
 *
 * 这两页不是弹窗 —— 菜单本身就能切走，标题右边不该再挂一个叉。
 *
 * 两页改的是同一份草稿，所以按钮、快捷键、脏标记必须长得一模一样 —— 复制两遍迟早会走样。
 * 表单内容由各页自己放进 slot。
 */
const props = defineProps<{
  /** 页面标题的 i18n 键。 */
  titleKey: MessageKey
  /** 页头那颗图标的语义与配色，和作业页保持一致。 */
  icon: IconName
  tone: 'accent' | 'warn' | 'ok'
  /** 标题下面那行说明。 */
  leadKey: MessageKey
  /** aria-labelledby 的锚点：同一时刻页面上只该有一个。 */
  anchor: string
  dirty: boolean
  loaded: boolean
  loading: boolean
  saving: boolean
  loadError: string | null
}>()

const emit = defineEmits<{
  save: []
  discard: []
  retry: []
}>()

const { t } = useI18n()

/* 切到这一页时把焦点搬进来：键盘用户按 Ctrl+, 之后 Tab 应该从这里开始 */
const panel = ref<HTMLElement | null>(null)

onMounted(() => {
  void nextTick(() => panel.value?.focus())
})
</script>

<template>
  <div class="settings-view">
    <section
      ref="panel"
      tabindex="-1"
      class="panel form"
      role="region"
      :aria-labelledby="props.anchor"
    >
      <PageHead
        :id="props.anchor"
        :icon="props.icon"
        :tone="props.tone"
        :title-key="props.titleKey"
        :lead-key="props.leadKey"
      >
        <template #badge>
          <span v-if="props.dirty" class="form__dirty">{{ t('settings.unsaved') }}</span>
        </template>
      </PageHead>

      <div class="form__body">
        <template v-if="props.loaded">
          <slot />
        </template>

        <div v-else class="placeholder">
          <p v-if="props.loading" role="status" aria-live="polite">{{ t('settings.loading') }}</p>
          <template v-else>
            <p class="placeholder__error">{{ props.loadError ?? t('settings.loadFailed') }}</p>
            <button type="button" class="btn btn--secondary btn--sm" @click="emit('retry')">
              <AppIcon name="refresh" :size="14" />
              {{ t('common.reload') }}
            </button>
          </template>
        </div>
      </div>

      <footer v-if="props.loaded" class="panel__foot panel__foot--form">
        <span class="form__kbd">
          <span class="kbd">Ctrl</span>
          <span class="kbd">Enter</span>
          {{ t('settings.shortcut.save') }}
        </span>
        <button
          type="button"
          class="btn btn--ghost btn--sm"
          :disabled="!props.dirty || props.saving"
          @click="emit('discard')"
        >
          {{ t('settings.discard') }}
        </button>
        <button
          type="button"
          class="btn btn--primary btn--sm"
          :disabled="!props.dirty || props.saving"
          @click="emit('save')"
        >
          {{ props.saving ? t('settings.saving') : t('settings.save') }}
        </button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
/* 和作业页同一套纵向骨架：页头 30px 上下留白，正文 34px 左右留白。 */
.panel.form {
  grid-template-rows: auto minmax(0, 1fr) auto;
  flex: 1 1 auto;
  width: 100%;
}

.form__body {
  min-height: 0;
  overflow-y: scroll;
  scrollbar-gutter: stable;
  padding: 18px 34px 24px;
}

.form__dirty {
  color: var(--warn);
  font-size: var(--fs-1);
  font-weight: 500;
}

.form__kbd {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-right: auto;
  color: var(--ink-3);
  font-size: var(--fs-1);
}

.panel__note--dirty {
  color: var(--warn);
  font-weight: 500;
}
</style>