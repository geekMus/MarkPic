#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { parseArgs } from 'node:util';

import { isEntry } from './lib/cli.mjs';
import { defaultExe, repoRoot } from './lib/paths.mjs';
import { ui } from './lib/ui.mjs';
import {
  buildGo,
  ensureIconResource,
  resolveGo,
  runGoTest,
  targetGoarch,
  targetGoos,
  writeWinres,
} from './lib/go.mjs';
import { buildWeb, requireEmbeddedUi } from './lib/web.mjs';
import { resolveBuildVersion, resolveCommit } from './lib/version.mjs';

const HELP = `
用法：node scripts/build.mjs [选项]

构建 MarkPic 的二进制（Windows 上是 exe）。默认先构建前端，再编 Go：
前端产物 internal/ui/dist 是 go:embed 的输入，不进版本库。

选项：
  --web            兼容旧用法；现在默认就会构建前端
  --no-web         跳过前端构建，直接编 Go（要求 internal/ui/dist 已存在）
  --test           编译前跑一遍 go test ./...
  --debug          编成调试模式（只扫描，不写文件、不上传）
  --output <路径>  输出文件，默认 bin/MarkPic.exe（Windows）或 bin/MarkPic（其他系统）
  --version <号>   写进二进制的版本号，默认取 git tag 或「今天+dev」
  --go <路径>      go 可执行文件，默认从 PATH / GOROOT / 常见位置查找
  -h, --help       显示这段帮助
`;

function parseCli(argv) {
  const { values } = parseArgs({
    args: argv,
    allowPositionals: false,
    strict: true,
    options: {
      web: { type: 'boolean', default: false },
      'no-web': { type: 'boolean', default: false },
      test: { type: 'boolean', default: false },
      debug: { type: 'boolean', default: false },
      output: { type: 'string' },
      version: { type: 'string' },
      go: { type: 'string' },
      help: { type: 'boolean', short: 'h', default: false },
    },
  });
  return values;
}

/**
 * 供其它脚本复用：不解析命令行，只做「前端 → 测试 → 图标 → 编译 → 版本信息」。
 */
export function buildProject(options = {}) {
  const go = options.go || resolveGo(options.goHint ?? '');
  const version = options.version || resolveBuildVersion();
  const commit = options.commit || resolveCommit();
  const target = options.target || targetGoos();
  const output = options.output ? path.resolve(process.cwd(), options.output) : defaultExe;

  if (options.web === false) {
    // 明确跳过前端：只接受已经建好的产物，不静默编出一个没有界面的 exe。
    requireEmbeddedUi();
  } else {
    buildWeb();
  }

  if (target !== 'windows') {
    ui.warn('原生窗口只有 Windows 版；这次会编出一个能跑测试、但不能开窗口的本机二进制。');
  }

  if (options.test) {
    ui.step('跑 Go 测试');
    runGoTest(go);
  }

  ensureIconResource(go, target);

  ui.step(`编译 exe（${target}/${targetGoarch()}）`);
  ui.detail(`版本 ${version}，提交 ${commit}`);
  buildGo({ go, output, version, commit, debug: Boolean(options.debug), target });
  writeWinres(go, output, version);

  const size = fs.statSync(output).size;
  ui.ok(`已生成 ${path.relative(repoRoot, output) || output}（${(size / 1024 / 1024).toFixed(1)} MB）`);
  return { output, version, commit, go, target };
}

function main() {
  const values = parseCli(process.argv.slice(2));
  if (values.help) {
    console.log(HELP.trim());
    return;
  }
  buildProject({
    web: values['no-web'] ? false : true,
    test: values.test,
    debug: values.debug,
    output: values.output,
    version: values.version,
    go: values.go,
  });
}

if (isEntry(import.meta.url)) {
  try {
    main();
  } catch (error) {
    ui.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}
