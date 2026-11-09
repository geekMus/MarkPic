import type { RunRowView } from './useRun'

/** 一处引用：哪篇文档的第几行。 */
export interface DeadRef {
  file: string
  line: number
}

/** 一个打不开的地址，以及它在文档里的全部引用处。 */
export interface DeadLink {
  url: string
  reason: string
  refs: DeadRef[]
}

/** 清单里每个地址最多列几处引用：再多就没人看了。 */
const MAX_REFS = 8

/**
 * 把逐图明细里「打不开」的那几行汇总成按地址去重的清单。
 *
 * 为什么要在界面里做：命令行跑完本来就会打一份去重清单，可界面上只有逐行列着的
 * 红字 —— 一份长文档里同一个死链被引用几十次是常事，要照着改文档只能一行一行抄。
 */
export function collectDeadLinks(rows: readonly RunRowView[]): DeadLink[] {
  const byURL = new Map<string, DeadLink>()
  for (const row of rows) {
    const res = row.result
    if (!res?.items) continue
    for (const item of res.items) {
      if (item.status !== 'remote-bad') continue
      const url = item.target
      let link = byURL.get(url)
      if (!link) {
        link = { url, reason: '', refs: [] }
        byURL.set(url, link)
      }
      if (link.reason === '' && item.error) link.reason = item.error
      link.refs.push({ file: row.name, line: item.line })
    }
  }
  // 引用最多的排前面：那是改起来最麻烦、最先要修的
  return [...byURL.values()].sort(
    (a, b) => b.refs.length - a.refs.length || a.url.localeCompare(b.url),
  )
}

export interface DeadReportStrings {
  title: string
  summary: (refs: number, urls: number) => string
  refs: (places: string, total: number) => string
  /** 引用之间的分隔符：中文用「、」，英文用「, 」。 */
  sep: string
  more: string
}

/**
 * 拼成一段可以直接贴进笔记的 Markdown。
 *
 * 每个地址占三行：地址、原因、引用处。原因自己就带括号（「…（HTTP 404）」），
 * 所以不能把「共 N 处」再接在同一行的括号后面 —— 那样会读成两个并排的括号。
 */
export function formatDeadReport(links: readonly DeadLink[], s: DeadReportStrings): string {
  const totalRefs = links.reduce((n, link) => n + link.refs.length, 0)
  const out: string[] = [`## ${s.title}`, '', s.summary(totalRefs, links.length), '']
  for (const link of links) {
    const shown = link.refs.slice(0, MAX_REFS).map((ref) => `${ref.file}:${ref.line}`)
    const places =
      link.refs.length > MAX_REFS ? `${shown.join(s.sep)} ${s.more}` : shown.join(s.sep)
    out.push(`- ${link.url}`)
    if (link.reason !== '') out.push(`  ${link.reason}`)
    out.push(`  ${s.refs(places, link.refs.length)}`)
  }
  return out.join('\n')
}

/**
 * 往剪贴板写文本。
 *
 * 先走 navigator.clipboard（127.0.0.1 算安全来源，WebView2 与浏览器都认），
 * 不行再退回老办法：临时塞一个 textarea、选中、execCommand('copy')。
 * 后者虽然过时，但它是唯一在「页面没拿到剪贴板权限」时还能用的路。
 */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    /* 落到下面的老办法 */
  }
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.position = 'fixed'
  area.style.top = '-1000px'
  area.style.opacity = '0'
  document.body.appendChild(area)
  try {
    area.select()
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    document.body.removeChild(area)
  }
}
