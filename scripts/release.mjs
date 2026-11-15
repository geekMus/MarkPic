#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { parseArgs } from 'node:util';

import { isEntry } from './lib/cli.mjs';
import { repoRoot } from './lib/paths.mjs';
import { formatBytes, isWindows } from './lib/proc.mjs';
import { ui } from './lib/ui.mjs';
import { buildProject } from './build.mjs';
import {
  resolveCommit,
  resolveReleaseVersion,
  sha256,
  writeManifest,
} from './lib/version.mjs';

const HELP = `
用法：node scripts/release.mjs [选项]

打一个版本：编译 exe、算 SHA-256、更新 version.json。只支持在 Windows 上跑。

选项：
  --version <号>  发布号，默认今天；同一天的 tag 已存在时自动加 .1、.2……
  --notes <文字>  更新说明，会写进 version.json 的 notes
  --skip-build    不重新编译，只按现有产物重写 version.json
  --output <路径> 产物路径，默认 bin/MarkPic-v<版本>-windows-amd64.exe
  --go <路径>     go 可执行文件，默认从 PATH / GOROOT / 常见位置查找
  -h, --help      显示这段帮助
`;

function main() {
  const { values } = parseArgs({
    allowPositionals: false,
    strict: true,
    options: {
      version: { type: 'string' },
      notes: { type: 'string', default: '' },
      'skip-build': { type: 'boolean', default: false },
      output: { type: 'string' },
      go: { type: 'string' },
      help: { type: 'boolean', short: 'h', default: false },
    },
  });
  if (values.help) {
    console.log(HELP.trim());
    return;
  }
  if (!isWindows) {
    throw new Error('release 需要 Windows 资源工具（tools/winres）来写版本信息，请在 Windows 上跑。');
  }

  const version = resolveReleaseVersion(values.version ?? '');
  const commit = resolveCommit();
  const output = path.resolve(
    process.cwd(),
    values.output || path.join('bin', `MarkPic-v${version}-windows-amd64.exe`),
  );

  if (!values['skip-build']) {
    buildProject({ version, commit, output, go: values.go });
  } else if (!fs.existsSync(output)) {
    throw new Error(`--skip-build 需要现有产物，但找不到 ${output}。`);
  }

  const size = fs.statSync(output).size;
  const digest = sha256(output);
  const manifest = writeManifest({
    version,
    notes: values.notes,
    file: output,
    sha256: digest,
    size,
  });

  ui.step(`发布 ${manifest.tag}`);
  ui.detail(`安装包：${path.relative(repoRoot, output) || output}（${formatBytes(size)}）`);
  ui.detail(`SHA-256：${digest}`);
  ui.ok('已更新 version.json。');
  ui.blank();
  console.log('下一步（顺序别反：先把 exe 传上 release，再推 version.json）：');
  console.log(`    gh release create ${manifest.tag} "${output}" --title "${manifest.tag}" --notes "<更新说明>"`);
  console.log(`    git add version.json`);
  console.log(`    git commit -m "release ${manifest.tag}"`);
  console.log(`    git push`);
}

if (isEntry(import.meta.url)) {
  try {
    main();
  } catch (error) {
    ui.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}
