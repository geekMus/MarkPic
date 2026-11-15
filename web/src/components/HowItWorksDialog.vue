<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import AppIcon from './AppIcon.vue'
import RewriteSpecimen from './RewriteSpecimen.vue'
import { useI18n, type MessageKey } from '../i18n'
import type { JobView } from '../types'

/**
 * 「看它怎么改」弹窗：把空态里那部分原理说明（示例纸 + 三条承诺 + 一句说明）
 * 收进来，空态本身只留「拖进来」这一个动作。
 *
 * 复用 AboutDialog 的 .dlg / .dlg__panel / .panel__head 弹窗骨架，
 * 内容跟当前菜单走：转换 / 检测 / 下载各说各的。
 */
const props = defineProps<{ mode: JobView }>()

const emit = defineEmits<{ close: [] }>()

const panel = ref<HTMLElement | null>(null)
onMounted(() => {
  void nextTick(() => panel.value?.focus())
})

const { t } = useI18n()

/** 三条承诺：用户真正担心的三件事，不是流程步骤。 */
const pledges = computed<MessageKey[]>(() => {
  if (props.mode === 'check') {
    return ['empty.check.pledge.a', 'empty.check.pledge.b', 'empty.check.pledge.c']
  }
  if (props.mode === 'download') {
    return ['empty.download.pledge.a', 'empty.download.pledge.b', 'empty.download.pledge.c']
  }
  return ['empty.pledge.copy', 'empty.pledge.once', 'empty.pledge.fail']
})

const specNoteKey = computed<MessageKey>(() => {
  if (props.mode === 'check') return 'empty.spec.note.check'
  if (props.mode === 'download') return 'empty.spec.note.download'
  return 'empty.spec.note'
})
</script>

<template>
  <!-- 弹窗挂到 body：EmptyState 深埋在带 overflow 的滚动容器里，固定定位会被祖先
       transform/overflow 影响，teleport 出去后 .dlg 的 fixed 才真正相对窗口。 -->
  <Teleport to="body">
    <div class="dlg" @mousedown.self="emit('close')">
      <section
        ref="panel"
        tabindex="-1"
        class="dlg__panel"
        role="dialog"
        aria-modal="true"
        aria-labelledby="how-title"
        @keydown.esc.stop="emit('close')"
      >
        <header class="panel__head">
          <div class="panel__title">
            <h2 id="how-title">{{ t('empty.how.title') }}</h2>
          </div>
          <div class="panel__actions">
            <button
              type="button"
              class="icon-btn"
              :title="t('common.close')"
              :aria-label="t('common.close')"
              @click="emit('close')"
            >
              <AppIcon name="x" :size="16" />
            </button>
          </div>
        </header>

        <div class="dlg__body how">
          <div class="how__specimen">
            <RewriteSpecimen :mode="mode" />
          </div>

          <ul class="how__pledges">
            <li v-for="key in pledges" :key="key" class="how__pledge">
              <AppIcon name="check" :size="13" class="how__mark" />
              {{ t(key) }}
            </li>
          </ul>

          <p class="how__note">{{ t(specNoteKey) }}</p>
        </div>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.how {
  padding: 4px 22px 18px;
}

.how__specimen {
  margin-top: 2px;
}

/* 三条承诺：没有编号、没有分隔线 —— 并列的保证，不是步骤。 */
.how__pledges {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin: 18px 0 0;
  padding: 14px 0 0;
  border-top: 1px solid var(--line);
  list-style: none;
}

.how__pledge {
  display: flex;
  align-items: center;
  gap: 9px;
  color: var(--ink-2);
  font-size: var(--fs-2);
}

.how__mark {
  color: var(--accent);
}

.how__note {
  margin: 14px 0 0;
  color: var(--ink-3);
  font-size: var(--fs-1);
  line-height: 1.7;
}
</style>
