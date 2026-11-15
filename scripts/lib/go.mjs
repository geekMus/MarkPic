import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import process from 'node:process';

import { repoRoot } from './paths.mjs';
import { capture, expandHome, isWindows, run, which } from './proc.mjs';
import { ui } from './ui.mjs';

function isDirectory(file) {
  try {
    return fs.statSync(file).isDirectory();
  } catch {
    return false;
  }
}

function goExeName() {
  return isWindows ? 'go.exe' : 'go';
}

/** 从 PATH、GOROOT、常见安装目录里找 go；hint 可以是目录也可以是可执行文件。 */
export function resolveGo(hint = '') {
  const candidates = [];
  const add = (value) => {
    if (!value) return;
    const expanded = expandHome(String(value).trim());
    if (!expanded) return;
    candidates.push(isDirectory(expanded) ? path.join(expanded, 'bin', goExeName()) : expanded);
  };

  add(hint);
  add(process.env.GO);
  const pathGo = which('go');
  if (pathGo) return pathGo;

  add(process.env.GOROOT ? path.join(process.env.GOROOT, 'bin', goExeName()) : '');
  add(process.env.GO_ROOT ? path.join(process.env.GO_ROOT, 'bin', goExeName()) : '');

  if (isWindows) {
    add('D:/Env/Go/bin/go.exe');
    add('C:/Program Files/Go/bin/go.exe');
    add(path.join(process.env.LOCALAPPDATA ?? path.join(os.homedir(), 'AppData', 'Local'), 'Programs', 'Go', 'bin', 'go.exe'));
  } else {
    add('/usr/local/go/bin/go');
    add('/usr/bin/go');
    add('/opt/homebrew/bin/go');
  }

  for (const candidate of candidates) {
    if (which(candidate)) return candidate;
  }
  throw new Error(
    '找不到 Go。装好 Go 1.24+ 并放进 PATH，或者用 --go 指定 go.exe 的路径（也可以在环境变量 GO 里写）。',
  );
}

export function goVersion(go) {
  return capture(go, ['version'], { cwd: repoRoot }).stdout;
}

export function targetGoos() {
  if (process.env.GOOS) return process.env.GOOS;
  if (process.platform === 'win32') return 'windows';
  if (process.platform === 'darwin') return 'darwin';
  return process.platform;
}

export function targetGoarch() {
  if (process.env.GOARCH) return process.env.GOARCH;
  if (process.arch === 'x64') return 'amd64';
  if (process.arch === 'ia32') return '386';
  return process.arch;
}

export function modulePath() {
  const text = fs.readFileSync(path.join(repoRoot, 'go.mod'), 'utf8');
  const match = /^module\s+(\S+)/m.exec(text);
  if (!match) throw new Error('go.mod 里找不到 module 行。');
  return match[1];
}

export function runGoTest(go, pattern = './...') {
  const clean = String(pattern).trim() || './...';
  run(go, ['test', clean, '-count=1'], { cwd: repoRoot, label: 'go test' });
}

/** 编译 exe；默认注入版本、提交号，debug 时再注入调试标记。 */
export function buildGo(options) {
  const {
    go,
    output,
    version,
    commit,
    debug = false,
    target = targetGoos(),
  } = options;

  fs.mkdirSync(path.dirname(output), { recursive: true });
  const module = modulePath();
  const ldflags = ['-s', '-w'];
  if (target === 'windows') ldflags.push('-H=windowsgui');
  ldflags.push(
    `-X ${module}/internal/version.Version=${version}`,
    `-X ${module}/internal/version.Commit=${commit}`,
  );
  if (debug) ldflags.push(`-X ${module}/internal/version.Debug=true`);

  run(
    go,
    ['build', '-trimpath', '-ldflags', ldflags.join(' '), '-o', output, './cmd/markpic'],
    { cwd: repoRoot, label: 'go build' },
  );

  if (target === 'windows') assertGuiSubsystem(output);
  return output;
}

/** 图标资源是提交过的；只有删了才联网重新生成。 */
export function ensureIconResource(go, target = targetGoos()) {
  if (target !== 'windows') return;
  const light = path.join(repoRoot, 'assets', 'icon-light.ico');
  const dark = path.join(repoRoot, 'assets', 'icon-dark.ico');
  const syso = path.join(repoRoot, 'cmd', 'markpic', 'rsrc_windows_amd64.syso');
  if (fs.existsSync(syso)) return;
  if (!fs.existsSync(light) || !fs.existsSync(dark)) {
    ui.warn('找不到 assets 下的 ico，exe 会使用默认图标。');
    return;
  }
  if (!isWindows) {
    ui.warn('当前不是 Windows，跳过图标资源生成；交叉编译出来的 exe 没有自定义图标。');
    return;
  }
  ui.step('生成图标资源（只做一次）');
  try {
    run(
      go,
      [
        'run',
        'github.com/akavel/rsrc@latest',
        '-ico',
        `${light},${dark}`,
        '-arch',
        'amd64',
        '-o',
        syso,
      ],
      { cwd: repoRoot, label: '生成图标资源' },
    );
  } catch (error) {
    ui.warn(`图标资源没生成成功：${error.message}`);
  }
}

/** 把版本信息与 asInvoker 清单写进 PE；失败不影响 exe 运行。 */
export function writeWinres(go, exe, version) {
  if (!isWindows) {
    ui.warn('当前不是 Windows，跳过版本信息写入（交叉编译的 exe 没有 FileVersion）。');
    return;
  }
  try {
    run(
      go,
      ['run', './tools/winres', '-exe', exe, '-version', String(version).replace(/\+dev$/u, '')],
      { cwd: repoRoot, label: '写入版本信息' },
    );
  } catch (error) {
    ui.warn(`版本信息没写进去：${error.message}`);
  }
}

/** 读 PE 头确认是 GUI 子系统；控制台子系统双击会先闪黑框。 */
export function assertGuiSubsystem(file) {
  const fd = fs.openSync(file, 'r');
  try {
    const dos = Buffer.alloc(0x40);
    if (fs.readSync(fd, dos, 0, dos.length, 0) < dos.length || dos.readUInt16LE(0) !== 0x5a4d) {
      throw new Error(`${file} 不是有效的 PE 文件。`);
    }
    const peOffset = dos.readUInt32LE(0x3c);
    const header = Buffer.alloc(0x60);
    const read = fs.readSync(fd, header, 0, header.length, peOffset);
    if (read < 0x5e || header.readUInt32LE(0) !== 0x00004550) {
      throw new Error(`${file} 的 PE 头不完整。`);
    }
    // PE 可选头从 0x18 开始，Subsystem 在可选头偏移 0x44。
    const subsystem = header.readUInt16LE(0x18 + 0x44);
    if (subsystem !== 2) {
      throw new Error(
        `${file} 是控制台子系统（${subsystem}），双击会先闪一个黑框。构建参数里的 -H=windowsgui 丢了？`,
      );
    }
  } finally {
    fs.closeSync(fd);
  }
}
