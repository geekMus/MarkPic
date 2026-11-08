<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import type { RunPhase, RunStats } from '../composables/useRun'

const props = defineProps<{
  stats: RunStats
  phase: RunPhase
  busy: boolean
  canStart: boolean
  canReveal: boolean
  dryRun: boolean
}>()

const emit = defineEmits<{
  run: []
  cancel: []
  reveal: []
}>()

const settled = computed(
  () =>
    props.phase === 'running' ||
    props.phase === 'cancelling' ||
    props.phase === 'done' ||
    props.phase === 'cancelled',
)

const summaryText = computed(() => {
  const stats = props.stats
  if (stats.files === 0) return '队列为空'

  const parts = [`${stats.files} 个文件`]
  parts.push(stats.images > 0 ? `${stats.images} 张图` : '未发现图片')
  if (settled.value) {
    parts.push(`替换 ${stats.uploaded}`)
    if (stats.failed > 0) parts.push(`失败 ${stats.failed}`)
  }
  if (props.phase === 'cancelled') parts.push('已取消')
  return parts.join(' · ')
})

const showReveal = computed(
  () => props.canReveal && (props.phase === 'done' || props.phase === 'cancelled'),
)
</script>

<template>
  <footer class="footer">
    <div class="footer__summary">
      <span class="footer__text num" role="status" aria-live="polite">{{ summaryText }}</span>
      <span v-if="dryRun" class="tag footer__dry">试运行</span>
    </div>

    <div class="footer__actions">
      <button
        v-if="showReveal"
        type="button"
        class="btn btn--secondary btn--sm"
        @click="emit('reveal')"
      >
        <AppIcon name="folder-open" :size="14" />
        打开输出目录
      </button>

      <button v-if="phase === 'preparing'" type="button" class="btn btn--primary" disabled>
        启动中
      </button>
      <button v-else-if="phase === 'cancelling'" type="button" class="btn btn--danger" disabled>
        正在取消
      </button>
      <button v-else-if="busy" type="button" class="btn btn--danger" @click="emit('cancel')">
        取消
      </button>
      <button
        v-else
        type="button"
        class="btn btn--primary"
        :disabled="!canStart"
        :title="canStart ? '开始转换' : '请先加入文件'"
        @click="emit('run')"
      >
        开始转换
      </button>
    </div>
  </footer>
</template>

<style scoped>
.footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  height: 56px;
  padding: 0 14px;
  border-top: 1px solid var(--border);
  background: var(--surface);
  z-index: 20;
}

.footer__summary {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.footer__text {
  color: var(--text-2);
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.footer__dry {
  border-color: var(--warning-border);
  background: var(--warning-soft);
  color: var(--warning);
  flex: none;
}

.footer__actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
}
</style>
