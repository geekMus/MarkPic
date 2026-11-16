/**
 * 路径的两种看法：一份拿来比「是不是同一个文件」，一份拿来拆出名字与所在目录。
 *
 * 队列里所有去重、查找都走 pathKey —— 它按 Windows 的习惯归一（反斜杠、不分大小写），
 * 和 Go 侧的比较口径一致，两边对「同一个文件」的认定才不会打架。
 */

export function pathKey(value: string): string {
  return value.replace(/\//g, '\\').replace(/\\+$/, '').toLowerCase()
}

export function splitPath(value: string): { name: string; dir: string } {
  const trimmed = value.trim().replace(/\//g, '\\')
  const index = trimmed.lastIndexOf('\\')
  if (index > 0) {
    return { name: trimmed.slice(index + 1), dir: trimmed.slice(0, index) }
  }
  return { name: trimmed, dir: '' }
}
