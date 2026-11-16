/**
 * 顶栏、底栏要的那几个总数。
 *
 * 全是 computed：行自己的变化由行自己触发重渲染，这里只负责「一共几行、跑完几个、
 * 替换了几处」这类一眼要看到的数。
 */
import { computed } from 'vue'
import type { RunRowView, RunStats } from './types'
import { lastReveal, live, rows, summary } from './page'

/**
 * 传给列表的行数组。
 *
 * 这里只做一次浅拷贝（数组元素还是那些行对象，不复制行）：所以它只在队列结构变化
 * （增行、删行、开始一轮）时才重新计算，行内属性的变化由每一行自己触发重渲染。
 */
export const rowViews = computed<RunRowView[]>(() => rows.value.slice())

export const totalCount = computed(() => rows.value.length)

export const completedCount = computed(() => live.settled)

export const stats = computed<RunStats>(() => {
  const finished = summary.value
  if (finished) {
    return {
      files: finished.files,
      // 跑完之后目录已经展开成一个个文件了，这里不再有「目录」这回事。
      dirs: 0,
      images: finished.images,
      uploaded: finished.uploaded,
      reused: finished.reused,
      uploadRequests: finished.uploadRequests ?? 0,
      failed: finished.failedImages,
      ok: finished.ok,
      partial: finished.partial,
      failedFiles: finished.failed,
      noImages: finished.noImages,
      localized: finished.localized ?? 0,
      localizedUnique: finished.localizedUnique ?? 0,
      remoteChecked: finished.remoteChecked ?? 0,
      remoteCheckedUnique: finished.remoteCheckedUnique ?? 0,
      remoteDead: finished.remoteDead ?? 0,
      remoteDeadUnique: finished.remoteDeadUnique ?? 0,
      remoteFailed: finished.remoteFailed ?? 0,
      remoteFailedUnique: finished.remoteFailedUnique ?? 0,
      remoteUnsupported: finished.remoteUnsupported ?? 0,
      localMissing: finished.localMissing ?? 0,
      remoteKept: finished.remoteKept ?? 0,
      filesChecked: finished.filesChecked ?? 0,
      remoteReused: finished.remoteReused ?? 0,
      abortReason: finished.abortReason ?? '',
    }
  }

  return {
    files: rows.value.length,
    dirs: rows.value.reduce((n, row) => n + (row.isDir ? 1 : 0), 0),
    images: live.images,
    uploaded: live.uploaded,
    reused: live.reused,
    uploadRequests: live.uploadRequests,
    failed: live.failed,
    ok: live.ok,
    partial: live.partial,
    failedFiles: live.failedFiles,
    noImages: live.noImages,
    localized: live.localized,
    localizedUnique: live.localizedUnique,
    remoteChecked: live.remoteChecked,
    remoteCheckedUnique: live.remoteCheckedUnique,
    remoteDead: live.remoteDead,
    remoteDeadUnique: live.remoteDeadUnique,
    remoteFailed: live.remoteFailed,
    remoteFailedUnique: live.remoteFailedUnique,
    remoteUnsupported: live.remoteUnsupported,
    localMissing: live.localMissing,
    remoteKept: live.remoteKept,
    filesChecked: live.filesChecked,
    remoteReused: live.remoteReused,
    abortReason: live.abortReason,
  }
})

export const revealTarget = computed<string | null>(() => {
  if (lastReveal.value !== null) return lastReveal.value
  // 只检测的那一轮一个文件都没写：这时候还挂着「打开输出目录」，点开的是源目录，
  // 名字和行为对不上（用户会以为转换结果就放在那儿）。
  const now = stats.value
  if (now.filesChecked > 0 && now.uploaded === 0 && now.localized === 0) return null
  return rows.value[0]?.dir ?? null
})
