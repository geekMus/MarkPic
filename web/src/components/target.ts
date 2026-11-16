/**
 * 一个引用指的是「外面的地址」还是「本机的文件」。
 *
 * 三种页面都在问同一件事：地址要连出去取，本地文件直接读。判据只有一条 ——
 * 先有 scheme（http:、ftp:、data:）再排除 Windows 盘符（`D:\…` 长得也像 scheme）。
 * 反过来问的时候不要再抄一遍正则：两个函数互为反面，改一处两处都对。
 */

/** 远程引用：http、ftp、data 都算，Windows 盘符不算。 */
export function isRemoteRef(target: string): boolean {
  return /^[a-z][a-z0-9+.-]*:/i.test(target) && !/^[a-z]:[\\/]/i.test(target)
}

/** 本机路径，包括带盘符的绝对路径和相对路径。 */
export function isLocalRef(target: string): boolean {
  return !isRemoteRef(target)
}
