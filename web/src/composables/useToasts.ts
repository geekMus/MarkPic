import { ref } from 'vue'

export type ToastTone = 'info' | 'success' | 'warning' | 'danger'

export interface Toast {
  id: number
  tone: ToastTone
  message: string
}

const MAX_TOASTS = 4
const DURATION = 4200
const DEDUPE_WINDOW = 1200

const toasts = ref<Toast[]>([])
const timers = new Map<number, number>()
let seq = 0
let lastMessage = ''
let lastAt = 0

function clearTimer(id: number): void {
  const timer = timers.get(id)
  if (timer !== undefined) {
    window.clearTimeout(timer)
    timers.delete(id)
  }
}

function dismiss(id: number): void {
  clearTimer(id)
  toasts.value = toasts.value.filter(toast => toast.id !== id)
}

function push(message: string, tone: ToastTone): number {
  const text = message.trim()
  if (text.length === 0) return 0

  const now = Date.now()
  if (text === lastMessage && now - lastAt < DEDUPE_WINDOW) return 0
  lastMessage = text
  lastAt = now

  const id = ++seq
  const next = [...toasts.value, { id, tone, message: text }]
  const overflow = next.length - MAX_TOASTS
  if (overflow > 0) {
    for (const dropped of next.slice(0, overflow)) clearTimer(dropped.id)
    toasts.value = next.slice(overflow)
  } else {
    toasts.value = next
  }

  const lifetime = tone === 'danger' ? 6000 : DURATION
  timers.set(id, window.setTimeout(() => dismiss(id), lifetime))
  return id
}

export function useToasts() {
  return {
    toasts,
    dismiss,
    push: (message: string, tone: ToastTone = 'info') => push(message, tone),
    info: (message: string) => push(message, 'info'),
    success: (message: string) => push(message, 'success'),
    warning: (message: string) => push(message, 'warning'),
    danger: (message: string) => push(message, 'danger'),
  }
}
