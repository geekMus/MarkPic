import { computed, reactive, ref } from 'vue'
import { describeError, getSettings, putSettings } from '../api'
import { t } from '../i18n'
import type { MessageKey } from '../i18n'
import type { Settings } from '../types'
import { useToasts } from './useToasts'

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
  'dryRun',
]

function defaultSettings(): Settings {
  return {
    endpoint: 'http://127.0.0.1:36677',
    concurrency: 4,
    retries: 2,
    output: 'sidecar',
    suffix: '.converted',
    outDir: '',
    backup: false,
    dryRun: false,
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
      16,
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
    dryRun: typeof value.dryRun === 'boolean' ? value.dryRun : base.dryRun,
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

/** 供发起转换 / 预览时使用的稳定快照。 */
function snapshot(): Settings {
  return normalize(draft)
}

async function save(): Promise<boolean> {
  if (saving.value) return false
  if (!loaded.value) {
    warning(t('error.settingsNotLoaded'))
    return false
  }

  const endpoint = draft.endpoint.trim()
  if (endpoint === '') {
    warning(t('error.picgoRequired'))
    return false
  }
  if (draft.output === 'directory' && draft.outDir.trim() === '') {
    warning(t('error.outDirRequired'))
    return false
  }

  saving.value = true
  try {
    const payload = normalize({ ...draft, endpoint })
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
    update,
    discard,
    snapshot,
    outputModes: OUTPUT_MODES,
  }
}
