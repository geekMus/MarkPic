<script setup lang="ts">
import AnimatedNumber from './AnimatedNumber.vue'
import AppIcon from './AppIcon.vue'
import type { IconName } from './AppIcon.vue'
import EmptyState from './EmptyState.vue'
import PageHead from './PageHead.vue'
import { useI18n } from '../i18n'
import type { MessageKey } from '../i18n'

/**
 * 检测 / 下载两页共用的「报告外壳」：页头 + 一行结论 + 可滚动的正文。
 *
 * 只复用外壳，不复用内容 —— 检测页画地址、下载页画「远程 → 本地」，
 * 正文由各自的页面塞进 slot。以前三个作业页共用同一张文件表，
 * 切菜单时画面一模一样，看着像点击没生效（2026-11-11 用户指出）。
 */
const props = defineProps<{
  mode: 'check' | 'download'
  icon: IconName
  tone: 'accent' | 'warn' | 'ok'
  titleKey: MessageKey
  hintKey: MessageKey
  /** 标题右边那句目标信息，例如「只读」「存到 images/」。 */
  tag?: string
  hasRows: boolean
  recent?: string[]
  /** 一行结论的几段：4 个地址 · 2 能打开 · 1 打不开。 */
  verdicts: { key: MessageKey; count: number; tone: 'plain' | 'ok' | 'warn' | 'bad' }[]
  /** 这一页正在跑：页头下面那条总进度线跟着走。 */
  busy?: boolean
  progress?: number
}>()

const emit = defineEmits<{
  'pick-files': []
  'pick-folder': []
  'pick-recent': [path: string]
  clear: []
}>()

const { t } = useI18n()
</script>

<template>
  <section class="panel report">
    <PageHead
      :icon="icon"
      :tone="tone"
      :title-key="titleKey"
      :lead-key="hintKey"
      :tag="tag"
      :busy="busy"
      :progress="progress"
    >
      <template #actions>
        <button
          type="button"
          class="icon-btn"
          :title="t('queue.pickFiles')"
          :aria-label="t('queue.pickFiles')"
          @click="emit('pick-files')"
        >
          <AppIcon name="file-plus" :size="16" />
        </button>
        <button
          type="button"
          class="icon-btn"
          :title="t('queue.pickFolder')"
          :aria-label="t('queue.pickFolder')"
          @click="emit('pick-folder')"
        >
          <AppIcon name="folder-plus" :size="16" />
        </button>
        <span class="report__divider" aria-hidden="true" />

        <button
          type="button"
          class="icon-btn icon-btn--danger"
          :title="t('queue.clear')"
          :aria-label="t('queue.clear')"
          :disabled="!hasRows"
          @click="emit('clear')"
        >
          <AppIcon name="trash" :size="16" />
        </button>
      </template>
    </PageHead>

    <!-- 一行结论：读成一句话，不做成一排卡片 -->
    <p v-if="hasRows && verdicts.length > 0" class="report__summary">
      <template v-for="(cell, index) in verdicts" :key="cell.key">
        <span v-if="index > 0" class="report__sep" aria-hidden="true">·</span>
        <span class="report__stat" :class="`report__stat--${cell.tone}`">
          <AnimatedNumber :value="cell.count" />
          <span class="report__lbl">{{ t(cell.key) }}</span>
        </span>
      </template>
    </p>

    <div v-if="hasRows" class="report__body">
      <slot />
    </div>
    <div v-else class="report__empty">
      <EmptyState
        :recent="recent ?? []"
        :remote-mode="mode"
        @pick-files="emit('pick-files')"
        @pick-folder="emit('pick-folder')"
        @pick-recent="emit('pick-recent', $event)"
      />
    </div>
    <!-- 底栏（统计与主按钮）由页面塞进来：它是这张卡片的脚注，和卡片同宽同底 -->
    <slot name="footer" />
  </section>
</template>

<style scoped>
.panel.report {
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.report__divider {
  width: 1px;
  height: 18px;
  margin: 0 4px;
  background: var(--line);
}

.report__summary {
  display: flex;
  flex: none;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 10px;
  margin: 20px 0 0;
  padding: 0 34px;
  color: var(--ink-3);
  font-size: var(--fs-3);
}

.report__stat {
  display: inline-flex;
  align-items: baseline;
  gap: 6px;
}

.report__stat .num {
  color: var(--ink);
  font-size: var(--fs-3);
  font-weight: 600;
}

.report__stat--ok .num {
  color: var(--ok);
}

.report__stat--bad .num {
  color: var(--bad);
}

.report__stat--warn .num {
  color: var(--warn);
}

.report__lbl {
  color: var(--ink-3);
}

.report__sep {
  color: var(--ink-3);
  opacity: 0.7;
}

.report__body {
  flex: 1 1 auto;
  min-height: 0;
  padding: 16px 34px 34px;
  /* 和队列页一样常留滚动条位置，切换菜单时正文不左右跳。 */
  overflow-y: scroll;
  scrollbar-gutter: stable;
}

.report__empty {
  display: flex;
  flex: 1 1 auto;
  min-height: 0;
  padding: 16px 34px 34px;
}

.report__empty > * {
  flex: 1;
}

@media (max-width: 900px) {
  .report__summary,
  .report__body,
  /* 空态也收：否则它比正文多出 14px 内边距，拖放区和上面的正文左边缘对不齐 */
  .report__empty {
    padding-inline: 20px;
  }

  /* 底部也一起收：窗口矮时 34px 尾巴会把空态文案顶出可视区。
     空态必须跟正文同值，否则在三个视图之间切换时正文底边会跳 10px。 */
  .report__body,
  .report__empty {
    padding-bottom: 24px;
  }
}
</style>