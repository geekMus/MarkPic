import { computed, ref } from 'vue'
import {
  checkUpdate,
  describeError,
  getUpdateStatus,
  installUpdate,
  openTarget,
} from '../api'
import type { UpdateStatus } from '../types'

/**
 * 更新状态放成模块级单例：底栏的版本按钮、关于对话框、「启动时自动检查」
 * 都要读同一份状态，分成几份会互相打架。
 */

const AUTO_CHECK_KEY = 'cmp.autoCheckUpdate'
const POLL_MS = 500

function blank(): UpdateStatus {
  return {
    state: 'idle',
    current: '',
    latest: '',
    tag: '',
    notes: '',
    published: '',
    asset: { url: '', sha256: '', size: 0 },
    received: 0,
    total: 0,
    error: '',
    releasePage: '',
  }
}

function readAutoCheck(): boolean {
  try {
    return window.localStorage.getItem(AUTO_CHECK_KEY) !== '0'
  } catch {
    return true
  }
}

const status = ref<UpdateStatus>(blank())
const busy = ref(false)
const error = ref('')
const autoCheck = ref(readAutoCheck())

let pollTimer: number | undefined

function stopPolling(): void {
  if (pollTimer !== undefined) {
    window.clearTimeout(pollTimer)
    pollTimer = undefined
  }
}

/** 安装是由服务端在后台推进的，前端只能轮询到「正在重启」为止。 */
function schedulePoll(): void {
  stopPolling()
  pollTimer = window.setTimeout(async () => {
    try {
      status.value = await getUpdateStatus()
    } catch {
      return // 服务已经退出了，说明正在重启
    }
    const state = status.value.state
    if (state === 'downloading' || state === 'installing') schedulePoll()
  }, POLL_MS)
}

async function check(quiet = false): Promise<void> {
  if (busy.value) return
  busy.value = true
  if (!quiet) error.value = ''
  status.value = { ...status.value, state: 'checking', error: '' }
  try {
    const next = await checkUpdate()
    status.value = next
    if (next.state === 'error') {
      // 静默检查失败就当没查过：下次打开对话框用户可以自己再点一次。
      if (quiet) {
        status.value = { ...blank() }
      } else {
        error.value = next.error
      }
    }
  } catch (e) {
    if (quiet) {
      status.value = { ...blank() }
    } else {
      error.value = describeError(e, '检查更新失败')
      status.value = { ...status.value, state: 'idle' }
    }
  } finally {
    busy.value = false
  }
}

async function install(): Promise<void> {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    status.value = await installUpdate()
    schedulePoll()
  } catch (e) {
    error.value = describeError(e, '安装失败')
  } finally {
    busy.value = false
  }
}

async function openReleasePage(): Promise<void> {
  // 发布页地址由服务端给（和后端那份清单地址是同一个来源），前端不写死。
  const url = status.value.releasePage
  if (!url) {
    error.value = '还不知道发布页地址'
    return
  }
  try {
    await openTarget(url)
  } catch (e) {
    error.value = describeError(e, '打不开浏览器')
  }
}

function setAutoCheck(value: boolean): void {
  autoCheck.value = value
  try {
    window.localStorage.setItem(AUTO_CHECK_KEY, value ? '1' : '0')
  } catch {
    // 隐私模式下存不了，只影响这次会话，不必打扰用户
  }
}

const state = computed(() => status.value.state)
const hasUpdate = computed(() => state.value === 'available')
const percent = computed(() => {
  const { received, total } = status.value
  if (total <= 0) return 0
  return Math.min(100, Math.round((received / total) * 100))
})

/** 正在下载/安装/重启时不该再让用户点别的。 */
const locked = computed(
  () => state.value === 'downloading' || state.value === 'installing' || state.value === 'restarting',
)

export function useUpdate() {
  return {
    status,
    state,
    hasUpdate,
    percent,
    busy,
    locked,
    error,
    autoCheck,
    check,
    install,
    openReleasePage,
    setAutoCheck,
    stopPolling,
  }
}
