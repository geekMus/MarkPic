/**
 * 契约类型：逐字对照 docs/API.md（已冻结），不要在前端另起字段名。
 */

export type Settings = {
  endpoint: string
  concurrency: number
  retries: number
  output: 'sidecar' | 'in-place' | 'directory'
  suffix: string
  outDir: string
  backup: boolean
  /** 复用上一次运行留下的上传结果，同一张图不再重传。 */
  reuseUploads: boolean
  /**
   * 远程图片怎么处理：ignore 原样留着；check 只检测能不能打开（不改文档）；
   * download 下载回本地并把链接改成相对路径。
   */
  remote: RemoteMode
  /** download 模式下图片落到文档旁边的哪个子目录。 */
  remoteDir: string
  /** 点窗口叉号时怎么办：每次询问、最小化到托盘，或者直接退出；有任务时总会先确认。 */
  closeAction: 'ask' | 'tray' | 'exit'
  /** 启动时只留托盘图标、不亮出窗口。默认关闭：双击图标就该看见东西。 */
  startInTray: boolean
}

export type RemoteMode = 'ignore' | 'check' | 'download'

/**
 * 侧边栏上的入口。左边三个是「作业视图」——共用同一条队列，区别只在远程图怎么处理
 * 和目标文案；右边两个是「表单页」——输出写到哪、PicGo 与外观。
 *
 * 以前远程图那三档藏在设置里当单选，用户得先知道「设置里有这个开关」才找得到；
 * 现在它们各自就是菜单上的一项。
 */
export type JobView = 'convert' | 'check' | 'download'
export type FormView = 'settings'
export type View = JobView | FormView

export const JOB_VIEWS: readonly JobView[] = ['convert', 'download', 'check']

/** 作业视图 → 这一轮远程图怎么处理。 */
export function modeOfView(view: View): RemoteMode {
  if (view === 'check') return 'check'
  if (view === 'download') return 'download'
  return 'ignore'
}

/** 反过来：给一个远程模式，找出该高亮哪个菜单项。 */
export function viewOfMode(mode: RemoteMode): JobView {
  if (mode === 'check') return 'check'
  if (mode === 'download') return 'download'
  return 'convert'
}

export type ImageStatus =
  | 'uploaded'
  | 'reused'
  | 'cached'
  | 'remote'
  | 'missing'
  | 'failed'
  | 'cancelled'
  | 'pending'
  /** download 模式：远程图已经下载到本地。 */
  | 'localized'
  /** download 模式：本地图片原样保留（这个模式不上传）。 */
  | 'local-kept'
  /** check 模式：地址能打开，拿到的确实是图片。 */
  | 'remote-ok'
  /** check 模式：地址打不开，或者返回的不是图片。 */
  | 'remote-bad'

export type ImageItem = {
  target: string
  absolutePath: string
  kind: 'markdown' | 'html'
  line: number
  status: ImageStatus
  url?: string
  error?: string
  /** 下载模式下这一份没有真的去网上拿：上次下过、或者本轮别的文档先下好了 */
  reused?: boolean
}

export type FileStatus =
  | 'ok'
  | 'partial'
  | 'no-images'
  | 'failed'
  | 'cancelled'
  | 'preview'
  /** check 模式：只检测、不写文件。 */
  | 'checked'

export type FileResult = {
  id: string
  path: string
  name: string
  dir: string
  status: FileStatus
  images: number
  uploaded: number
  /** 这一处替换是靠复用（本轮去重或上次的缓存）拿到的地址。 */
  reused?: number
  failed: number
  skipped: number
  /** download 模式：这一篇里有几张远程图被下载到了本地。 */
  localized?: number
  /**
   * download 模式：去重之后真正下载了几个文件。
   *
   * 同一张图在一篇文档里被引用 20 次时 localized 是 20，可 images/ 里只有一个
   * 文件 —— 只显示 20 会让人以为另外 19 张失败了。
   */
  localizedUnique?: number
  /** check 模式：这一篇里有几个远程地址打不开（或不是图片）。 */
  dead?: number
  /** 失效地址里去重后的个数（dead 按引用处算）。 */
  deadUnique?: number
  /** check 模式：这一篇里一共检测了几个远程地址。 */
  checked?: number
  /** 碰不了的远程地址（ftp://、data: 之类）：既不算检测过，也不算失效。 */
  remoteUnsupported?: number
  /** download 模式：远程地址这次没拿到图（连不上、超时）。同时也算在 failed 里。 */
  remoteFailed?: number
  /** 这些失败地址去重后的个数（remoteFailed 按引用处算）。 */
  remoteFailedUnique?: number
  /** 默认模式下原样保留、没动的远程图引用数（同时算在 skipped 里）。 */
  remoteKept?: number
  /** 检测/下载模式下引用了、但磁盘上找不到的本地图片数。 */
  localMissing?: number
  /** 去重后真正探活过的地址数（同一张图引用多次只算 1）。 */
  checkedUnique?: number
  outputPath?: string
  error?: string
  warnings?: string[]
  items: ImageItem[]
}

export type RunSummary = {
  runId: string
  files: number
  ok: number
  partial: number
  failed: number
  noImages: number
  images: number
  uploaded: number
  /** 这一轮靠缓存省下的去重图片数（不是引用数）。 */
  reused: number
  /**
   * 真的发给图床几次。同一份内容被多个名字引用时只有领头那张上行，
   * 所以它会小于 uploaded（uploaded 数的是「替换了几处」）。
   */
  uploadRequests?: number
  failedImages: number
  cancelled: boolean
  /**
   * 这一轮被提前掐掉的原因（目前只有「图床成片失败」一种）。
   * 取消事件也带汇总，所以界面能说清楚「为什么停了」。
   */
  abortReason?: string
  /** download 模式：这一轮把几张远程图下载到了本地。 */
  localized?: number
  /** download 模式：去重之后真正下载了几个文件（localized 是按引用处算的）。 */
  localizedUnique?: number
  /** check 模式：这一轮检测了几个远程地址、其中几个打不开。 */
  remoteChecked?: number
  /** 去重后真正探活过的地址数：同一张图被引用多次只发 1 个请求。 */
  remoteCheckedUnique?: number
  remoteDead?: number
  /** 失效地址里去重后的个数：同一个死链引用 3 次只算 1 个。 */
  remoteDeadUnique?: number
  /** 碰不了的远程地址（ftp://、data: 之类）：既不算检测过，也不算失效。 */
  remoteUnsupported?: number
  /** download 模式：远程地址这次没拿到图（连不上、超时）。同时也算在 failed 里。 */
  remoteFailed?: number
  /** 这些失败地址去重后的个数（remoteFailed 按引用处算）。 */
  remoteFailedUnique?: number
  /** 默认模式下原样保留、没动的远程图引用数（同时算在 skipped 里）。 */
  remoteKept?: number
  /** 检测/下载模式下引用了、但磁盘上找不到的本地图片数。 */
  localMissing?: number
  /** 去重后真正探活过的地址数（同一张图引用多次只算 1）。 */
  checkedUnique?: number
  /** check 模式：这一轮有几个文件只是被检测（没写输出）。 */
  filesChecked?: number
  /** 上次已经下过、这次没再下载的远程图张数（按去重后的图片算）。 */
  remoteReused?: number
  durationMs: number
}

/** run.started 中携带的文件信息。 */
export type RunFileInfo = {
  id: string
  path: string
  name: string
  dir: string
  images: number
}

/**
 * 扫描事件：Go 侧每扫完一个文件就推一条，扫完一整批再推 scan.done。
 *
 * 和转换事件走同一条推送通道，但语义是分开的：runId 那个位置放的是前端生成的
 * scanId，路径直接写在事件里 —— 一张卡片对应一个文件，谁的结果贴给谁不用猜。
 */
export type ScanEvent =
  | {
      type: 'scan.file'
      runId: string
      path: string
      scanned?: FileResult[]
      cancelled?: boolean
      timedOut?: boolean
    }
  | { type: 'scan.done'; runId: string; cancelled?: boolean }

export type RunEvent =
  | ScanEvent
  | { type: 'run.started'; runId: string; mode?: RemoteMode; files: RunFileInfo[] }
  | { type: 'file.started'; runId: string; id: string }
  | {
      type: 'file.progress'
      runId: string
      id: string
      done?: number
      uploaded: number
      total: number
      /** 这一条进度里刚处理完的那几处（按 items 的下标），卡片据此逐条换状态。 */
      items?: { index: number; item: ImageItem }[]
      /** 这一轮到目前为止的累计汇总：页脚那几个数直接用，不再由前端重算一遍。 */
      summary?: RunSummary
    }
  | {
      type: 'file.finished'
      runId: string
      id: string
      result: FileResult
      /** 同上：每跑完一个文件，后端把最新累计值一并带回来。 */
      summary?: RunSummary
    }
  | { type: 'run.finished'; runId: string; summary: RunSummary }
  | { type: 'run.cancelled'; runId: string; summary?: RunSummary }
  | { type: 'log'; level: 'info' | 'warn' | 'error'; message: string }

export type Health = {
  version: string
  endpoint: string
  picgo: {
    online: boolean
    detail: string
    /** 这次运行到底要不要 PicGo：只检测 / 下载回本地时不需要，掉线也不算错。 */
    needed: boolean
  }
  /** 设置与 WebView 缓存所在的位置（默认是 exe 旁边的 data 目录）。 */
  dataDir: string
  /** 没能放到 exe 旁边时的原因，正常情况下是空。 */
  dataNote?: string
  /** 设置文件读不出来时的说明（此时跑的是默认值），正常情况是空。 */
  settingsNote?: string
  /**
   * 调试构建（npm run start --debug 编的那一版）：标题栏挂「调试模式」，整轮只扫描、不写文件、不上传。
   * 默认的开发构建和发布版一样，这里恒为 false —— 它不是一个用户设置。
   */
  debug: boolean
}

export type DialogResult = { paths: string[] }

/** 用户点的是哪一个操作。它和设置里的 remote 是两件事：那是「文档里的网络图片怎么办」。 */
export type RunOp = 'upload' | 'check' | 'download'

export type RunStartResult = { runId: string; op: RunOp }

/** scan.start 的返回：只是确认收下了，结果走 scan.file 事件。 */
export type ScanStartResult = { scanId: string; paths: string[] }

export type OkResult = { ok: boolean }

/** 更新包（清单里给出的地址、校验值与大小）。 */
export type UpdateAsset = {
  url: string
  sha256: string
  size: number
}

export type UpdateState =
  | 'idle'
  | 'checking'
  | 'latest'
  | 'available'
  | 'downloading'
  | 'installing'
  | 'restarting'
  /** 这个构建没有版本号（直接 go build），不去和发布版比，也不提示更新。 */
  | 'unversioned'
  | 'error'

/** /api/update/status 与 /api/update/check 共用的形状。 */
export type UpdateStatus = {
  state: UpdateState
  current: string
  latest: string
  tag: string
  notes: string
  /** 发布说明正文（Markdown）：清单里的 notesBody，没有就是空串。 */
  notesBody: string
  published: string
  asset: UpdateAsset
  received: number
  total: number
  error: string
  releasePage: string
}
