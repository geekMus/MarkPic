import { ref } from 'vue'
import { describeError, getHealth } from '../api'
import type { Health } from '../types'
import { useToasts } from './useToasts'

const { danger } = useToasts()

const health = ref<Health | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
const checkedAt = ref<number | null>(null)
let inflight: Promise<void> | null = null

/** 读取 /api/health：同时刷新版本号与 PicGo 连接状态。 */
function refresh(): Promise<void> {
  if (inflight !== null) return inflight

  loading.value = true
  inflight = (async () => {
    try {
      health.value = await getHealth()
      error.value = null
    } catch (err) {
      error.value = describeError(err, '无法读取服务状态')
      health.value = null
      danger(error.value)
    } finally {
      checkedAt.value = Date.now()
      loading.value = false
      inflight = null
    }
  })()

  return inflight
}

export function useHealth() {
  return {
    health,
    loading,
    error,
    checkedAt,
    refresh,
  }
}
