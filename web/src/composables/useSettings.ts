import { computed, reactive, ref } from 'vue'
import { describeError, getSettings, putSettings } from '../api'
import type { Settings } from '../types'
import { useToasts } from './useToasts'

const { danger, warning, success } = useToasts()

export interface OutputModeOption {
  value: Settings['output']
  label: string
  hint: string
}

export const OUTPUT_MODES: readonly OutputModeOption[] = [
  { value: 'sidecar', label: '旁挂副本', hint: '生成后缀副本，原文件不动' },
  { value: 'in-place', label: '原地替换', hint: '直接改写原文件' },
  { value: 'directory', label: '指定目录', hint: '统一写入输出目录' },
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
      loadError.value = describeError(err, '读取设置失败')
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
    warning('设置尚未读取成功，暂不能保存')
    return false
  }

  const endpoint = draft.endpoint.trim()
  if (endpoint === '') {
    warning('请填写 PicGo 服务地址')
    return false
  }
  if (draft.output === 'directory' && draft.outDir.trim() === '') {
    warning('请选择输出目录')
    return false
  }

  saving.value = true
  try {
    const payload = normalize({ ...draft, endpoint })
    const persisted = await putSettings(payload)
    adopt(persisted)
    loaded.value = true
    loadError.value = null
    success('设置已保存')
    return true
  } catch (err) {
    danger(describeError(err, '保存设置失败'))
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
