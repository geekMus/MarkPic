import { computed, ref } from 'vue'
import { zh } from './zh'
import { en } from './en'

export type Lang = 'zh' | 'en'
export type MessageKey = keyof typeof zh

const CATALOG: Record<Lang, Record<MessageKey, string>> = { zh, en }

const STORAGE_KEY = 'convert-md-picture.lang'

function readLang(): Lang {
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY)
    if (stored === 'zh' || stored === 'en') return stored
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

/** 当前语言，供 api.ts 之类的非组件代码使用。 */
export function currentLang(): Lang {
  return lang.value
}

/**
 * 每次请求都带上它：服务端返回的文字（逐图失败原因、PicGo 提示、更新错误等）
 * 也要跟着界面语言走。
 */
export function langHeader(): string {
  return lang.value === 'en' ? 'en' : 'zh-CN'
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
