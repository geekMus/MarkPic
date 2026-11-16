import type { FileStatus, ImageStatus } from '../types'
import type { MessageKey } from '../i18n'

/** 状态标签的语气。五种，全应用共用一套：点、牌、badge 都从这里取色。 */
export type Tone = 'neutral' | 'accent' | 'success' | 'warning' | 'danger'

/** 一行跑完之后的总结论：一轮里做了什么事、成没成。 */
export const FILE_STATUS_META: Record<FileStatus, { tone: Tone; labelKey: MessageKey }> = {
  ok: { tone: 'success', labelKey: 'row.status.done' },
  partial: { tone: 'warning', labelKey: 'row.status.partial' },
  'no-images': { tone: 'neutral', labelKey: 'row.status.noImages' },
  failed: { tone: 'danger', labelKey: 'row.status.failed' },
  cancelled: { tone: 'warning', labelKey: 'row.status.cancelled' },
  preview: { tone: 'neutral', labelKey: 'row.status.preview' },
  checked: { tone: 'neutral', labelKey: 'row.status.checked' },
}

/** 单张图的结论。检测、下载、上传三种模式共用这张表，所以名字要中性。 */
export const IMAGE_STATUS_META: Record<ImageStatus, { tone: Tone; labelKey: MessageKey }> = {
  uploaded: { tone: 'success', labelKey: 'row.status.uploaded' },
  reused: { tone: 'accent', labelKey: 'row.status.reused' },
  cached: { tone: 'accent', labelKey: 'row.status.cached' },
  remote: { tone: 'neutral', labelKey: 'row.status.remote' },
  missing: { tone: 'warning', labelKey: 'row.status.missing' },
  failed: { tone: 'danger', labelKey: 'row.status.uploadFailed' },
  cancelled: { tone: 'neutral', labelKey: 'row.status.cancelled' },
  pending: { tone: 'neutral', labelKey: 'row.status.pending' },
  localized: { tone: 'success', labelKey: 'row.status.localized' },
  'local-kept': { tone: 'neutral', labelKey: 'row.status.localKept' },
  'remote-ok': { tone: 'success', labelKey: 'row.status.remoteOk' },
  'remote-bad': { tone: 'danger', labelKey: 'row.status.remoteBad' },
}
