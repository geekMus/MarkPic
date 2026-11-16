<script setup lang="ts">
import { computed } from 'vue'
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
const props = withDefaults(defineProps<{
  icon: IconName
  /** 图标底色：转换是强调色，检测是琥珀，下载是绿色。 */
  tone: 'accent' | 'warn' | 'ok'
  titleKey: MessageKey
  leadKey: MessageKey
  /** 给 h2 一个锚点 id：外层 role="region" 的 aria-labelledby 指向它。 */
  id?: string
  /** 这一页正在跑：页头下面那条进度线才出现。 */
  busy?: boolean
  /**
   * 0..1 的总进度。
   *
   * 分母是「这一轮真正要处理的单元」而不是文件数：上传按去重后的本地图算，
   * 检测/下载按去重后的远程地址算。所以一轮只有一个文件时，这条线也会随
   * 每张图推进，不再退回 indeterminate 那种来回扫的光。
   */
  progress?: number
}>(), { busy: false, progress: 0 })

const { t } = useI18n()

const fill = computed(() => Math.max(0, Math.min(1, props.progress)))
const percent = computed(() => Math.round(fill.value * 100))
</script>

<template>
  <header class="pagehead">
    <div class="pagehead__text">
      <div class="pagehead__row">
        <span class="pagehead__mark" :class="`pagehead__mark--${tone}`">
          <AppIcon :name="icon" :size="14" />
        </span>
        <h2 :id="id" class="pagehead__title">{{ t(titleKey) }}</h2>
        <slot name="badge" />
        <span v-if="busy" class="pagehead__pct num" aria-hidden="true">{{ percent }}%</span>
      </div>
      <p class="pagehead__lead">{{ t(leadKey) }}</p>
    </div>
    <div class="pagehead__tools">
      <slot name="actions" />
    </div>

    <!-- 跑起来的时候，页头下面亮一条极细的总进度线；停下来就消失，不留痕迹。 -->
    <span v-if="busy" class="pagehead__rule" aria-hidden="true">
      <span class="pagehead__fill" :style="{ transform: `scaleX(${fill})` }" />
    </span>
  </header>
</template>

<style scoped>
.pagehead {
  position: relative;
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

/* 总进度百分比：和页头的细线一起出现，跑完就一起消失。 */
.pagehead__pct {
  min-width: 3ch;
  color: var(--ink-3);
  font-size: var(--fs-1);
  text-align: right;
  white-space: nowrap;
}

/* 说明单独一行，跟标题之间留 8px：读起来是「标题 + 一句话」，不是一条工具条 */
.pagehead__lead {
  margin: 8px 0 0;
  /* 默认窗口下尽量一行读完；窄到放不下时由容器宽度自然折行。 */
  max-width: none;
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

/* 窗口窄到贴住最小宽度时，页头这一行放不下「图标 + 标题 + 文件数 + 三个按钮」。
   以前这里只缩内边距、不禁换行，标题被挤成一条 19px 宽的竖排字，还会和空态大标题叠在一起。
   现在允许换行：标题先占满一行，文件数与按钮落到下一行，字始终是横的。 */
@media (max-width: 900px) {
  .pagehead {
    flex-wrap: wrap;
    gap: 10px 12px;
    padding: 24px 20px 0;
  }

  .pagehead__row {
    flex-wrap: wrap;
    row-gap: 6px;
  }

  /* 标题不能折行折到字与字上下摞起来：它要么整行放下，要么把徽标挤到下一行。 */
  .pagehead__title {
    white-space: nowrap;
  }

  /* 说明文字保持可见：默认一行，窄窗口按可用宽度自然折行。 */
}

.pagehead__rule {
  position: absolute;
  right: 34px;
  bottom: -11px;
  left: 34px;
  height: 2px;
  overflow: hidden;
  border-radius: var(--r-pill);
  background: var(--line-2);
}

.pagehead__fill {
  display: block;
  width: 100%;
  height: 100%;
  border-radius: inherit;
  background: linear-gradient(90deg, var(--accent), var(--accent-ink));
  box-shadow: 0 0 10px color-mix(in srgb, var(--accent) 45%, transparent);
  transform-origin: left center;
  transition: transform 420ms var(--ease);
}

@media (max-width: 900px) {
  .pagehead__rule {
    right: 20px;
    left: 20px;
  }
}

/* 进度条只按真实比例伸缩；不再有「说不准就来回扫」的分支。 */
@media (prefers-reduced-motion: reduce) {
  .pagehead__fill {
    transition: none;
  }
}
</style>