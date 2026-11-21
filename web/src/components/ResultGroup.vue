<script setup lang="ts">
import { computed, ref } from 'vue'
import AppIcon from './AppIcon.vue'
import ScanBadge from './ScanBadge.vue'
import ScanFileList from './ScanFileList.vue'
import { plural, useI18n } from '../i18n'
import { shortDir } from './shortDir'
import type { ScanState } from '../composables/useRun'
import type { FileResult } from '../types'

/**
 * 一张「文档卡」的外壳：卡头（文档名、目录、结论徽标、打开/定位/移除）、
 * 整篇进度轨、以及可折叠的正文。
 *
 * 检测页和下载页的卡头逐字相同，正文却完全不同 —— 所以正文走默认插槽，
 * 徽标走 `#badges` 插槽（两边数的是不同的东西），进度轨走 `#rail`
 * （只有下载页有）。折叠状态留在卡片自己身上：一张卡收没收起来，是这张
 * 卡的事，页面不必替它记。
 */
const props = defineProps<{
  /** 文档名，卡头第一行。 */
  name: string
  /** 文档所在目录：卡头只写最后一级，完整路径在 title 里。 */
  dir: string
  /** 这张卡自己的扫描状态。没扫通时卡里说的是扫描，不是「0 条」。 */
  scan: ScanState
  scanError?: string
  /** 整轮是否还在跑：跑的时候不给移除。 */
  busy: boolean
  /** 卡头那颗图标的语气：有坏的就红，有碰不了的黄，否则中性。 */
  tone: 'neutral' | 'warning' | 'danger'
  /** 正文里有没有可展开的行；没有就不放「查看详情」。 */
  expandable: boolean
  /** 这是一张「扫描卡片」（拖进来的是文件夹/盘）：卡头写发现了几份，扫完给「确认导入」。 */
  scanCard?: boolean
  /** 扫描卡片已经发现的 Markdown 份数。 */
  scanFound?: number
  /** 扫描卡片发现到的文件清单（发现档只带路径，还没有图片数）。 */
  scanFiles?: FileResult[]
  /** 扫描的根（文件夹 / 盘）：清单里只写它下面那一段。 */
  scanRoot?: string
}>()

const emit = defineEmits<{
  /** 打开这份文件（写过副本就开副本）。 */
  open: []
  /** 在文件管理器里定位。 */
  reveal: []
  remove: []
  /** 这张卡自己的扫描：取消 / 恢复（重试）。 */
  'cancel-scan': []
  'retry-scan': []
  /** 扫描卡片：把发现到的文件铺成文件卡片。 */
  'confirm-import': []
}>()

const { t } = useI18n()

const open = ref(false)

/**
 * 能不能展开：地址表非空，或者这是一张「有文件清单的扫描卡片」。
 * 扫描卡片的清单只在展开后看，收起时卡片保持一行的高度。
 */
const canExpand = computed(
  () => props.expandable || (props.scanCard === true && (props.scanFiles?.length ?? 0) > 0),
)

function toggle(): void {
  if (!canExpand.value) return
  open.value = !open.value
}
</script>

<template>
  <section class="group-card">
    <header
      class="group-card__head group-card__head--fold"
      :class="{ 'group-card__head--scan': props.scanCard }"
      role="button"
      tabindex="0"
      :aria-expanded="open"
      @click="toggle"
      @keydown.enter.prevent="toggle"
      @keydown.space.prevent="toggle"
    >
      <span class="group-card__mark" :data-tone="props.tone" aria-hidden="true">
        <AppIcon name="doc" :size="15" />
      </span>
      <span class="group-card__text">
        <span class="group-card__name" :title="props.name">{{ props.name }}</span>
        <span class="group-card__sub mono" :title="props.dir">{{ shortDir(props.dir) }}</span>
      </span>
      <span class="group-card__badges">
        <!-- 扫描卡片数的是「发现了几份 Markdown」，不是地址 -->
        <span v-if="props.scanCard" class="badge">
          {{ t(plural('row.count.found', props.scanFound ?? 0), { count: props.scanFound ?? 0 }) }}
        </span>
        <!-- 还没扫通：卡上说的是扫描本身，不是「0 条」 -->
        <ScanBadge
          v-if="props.scan !== 'ok'"
          :state="props.scan"
          :error="props.scanError"
          :busy="props.busy"
          @cancel="emit('cancel-scan')"
          @retry="emit('retry-scan')"
        />
        <!-- 扫描卡片没有地址/图片可数：只写发现了几份，别把正文的徽标也挂上来 -->
        <template v-else-if="!props.scanCard">
          <slot name="badges" />
        </template>
      </span>
      <span
        v-if="canExpand"
        class="group-card__more"
        :class="{ 'group-card__more--on': open }"
      >
        {{ t(open ? 'row.details.collapse' : 'row.details.expand') }}
        <AppIcon class="group-card__chev" name="chevron-down" :size="13" />
      </span>
      <!-- 打开、定位、移除：和上传页的卡头同一组，不必先展开才够得着 -->
      <span class="group-card__tools" @keydown.stop>
        <button
          v-if="props.scanCard && props.scan === 'ok' && (props.scanFound ?? 0) > 0"
          type="button"
          class="btn btn--primary btn--sm group-card__import"
          :disabled="props.busy"
          @click.stop="emit('confirm-import')"
        >
          {{ t('row.scan.confirm') }}
        </button>
        <button
          type="button"
          class="icon-btn icon-btn--sm"
          :title="t('row.open')"
          :aria-label="t('row.open')"
          @click.stop="emit('open')"
        >
          <AppIcon name="external" :size="13" />
        </button>
        <button
          type="button"
          class="icon-btn icon-btn--sm"
          :title="t('row.reveal')"
          :aria-label="t('row.reveal')"
          @click.stop="emit('reveal')"
        >
          <AppIcon name="folder-open" :size="13" />
        </button>
        <button
          type="button"
          class="icon-btn icon-btn--sm icon-btn--drop"
          :title="t('row.remove')"
          :aria-label="t('row.remove')"
          :disabled="props.busy"
          @click.stop="emit('remove')"
        >
          <AppIcon name="x" :size="13" />
        </button>
      </span>
    </header>
    <!-- 跑动中整张卡一条进度轨：卡头数字说逐图结论，这条说整篇走到哪了 -->
    <slot name="rail" />
    <div class="group-card__fold" :class="{ 'group-card__fold--open': open }">
      <!-- 扫描卡片的文件清单只在展开后出现 -->
      <div v-if="props.scanCard && (props.scanFiles?.length ?? 0) > 0" class="group-card__scan">
        <ScanFileList class="group-card__scan-list" :files="props.scanFiles ?? []" :root="props.scanRoot" />
      </div>
      <slot />
    </div>
  </section>
</template>
