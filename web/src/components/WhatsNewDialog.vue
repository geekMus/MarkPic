<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import AppIcon from './AppIcon.vue'
import ReleaseNotes from './ReleaseNotes.vue'
import { describeError, openTarget } from '../api'
import { RELEASES_URL, useUpdate } from '../composables/useUpdate'
import { useI18n } from '../i18n'

/**
 * 「更新说明」对话框。
 *
 * 关于弹窗里只剩一行「更新说明 v2026.11.17 … 打开」，正文搬到这儿来 ——
 * 做法照 magpie 的 What's new：一个独立弹窗，顶部是版本号和日期，正文按
 * 分类小标题排发布说明，底下给「打开发布页」和「关闭」。
 *
 * 内容来自版本清单的 notesBody（发布时整篇写进去的更新说明）；老清单没有
 * 这个字段时退回 notes 那句摘要，再没有就说没拿到，并让人去发布页看。
 */
const emit = defineEmits<{ close: [] }>()

const panel = ref<HTMLElement | null>(null)
onMounted(() => {
  void nextTick(() => panel.value?.focus())
})

const { t } = useI18n()
const { status, state } = useUpdate()

const version = computed(() => status.value.latest.trim() || status.value.current.trim())
const published = computed(() => status.value.published.trim())
const notes = computed(() => status.value.notesBody.trim() || status.value.notes.trim())
const hasUpdate = computed(() => state.value === 'available')
const releasePage = computed(() => status.value.releasePage.trim() || RELEASES_URL)

const linkError = ref('')

async function openRelease(): Promise<void> {
  linkError.value = ''
  try {
    await openTarget(releasePage.value)
  } catch (error) {
    linkError.value = describeError(error, t('error.openBrowser'))
  }
}
</script>

<template>
  <Teleport to="body">
    <div class="dlg" @mousedown.self="emit('close')">
      <section
        ref="panel"
        tabindex="-1"
        class="dlg__panel dlg__panel--notes"
        role="dialog"
        aria-modal="true"
        aria-labelledby="whatsnew-title"
        @keydown.esc.stop="emit('close')"
      >
        <header class="panel__head">
          <div class="panel__title">
            <h2 id="whatsnew-title">{{ t('whatsnew.title') }}</h2>
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

        <div class="dlg__body wn">
          <div class="wn__meta">
            <span class="wn__ver mono">{{ version ? 'v' + version : t('about.unknown') }}</span>
            <span v-if="published" class="wn__date mono">{{ published }}</span>
            <span v-if="hasUpdate" class="wn__badge">{{ t('whatsnew.newVersion') }}</span>
          </div>

          <ReleaseNotes v-if="notes" :markdown="notes" />
          <p v-else class="wn__empty">{{ t('whatsnew.empty') }}</p>

          <p v-if="linkError" class="wn__error selectable">{{ linkError }}</p>
        </div>

        <footer class="panel__foot">
          <button type="button" class="btn btn--ghost" @click="openRelease">
            {{ t('whatsnew.release') }}
          </button>
          <span class="wn__spacer" />
          <button type="button" class="btn btn--primary" @click="emit('close')">
            {{ t('common.close') }}
          </button>
        </footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
/* 比关于宽一档：更新说明是成段的列表，太窄会一行折三次 */
.dlg__panel--notes {
  width: min(600px, 100%);
}

.wn {
  padding: 2px 22px 14px;
}

.wn__meta {
  display: flex;
  align-items: baseline;
  gap: 10px;
  margin-bottom: 12px;
  padding: 10px 0 10px;
  border-bottom: 1px solid var(--line);
}

.wn__ver {
  color: var(--ink);
  font-size: var(--fs-2);
  font-weight: 600;
}

.wn__date {
  color: var(--ink-3);
  font-size: var(--fs-1);
}

.wn__badge {
  margin-left: auto;
  padding: 1px 8px;
  border-radius: var(--r-pill);
  background: var(--track);
  color: var(--accent-ink);
  font-size: var(--fs-1);
}

.wn__empty {
  margin: 8px 0 0;
  color: var(--ink-3);
  line-height: 1.6;
}

.wn__error {
  margin: 12px 0 0;
  color: var(--bad);
}

.wn__spacer {
  margin-right: auto;
}
</style>
