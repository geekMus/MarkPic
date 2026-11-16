import { computed, ref } from 'vue'
import {
  checkUpdate,
  describeError,
  getHealth,
  getUpdateStatus,
  installUpdate,
  openTarget,
} from '../api'
import { t } from '../i18n'
import type { UpdateStatus } from '../types'

/**
 * 更新状态放成模块级单例：侧边栏左下角的版本按钮、关于对话框、「启动时自动检查」
 * 都要读同一份状态，分成几份会互相打架。
 */

/**
 * 仓库地址：更新清单没给发布页时（离线、还没发布过版本）拿它兜底，
 * 关于对话框里也一直挂着这个地址 —— 开源项目得让人找得到家。
 */
export const PROJECT_URL = 'https://github.com/geekMus/MarkPic'

/** 发布页：优先用服务端给的，没有就用仓库的 releases。 */
export const RELEASES_URL = PROJECT_URL + '/releases'

const AUTO_CHECK_KEY = 'cmp.autoCheckUpdate'
const POLL_MS = 500
// 检查在 Go 的后台协程里跑，前端用 update.status 轮询结果。150ms 够跟手，
// 又不会把窗口线程的 RPC 队列刷屏；20 秒比服务端 16 秒的请求上限留了余量。
const CHECK_POLL_MS = 150
const CHECK_DEADLINE_MS = 20_000

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

/**
 * 等后台检查落地。服务端只把状态切到 checking 就返回，网络请求不占窗口线程；
 * 这里轮询到 latest / available / error 为止，超时就当检查失败。
 */
async function waitForCheck(): Promise<UpdateStatus> {
  const deadline = Date.now() + CHECK_DEADLINE_MS
  let next = status.value
  while (next.state === 'checking' && Date.now() < deadline) {
    await new Promise((resolve) => window.setTimeout(resolve, CHECK_POLL_MS))
    try {
      next = await getUpdateStatus()
    } catch (e) {
      return { ...next, state: 'error', error: describeError(e, t('error.checkUpdate')) }
    }
  }
  if (next.state === 'checking') {
    return { ...next, state: 'error', error: t('error.checkUpdate') }
  }
  return next
}

async function check(quiet = false): Promise<void> {
  if (busy.value) return
  busy.value = true
  if (!quiet) error.value = ''
  status.value = { ...status.value, state: 'checking', error: '' }
  try {
    const started = await checkUpdate()
    status.value = started
    const next = started.state === 'checking' ? await waitForCheck() : started
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
      error.value = describeError(e, t('error.checkUpdate'))
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
    const wasVersion = status.value.current
    status.value = await installUpdate()
    schedulePoll()
    // 装完之后新进程会接管同一个端口，但当前这个页面还是旧的那份。
    // 浏览器形态下用户不刷新就永远停在「正在重启」，所以这里盯着端口，
    // 一旦回答的版本号变了就自己刷新一次（桌面窗口形态下新窗口已经开出来了，
    // 这里刷不刷新都无所谓）。
    void waitForRestart(wasVersion)
  } catch (e) {
    error.value = describeError(e, t('error.install'))
  } finally {
    busy.value = false
  }
}

/**
 * 等新版本接管端口，然后把当前页面刷新到新界面。
 * 只认「版本号变了」这一个信号：重启中途端口会短暂不可用，
 * 而旧进程可能还在应答同一个版本，这两种都不该触发刷新。
 */
async function waitForRestart(wasVersion: string): Promise<void> {
  const deadline = Date.now() + 90_000
  while (Date.now() < deadline) {
    await new Promise((resolve) => window.setTimeout(resolve, 700))
    try {
      const health = await getHealth()
      if (health.version && versionCore(health.version) !== versionCore(wasVersion)) {
        window.location.reload()
        return
      }
    } catch {
      // 还没起来，接着等
    }
  }
}

/**
 * 取版本号里的数字部分再比。界面上的 current 写作 "v2026.11.14"，
 * 而 health 报的是 "2026.11.14+dev (91eca40)" —— 直接比字符串会立刻判成「变了」，
 * 于是页面在旧进程还占着端口时就刷新了，刷完还是旧界面。
 *
 * 数字必须从开头取：开发构建报的是 "dev (91eca40)"，从头到尾扫数字会把提交号
 * 里的 91 当成版本号，于是重启等到超时也不刷新。
 */
function versionCore(text: string): string {
  const match = /^\s*v?(\d+(?:\.\d+)*)/.exec(text ?? '')
  return match ? match[1] : (text ?? '')
}

async function openReleasePage(): Promise<void> {
  // 发布页地址由服务端给（和后端那份清单地址是同一个来源）；
  // 还没发布过版本、或者清单读不出来时 releasePage 是空的 ——
  // 那时候不该「点了没反应」，退到仓库的 releases 页。
  const url = status.value.releasePage.trim() || RELEASES_URL
  try {
    await openTarget(url)
  } catch (e) {
    error.value = describeError(e, t('error.openBrowser'))
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
