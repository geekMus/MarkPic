import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { ui } from './ui.mjs';

export const isWindows = process.platform === 'win32';
const DEFAULT_PATHEXT = '.COM;.EXE;.BAT;.CMD';

function environment() {
  return process.env;
}

function pathValue() {
  return environment().PATH ?? environment().Path ?? '';
}

function pathext() {
  return (environment().PATHEXT ?? DEFAULT_PATHEXT)
    .split(';')
    .map((entry) => entry.trim())
    .filter(Boolean);
}

function isFile(file) {
  try {
    return fs.statSync(file).isFile();
  } catch {
    return false;
  }
}

export function expandHome(value) {
  if (!value) return value;
  if (value === '~') return os.homedir();
  if (value.startsWith('~/') || value.startsWith('~\\')) {
    return path.join(os.homedir(), value.slice(2));
  }
  return value;
}

/** 在 Windows 上把裸命令展开成带 PATHEXT 后缀的候选；其他地方原样返回。 */
function candidatesFor(file) {
  const expanded = expandHome(file);
  if (!isWindows || path.extname(expanded)) return [expanded];
  return [expanded, ...pathext().map((ext) => expanded + ext)];
}

function executable(file) {
  if (!isFile(file)) return false;
  if (isWindows) return true;
  try {
    fs.accessSync(file, fs.constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

/**
 * 找可执行文件。既接受裸命令（走 PATH），也接受带路径的文件。
 * 找不到返回 null，由调用方决定怎么解释。
 */
export function which(name) {
  if (!name || typeof name !== 'string') return null;
  const trimmed = name.trim();
  if (!trimmed) return null;

  if (path.isAbsolute(trimmed) || trimmed.includes('/') || trimmed.includes('\\')) {
    for (const candidate of candidatesFor(trimmed)) {
      if (executable(candidate)) return candidate;
    }
    return null;
  }

  const dirs = pathValue()
    .split(path.delimiter)
    .map((entry) => entry.replace(/^"(.*)"$/, '$1'))
    .filter(Boolean);

  for (const dir of dirs) {
    for (const candidate of candidatesFor(path.join(dir, trimmed))) {
      if (executable(candidate)) return candidate;
    }
  }
  return null;
}

export function looksLikeFile(value) {
  return Boolean(value) && isFile(expandHome(value));
}

export function formatCommand(command, args = []) {
  const quote = (value) => {
    const text = String(value);
    return /\s|"/u.test(text) ? `"${text.replace(/"/g, '\\"')}"` : text;
  };
  return [command, ...args].map(quote).join(' ');
}

/** 同步跑一个命令，默认把输出直接接到当前终端；失败抛错。 */
export function run(command, args = [], options = {}) {
  const cwd = options.cwd ?? process.cwd();
  const env = options.env ? { ...environment(), ...options.env } : environment();
  if (options.echo !== false) ui.detail('$ ' + formatCommand(command, args));

  const result = spawnSync(command, args, {
    cwd,
    env,
    stdio: options.stdio ?? 'inherit',
    shell: options.shell ?? false,
    windowsHide: options.windowsHide ?? false,
  });

  if (result.error) {
    if (result.error.code === 'ENOENT') {
      throw new Error(`找不到命令：${command}`);
    }
    throw new Error(`运行 ${command} 失败：${result.error.message}`);
  }
  if (result.status !== 0 && !options.allowFailure) {
    const label = options.label ?? formatCommand(command, args);
    const code = result.signal ? `信号 ${result.signal}` : `退出码 ${result.status}`;
    throw new Error(`${label} 失败（${code}）`);
  }
  return result;
}

/** 同步跑一个命令并收集输出；不打印命令本身。 */
export function capture(command, args = [], options = {}) {
  const cwd = options.cwd ?? process.cwd();
  const env = options.env ? { ...environment(), ...options.env } : environment();
  const result = spawnSync(command, args, {
    cwd,
    env,
    encoding: 'utf8',
    maxBuffer: 16 * 1024 * 1024,
    shell: options.shell ?? false,
    windowsHide: options.windowsHide ?? true,
  });

  if (result.error) {
    if (result.error.code === 'ENOENT') {
      throw new Error(`找不到命令：${command}`);
    }
    throw new Error(`运行 ${command} 失败：${result.error.message}`);
  }

  const stdout = (result.stdout ?? '').replace(/^\uFEFF/, '').replace(/\r\n/g, '\n').trimEnd();
  const stderr = (result.stderr ?? '').replace(/^\uFEFF/, '').replace(/\r\n/g, '\n').trimEnd();

  if (result.status !== 0 && !options.allowFailure) {
    const reason = stderr || stdout || `退出码 ${result.status}`;
    throw new Error(`${options.label ?? formatCommand(command, args)} 失败：${reason}`);
  }
  return { status: result.status, signal: result.signal, stdout, stderr };
}

export function git(args, options = {}) {
  const exe = options.git ?? which('git');
  if (!exe) {
    if (options.allowFailure) {
      return { status: 1, signal: null, stdout: '', stderr: 'git not found' };
    }
    throw new Error('找不到 git，请先安装 Git 并确保它在 PATH 里。');
  }
  return capture(exe, args, {
    cwd: options.cwd ?? process.cwd(),
    allowFailure: options.allowFailure ?? false,
    label: options.label ?? 'git ' + args.join(' '),
  });
}

export function gitOrNull(args, options = {}) {
  try {
    const result = git(args, { ...options, allowFailure: true });
    return result.status === 0 ? result.stdout : null;
  } catch {
    return null;
  }
}

export function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function connectOnce(port, host, timeoutMs) {
  return new Promise((resolve) => {
    const socket = net.connect({ port, host });
    let settled = false;
    const finish = (value) => {
      if (settled) return;
      settled = true;
      socket.destroy();
      resolve(value);
    };
    socket.setTimeout(timeoutMs);
    socket.once('connect', () => finish(true));
    socket.once('timeout', () => finish(false));
    socket.once('error', () => finish(false));
  });
}

/** 轮询一个本地端口，直到能连上或超时。 */
export async function waitForPort(port, timeoutMs = 40000, host = '127.0.0.1') {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await connectOnce(port, host, 500)) return true;
    await delay(250);
  }
  return false;
}

/** 启动一个长进程；调用方负责 stopProcess。 */
export function startProcess(command, args = [], options = {}) {
  const env = options.env ? { ...environment(), ...options.env } : environment();
  return spawn(command, args, {
    cwd: options.cwd ?? process.cwd(),
    env,
    stdio: options.stdio ?? 'inherit',
    shell: options.shell ?? false,
    windowsHide: options.windowsHide ?? false,
    detached: options.detached ?? !isWindows,
  });
}

/** 等子进程退出；已经退出的立即返回。 */
export function waitForExit(child) {
  if (child.exitCode !== null || child.signalCode !== null) {
    return Promise.resolve({ code: child.exitCode, signal: child.signalCode });
  }
  return new Promise((resolve, reject) => {
    child.once('error', reject);
    child.once('exit', (code, signal) => resolve({ code, signal }));
  });
}

/** 连子进程树一起结束。Windows 用 taskkill，POSIX 优先杀整个进程组。 */
export function killTree(child, signal = 'SIGTERM') {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  if (child.pid === undefined) return;

  if (isWindows) {
    spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], {
      stdio: 'ignore',
      windowsHide: true,
      env: environment(),
    });
    return;
  }

  try {
    process.kill(-child.pid, signal);
    return;
  } catch {
    // 进程组可能已经没了，退回单进程。
  }
  try {
    child.kill(signal);
  } catch {
    // 已经退出。
  }
}

export async function stopProcess(child, graceMs = 2500) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  killTree(child);
  const exited = await Promise.race([
    waitForExit(child).then(() => true).catch(() => true),
    delay(graceMs).then(() => false),
  ]);
  if (!exited) killTree(child, 'SIGKILL');
}

export function formatBytes(bytes) {
  if (!Number.isFinite(bytes) || bytes < 0) return String(bytes);
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`;
}
