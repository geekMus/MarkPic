#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { parseArgs } from 'node:util';

import { isEntry } from './lib/cli.mjs';
import { defaultExe, repoRoot } from './lib/paths.mjs';
import { resolveGo } from './lib/go.mjs';
import { capture, delay, isWindows, startProcess, stopProcess, waitForExit, waitForPort } from './lib/proc.mjs';
import { ui } from './lib/ui.mjs';
import { buildProject } from './build.mjs';
import { resolveBuildVersion, resolveCommit } from './lib/version.mjs';
import { ensureEmbeddedUi, ensureWebDependencies, startVite } from './lib/web.mjs';

const HELP = `
用法：node scripts/start.mjs [选项]

开发模式：Vite 热更新 + 原生窗口（Windows）。改 .vue 在窗口里立刻生效，改 .go 自动重编并重开窗口。
非 Windows 上只起 Vite，方便调界面。
已经开着的旧窗口会先关掉，保证窗口里跑的是当前这份源码。

选项：
  --port <端口>       Vite 端口，默认 5173
  --data-dir <路径>   数据目录，默认 bin/data（--mock 时是 .tmp-mock-data）
  --mock              起假图床 tools/mockpicgo，并把图床地址指过去
  --mock-port <端口>  假图床端口，默认 36678
  --mock-delay <时长> 每张图的模拟耗时，默认 5ms
  --mock-fail <比例>  按比例注入失败，形如 1/13
  --no-watch          不监视 .go 改动（省得编译总打断你）
  --rebuild           源码没变也强制重编一次
  --debug             编成调试版：只扫描，不写文件、不上传（默认和发布版一致）
  --go <路径>         go 可执行文件，默认从 PATH / GOROOT / 常见位置查找
  -h, --help          显示这段帮助
`;

const WATCH_DIRS = ['cmd', 'internal'];
const WATCH_FILES = ['go.mod', 'go.sum'];
const WATCH_INTERVAL_MS = 500;
const REBUILD_DEBOUNCE_MS = 250;


/** 正在跑的 MarkPic.exe。程序是单实例的，多开没意义。 */
function runningInstances() {
  const list = capture('tasklist', ['/FI', 'IMAGENAME eq MarkPic.exe', '/FO', 'CSV', '/NH'], {
    allowFailure: true,
  });
  const pids = [];
  for (const match of list.stdout.matchAll(/"MarkPic\.exe","(\d+)"/g)) {
    const pid = Number(match[1]);
    if (Number.isInteger(pid) && pid > 0 && !pids.includes(pid)) pids.push(pid);
  }
  return pids;
}

/**
 * 把上一轮留下的窗口收掉。
 *
 * 程序抢不到单实例锁时会把旧窗口叫到前面、自己直接退出（退出码 0）。脚本这边
 * 只看得到「窗口 pid N」，紧接着「窗口关掉了，收工」——旧窗口里还是上一版代码，
 * 于是改了界面也看不出效果，还以为没修好。开发脚本的语义是「跑当前这份源码」，
 * 所以开窗前先把旧的关掉。只发 WM_CLOSE 不够：默认设置是「关窗口＝收进
 * 托盘」，进程会一直在后台跑（taskkill 不带 /F 也是这个结果），所以这里直接
 * 结束进程。
 */
async function retireRunningInstances() {
  if (!isWindows) return;

  const pids = runningInstances();
  if (pids.length === 0) return;

  ui.warn(`上一个窗口还开着（pid ${pids.join('、')}），先关掉它再开，免得你看到的是上一版界面`);
  for (const pid of pids) {
    capture('taskkill', ['/PID', String(pid), '/T', '/F'], { allowFailure: true });
  }
  await delay(500);
  if (runningInstances().length > 0) {
    ui.warn('还有窗口没关掉；要是它一直赖着，从托盘退出再重跑。');
  }
}

function parsePort(raw, flag) {
  const port = Number(raw);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error(`${flag} 的「${raw}」不对，应该是 1 到 65535 之间的整数。`);
  }
  return port;
}

/**
 * 把 cmd/ 与 internal/ 下的 .go（加上 go.mod / go.sum）揉成一个指纹：
 * 文件数、总字节、最新的修改时间。只要有一样变了就该重编。
 *
 * 用轮询而不是 fs.watch：recursive 在各平台上支持得参差不齐，而这里要看的
 * 文件一共几百个，500ms 走一遍的开销可以忽略。
 */
function goSourceStamp() {
  let files = 0;
  let bytes = 0;
  let newest = 0;

  const note = (file) => {
    try {
      const stat = fs.statSync(file);
      files += 1;
      bytes += stat.size;
      if (stat.mtimeMs > newest) newest = stat.mtimeMs;
    } catch {
      // 半秒前还在、现在没了：下一次轮询会看到变化。
    }
  };

  const visit = (dir) => {
    let entries;
    try {
      entries = fs.readdirSync(dir, { withFileTypes: true });
    } catch {
      return;
    }
    for (const entry of entries) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        if (entry.name === 'node_modules') continue;
        visit(full);
      } else if (entry.name.endsWith('.go')) {
        note(full);
      }
    }
  };

  for (const dir of WATCH_DIRS) visit(path.join(repoRoot, dir));
  for (const file of WATCH_FILES) note(path.join(repoRoot, file));
  return `${files}:${bytes}:${newest}`;
}

/** 编译记录跟在 exe 旁边（bin/.build-stamp.json），不进版本库。 */
function buildStampPath() {
  return path.join(path.dirname(defaultExe), '.build-stamp.json');
}

function readBuildStamp() {
  try {
    const parsed = JSON.parse(fs.readFileSync(buildStampPath(), 'utf8'));
    return parsed && typeof parsed === 'object' ? parsed : null;
  } catch {
    return null;
  }
}

/**
 * 上一次编出来的 exe 还能不能用：源码指纹、版本号、提交号、调试开关四样都对得上，
 * 而且 exe 本体没被换过（大小与写入时刻一致）。
 *
 * mtime 用整数毫秒判等，不靠比大小 —— 时钟回拨、文件被拷回来的情况都能挡住。
 */
function buildIsFresh(stamp) {
  const saved = readBuildStamp();
  if (!saved) return false;
  if (saved.source !== stamp.source || saved.version !== stamp.version) return false;
  if (saved.commit !== stamp.commit || saved.debug !== stamp.debug) return false;
  try {
    const exe = fs.statSync(defaultExe);
    return exe.size === saved.bytes && Math.round(exe.mtimeMs) === saved.mtimeMs;
  } catch {
    return false;
  }
}

function writeBuildStamp(stamp) {
  try {
    const exe = fs.statSync(defaultExe);
    fs.writeFileSync(
      buildStampPath(),
      `${JSON.stringify({ ...stamp, bytes: exe.size, mtimeMs: Math.round(exe.mtimeMs) }, null, 2)}\n`,
      'utf8',
    );
  } catch {
    // 记不下来只是下次多编一遍，不影响启动。
  }
}

/**
 * 该编的时候才编。
 *
 * 一次 go build 约 2.4 秒、后面 winres 再 0.55 秒，是启动耗时里最大的一块；
 * 而绝大多数时候源码根本没变（改 .vue 有 Vite 热更新，连 exe 都不用换）。
 * 源码指纹、版本、提交、调试开关都对得上时，直接用 bin 里那份。
 * 想推翻这个判断：--rebuild，或者删掉 bin 里的 exe。
 */
function ensureBuilt({ go, debug, force }) {
  // 前端产物不再进版本库：没有就先建一次；刚建过必须重编，否则 go:embed 还是空的。
  if (ensureEmbeddedUi()) force = true;
  const version = resolveBuildVersion();
  const commit = resolveCommit();
  const stamp = { source: goSourceStamp(), version, commit, debug };
  if (!force && buildIsFresh(stamp)) {
    ui.detail('源码没变，跳过编译（要强制重编加 --rebuild）');
    return;
  }
  const built = buildProject({ go, debug, output: defaultExe, version, commit, web: false });
  writeBuildStamp({ source: stamp.source, version: built.version, commit: built.commit, debug });
}

/** 盯着 Go 源码；变了就调 onChange。编辑器保存一次往往要写好几个文件，所以留一点防抖。 */
function watchGoSources(onChange) {
  let stamp = goSourceStamp();
  let timer = null;
  const poll = setInterval(() => {
    if (timer) return;
    if (goSourceStamp() === stamp) return;
    timer = setTimeout(() => {
      timer = null;
      // 防抖期间可能又动过，重新取一次指纹，免得刚编完又编一遍。
      stamp = goSourceStamp();
      onChange();
    }, REBUILD_DEBOUNCE_MS);
  }, WATCH_INTERVAL_MS);

  return () => {
    clearInterval(poll);
    if (timer) clearTimeout(timer);
  };
}

/**
 * 假图床地址没法从命令行传给 exe（设置文件才是唯一来源），所以启动前把它写进
 * 数据目录的 settings.json：只改 endpoint，别的字段原样留着；文件坏了就重写一份。
 */
function pointSettingsAtMock(dataDir, endpoint) {
  fs.mkdirSync(dataDir, { recursive: true });
  const file = path.join(dataDir, 'settings.json');
  let current = {};
  if (fs.existsSync(file)) {
    try {
      const parsed = JSON.parse(fs.readFileSync(file, 'utf8').replace(/^\uFEFF/u, ''));
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) current = parsed;
    } catch {
      ui.warn('数据目录里的 settings.json 读不出来，这次会重写一份。');
    }
  }
  current.endpoint = endpoint;
  // 写法与 Go 侧 Store.Save 一致：2 空格缩进、结尾换行。
  fs.writeFileSync(file, `${JSON.stringify(current, null, 2)}\n`, 'utf8');
  return file;
}

/** 起假图床。端口被占（比如真的 PicGo 正开着）时直说，不去抢。 */
async function startMockPicgo(go, { port, delay, fail }) {
  if (await waitForPort(port, 400)) {
    throw new Error(`端口 ${port} 已经在用了（可能是真的 PicGo 开着）。换一个：--mock-port 36679。`);
  }
  ui.step('启动假图床（tools/mockpicgo）');
  const args = ['run', './tools/mockpicgo', '-port', String(port), '-delay', delay];
  if (fail) args.push('-fail', fail);
  const child = startProcess(go, args, { cwd: repoRoot });
  if (!(await waitForPort(port, 30000))) {
    await stopProcess(child);
    throw new Error(`假图床没有在 30 秒内起来（端口 ${port}）。第一次要现编，可能得再等一会儿。`);
  }
  ui.detail(`假图床在 http://127.0.0.1:${port}（累计统计看 /stats）`);
  return child;
}

async function waitForVite(child, port) {
  const exited = new Promise((resolve) => {
    child.once('exit', (code, signal) => resolve({ exited: true, code, signal }));
  });
  const ready = waitForPort(port, 40000).then((ok) => ({ ready: ok }));
  const first = await Promise.race([exited, ready]);
  if (first.exited) {
    throw new Error(`Vite 提前退出（退出码 ${first.code ?? first.signal ?? '未知'}）。`);
  }
  if (!first.ready) {
    throw new Error(`Vite 没有在 40 秒内起来（端口 ${port}）。`);
  }
}

async function main() {
  const { values } = parseArgs({
    allowPositionals: false,
    strict: true,
    options: {
      port: { type: 'string', default: '5173' },
      'data-dir': { type: 'string' },
      mock: { type: 'boolean', default: false },
      'mock-port': { type: 'string', default: '36678' },
      'mock-delay': { type: 'string', default: '5ms' },
      'mock-fail': { type: 'string', default: '' },
      'no-watch': { type: 'boolean', default: false },
      rebuild: { type: 'boolean', default: false },
      debug: { type: 'boolean', default: false },
      go: { type: 'string' },
      help: { type: 'boolean', short: 'h', default: false },
    },
  });
  if (values.help) {
    console.log(HELP.trim());
    return;
  }

  const mock = values.mock === true;
  const port = parsePort(values.port, '--port');
  const mockPort = parsePort(values['mock-port'], '--mock-port');
  if (!mock && (values['mock-delay'] !== '5ms' || values['mock-fail'] !== '')) {
    throw new Error('--mock-delay / --mock-fail 只有配合 --mock 才有意义。');
  }
  if (mock && mockPort === port) {
    throw new Error(`假图床和 Vite 不能挤在同一个端口（${port}）。`);
  }

  const debug = values.debug === true;
  if (debug && mock) {
    throw new Error('--debug 和 --mock 不能一起用：调试版只扫描，根本不会上传。');
  }

  const dataDir = values['data-dir']
    ? path.resolve(process.cwd(), values['data-dir'])
    : mock
      ? path.join(repoRoot, '.tmp-mock-data')
      : '';

  ensureWebDependencies();
  // 非 Windows 上只起 Vite，别为了调界面先要求装 Go。
  const go = isWindows ? resolveGo(values.go ?? '') : '';

  const env = { ...process.env, MARKPIC_DEV_URL: `http://127.0.0.1:${port}` };
  if (dataDir) env.MARKPIC_DATA_DIR = dataDir;

  // Vite 与编译同时跑：Vite 几百毫秒就好，编译要几秒，没道理排成一队等。
  ui.step(`启动 Vite（127.0.0.1:${port}）`);
  const vite = startVite(port);

  if (isWindows) {
    ensureBuilt({ go, debug, force: values.rebuild === true });
    if (mock) {
      ui.detail('假图床只接管上传：PicGo 地址指到本机假服务；下载和检测不经过 PicGo，照旧。');
    }
    if (debug) {
      ui.warn('--debug：这一轮只扫描，不写文件、不上传。');
    }
  }

  const watching = values['no-watch'] !== true;
  let app = null;
  let mockPicgo = null;
  let stopWatching = null;
  let stopping = false;
  let cleaning = false;
  // 被主动收掉的进程：它们的退出是「我们干的」，不该当成用户关了窗口。
  const retired = new Set();
  let finish = () => {};
  const finished = new Promise((resolve) => {
    finish = resolve;
  });

  const cleanup = async () => {
    if (cleaning) return;
    cleaning = true;
    if (stopWatching) stopWatching();
    if (app) {
      retired.add(app);
      await stopProcess(app);
      app = null;
    }
    if (mockPicgo) {
      await stopProcess(mockPicgo);
      mockPicgo = null;
    }
    await stopProcess(vite);
  };

  const interrupt = () => {
    if (stopping) return;
    stopping = true;
    ui.detail('收到 Ctrl+C，收掉窗口、假图床和 Vite');
    void cleanup().finally(() => process.exit(130));
  };
  process.once('SIGINT', interrupt);

  const launch = () => {
    app = startProcess(defaultExe, [], { cwd: repoRoot, env });
    const child = app;
    ui.detail(`窗口 pid ${child.pid}`);
    void waitForExit(child).then((result) => {
      if (retired.has(child) || child !== app) return;
      app = null;
      if (stopping) return;
      if (result.code === 0 || result.code === null) {
        ui.detail('窗口关掉了，收工。');
        finish();
        return;
      }
      ui.warn(`窗口退出了（退出码 ${result.code}）。`);
      // 有监视器时崩了不算结束：改好代码会自动重编重开，直到按 Ctrl+C。
      // 没监视器（--no-watch）就没人会再开窗口了，只能收工，把退出码带出去。
      if (watching) {
        ui.detail('改 .go 会自动重编再开一个。');
        return;
      }
      process.exitCode = result.code;
      finish();
    });
  };

  let rebuilding = false;
  let queued = false;
  const rebuild = async () => {
    ui.step('Go 代码变了，重新编译');
    if (app) {
      retired.add(app);
      await stopProcess(app);
      app = null;
    }
    ensureBuilt({ go, debug, force: true });
    if (!stopping) launch();
  };
  const requestRebuild = () => {
    if (stopping) return;
    if (rebuilding) {
      queued = true;
      return;
    }
    rebuilding = true;
    void (async () => {
      try {
        do {
          queued = false;
          try {
            await rebuild();
          } catch (error) {
            // 编译没过就把窗口留在关着的状态，等人改好；改好会自动再试一次。
            ui.error(error instanceof Error ? error.message : String(error));
            ui.detail('这次没编过，窗口先不开；改好 .go 会自动再试。');
          }
        } while (queued && !stopping);
      } finally {
        rebuilding = false;
      }
    })();
  };

  try {
    await waitForVite(vite, port);

    if (!isWindows) {
      if (mock) ui.warn('原生窗口只有 Windows 版，这次没有起假图床。');
      ui.warn('原生窗口只有 Windows 版。Vite 已经起来，可以在浏览器打开下面的地址调界面；bridge / 文件对话框等功能不可用。');
      console.log(`    http://127.0.0.1:${port}`);
      console.log('    按 Ctrl+C 结束。');
      await waitForExit(vite);
      return;
    }

    if (mock) {
      mockPicgo = await startMockPicgo(go, {
        port: mockPort,
        delay: values['mock-delay'],
        fail: values['mock-fail'],
      });
      const file = pointSettingsAtMock(dataDir, `http://127.0.0.1:${mockPort}`);
      ui.detail(`图床地址写成 ${path.relative(repoRoot, file) || file}`);
    }
    if (dataDir) ui.detail(`数据目录：${dataDir}`);

    await retireRunningInstances();

    ui.step(
      debug
        ? '启动窗口（界面来自 Vite；这一轮是调试模式：只扫描，不写文件、不上传）'
        : '启动窗口（界面来自 Vite）',
    );
    launch();

    if (values['no-watch']) {
      ui.detail('--no-watch：不监视 .go，改完自己重跑。');
    } else {
      stopWatching = watchGoSources(requestRebuild);
      ui.detail('改 .vue 会热更新；改 .go 会自动重编重开窗口。关掉窗口即退出。');
    }

    await finished;
  } finally {
    process.off('SIGINT', interrupt);
    await cleanup();
  }
}

if (isEntry(import.meta.url)) {
  main().catch((error) => {
    ui.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  });
}
