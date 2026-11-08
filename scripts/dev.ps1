<#
.SYNOPSIS
  启动开发环境：Go 后端 + Vite 前端（Vue 热更新）。

.DESCRIPTION
  本脚本会拉起两条进程：
    · 后端  http://127.0.0.1:<ApiPort>  —— 先编译成 bin\dev-api.exe 再启动（比 go run 好收尾）
    · 前端  http://localhost:<WebPort>  —— Vite 开发服务器，/api 请求由它代理到后端

  浏览器请打开「前端」那个地址（默认 http://localhost:5173）。
  改 .vue/.ts 立刻热更新；改 Go 代码需要按 Ctrl+C 重跑本脚本。
  结束脚本时会一并关掉它启动的后端进程。

.PARAMETER ApiPort
  后端端口，默认 17860。若该端口已经有本项目的实例在跑，则直接复用，不重复启动。

.PARAMETER WebPort
  Vite 开发服务器端口，默认 5173。

.PARAMETER Go
  go.exe 的路径。默认自动查找（PATH → GOROOT → 常见安装位置）。

.PARAMETER Pnpm
  pnpm 的路径。默认自动查找。

.PARAMETER NoInstall
  跳过 web/node_modules 的自动安装。

.PARAMETER ApiOnly
  只启动后端（前端自己另开终端跑 pnpm --dir web dev）。

.EXAMPLE
  pnpm dev

.EXAMPLE
  .\scripts\dev.ps1 -ApiPort 17860 -WebPort 5173

.EXAMPLE
  .\scripts\dev.ps1 -ApiOnly
#>
[CmdletBinding()]
param(
    [int]$ApiPort = 17860,
    [int]$WebPort = 5173,
    [string]$Go = '',
    [string]$Pnpm = '',
    [switch]$NoInstall,
    [switch]$ApiOnly
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '_tools.ps1')

$root = Split-Path -Parent $PSScriptRoot
Push-Location $root

$apiProc = $null
try {
    $goExe = Resolve-GoCommand -Hint $Go

    # 收尾时按住的是这个 exe，而不是 go run 那层包装，所以先把后端编译出来。
    $devExe = Join-Path $root 'bin\dev-api.exe'
    $binDir = Split-Path -Parent $devExe
    if (-not (Test-Path $binDir)) { New-Item -ItemType Directory -Path $binDir | Out-Null }

    Write-Host '==> 编译后端' -ForegroundColor Cyan
    & $goExe build -o $devExe ./cmd/convert-md-picture
    if ($LASTEXITCODE -ne 0) { throw '后端编译失败，先看看上面的报错。' }

    # 上一次没退干净的僵尸进程：端口没响应但进程还在，直接清掉。
    $stale = @(Get-Process -Name 'dev-api' -ErrorAction SilentlyContinue)
    if ($stale.Count -gt 0 -and -not (Test-ApiHealth -Port $ApiPort)) {
        Write-Host '==> 清理残留的后端进程' -ForegroundColor Yellow
        $stale | Stop-Process -Force -ErrorAction SilentlyContinue
        Start-Sleep -Milliseconds 300
    }

    if (Test-ApiHealth -Port $ApiPort) {
        Write-Host "==> 端口 $ApiPort 上已经有本项目实例在跑，直接复用" -ForegroundColor Yellow
    }
    else {
        Write-Host "==> 启动后端（端口 $ApiPort）" -ForegroundColor Cyan
        # 用 .NET 直接起进程：Windows PowerShell 5.1 的 Start-Process 在本机可能因为
        # 环境变量里同时存在 NO_PROXY / no_proxy 而报 ArgumentException。
        $psi = New-Object System.Diagnostics.ProcessStartInfo
        $psi.FileName = $devExe
        $psi.Arguments = "--port $ApiPort --no-open"
        $psi.WorkingDirectory = $root
        $psi.UseShellExecute = $false
        $apiProc = [System.Diagnostics.Process]::Start($psi)

        if (-not (Wait-ApiHealth -Port $ApiPort -TimeoutSeconds 30)) {
            throw "后端 30 秒内没有就绪。可以手动运行 `"$devExe --port $ApiPort`" 看具体报错。"
        }
    }

    $health = Get-ApiHealth -Port $ApiPort
    $picgoText = '未检测到 PicGo'
    if ($health -and $health.picgo -and $health.picgo.online) { $picgoText = 'PicGo 已连接' }

    Write-Host ''
    Write-Host '--------------------------------------------------------------' -ForegroundColor DarkGray
    Write-Host ("  后端   http://127.0.0.1:{0}   版本 {1}，{2}" -f $ApiPort, $health.version, $picgoText)
    if (-not $ApiOnly) {
        Write-Host ("  前端   http://127.0.0.1:{0}   ← 浏览器打开这个" -f $WebPort) -ForegroundColor Green
        Write-Host ("  接口   /api/* 由 Vite 代理到 http://127.0.0.1:{0}（见 docs/API.md）" -f $ApiPort)
    }
    Write-Host '--------------------------------------------------------------' -ForegroundColor DarkGray
    Write-Host ''

    if ($ApiOnly) {
        Write-Host '只启动了后端，按 Ctrl+C 结束。' -ForegroundColor DarkGray
        while (-not $apiProc.HasExited) { Start-Sleep -Milliseconds 500 }
        return
    }

    $pnpmExe = Resolve-PnpmCommand -Hint $Pnpm

    if (-not (Test-Path (Join-Path $root 'web\node_modules'))) {
        if ($NoInstall) {
            throw 'web\node_modules 不存在，而 -NoInstall 指定了不安装。请先运行：pnpm --dir web install'
        }
        Write-Host '==> 安装前端依赖（首次运行需要，之后会跳过）' -ForegroundColor Cyan
        & $pnpmExe --dir web install
        if ($LASTEXITCODE -ne 0) { throw '前端依赖安装失败。' }
    }

    # 让 vite.config.ts 知道后端在哪个端口（它读 CMP_API，默认 17860）。
    $env:CMP_API = "http://127.0.0.1:$ApiPort"

    Write-Host '==> 启动前端 Vite（改 Vue 代码即时生效，Ctrl+C 结束）' -ForegroundColor Cyan
    & $pnpmExe --dir web dev --port $WebPort --strictPort
}
finally {
    if ($apiProc -and -not $apiProc.HasExited) {
        Write-Host ''
        Write-Host '==> 关闭后端' -ForegroundColor Cyan
        try { $apiProc.Kill() } catch { }
        try { $apiProc.WaitForExit(3000) | Out-Null } catch { }
    }
    Pop-Location
}
