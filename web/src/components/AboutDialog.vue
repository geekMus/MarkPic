<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import AppIcon from './AppIcon.vue'
import { describeError, openTarget, revealPath } from '../api'
import { PROJECT_URL, useUpdate } from '../composables/useUpdate'
import { useI18n } from '../i18n'

const props = defineProps<{
/** 服务端报上来的版本串，例如 2026.11.14+dev (91eca40)。 */
  version: string
  /** 设置与 WebView 缓存所在的位置。 */
  dataDir?: string
  /** 没能放到 exe 旁边时的原因。 */
  dataNote?: string
  /** 设置文件读不出来时的说明（此时跑的是默认值）。 */
  settingsNote?: string
}>()

const emit = defineEmits<{ close: [] }>()

/* 打开就把焦点搬进对话框：键盘操作不该落在背后的页面上 */
const panel = ref<HTMLElement | null>(null)

onMounted(() => {
  void nextTick(() => panel.value?.focus())
})

const { t } = useI18n()

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

const currentLabel = computed(() => props.version.trim() || t('about.unknown'))
const latestLabel = computed(() => status.value.latest.trim() || '—')

const canInstall = computed(() => state.value === 'available' && !busy.value)

/** 一行状态：什么时候该显示什么，全在这里。 */
const statusLine = computed(() => {
  const now = status.value
  switch (state.value) {
    case 'checking':
      return t('about.checking')
    case 'latest':
      return t('about.state.latest')
    case 'available':
      return t('about.state.available')
    case 'downloading':
      return t('about.state.downloading', { percent: percent.value })
    case 'installing':
      return t('about.state.installing')
    case 'restarting':
      return t('about.state.restarting')
    case 'error':
      return error.value || now.error || t('about.state.error')
    default:
      return t('about.state.idle')
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

const dirOpenError = ref('')
const linkError = ref('')

/** 地址只写域名加仓库名，前面那截 https:// 省掉，省得一行挤满。 */
const projectLabel = PROJECT_URL.replace(/^https?:\/\//, '')

/** 打开项目主页：和发布页一样走系统默认浏览器。 */
async function openProject(): Promise<void> {
  linkError.value = ''
  try {
    await openTarget(PROJECT_URL)
  } catch (error) {
    linkError.value = describeError(error, t('error.openBrowser'))
  }
}

/** 在资源管理器里打开数据目录，方便用户备份或清空。 */
async function openDataDir(): Promise<void> {
  const dir = props.dataDir?.trim()
  if (!dir) return
  dirOpenError.value = ''
  try {
    await revealPath(dir)
  } catch (error) {
    dirOpenError.value = describeError(error, t('error.openDataDir'))
  }
}
</script>

<template>
  <!-- 关于 / 更新：版本号在侧边栏左下角，点那里打开 -->
  <div class="dlg" @mousedown.self="emit('close')">
    <section
      ref="panel"
      tabindex="-1"
      class="dlg__panel"
      role="dialog"
      aria-modal="true"
      aria-labelledby="about-title"
      @keydown.esc.stop="emit('close')"
    >
      <header class="panel__head">
        <div class="panel__title">
          <h2 id="about-title">{{ t('about.title') }}</h2>
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

      <div class="dlg__body about">
        <dl class="about__facts">
          <div class="about__fact">
            <dt>{{ t('about.current') }}</dt>
            <dd class="mono">{{ currentLabel }}</dd>
          </div>
          <div class="about__fact">
            <dt>{{ t('about.latest') }}</dt>
            <dd class="mono">{{ latestLabel }}</dd>
          </div>
          <div class="about__fact">
            <dt>{{ t('about.status') }}</dt>
            <dd :data-tone="statusTone">{{ statusLine }}</dd>
          </div>
          <div v-if="sizeLabel && state === 'available'" class="about__fact">
            <dt>{{ t('about.asset') }}</dt>
            <dd class="mono">{{ sizeLabel }}</dd>
          </div>
          <div class="about__fact">
            <dt>{{ t('about.project') }}</dt>
            <dd class="about__path">
              <span
                class="about__path-text mono about__link"
                :title="PROJECT_URL"
                role="button"
                tabindex="0"
                @click="openProject"
                @keydown.enter="openProject"
              >{{ projectLabel }}</span>
              <button type="button" class="btn btn--ghost btn--sm" @click="openProject">
                {{ t('common.open') }}
              </button>
            </dd>
          </div>
          <div v-if="dataDir" class="about__fact">
            <dt>{{ t('about.dataDir') }}</dt>
            <dd class="about__path">
              <span class="about__path-text mono" :title="dataDir">{{ dataDir }}</span>
              <button type="button" class="btn btn--ghost btn--sm" @click="openDataDir">
                {{ t('common.open') }}
              </button>
            </dd>
          </div>
        </dl>

        <p v-if="dataNote" class="about__hint">{{ dataNote }}</p>
        <p v-if="settingsNote" class="about__hint about__hint--bad">{{ settingsNote }}</p>
        <p v-if="dirOpenError" class="about__hint about__hint--bad">{{ dirOpenError }}</p>
        <p v-if="linkError" class="about__hint about__hint--bad">{{ linkError }}</p>

        <div
          v-if="state === 'downloading'"
          class="about__bar"
          role="progressbar"
          :aria-label="t('about.downloadProgress')"
          :aria-valuenow="percent"
          aria-valuemin="0"
          aria-valuemax="100"
        >
          <span class="about__bar-fill" :style="{ width: percent + '%' }" />
        </div>

        <div v-if="notes" class="about__notes">
          <h3 class="about__notes-title">
            {{ t('about.notes') }}
            <span v-if="published" class="about__notes-date mono">{{ published }}</span>
          </h3>
          <pre class="about__notes-body">{{ notes }}</pre>
        </div>

        <label class="check about__auto">
          <input type="checkbox" :checked="autoCheck" @change="onAutoCheck" />
          <span>{{ t('about.autoCheck') }}</span>
        </label>
      </div>

      <footer class="panel__foot">
        <button type="button" class="btn btn--ghost" @click="openReleasePage()">
          {{ t('about.release') }}
        </button>
        <span class="about__spacer" />
        <button
          type="button"
          class="btn btn--secondary"
          :disabled="busy || locked"
          @click="check()"
        >
          {{ state === 'checking' ? t('about.checking') : t('about.checkUpdate') }}
        </button>
        <button type="button" class="btn btn--primary" :disabled="!canInstall" @click="install()">
          {{ t('about.install') }}
        </button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.about__link {
  cursor: pointer;
  color: var(--accent-ink);
}

.about__link:hover {
  text-decoration: underline dotted;
}

.about {
  padding: 2px 20px 16px;
}

/* 事实表：一行一条细线，标签窄、值宽；不再是一张卡片里挤五行 */
.about__facts {
  display: grid;
  gap: 0;
  margin: 0;
}

.about__fact {
  display: grid;
  grid-template-columns: 92px minmax(0, 1fr);
  align-items: baseline;
  gap: 12px;
  min-height: 34px;
  padding: 7px 0;
  border-top: 1px solid var(--line);
}

.about__fact:first-child {
  border-top: 0;
}

.about__fact dt {
  color: var(--ink-3);
  font-size: var(--fs-1);
  letter-spacing: 0.04em;
}

.about__fact dd {
  margin: 0;
  /* 路径这类长内容要能省略号截断，min-width 不能是 auto */
  min-width: 0;
  color: var(--ink);
  font-size: var(--fs-2);
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

/* 数据目录：路径一行显示不下就用省略号，鼠标悬停能看全（title） */
.about__path {
  display: flex;
  align-items: center;
  gap: 8px;
}

.about__path-text {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--ink-2);
}

.about__auto {
  margin-top: 12px;
  padding-top: 10px;
  border-top: 1px solid var(--line);
  color: var(--ink-2);
  font-size: var(--fs-2);
}

.about__hint {
  margin: 10px 0 0;
  color: var(--ink-3);
  font-size: var(--fs-2);
  line-height: 1.5;
}

.about__hint--bad {
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
  /* 凹槽（--track）比面板再暗一档：以前这个令牌根本没定义，整条槽是透明的，
     下载进度只剩那截青色在裸奔。 */
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
