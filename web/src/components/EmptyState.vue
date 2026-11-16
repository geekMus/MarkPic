<script setup lang="ts">
import { computed, ref } from 'vue'
import AppIcon from './AppIcon.vue'
import HowItWorksDialog from './HowItWorksDialog.vue'
import { useI18n, type MessageKey } from '../i18n'
import { viewOfMode, type JobView, type RemoteMode } from '../types'

const props = defineProps<{
  /** 最近用过的路径（最新的在前）。空数组时整块不出现。 */
  recent: string[]
  /** 当前菜单：同一套版面，三种作业说三件不同的事。 */
  remoteMode?: RemoteMode
}>()

const emit = defineEmits<{
  'pick-files': []
  'pick-folder': []
  'pick-recent': [path: string]
}>()

/** 原理收进弹窗，空态只留动作，所以这里只有「弹窗开没开」。 */
const howOpen = ref(false)

/**
 * 显示最后两段，整条路径留给 title。
 * 只取最后一段时，两个都叫 docs 的目录在列表里长得一模一样，选了哪个全靠猜。
 */
function shortPath(path: string): string {
  const parts = path.replace(/[\\/]+$/, '').split(/[\\/]+/)
  return parts.length >= 2 ? parts.slice(-2).join('\\') : (parts[0] ?? path)
}

const { t } = useI18n()

/** 队列是三条作业共用的，空态文案跟着菜单走。 */
const mode = computed<JobView>(() => viewOfMode(props.remoteMode ?? 'ignore'))

const titleKey = computed<MessageKey>(() => {
  if (mode.value === 'check') return 'empty.check.title'
  if (mode.value === 'download') return 'empty.download.title'
  return 'empty.title'
})

const textKey = computed<MessageKey>(() => {
  if (mode.value === 'check') return 'empty.check.text'
  if (mode.value === 'download') return 'empty.download.text'
  return 'empty.text'
})
</script>

<template>
  <div class="empty">
    <div class="empty__lead">
      <h2 class="lead__title">{{ t(titleKey) }}</h2>
      <p class="lead__text">{{ t(textKey) }}</p>

      <!-- 整个窗口都接拖放，但以前没人看得出来。把最快的路径摆成第一动作。 -->
      <button type="button" class="drop" @click="emit('pick-files')">
        <AppIcon name="file-plus" :size="20" class="drop__icon" />
        <span class="drop__body">
          <span class="drop__title">{{ t('empty.drop.title') }}</span>
          <span class="drop__hint">
            {{ t('empty.drop.or') }}
            <span class="drop__link">{{ t('empty.pickFiles') }}</span>
          </span>
        </span>
      </button>

      <div class="alt">
        <button type="button" class="btn btn--secondary" @click="emit('pick-folder')">
          <AppIcon name="folder-plus" :size="14" />
          {{ t('empty.pickFolder') }}
        </button>
        <button type="button" class="btn btn--ghost" @click="howOpen = true">
          <AppIcon name="image" :size="14" />
          {{ t('empty.how.open') }}
        </button>
      </div>

      <div v-if="props.recent.length > 0" class="recent">
        <span class="recent__label">{{ t('empty.recent') }}</span>
        <ul class="recent__list">
          <li v-for="path in props.recent" :key="path">
            <button
              type="button"
              class="recent__item"
              :title="path"
              @click="emit('pick-recent', path)"
            >
              {{ shortPath(path) }}
            </button>
          </li>
        </ul>
      </div>
    </div>

    <HowItWorksDialog v-if="howOpen" :mode="mode" @close="howOpen = false" />
  </div>
</template>

<style scoped>
/* 单栏、从顶部起排：空态只回答「怎么开始」这一件事，原理收进弹窗。
   窗口最窄（500x560）时内容仍比容器高，所以它自己滚 —— .empty__lead 的 margin-block: auto
   在有富余时负责居中，内容超高时 auto 归零、从顶部起排，不会被上下同时裁掉。 */
.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: flex-start;
  height: 100%;
  min-height: 0;
  padding: 24px 34px;
  overflow-y: auto;
}

.empty__lead {
  display: flex;
  flex-direction: column;
  align-items: center;
  width: 100%;
  max-width: 640px;
  margin-block: auto;
}

.lead__title {
  margin: 0;
  color: var(--ink);
  font-size: var(--fs-6);
  font-weight: 600;
  letter-spacing: -0.012em;
  line-height: var(--lh-tight);
  text-align: center;
}

.lead__text {
  /* 默认窗口下尽量一行；窄窗口由 .empty__lead 的宽度自然折行。 */
  max-width: none;
  margin: 12px 0 0;
  color: var(--ink-2);
  font-size: var(--fs-3);
  line-height: 1.75;
  text-align: center;
}

/* 第一动作：既是拖放区，也是「选择文件」按钮。 */
.drop {
  display: flex;
  align-items: center;
  gap: 14px;
  width: 100%;
  margin-top: 28px;
  padding: 18px 20px;
  border: 1px dashed var(--line-3);
  border-radius: var(--r-4);
  background: var(--panel-2);
  color: var(--ink);
  font: inherit;
  text-align: left;
  cursor: pointer;
  transition:
    border-color var(--t-base) var(--ease),
    background var(--t-base) var(--ease);
}

.drop:hover {
  border-color: var(--accent-line);
  background: var(--accent-soft);
}

.drop__icon {
  color: var(--ink-3);
  transition: color var(--t-base) var(--ease);
}

.drop:hover .drop__icon {
  color: var(--accent-ink);
}

.drop__body {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}

.drop__title {
  font-size: var(--fs-4);
  font-weight: 600;
}

.drop__hint {
  color: var(--ink-3);
  font-size: var(--fs-2);
}

.drop__link {
  color: var(--accent-ink);
  font-weight: 600;
}

.alt {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 8px;
  margin-top: 12px;
}

/* 最近用过：一排小片，标签比内容淡一档 */
.recent {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: 8px;
  margin-top: 20px;
}

.recent__label {
  color: var(--ink-3);
  font-size: var(--fs-1);
}

.recent__list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.recent__item {
  max-width: 180px;
  overflow: hidden;
  padding: 3px 9px;
  border: 1px solid var(--line);
  border-radius: var(--r-3);
  background: transparent;
  color: var(--ink-2);
  font: inherit;
  font-size: var(--fs-1);
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: pointer;
  transition:
    background var(--t-fast) var(--ease),
    color var(--t-fast) var(--ease);
}

.recent__item:hover {
  background: var(--panel-2);
  color: var(--ink);
}

/* 窄窗口：外层容器收到 20px（ReportShell / FileQueue 同一档），这里也收到 20px ——
   两层加起来 40px，正文还剩 244px，大标题从 3 行回到 2 行，「选文件」按钮整块落在首屏里。
   34px 是给宽窗口的；窄窗口下那点心边距不值当换掉一行标题。 */
@media (max-width: 900px) {
  .empty {
    padding-inline: 20px;
  }
}
</style>
