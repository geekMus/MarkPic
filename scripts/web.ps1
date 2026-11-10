<#
.SYNOPSIS
  启动开发环境：Go 后端 + Vite 前端（两边都带热更新）。

.DESCRIPTION
  本脚本会拉起两条进程：
    · 后端  http://127.0.0.1:<ApiPort>  —— 先编译成 bin\dev-api.exe 再启动（比 go run 好收尾）
    · 前端  http://localhost:<WebPort>  —— Vite 开发服务器，/api 请求由它代理到后端

  浏览器请打开「前端」那个地址（默认 http://localhost:5173）。
  默认不自动打开浏览器，只在终端里打印地址（加 -Open 才会自动打开）。
  改 .vue/.ts 由 Vite 热更新，改 .go 由 watch-api.ps1 自动重新编译并重启后端，
  两边都不用手动重跑本脚本。
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

.PARAMETER Open
  启动后自动用系统默认浏览器打开前端地址（默认不打开，只在终端里打印地址）。

.PARAMETER NoWatch
  后端不监视源码（默认改 Go 代码会自动重新编译并重启后端）。

.EXAMPLE
  pnpm web

.EXAMPLE
  .\scripts\web.ps1 -ApiPort 17860 -WebPort 5173

.EXAMPLE
  .\scripts\web.ps1 -ApiOnly
#>
[CmdletBinding()]
param(
    [int]$ApiPort = 17860,
    [int]$WebPort = 5173,
    [string]$Go = '',
    [string]$Pnpm = '',
    [switch]$NoInstall,
    [switch]$ApiOnly,
    [switch]$Open,
    [switch]$NoWatch
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '_tools.ps1')

$root = Split-Path -Parent $PSScriptRoot
Push-Location $root

$apiProc = $null
$opener = $null
try {
    $goExe = Resolve-GoCommand -Hint $Go

    # 收尾时按住的是这个 exe，而不是 go run 那层包装，所以先把后端编译出来。
    # 用监视器（默认）时它会自己先编译一次，这里就不重复编译了，省一两秒。
    $devExe = Join-Path $root 'bin\dev-api.exe'
    $binDir = Split-Path -Parent $devExe
    if (-not (Test-Path $binDir)) { New-Item -ItemType Directory -Path $binDir | Out-Null }

    if ($NoWatch -or -not (Test-Path $devExe)) {
        Write-Host '==> 编译后端' -ForegroundColor Cyan
        if ((Invoke-Native -Command $goExe -Arguments @('build', '-o', $devExe, './cmd/markpic')) -ne 0) { throw '后端编译失败，先看看上面的报错。' }
    }

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
    elseif ($NoWatch) {
        Write-Host "==> 启动后端（端口 $ApiPort，不监视源码）" -ForegroundColor Cyan
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
    else {
        Write-Host "==> 启动后端（端口 $ApiPort，改 Go 代码自动重启）" -ForegroundColor Cyan
        # 后端交给 watch-api.ps1 看着：它是独立进程，但输出不重定向，
        # 所以热重启的日志和 Vite 的日志打在同一个控制台里。
        $watchScript = Join-Path $PSScriptRoot 'watch-api.ps1'
        $watchArgs = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $watchScript, '-Port', $ApiPort, '-Root', $root)
        if ($Go) { $watchArgs += @('-Go', $Go) }
        $psi = New-Object System.Diagnostics.ProcessStartInfo
        $psi.FileName = 'powershell.exe'
        $psi.Arguments = (($watchArgs | ForEach-Object { if ($_ -match '\s') { '"' + $_ + '"' } else { $_ } }) -join ' ')
        $psi.WorkingDirectory = $root
        $psi.UseShellExecute = $false
        $apiProc = [System.Diagnostics.Process]::Start($psi)

        if (-not (Wait-ApiHealth -Port $ApiPort -TimeoutSeconds 40)) {
            throw "后端 40 秒内没有就绪。可以手动运行 `"$devExe --port $ApiPort`" 看具体报错。"
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
        if ((Invoke-Native -Command $pnpmExe -Arguments @('--dir', 'web', 'install')) -ne 0) { throw '前端依赖安装失败。' }
    }

    # 让 vite.config.ts 知道后端在哪个端口（它读 MARKPIC_API，默认 17860；旧名 CMP_API 也认）。
    $env:MARKPIC_API = "http://127.0.0.1:$ApiPort"

    # 默认不碰浏览器：开发机常常同时在干别的事（比如打游戏），
    # 弹一个窗口出来抢焦点很讨厌。要自动打开就显式加 -Open。
    # 真要开的话得等 Vite 真的在监听：Vite 在下面这行以前台方式跑，
    # 所以只能放到后台作业里等，早了用户看到的是一张连接失败页。
    if ($Open) {
        $opener = Start-Job -ScriptBlock {
            param($port, $url, $tools)
            . $tools
            if (Wait-TcpPort -Port $port -TimeoutSeconds 30) {
                Open-Url -Url $url | Out-Null
            }
        } -ArgumentList $WebPort, "http://127.0.0.1:$WebPort", (Join-Path $PSScriptRoot '_tools.ps1')
        Write-Host ("  Vite 就绪后会自动打开 http://127.0.0.1:{0}" -f $WebPort) -ForegroundColor DarkGray
    }
    else {
        Write-Host ("  Vite 就绪后请自己打开 http://127.0.0.1:{0}（要它自动打开就加 -Open）" -f $WebPort) -ForegroundColor DarkGray
    }

    Write-Host '==> 启动前端 Vite（Ctrl+C 结束；改 .vue/.ts 前端热更新，改 .go 后端自动重启）' -ForegroundColor Cyan
    # 这里不用 Invoke-Native：Vite 是前台长进程，输出要原样接在控制台上
    # （交互键 r/u/o 也得能用），只把 stderr 不再当异常这一点调好。
    $previousPref = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        & $pnpmExe --dir web dev --port $WebPort --strictPort
    }
    finally {
        $ErrorActionPreference = $previousPref
    }
}
finally {
    if ($opener) {
        Stop-Job $opener -ErrorAction SilentlyContinue
        Remove-Job $opener -Force -ErrorAction SilentlyContinue
    }
    if ($apiProc -and -not $apiProc.HasExited) {
        Write-Host ''
        Write-Host '==> 关闭后端' -ForegroundColor Cyan
        try { $apiProc.Kill() } catch { }
        try { $apiProc.WaitForExit(3000) | Out-Null } catch { }
    }
    # watch-api.ps1 被强杀时不一定能带走它启动的后端，这里兜一下。
    Get-Process -Name 'dev-api' -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    Pop-Location
}
