/**
 * 顶栏、底栏要的那几个总数。
 *
 * 数字全部来自后端：每个事件都带着「这一轮到目前为止」的累计汇总，这里只把它拼成
 * 界面的形状。前端不再自己数一遍 —— 跑动中和跑完后的口径因此不可能对不上。
 */
import { computed } from 'vue'
import type { RunRowView, RunStats } from './types'
import { live, rows } from './page'

/**
 * 传给列表的行数组。
 *
 * 这里只做一次浅拷贝（数组元素还是那些行对象，不复制行）：所以它只在队列结构变化
 * （增行、删行、开始一轮）时才重新计算，行内属性的变化由每一行自己触发重渲染。
 */
export const rowViews = computed<RunRowView[]>(() => rows.value.slice())

/**
 * 这一页真正的任务行：只算「文件卡片」。
 *
 * 扫描卡片（拖进来的是文件夹 / 盘）不是任务 —— 它只是一份「发现了这些 Markdown」的
 * 清单，用户点了「确认导入」之后才会铺成一张张文件卡片。把文件夹本身交给后端去跑，
 * 既算不出它有几张图，也不是用户按下「开始上传」时想做的事。
 *
 * 所以「能不能开始」「跑哪些路径」「底栏数几个文件」一律只认这批行。
 */
export const taskRows = computed<RunRowView[]>(() => rows.value.filter((row) => row.scanCard !== true))

/** 还等着「确认导入」的文件夹 / 盘：它们不进任务，但底栏要说一声。 */
export const scanCardCount = computed(() => rows.value.length - taskRows.value.length)

export const totalCount = computed(() => taskRows.value.length)

/**
 * 队列里已经收工的行数（有结果，或被取消）。
 *
 * 不从后端的计数桶里拼：取消的文件不属于任何一个桶，拼出来的数会让进度条在取消后
 * 停在中途；队列行的相位才是「这一行还会不会动」的准确答案。
 */
export const completedCount = computed(
  () => taskRows.value.filter((row) => row.phase === 'settled' || row.phase === 'cancelled').length,
)

export const stats = computed<RunStats>(() => ({
  // 任务行有几行就是几个文件：跑动中它不能变成「已经处理了几个」，
  // 否则底栏的文件数会随着进度往上跳。开跑时目录会展开成一个个文件，
  // 所以跑起来之后 dirs 自然回到 0。扫描卡片不算任务，不进这个数。
  files: taskRows.value.length,

  // 引用数是「队列里一共多少张」，不是「已经处理了几张」：底栏那行是读队列，
  // 进度由页头的百分比和「替换 N」负责。扫描卡片还没数图，本来也不该计入。
  images: taskRows.value.reduce((n, row) => n + (row.images ?? row.total), 0),
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
}))

