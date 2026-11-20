import { computed, ref } from 'vue'
import { zh } from './zh'
import { en } from './en'

export type Lang = 'zh' | 'en'
export type MessageKey = keyof typeof zh

const CATALOG: Record<Lang, Record<MessageKey, string>> = { zh, en }

const STORAGE_KEY = 'markpic.lang'

// 改名前的键：老用户选的英文还在旧键里，首次启动搬过来。
const LEGACY_KEY = 'convert-md-picture.lang'

function readLang(): Lang {
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY)
    if (stored === 'zh' || stored === 'en') return stored
    const legacy = window.localStorage.getItem(LEGACY_KEY)
    if (legacy === 'zh' || legacy === 'en') {
      window.localStorage.setItem(STORAGE_KEY, legacy)
      window.localStorage.removeItem(LEGACY_KEY)
      return legacy
    }
  } catch {
    // 隐私模式下读不到存储，用默认值
  }
  // 默认中文：这个工具的第一批用户就是中文用户
  return 'zh'
}

const lang = ref<Lang>(readLang())

function apply(): void {
  document.documentElement.lang = lang.value === 'en' ? 'en' : 'zh-CN'
}

apply()

function setLang(next: Lang): void {
  lang.value = next
  try {
    window.localStorage.setItem(STORAGE_KEY, next)
  } catch {
    // 无法持久化时仅本次生效
  }
  apply()
}

/**
 * 取单复数正确的 key：count 为 1 且词典里有 `<key>.one` 时用那一条。
 *
 * 英文里「1 images」一眼就是没做完的样子；中文两条文案相同，纯属占位。
 * 所有计数文案都走这里，不要在调用点上各写一遍。
 */
export function plural(key: MessageKey, count: number): MessageKey {
  if (count !== 1) return key
  const one = `${key}.one` as MessageKey
  return one in zh ? one : key
}

/** 取文案；{name} 会被 params 里的同名值替换。 */
export function t(key: MessageKey, params?: Record<string, string | number>): string {
  const text = CATALOG[lang.value][key] ?? zh[key] ?? key
  if (params === undefined) return text
  return text.replace(/\{(\w+)\}/g, (whole, name: string) =>
    name in params ? String(params[name]) : whole,
  )
}

export function useI18n() {
  return {
    lang: computed(() => lang.value),
    setLang,
    t,
  }
}
