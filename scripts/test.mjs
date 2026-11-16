#!/usr/bin/env node
import process from 'node:process';
import { parseArgs } from 'node:util';

import { isEntry } from './lib/cli.mjs';
import { resolveGo, runGoTest } from './lib/go.mjs';
import { ensureEmbeddedUi } from './lib/web.mjs';
import { ui } from './lib/ui.mjs';

const HELP = `
用法：node scripts/test.mjs [选项]

跑 Go 测试。默认 go test ./... -count=1。

选项：
  --pattern <包>  传给 go test 的包模式，默认 ./...
  --go <路径>     go 可执行文件，默认从 PATH / GOROOT / 常见位置查找
  -h, --help      显示这段帮助
`;

function main() {
  const { values } = parseArgs({
    allowPositionals: false,
    strict: true,
    options: {
      pattern: { type: 'string', default: './...' },
      go: { type: 'string' },
      help: { type: 'boolean', short: 'h', default: false },
    },
  });
  if (values.help) {
    console.log(HELP.trim());
    return;
  }
  const go = resolveGo(values.go ?? '');
  // go test 也要编译 internal/ui（//go:embed dist），首次 clone 得先把前端产物建出来。
  ensureEmbeddedUi();
  ui.step('跑 Go 测试');
  runGoTest(go, values.pattern);
}

if (isEntry(import.meta.url)) {
  try {
    main();
  } catch (error) {
    ui.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}
