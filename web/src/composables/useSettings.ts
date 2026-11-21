import { computed, reactive, ref } from 'vue'
import { describeError, getSettings, putSettings, setCloseAction } from '../api'
import { t } from '../i18n'
import type { MessageKey } from '../i18n'
import type { Settings } from '../types'
import { useToasts } from './useToasts'
import { useHealth } from './useHealth'

const { danger, warning, success } = useToasts()

export interface OutputModeOption {
  value: Settings['output']
  /** 文案放在 i18n 里（见 web/src/i18n），这里只留键名。 */
  labelKey: MessageKey
  hintKey: MessageKey
}

export const OUTPUT_MODES: readonly OutputModeOption[] = [
  { value: 'sidecar', labelKey: 'settings.output.sidecar', hintKey: 'settings.output.sidecar.hint' },
  { value: 'in-place', labelKey: 'settings.output.inPlace', hintKey: 'settings.output.inPlace.hint' },
  {
    value: 'directory',
    labelKey: 'settings.output.directory',
    hintKey: 'settings.output.directory.hint',
  },
]

const SETTINGS_KEYS: readonly (keyof Settings)[] = [
  'endpoint',
  'concurrency',
  'retries',
  'output',
  'suffix',
  'outDir',
  'backup',
  'reuseUploads',
  'remote',
  'remoteDir',
  'closeAction',
  'startInTray',
]

function defaultSettings(): Settings {
  return {
    endpoint: 'http://127.0.0.1:36677',
    // 与服务端 internal/settings.Default() 保持一致：真实图床上传是纯等网络，
    // 实测（150 KB 的图、59 张）并发 16 比并发 8 快一倍左右，再往上就没收益了。
    concurrency: 48,
    retries: 2,
    output: 'sidecar',
    suffix: '.converted',
    outDir: '',
    backup: true,
    // 同一张图第二次跑就不用再传了，直接复用上次拿到的地址。
    reuseUploads: true,
    // 默认什么都不做：远程图原样留着，和以前的行为一致。
    remote: 'ignore',
    remoteDir: 'images',
    closeAction: 'ask',
    startInTray: false,
  }
}

function clampInt(value: number, min: number, max: number, fallback: number): number {
  if (!Number.isFinite(value)) return fallback
  return Math.min(max, Math.max(min, Math.round(value)))
}

/** 把服务端返回或用户输入收敛成契约允许的形状，避免非法值进入请求体。 */
function normalize(value: Partial<Settings> | null | undefined): Settings {
  const base = defaultSettings()
  if (!value) return base

  const output =
    value.output === 'sidecar' || value.output === 'in-place' || value.output === 'directory'
      ? value.output
      : base.output

  return {
    endpoint:
      typeof value.endpoint === 'string' && value.endpoint.trim() !== ''
        ? value.endpoint.trim()
        : base.endpoint,
    concurrency: clampInt(
      typeof value.concurrency === 'number' ? value.concurrency : base.concurrency,
      1,
      64,
      base.concurrency,
    ),
    retries: clampInt(
      typeof value.retries === 'number' ? value.retries : base.retries,
      0,
      5,
      base.retries,
    ),
    output,
    suffix: typeof value.suffix === 'string' ? value.suffix : base.suffix,
    outDir: typeof value.outDir === 'string' ? value.outDir.trim() : base.outDir,
    backup: typeof value.backup === 'boolean' ? value.backup : base.backup,
    reuseUploads:
      typeof value.reuseUploads === 'boolean' ? value.reuseUploads : base.reuseUploads,
    remote:
      value.remote === 'ignore' || value.remote === 'check' || value.remote === 'download'
        ? value.remote
        : base.remote,
    remoteDir:
      typeof value.remoteDir === 'string' && value.remoteDir.trim() !== ''
        ? value.remoteDir.trim()
        : base.remoteDir,
    closeAction:
      value.closeAction === 'ask' || value.closeAction === 'tray' || value.closeAction === 'exit'
        ? value.closeAction
        : (value.closeAction as string | undefined) === 'minimize'
          ? 'tray'
          : base.closeAction,
    startInTray:
      typeof value.startInTray === 'boolean' ? value.startInTray : base.startInTray,
  }
}

function clone(value: Settings): Settings {
  return { ...value }
}

function sameSettings(a: Settings, b: Settings): boolean {
  return SETTINGS_KEYS.every(key => a[key] === b[key])
}

const draft = reactive<Settings>(defaultSettings())
const baseline = ref<Settings>(defaultSettings())
const loaded = ref(false)
const loading = ref(false)
const saving = ref(false)
const loadError = ref<string | null>(null)
let inflight: Promise<void> | null = null

const dirty = computed(() => !sameSettings(draft, baseline.value))

function adopt(value: Partial<Settings> | null | undefined): void {
  const normalized = normalize(value)
  Object.assign(draft, normalized)
  baseline.value = clone(normalized)
}

function load(): Promise<void> {
  if (inflight !== null) return inflight

  loading.value = true
  inflight = (async () => {
    try {
      const remote = await getSettings()
      adopt(remote)
      loaded.value = true
      loadError.value = null
    } catch (err) {
      loadError.value = describeError(err, t('error.loadSettings'))
      danger(loadError.value)
    } finally {
      loading.value = false
      inflight = null
    }
  })()

  return inflight
}

function update(patch: Partial<Settings>): void {
  Object.assign(draft, patch)
}

function discard(): void {
  adopt(baseline.value)
}

async function save(): Promise<boolean> {
  if (saving.value) return false
  if (!loaded.value) {
    warning(t('error.settingsNotLoaded'))
    return false
  }

  const merged = { ...draft }
  const endpoint = merged.endpoint.trim()
  // 只有真要上传时才非得有 PicGo 地址：只检测 / 只下载远程图、以及调试构建都不上传，
  // 这时候拦住「保存」纯属添乱（服务端本来也会把空地址回落成默认值）。
  const uploads = merged.remote === 'ignore' && !useHealth().health.value?.debug
  if (uploads && endpoint === '') {
    warning(t('error.picgoRequired'))
    return false
  }
  if (merged.output === 'directory' && merged.outDir.trim() === '') {
    warning(t('error.outDirRequired'))
    return false
  }
  if (merged.remote !== 'ignore' && merged.remoteDir.trim() === '') {
    warning(t('error.remoteDirRequired'))
    return false
  }

  saving.value = true
  try {
    const payload = normalize({ ...merged, endpoint })
    const persisted = await putSettings(payload)
    adopt(persisted)
    loaded.value = true
    loadError.value = null
    success(t('toast.settingsSaved'))
    return true
  } catch (err) {
    danger(describeError(err, t('error.saveSettings')))
    return false
  } finally {
    saving.value = false
  }
}

/** 关窗时勾了「不再询问」：只把这一项写回设置，保留别的草稿与脏标记。 */
async function rememberCloseAction(action: Settings['closeAction']): Promise<boolean> {
  try {
    const persisted = await setCloseAction(action)
    draft.closeAction = persisted.closeAction
    baseline.value = { ...baseline.value, closeAction: persisted.closeAction }
    return true
  } catch (err) {
    danger(describeError(err, t('error.saveSettings')))
    return false
  }
}

export function useSettings() {
  return {
    draft,
    baseline,
    dirty,
    loaded,
    loading,
    saving,
    loadError,
    load,
    save,
    rememberCloseAction,
    update,
    discard,
    outputModes: OUTPUT_MODES,
  }
}
