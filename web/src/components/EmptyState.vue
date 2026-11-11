<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import RewriteSpecimen from './RewriteSpecimen.vue'
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
  manual: []
}>()

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

/** 三条承诺：用户真正担心的三件事，不是流程步骤。 */
const pledges = computed<MessageKey[]>(() => {
  if (mode.value === 'check') {
    return ['empty.check.pledge.a', 'empty.check.pledge.b', 'empty.check.pledge.c']
  }
  if (mode.value === 'download') {
    return ['empty.download.pledge.a', 'empty.download.pledge.b', 'empty.download.pledge.c']
  }
  return ['empty.pledge.copy', 'empty.pledge.once', 'empty.pledge.fail']
})

const specNoteKey = computed<MessageKey>(() => {
  if (mode.value === 'check') return 'empty.spec.note.check'
  if (mode.value === 'download') return 'empty.spec.note.download'
  return 'empty.spec.note'
})

/** 三种写法跟着语言走 —— 英文界面里不该冒出「带 空格.png」。 */
const SPEC_FORMS = ['empty.spec.a', 'empty.spec.b', 'empty.spec.c'] as const
</script>

<template>
  <div class="empty">
    <div class="empty__grid">
      <div class="lead">
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
          <button type="button" class="btn btn--ghost" @click="emit('manual')">
            <AppIcon name="terminal" :size="14" />
            {{ t('queue.manual.toggle') }}
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

        <ul class="pledge">
          <li v-for="key in pledges" :key="key" class="pledge__item">
            <AppIcon name="check" :size="13" class="pledge__mark" />
            {{ t(key) }}
          </li>
        </ul>
      </div>

      <div class="side">
        <RewriteSpecimen :mode="mode" />

        <section class="spec">
          <h3 class="spec__title">{{ t('empty.spec.title') }}</h3>
          <ul class="spec__list mono">
            <li v-for="key in SPEC_FORMS" :key="key">{{ t(key) }}</li>
          </ul>
          <p class="spec__note">{{ t(specNoteKey) }}</p>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 两栏不对称：左边回答「怎么开始」，右边回答「会发生什么」。
   留白是版面的一部分 —— 内容不再挤在一条 560px 的窄柱里、四周空一大片。 */
.empty {
  /* 按内容区自己的宽度断行，不按窗口宽度：侧边栏展开会吃掉 140px，
     用窗口宽度判断的话，900px 窗口 + 展开侧栏会挤成两条 300px 的窄柱。 */
  container-type: inline-size;
  display: grid;
  height: 100%;
  overflow: auto;
  place-items: center;
  /* 内容比容器高时，居中会把顶部推到滚动区外面、再也滚不回来。 */
  align-items: safe center;
  padding: clamp(28px, 4vw, 48px) clamp(24px, 4.5vw, 56px);
}

.empty__grid {
  display: grid;
  grid-template-columns: minmax(0, 1.04fr) minmax(0, 0.96fr);
  gap: 56px;
  align-items: start;
  width: 100%;
  max-width: 940px;
}

.lead {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  min-width: 0;
}

.lead__title {
  margin: 0;
  color: var(--ink);
  font-size: var(--fs-6);
  font-weight: 600;
  letter-spacing: -0.012em;
  line-height: var(--lh-tight);
}

.lead__text {
  max-width: 34ch;
  margin: 12px 0 0;
  color: var(--ink-2);
  font-size: var(--fs-3);
  line-height: 1.75;
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
  gap: 8px;
  margin-top: 12px;
}

/* 最近用过：一排小片，标签比内容淡一档 */
.recent {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
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

/* 三条承诺：没有编号、没有分隔线 —— 它们不是步骤，是并列的保证。 */
.pledge {
  display: flex;
  flex-direction: column;
  gap: 9px;
  margin: 30px 0 0;
  padding: 0;
  list-style: none;
}

.pledge__item {
  display: flex;
  align-items: center;
  gap: 9px;
  color: var(--ink-2);
  font-size: var(--fs-2);
}

.pledge__mark {
  color: var(--accent);
}

.side {
  display: flex;
  flex-direction: column;
  gap: 26px;
  min-width: 0;
}

.spec__title {
  margin: 0 0 10px;
  color: var(--ink-2);
  font-size: var(--fs-2);
  font-weight: 600;
}

.spec__list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin: 0;
  padding: 0;
  color: var(--ink-2);
  font-size: var(--fs-1);
  line-height: 1.7;
  list-style: none;
}

.spec__note {
  margin: 12px 0 0;
  color: var(--ink-3);
  font-size: var(--fs-1);
  line-height: 1.7;
}

/* 窄内容区：先收间距，再并成一栏（配图挪到动作后面，不抢第一动作）。 */
@container (max-width: 900px) {
  .empty__grid {
    gap: 40px;
  }
}

@container (max-width: 720px) {
  .empty__grid {
    grid-template-columns: minmax(0, 1fr);
    gap: 32px;
    max-width: 520px;
  }
}
</style>