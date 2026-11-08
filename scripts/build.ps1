<#
.SYNOPSIS
  构建 convert-md-picture。

.DESCRIPTION
  默认只构建 Go 部分（前端产物已提交在 internal/webui/dist，无需 Node）。
  加 -Web 会先用 pnpm 重新构建前端再打 exe。

.EXAMPLE
  .\scripts\build.ps1
  .\scripts\build.ps1 -Web -Test
#>
[CmdletBinding()]
param(
    [switch]$Web,
    [switch]$Test,
    [string]$Output = "bin\convert-md-picture.exe",
    [string]$Version = ""
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root

try {
    # 先把工具链检查清楚，否则后面只会报一句“无法将 go 项识别为 cmdlet”，很难排查。
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        throw '找不到 go 命令：请安装 Go（https://go.dev/dl/）并把它加入 PATH，然后重新运行本脚本。'
    }

    if ($Web) {
        Write-Host '==> 构建前端' -ForegroundColor Cyan
        if (-not (Get-Command pnpm -ErrorAction SilentlyContinue)) {
            throw '找不到 pnpm 命令：-Web 需要先安装 Node.js 与 pnpm（npm i -g pnpm）。'
        }
        Push-Location (Join-Path $root 'web')
        try {
            if (-not (Test-Path 'node_modules')) { pnpm install }
            pnpm build
        }
        finally {
            Pop-Location
        }
    }

    if ($Test) {
        Write-Host '==> 运行测试' -ForegroundColor Cyan
        go test ./...
        if ($LASTEXITCODE -ne 0) { throw '测试未通过' }
    }

    # Windows 图标：.syso 生成一次就会跟着仓库走，之后无需联网。
    # 注意 Go 只链接「主包目录」里的 .syso，所以它必须放在 cmd/convert-md-picture 下。
    # 失败不影响构建，只是 exe 用默认图标。
    $ico = Join-Path $root 'assets\icon.ico'
    $syso = Join-Path $root 'cmd\convert-md-picture\rsrc_windows_amd64.syso'
    if ((Test-Path $ico) -and -not (Test-Path $syso)) {
        Write-Host '==> 生成 Windows 图标资源' -ForegroundColor Cyan
        $prevPref = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        try {
            & go run github.com/akavel/rsrc@latest -ico $ico -arch amd64 -o $syso 2>&1 | Out-Host
            if ($LASTEXITCODE -ne 0) {
                Write-Warning '图标资源生成失败（不影响构建，exe 将使用默认图标）'
            }
        }
        catch {
            Write-Warning "图标资源生成失败（不影响构建）：$_"
        }
        finally {
            $ErrorActionPreference = $prevPref
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

    $module = 'github.com/YiiMus/convert-md-picture/internal/version'
    $ldflags = "-s -w -X $module.Version=$Version -X $module.Commit=$commit"

    Write-Host "==> 构建 exe（版本 $Version）" -ForegroundColor Cyan
    $outDir = Split-Path -Parent $Output
    if ($outDir -and -not (Test-Path $outDir)) { New-Item -ItemType Directory -Path $outDir | Out-Null }

    go build -trimpath -ldflags $ldflags -o $Output ./cmd/convert-md-picture
    if ($LASTEXITCODE -ne 0) { throw '构建失败' }

    $file = Get-Item $Output
    Write-Host ("完成：{0}（{1:N1} MB）" -f $file.FullName, ($file.Length / 1MB)) -ForegroundColor Green
}
finally {
    Pop-Location
}
