<#
.SYNOPSIS
  开发模式：Vite 热更新 + 原生窗口。

.DESCRIPTION
  界面还是同一份 Vue 代码，只是不进 exe 内嵌的那份产物：

    1. 起 Vite 开发服务器（默认 5173）；
    2. 编译 bin\MarkPic.exe；
    3. 用 MARKPIC_DEV_URL 让程序去加载 http://127.0.0.1:5173 —— 窗口、bridge、
       所有 Go 侧能力都是真的，只有界面来自 Vite，所以改 .vue 会热更新。

  没有「后端端口」这回事：Go 与界面之间走窗口自带的 bridge，不经过网络。

.PARAMETER WebPort
  Vite 开发服务器的端口，默认 5173。

.PARAMETER Go
  go.exe 的路径。默认自动查找。

.PARAMETER Pnpm
  pnpm 的路径。默认自动查找。

.EXAMPLE
  pnpm dev
#>
[CmdletBinding()]
param(
    [int]$WebPort = 5173,
    [string]$Go = '',
    [string]$Pnpm = ''
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '_tools.ps1')

$root = Split-Path -Parent $PSScriptRoot
Push-Location $root

$vite = $null
try {
    $pnpmExe = Resolve-PnpmCommand -Hint $Pnpm
    $goExe = Resolve-GoCommand -Hint $Go

    $webDir = Join-Path $root 'web'
    if (-not (Test-Path (Join-Path $webDir 'node_modules'))) {
        Write-Host '==> 首次运行，先安装前端依赖' -ForegroundColor Cyan
        if ((Invoke-Native -Command $pnpmExe -Arguments @('--dir', 'web', 'install')) -ne 0) {
            throw '前端依赖安装失败。'
        }
    }

    Write-Host ("==> 编译 exe") -ForegroundColor Cyan
    $exe = Join-Path $root 'bin\MarkPic.exe'
    if ((Invoke-Native -Command $goExe -Arguments @('build', '-o', $exe, './cmd/markpic')) -ne 0) {
        throw '编译失败。'
    }

    Write-Host ("==> 启动 Vite（127.0.0.1:{0}）" -f $WebPort) -ForegroundColor Cyan
    $vite = Start-Process -FilePath $pnpmExe `
        -ArgumentList @('--dir', 'web', 'exec', 'vite', '--host', '127.0.0.1', '--port', "$WebPort", '--strictPort') `
        -WorkingDirectory $root -PassThru -WindowStyle Hidden

    if (-not (Wait-TcpPort -Port $WebPort -TimeoutSeconds 40)) {
        throw ("Vite 没有在 40 秒内起来（端口 {0}）。" -f $WebPort)
    }

    Write-Host '==> 启动窗口（界面来自 Vite，Go 侧全是真的）' -ForegroundColor Cyan
    $env:MARKPIC_DEV_URL = "http://127.0.0.1:$WebPort"
    Write-Host '    改 .vue 会热更新；关掉窗口即退出，脚本会把 Vite 一起收掉。' -ForegroundColor DarkGray

    $proc = Start-Process -FilePath $exe -PassThru
    Wait-Process -Id $proc.Id
}
finally {
    if ($vite -ne $null -and -not $vite.HasExited) {
        Write-Host '==> 收掉 Vite' -ForegroundColor DarkGray
        Stop-Process -Id $vite.Id -Force -ErrorAction SilentlyContinue
        Get-CimInstance Win32_Process -Filter "ParentProcessId = $($vite.Id)" -ErrorAction SilentlyContinue |
            ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }
    }
    Pop-Location
}