<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import type { IconName } from './AppIcon.vue'
import { useI18n } from '../i18n'
import type { MessageKey } from '../i18n'

/**
 * 作业页的页头：一个大一号的标题、它下面单独一行说明、右边放工具按钮。
 *
 * 三个作业页（转换 / 检测 / 下载）共用它，保证同一套字号与留白。
 * 以前这里是 44px 的一条工具条，标题和说明挤在同一行、还都只有 13px，
 * 一整屏看下来没有主次（2026-11-11 用户提「布局、大小和样式都不对」）。
 */
defineProps<{
  icon: IconName
  /** 图标底色：转换是强调色，检测是琥珀，下载是绿色。 */
  tone: 'accent' | 'warn' | 'ok'
  titleKey: MessageKey
  leadKey: MessageKey
  /** 标题后面那个目标信息，例如「只读」「存到 images/」。 */
  tag?: string
}>()

const { t } = useI18n()
</script>

<template>
  <header class="pagehead">
    <div class="pagehead__text">
      <div class="pagehead__row">
        <span class="pagehead__mark" :class="`pagehead__mark--${tone}`">
          <AppIcon :name="icon" :size="14" />
        </span>
        <h2 class="pagehead__title">{{ t(titleKey) }}</h2>
        <slot name="badge" />
        <span v-if="tag" class="pagehead__tag num">{{ tag }}</span>
      </div>
      <p class="pagehead__lead">{{ t(leadKey) }}</p>
    </div>
    <div class="pagehead__tools">
      <slot name="actions" />
    </div>
  </header>
</template>

<style scoped>
.pagehead {
  display: flex;
  flex: none;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
  padding: 30px 34px 0;
}

.pagehead__text {
  min-width: 0;
}

.pagehead__row {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.pagehead__mark {
  display: grid;
  flex: none;
  place-items: center;
  width: 24px;
  height: 24px;
  border: 1px solid var(--line);
  border-radius: var(--r-2);
  background: var(--panel-2);
  color: var(--ink-2);
}

.pagehead__mark--accent {
  border-color: var(--accent-line);
  background: var(--accent-soft);
  color: var(--accent);
}

.pagehead__mark--warn {
  border-color: var(--warn-line);
  background: var(--warn-soft);
  color: var(--warn);
}

.pagehead__mark--ok {
  border-color: var(--ok-line);
  background: var(--ok-soft);
  color: var(--ok);
}

.pagehead__title {
  margin: 0;
  color: var(--ink);
  font-size: var(--fs-5);
  font-weight: 600;
  letter-spacing: -0.01em;
  line-height: 1.2;
}

.pagehead__tag {
  padding: 2px 8px;
  border: 1px solid var(--line);
  border-radius: var(--r-pill);
  background: var(--panel-2);
  color: var(--ink-3);
  font-size: var(--fs-1);
  white-space: nowrap;
}

/* 说明单独一行，跟标题之间留 8px：读起来是「标题 + 一句话」，不是一条工具条 */
.pagehead__lead {
  margin: 8px 0 0;
  max-width: 62ch;
  color: var(--ink-3);
  font-size: var(--fs-3);
  line-height: 1.5;
}

.pagehead__tools {
  display: flex;
  flex: none;
  align-items: center;
  gap: 6px;
}

.pagehead__tools :deep(.icon-btn) {
  width: 30px;
  height: 30px;
}

@media (max-width: 900px) {
  .pagehead {
    padding: 24px 20px 0;
  }

  .pagehead__lead {
    display: none;
  }
}
</style>