<script setup lang="ts">
import { computed } from 'vue'
import StatusPill from './StatusPill.vue'
import type { ScanState } from '../composables/useRun'
import { useI18n, type MessageKey } from '../i18n'

type Tone = 'neutral' | 'accent' | 'success' | 'warning' | 'danger'

/**
 * 一张卡片自己的扫描状态：扫的时候在卡片里转，扫不通也在这张卡片里说。
 *
 * 扫通了就什么都不画 —— 那时候卡上要说的是「几张图」「有没有问题」，
 * 再挂一句「已扫描」只是噪音。
 */
const props = withDefaults(
  defineProps<{
    state: ScanState
    /** 扫不动的原因（超时、找不到文件），鼠标停在按钮上时给个说法。 */
    error?: string
    /** 这一轮正在跑：这时不该再动扫描，取消/重试都灰着。 */
    busy?: boolean
  }>(),
  { error: '', busy: false },
)

const emit = defineEmits<{ cancel: []; retry: [] }>()

const { t } = useI18n()

const tone = computed<Tone>(() => {
  switch (props.state) {
    case 'scanning':
      return 'accent'
    case 'timeout':
      return 'warning'
    case 'failed':
      return 'danger'
    default:
      return 'neutral'
  }
})

const label = computed<MessageKey>(() => `scan.state.${props.state}` as MessageKey)

const action = computed<'cancel' | 'retry' | null>(() => {
  if (props.state === 'scanning') return 'cancel'
  if (props.state === 'cancelled' || props.state === 'timeout' || props.state === 'failed') return 'retry'
  return null
})

const actionLabel = computed<MessageKey>(() => {
  if (props.state === 'scanning') return 'scan.action.cancel'
  // 取消过的叫「恢复扫描」：它不是出错，只是这份文档还差一次扫描。
  if (props.state === 'cancelled') return 'scan.action.resume'
  return 'scan.action.retry'
})

const hint = computed(() => {
  if (props.error !== '') return props.error
  if (props.state === 'cancelled') return t('scan.hint.cancelled')
  return ''
})
</script>

<template>
  <span v-if="state !== 'ok' && state !== 'idle'" class="scan" :data-state="state">
    <StatusPill :label="t(label)" :tone="tone" :pulse="state === 'scanning'" compact />
    <button
      v-if="action !== null"
      type="button"
      class="scan__act"
      :disabled="busy"
      :title="hint"
      @click.stop="action === 'cancel' ? emit('cancel') : emit('retry')"
    >
      {{ t(actionLabel) }}
    </button>
  </span>
</template>

<style scoped>
.scan {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  white-space: nowrap;
}

/* 一行小字按钮：扫描没通过的时候，卡面上唯一该点的东西就是它。 */
.scan__act {
  padding: 0;
  border: 0;
  background: none;
  color: var(--accent-ink);
  font: inherit;
  font-size: var(--fs-1);
  font-weight: 500;
  text-decoration: underline;
  text-decoration-color: var(--accent-line);
  text-underline-offset: 3px;
  cursor: pointer;
}

.scan__act:hover:not(:disabled) {
  text-decoration-color: currentColor;
}

.scan__act:disabled {
  color: var(--ink-3);
  text-decoration-color: transparent;
  cursor: default;
}
</style>
