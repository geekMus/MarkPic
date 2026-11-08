<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import { useUpdate } from '../composables/useUpdate'

const props = defineProps<{
  /** 服务端报上来的版本串，例如 1.0.0 (b7fb0d2)。 */
  version: string
}>()

const emit = defineEmits<{ close: [] }>()

const {
  status,
  state,
  busy,
  locked,
  error,
  autoCheck,
  percent,
  check,
  install,
  openReleasePage,
  setAutoCheck,
} = useUpdate()

const currentLabel = computed(() => props.version.trim() || '未知')
const latestLabel = computed(() => status.value.latest.trim() || '—')

const canInstall = computed(() => state.value === 'available' && !busy.value)

/** 一行状态：什么时候该显示什么，全在这里。 */
const statusLine = computed(() => {
  const now = status.value
  switch (state.value) {
    case 'checking':
      return '正在检查…'
    case 'latest':
      return '已是最新版本'
    case 'available':
      return '有新版本可以更新'
    case 'downloading':
      return `正在下载 ${percent.value}%`
    case 'installing':
      return '正在校验并替换程序…'
    case 'restarting':
      return '正在重启，界面马上会重新打开'
    case 'error':
      return error.value || now.error || '检查失败'
    default:
      return '还没有检查过'
  }
})

const statusTone = computed(() => {
  switch (state.value) {
    case 'latest':
      return 'ok'
    case 'available':
      return 'accent'
    case 'error':
      return 'bad'
    case 'restarting':
      return 'accent'
    default:
      return 'quiet'
  }
})

const notes = computed(() => status.value.notes.trim())
const published = computed(() => status.value.published.trim())

const sizeLabel = computed(() => {
  const size = status.value.asset.size
  if (size <= 0) return ''
  return `${(size / (1024 * 1024)).toFixed(1)} MB`
})

function onAutoCheck(event: Event): void {
  setAutoCheck((event.target as HTMLInputElement).checked)
}
</script>

<template>
  <!-- 关于 / 更新：版本号从标题栏挪到这里，点底栏的版本号就能打开 -->
  <div class="dlg" @mousedown.self="emit('close')">
    <section
      class="dlg__panel"
      role="dialog"
      aria-modal="true"
      aria-labelledby="about-title"
      @keydown.esc.stop="emit('close')"
    >
      <header class="panel__head">
        <div class="panel__title">
          <h2 id="about-title">关于</h2>
          <span class="panel__note mono">convert-md-picture</span>
        </div>
        <div class="panel__actions">
          <button type="button" class="icon-btn" title="关闭" aria-label="关闭" @click="emit('close')">
            <AppIcon name="x" :size="16" />
          </button>
        </div>
      </header>

      <div class="dlg__body about">
        <dl class="about__facts">
          <div class="about__fact">
            <dt>当前版本</dt>
            <dd class="mono">{{ currentLabel }}</dd>
          </div>
          <div class="about__fact">
            <dt>最新版本</dt>
            <dd class="mono">{{ latestLabel }}</dd>
          </div>
          <div class="about__fact">
            <dt>状态</dt>
            <dd :data-tone="statusTone">{{ statusLine }}</dd>
          </div>
          <div v-if="sizeLabel && state === 'available'" class="about__fact">
            <dt>安装包</dt>
            <dd class="mono">{{ sizeLabel }}</dd>
          </div>
        </dl>

        <div
          v-if="state === 'downloading'"
          class="about__bar"
          role="progressbar"
          aria-label="下载进度"
          :aria-valuenow="percent"
          aria-valuemin="0"
          aria-valuemax="100"
        >
          <span class="about__bar-fill" :style="{ width: percent + '%' }" />
        </div>

        <div v-if="notes" class="about__notes">
          <h3 class="about__notes-title">
            更新说明
            <span v-if="published" class="about__notes-date mono">{{ published }}</span>
          </h3>
          <pre class="about__notes-body">{{ notes }}</pre>
        </div>

        <label class="check about__auto">
          <input type="checkbox" :checked="autoCheck" @change="onAutoCheck" />
          <span>启动时自动检查更新</span>
        </label>
      </div>

      <footer class="panel__foot">
        <button type="button" class="btn btn--ghost" @click="openReleasePage()">打开发布页</button>
        <span class="about__spacer" />
        <button
          type="button"
          class="btn btn--secondary"
          :disabled="busy || locked"
          @click="check()"
        >
          {{ state === 'checking' ? '检查中…' : '检查更新' }}
        </button>
        <button type="button" class="btn btn--primary" :disabled="!canInstall" @click="install()">
          立即更新
        </button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.about {
  padding: 14px 18px 16px;
}

.about__facts {
  display: grid;
  gap: 8px;
  margin: 0;
}

.about__fact {
  display: grid;
  grid-template-columns: 76px minmax(0, 1fr);
  align-items: baseline;
  gap: 10px;
}

.about__fact dt {
  color: var(--ink-3);
  font-size: var(--fs-2);
}

.about__fact dd {
  margin: 0;
  font-size: var(--fs-3);
  word-break: break-all;
}

.about__fact dd[data-tone='ok'] {
  color: var(--ok);
}

.about__fact dd[data-tone='accent'] {
  color: var(--accent-ink);
}

.about__fact dd[data-tone='bad'] {
  color: var(--bad);
}

.about__fact dd[data-tone='quiet'] {
  color: var(--ink-2);
}

.about__bar {
  height: 3px;
  margin: 14px 0 0;
  overflow: hidden;
  border-radius: var(--r-pill);
  background: var(--track);
}

.about__bar-fill {
  display: block;
  height: 100%;
  background: var(--accent);
  transition: width 200ms var(--ease);
}

.about__notes {
  margin-top: 14px;
  padding-top: 12px;
  border-top: 1px solid var(--line);
}

.about__notes-title {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 10px;
  margin: 0 0 6px;
  color: var(--ink-3);
  font-size: var(--fs-2);
  font-weight: 500;
}

.about__notes-date {
  font-size: var(--fs-1);
}

.about__notes-body {
  max-height: 132px;
  margin: 0;
  overflow-y: auto;
  color: var(--ink-2);
  font-family: var(--font);
  font-size: var(--fs-2);
  line-height: 1.55;
  white-space: pre-wrap;
}

.about__auto {
  margin-top: 14px;
  color: var(--ink-2);
  font-size: var(--fs-2);
}

.about__spacer {
  margin-right: auto;
}

@media (prefers-reduced-motion: reduce) {
  .about__bar-fill {
    transition: none;
  }
}
</style>
