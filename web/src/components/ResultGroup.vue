<script setup lang="ts">
import { ref } from 'vue'
import AppIcon from './AppIcon.vue'
import ScanBadge from './ScanBadge.vue'
import { useI18n } from '../i18n'
import { shortDir } from './shortDir'
import type { ScanState } from '../composables/useRun'

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
}>()

const { t } = useI18n()

const open = ref(false)

function toggle(): void {
  open.value = !open.value
}
</script>

<template>
  <section class="group-card">
    <header
      class="group-card__head group-card__head--fold"
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
        <!-- 还没扫通：卡上说的是扫描本身，不是「0 条」 -->
        <ScanBadge
          v-if="props.scan !== 'ok'"
          :state="props.scan"
          :error="props.scanError"
          :busy="props.busy"
          @cancel="emit('cancel-scan')"
          @retry="emit('retry-scan')"
        />
        <template v-else>
          <slot name="badges" />
        </template>
      </span>
      <span
        v-if="props.expandable"
        class="group-card__more"
        :class="{ 'group-card__more--on': open }"
      >
        {{ t(open ? 'row.details.collapse' : 'row.details.expand') }}
        <AppIcon class="group-card__chev" name="chevron-down" :size="13" />
      </span>
      <!-- 打开、定位、移除：和上传页的卡头同一组，不必先展开才够得着 -->
      <span class="group-card__tools" @keydown.stop>
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
      <slot />
    </div>
  </section>
</template>
