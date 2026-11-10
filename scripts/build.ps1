<#
.SYNOPSIS
  构建 MarkPic 的单个 exe。

.DESCRIPTION
  流程：前端（可选）→ 测试（可选）→ 图标资源（缺了才生成）→ 编译 exe（注入版本号）。

  默认只构建 Go 部分：前端产物已经提交在 internal/webui/dist，构建 exe 不需要 Node。
  改了前端界面再加 -Web，脚本会用 pnpm 重建前端再编译。

.PARAMETER Web
  先用 pnpm 重建前端，再编译 exe。

.PARAMETER Test
  编译前先跑一遍 go test ./...。

.PARAMETER Output
  输出路径，默认 bin\MarkPic.exe。

.PARAMETER Version
  写进二进制的版本号。默认取 git describe（仓库没打过 tag 时回落到 1.0.0）。

.PARAMETER Go
  go.exe 的路径。默认自动查找（PATH → GOROOT → 常见安装位置）。

.PARAMETER Pnpm
  pnpm 的路径。仅 -Web 时需要，默认自动查找。

.EXAMPLE
  pnpm build

.EXAMPLE
  .\scripts\build.ps1 -Web -Test
#>
[CmdletBinding()]
param(
    [switch]$Web,
    [switch]$Test,
    [string]$Output = "bin\MarkPic.exe",
    [string]$Version = "",
    [string]$Go = '',
    [string]$Pnpm = ''
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '_tools.ps1')

$root = Split-Path -Parent $PSScriptRoot
Push-Location $root

try {
    $goExe = Resolve-GoCommand -Hint $Go

    if ($Web) {
        Write-Host '==> 构建前端（Vue 3 + Vite → internal/webui/dist）' -ForegroundColor Cyan
        $pnpmExe = Resolve-PnpmCommand -Hint $Pnpm
        Push-Location (Join-Path $root 'web')
        try {
            if (-not (Test-Path 'node_modules')) {
                Write-Host '    首次运行，先安装前端依赖' -ForegroundColor DarkGray
                if ((Invoke-Native -Command $pnpmExe -Arguments @('install')) -ne 0) { throw '前端依赖安装失败。' }
            }
            if ((Invoke-Native -Command $pnpmExe -Arguments @('build')) -ne 0) { throw '前端构建失败。' }
        }
        finally {
            Pop-Location
        }
    }

    if ($Test) {
        Write-Host '==> 运行测试' -ForegroundColor Cyan
        if ((Invoke-Native -Command $goExe -Arguments @('test', './...', '-count=1')) -ne 0) { throw '测试未通过。' }
    }

    # 图标资源：.syso 生成一次就跟着仓库走，之后无需联网。
    # 注意 Go 只链接「主包目录」里的 .syso，所以它必须放在 cmd/markpic 下。
    # 失败不影响构建，只是 exe 用默认图标。
    $ico = Join-Path $root 'assets\icon.ico'
    $syso = Join-Path $root 'cmd\markpic\rsrc_windows_amd64.syso'
    if ((Test-Path $ico) -and -not (Test-Path $syso)) {
        Write-Host '==> 生成 Windows 图标资源' -ForegroundColor Cyan
        try {
            if ((Invoke-Native -Command $goExe -Arguments @('run', 'github.com/akavel/rsrc@latest', '-ico', $ico, '-arch', 'amd64', '-o', $syso)) -ne 0) {
                Write-Warning '图标资源生成失败（不影响构建，exe 将使用默认图标）'
            }
        }
        catch {
            Write-Warning "图标资源生成失败（不影响构建）：$_"
        }
    }

    if (-not $Version) {
        # 打过 tag 时 git describe 给的是 v1.0.0 或 v1.0.0-3-g1234567，直接当版本号；
        # 没打过 tag 时它给的是提交号（dc9dd87、117391e），拿它当版本号界面会显示一串
        # 哈希，所以那种情况回落到源码里的默认版本号。判据是「数字后面跟着点」，
        # 因为提交号可能也以数字开头，只看首位数字会误判。
        $Version = (git describe --tags --always --dirty 2>$null)
        if ($Version -match '^v?\d+\.\d+') {
            $Version = $Version -replace '^v', ''
        }
        else {
            $Version = '1.0.0'
        }
    }
    $commit = (git rev-parse --short HEAD 2>$null)
    if (-not $commit) { $commit = 'none' }

    $module = 'github.com/geekMus/markpic/internal/version'
    $ldflags = "-s -w -X $module.Version=$Version -X $module.Commit=$commit"

    Write-Host "==> 编译 exe（版本 $Version，提交 $commit）" -ForegroundColor Cyan
    $outDir = Split-Path -Parent $Output
    if ($outDir -and -not (Test-Path $outDir)) { New-Item -ItemType Directory -Path $outDir | Out-Null }

    if ((Invoke-Native -Command $goExe -Arguments @('build', '-trimpath', '-ldflags', $ldflags, '-o', $Output, './cmd/markpic')) -ne 0) { throw '构建失败。' }

    $file = Get-Item $Output
    Write-Host ("完成：{0}（{1:N1} MB）" -f $file.FullName, ($file.Length / 1MB)) -ForegroundColor Green
    Write-Host '直接双击这个 exe 即可运行（图形界面），或带上文件参数当命令行用。' -ForegroundColor DarkGray
}
finally {
    Pop-Location
}
