<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import { useI18n } from '../i18n'

const emit = defineEmits<{
  'pick-files': []
  'pick-folder': []
  manual: []
}>()

const { t } = useI18n()
</script>

<template>
  <div class="empty">
    <div class="empty__inner">
      <h2 class="empty__title">{{ t('empty.title') }}</h2>
      <p class="empty__text">{{ t('empty.text') }}</p>

      <div class="empty__actions">
        <button type="button" class="btn btn--primary btn--lg" @click="emit('pick-files')">
          <AppIcon name="file-plus" :size="15" />
          {{ t('empty.pickFiles') }}
        </button>
        <button type="button" class="btn btn--secondary btn--lg" @click="emit('pick-folder')">
          <AppIcon name="folder-plus" :size="15" />
          {{ t('empty.pickFolder') }}
        </button>
        <button type="button" class="btn btn--ghost btn--lg" @click="emit('manual')">
          <AppIcon name="terminal" :size="15" />
          {{ t('queue.manual.toggle') }}
        </button>
      </div>

      <p class="empty__foot">{{ t('empty.foot') }}</p>

      <!-- 支持的写法：第一次用的人最想知道「我这份文档能不能被认出来」 -->
      <section class="spec">
        <h3 class="spec__title">{{ t('empty.spec.title') }}</h3>
        <ul class="spec__list">
          <li class="spec__item mono">![说明](images/a.png)</li>
          <li class="spec__item mono">![](<带 空格.png>)</li>
          <li class="spec__item mono">&lt;img src="a.png" width="60"&gt;</li>
        </ul>
        <p class="spec__note">{{ t('empty.spec.note') }}</p>
      </section>

      <!-- 三步说明：竖着排。这一页只有一列，读下来就是一条线。 -->
      <ol class="steps">
        <li class="step">
          <span class="step__n num">1</span>
          <span class="step__body">
            <span class="step__t">{{ t('empty.step.add') }}</span>
            <span class="step__d">{{ t('empty.step.add.detail') }}</span>
          </span>
        </li>
        <li class="step">
          <span class="step__n num">2</span>
          <span class="step__body">
            <span class="step__t">{{ t('empty.step.upload') }}</span>
            <span class="step__d">{{ t('empty.step.upload.detail') }}</span>
          </span>
        </li>
        <li class="step">
          <span class="step__n num">3</span>
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
/* 一列到底：标题、说明、按钮、支持的写法、三步。
   以前这里是「左边文案 + 右边插图 + 底下三步」，看着像三个独立板块，
   其实只是一件事情的入口。 */
.empty {
  display: flex;
  height: 100%;
  overflow: auto;
  padding: 34px 40px 30px;
}

.empty__inner {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 12px;
  width: 100%;
  max-width: 560px;
  margin: 0 auto;
}

.empty__title {
  margin: 0;
  color: var(--ink);
  font-size: 24px;
  font-weight: 600;
  letter-spacing: -0.015em;
  line-height: 1.3;
}

.empty__text {
  margin: 0;
  color: var(--ink-2);
  font-size: var(--fs-3);
  line-height: 1.7;
}

.empty__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
}

.empty__foot {
  margin: 0;
  color: var(--ink-3);
  font-size: var(--fs-1);
}

.spec {
  display: flex;
  flex-direction: column;
  gap: 10px;
  width: 100%;
  margin-top: 14px;
  padding: 14px 16px;
  border: 1px solid var(--line);
  border-radius: var(--r-3);
  background: var(--panel-2);
}

.spec__title {
  margin: 0;
  color: var(--ink-3);
  font-size: var(--fs-1);
  font-weight: 600;
  letter-spacing: 0.01em;
}

.spec__list {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.spec__item {
  padding: 5px 9px;
  border: 1px solid var(--line-2);
  border-radius: var(--r-1);
  background: var(--panel);
  color: var(--ink-2);
  font-size: var(--fs-2);
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
  gap: 10px;
  width: 100%;
  margin: 10px 0 0;
  padding: 14px 0 0;
  border-top: 1px solid var(--line);
  list-style: none;
}

.step {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  min-width: 0;
}

.step__n {
  display: grid;
  place-items: center;
  flex: none;
  width: 20px;
  height: 20px;
  margin-top: 1px;
  border: 1px solid var(--accent-line);
  border-radius: 50%;
  background: var(--accent-soft);
  color: var(--accent-ink);
  font-size: 11px;
  font-weight: 600;
}

.step__body {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.step__t {
  color: var(--ink);
  font-size: var(--fs-2);
  font-weight: 600;
}

.step__d {
  color: var(--ink-3);
  font-size: var(--fs-1);
  line-height: 1.5;
}

@media (max-width: 900px) {
  .empty {
    padding: 22px 20px 18px;
  }

  .empty__title {
    font-size: 21px;
  }

  .steps {
    padding-top: 12px;
  }
}
</style>