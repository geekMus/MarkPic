import { computed, ref } from 'vue'

export type ThemeChoice = 'system' | 'light' | 'dark'
export type ResolvedTheme = 'light' | 'dark'

const STORAGE_KEY = 'convert-md-picture.theme'
const media = window.matchMedia('(prefers-color-scheme: dark)')

function readChoice(): ThemeChoice {
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY)
    if (stored === 'light' || stored === 'dark' || stored === 'system') return stored
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
    toggle: () => setChoice(resolved.value === 'dark' ? 'light' : 'dark'),
  }
}
