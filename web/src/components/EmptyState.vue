<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import { useI18n } from '../i18n'

const props = defineProps<{
  /** 最近用过的路径（最新的在前）。空数组时整块不出现。 */
  recent: string[]
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
  return parts.length >= 2 ? parts.slice(-2).join('\\') : parts[0] ?? path
}

const { t } = useI18n()
</script>

<template>
  <div class="empty">
    <div class="empty__inner">
      <p class="empty__eyebrow">{{ t('empty.eyebrow') }}</p>
      <h2 class="empty__title">{{ t('empty.title') }}</h2>
      <p class="empty__text">{{ t('empty.text') }}</p>

      <div class="empty__actions">
        <button type="button" class="btn btn--primary" @click="emit('pick-files')">
          <AppIcon name="file-plus" :size="14" />
          {{ t('empty.pickFiles') }}
        </button>
        <button type="button" class="btn btn--secondary" @click="emit('pick-folder')">
          <AppIcon name="folder-plus" :size="14" />
          {{ t('empty.pickFolder') }}
        </button>
        <button type="button" class="btn btn--ghost" @click="emit('manual')">
          <AppIcon name="terminal" :size="14" />
          {{ t('queue.manual.toggle') }}
        </button>
      </div>

      <p class="empty__foot">{{ t('empty.foot') }}</p>

      <div v-if="props.recent.length > 0" class="recent">
        <span class="recent__label">{{ t('empty.recent') }}</span>
        <ul class="recent__list">
          <li v-for="path in props.recent" :key="path">
            <button type="button" class="recent__item" :title="path" @click="emit('pick-recent', path)">
              {{ shortPath(path) }}
            </button>
          </li>
        </ul>
      </div>

      <section class="spec">
        <h3 class="spec__title">{{ t('empty.spec.title') }}</h3>
        <ul class="spec__list">
          <li class="spec__item mono">![说明](images/a.png)</li>
          <li class="spec__item mono">![](<带 空格.png>)</li>
          <li class="spec__item mono">&lt;img src="a.png" width="60"&gt;</li>
        </ul>
        <p class="spec__note">{{ t('empty.spec.note') }}</p>
      </section>

      <ol class="steps">
        <li class="step">
          <span class="step__n num">01</span>
          <span class="step__body">
            <span class="step__t">{{ t('empty.step.add') }}</span>
            <span class="step__d">{{ t('empty.step.add.detail') }}</span>
          </span>
        </li>
        <li class="step">
          <span class="step__n num">02</span>
          <span class="step__body">
            <span class="step__t">{{ t('empty.step.upload') }}</span>
            <span class="step__d">{{ t('empty.step.upload.detail') }}</span>
          </span>
        </li>
        <li class="step">
          <span class="step__n num">03</span>
          <span class="step__body">
            <span class="step__t">{{ t('empty.step.write') }}</span>
            <span class="step__d">{{ t('empty.step.write.detail') }}</span>
          </span>
        </li>
      </ol>
    </div>
  </div>
</template>

<style scoped>
/* 最近用过：一排小片，标签比内容淡一档 */
.recent {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-top: 14px;
}

.recent__label {
  color: var(--ink-3);
  font-size: var(--fs-1);
  letter-spacing: 0.04em;
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
  transition: background 120ms var(--ease), color 120ms var(--ease);
}

.recent__item:hover {
  background: var(--panel-2);
  color: var(--ink);
}

/* 一列到底，靠字号与留白分节：没有卡片、没有插图。
   第一屏只回答一个问题——「把东西放进来」。 */
.empty {
  display: flex;
  height: 100%;
  overflow: auto;
  padding: 34px 36px 28px;
}

.empty__inner {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  width: 100%;
  max-width: 560px;
  margin: 0 auto;
}

.empty__eyebrow {
  margin: 0 0 8px;
  color: var(--accent-ink);
  font-size: var(--fs-1);
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.empty__title {
  margin: 0;
  color: var(--ink);
  font-size: var(--fs-4);
  font-weight: 600;
  letter-spacing: -0.005em;
  line-height: var(--lh-tight);
}

.empty__text {
  max-width: 50ch;
  margin: 8px 0 0;
  color: var(--ink-2);
  font-size: var(--fs-3);
  line-height: 1.65;
}

.empty__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-top: 18px;
}

.empty__foot {
  margin: 10px 0 0;
  color: var(--ink-3);
  font-size: var(--fs-1);
}

.spec {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
  margin-top: 26px;
  padding-top: 16px;
  border-top: 1px solid var(--line);
}

.spec__title {
  margin: 0;
  color: var(--ink-3);
  font-size: var(--fs-1);
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
}

.spec__list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.spec__item {
  padding: 3px 8px;
  border: 1px solid var(--line);
  border-radius: var(--r-1);
  background: var(--panel-2);
  color: var(--ink-2);
  font-size: var(--fs-1);
  line-height: 1.6;
}

.spec__note {
  margin: 0;
  color: var(--ink-3);
  font-size: var(--fs-1);
  line-height: 1.6;
}

.steps {
  display: flex;
  flex-direction: column;
  gap: 0;
  width: 100%;
  margin: 22px 0 0;
  padding: 0;
  border-top: 1px solid var(--line);
  list-style: none;
}

.step {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 10px 0;
  border-bottom: 1px solid var(--line);
}

.step:last-child {
  border-bottom: 0;
}

.step__n {
  flex: none;
  width: 18px;
  margin-top: 1px;
  color: var(--ink-3);
  font-family: var(--mono);
  font-size: 10.5px;
  font-weight: 500;
  letter-spacing: 0.02em;
}

.step__body {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  min-width: 0;
}

.step__t {
  flex: none;
  color: var(--ink);
  font-size: var(--fs-2);
  font-weight: 500;
}

.step__d {
  color: var(--ink-3);
  font-size: var(--fs-2);
  line-height: 1.5;
}

@media (max-width: 900px) {
  .empty {
    padding: 24px 20px 20px;
  }
}
</style>