import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

/** 仓库根目录：scripts/lib/paths.mjs 往上两级。 */
export const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
export const scriptsDir = path.join(repoRoot, 'scripts');
export const webDir = path.join(repoRoot, 'web');
export const binDir = path.join(repoRoot, 'bin');
/** Windows 是可执行文件，其他系统没有扩展名。 */
export const defaultExeName = process.platform === 'win32' ? 'MarkPic.exe' : 'MarkPic';
export const defaultExe = path.join(binDir, defaultExeName);
