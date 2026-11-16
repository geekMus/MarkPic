/**
 * 只写最后一级目录名。
 *
 * 卡面一行只有那么宽，整条 `D:\Projects\convert-md-picture\.tmp-scroll` 在每张卡上
 * 都重复一遍，十张卡就是十行一样的灰字；完整路径交给 title 显示。
 * 分隔符两种都认 —— 路径可能是从 Windows 那边带过来的。
 */
export function shortDir(target: string): string {
  const parts = target.split(/[\\/]/).filter((part) => part !== '')
  return parts.length > 0 ? parts[parts.length - 1] : target
}
