import { computed, ref } from 'vue'

export type ThemeChoice = 'system' | 'light' | 'dark'
export type ResolvedTheme = 'light' | 'dark'

const STORAGE_KEY = 'markpic.theme'

// 改名前的键。老用户的选择还在旧键里，第一次启动时搬过来，之后只认新键。
const LEGACY_KEY = 'convert-md-picture.theme'
const media = window.matchMedia('(prefers-color-scheme: dark)')

function readChoice(): ThemeChoice {
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY)
    if (stored === 'light' || stored === 'dark' || stored === 'system') return stored
    const legacy = window.localStorage.getItem(LEGACY_KEY)
    if (legacy === 'light' || legacy === 'dark' || legacy === 'system') {
      window.localStorage.setItem(STORAGE_KEY, legacy)
      window.localStorage.removeItem(LEGACY_KEY)
      return legacy
    }
  } catch {
    // 忽略隐私模式下的存储异常，回退到系统偏好
  }
  return 'system'
}

const choice = ref<ThemeChoice>(readChoice())
const systemDark = ref(media.matches)

const resolved = computed<ResolvedTheme>(() => {
  if (choice.value === 'system') return systemDark.value ? 'dark' : 'light'
  return choice.value
})

function apply(): void {
  document.documentElement.dataset.theme = resolved.value
}

apply()

media.addEventListener('change', event => {
  systemDark.value = event.matches
  if (choice.value === 'system') apply()
})

function setChoice(next: ThemeChoice): void {
  choice.value = next
  try {
    window.localStorage.setItem(STORAGE_KEY, next)
  } catch {
    // 无法持久化时仅本次生效
  }
  apply()
}

export function useTheme() {
  return {
    choice,
    resolved,
    isDark: computed(() => resolved.value === 'dark'),
    setChoice,
    /** 标题栏那个按钮：跟随系统 → 亮色 → 暗色 → 跟随系统。 */
    cycle: () => {
      const order: ThemeChoice[] = ['system', 'light', 'dark']
      const next = order[(order.indexOf(choice.value) + 1) % order.length]
      setChoice(next ?? 'system')
    },
  }
}
