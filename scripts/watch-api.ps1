<#
.SYNOPSIS
  监视 Go 源码，自动重新编译并重启后端（开发时的后端热重启）。

.DESCRIPTION
  轮询 cmd/ 与 internal/ 下的 .go 文件、以及 go.mod / go.sum 的修改时间：

    · 有改动   → 重新编译到 bin\dev-api.exe
    · 编译失败 → 打印编译器报错，并让旧进程继续服务（不会把正在调试的环境弄挂）
    · 编译成功 → 关掉旧进程、用同一个端口重新拉起，等 /api/health 就绪后打印耗时

  日志直接写在当前控制台；web.ps1 就是以「继承控制台」的方式启动它的，所以能看见。
  也可以单独运行：.\scripts\watch-api.ps1 -Port 17860

.PARAMETER Port
  后端端口，默认 17860。

.PARAMETER Root
  仓库根目录，默认取本脚本上一级。

.PARAMETER Go
  go.exe 路径，默认自动查找。

.PARAMETER Tools
  _tools.ps1 的路径，默认取同目录。

.PARAMETER IntervalMs
  轮询间隔（毫秒），默认 700。

.EXAMPLE
  .\scripts\watch-api.ps1 -Port 17860
#>
[CmdletBinding()]
param(
    [int]$Port = 17860,
    [string]$Root = '',
    [string]$Go = '',
    [string]$Tools = '',
    [int]$IntervalMs = 700
)

$ErrorActionPreference = 'Stop'
if (-not $Tools) { $Tools = Join-Path $PSScriptRoot '_tools.ps1' }
. $Tools

if (-not $Root) { $Root = Split-Path -Parent $PSScriptRoot }
Push-Location $Root

$exe = Join-Path $Root 'bin\dev-api.exe'
$goExe = Resolve-GoCommand -Hint $Go

# 源码指纹：文件名 + 最后写入时间 + 长度。够用且不用引第三方 watcher。
function Get-SourceStamp {
    $files = @()
    foreach ($dir in @('cmd', 'internal')) {
        $path = Join-Path $Root $dir
        if (Test-Path $path) {
            $files += @(Get-ChildItem -Path $path -Recurse -Filter '*.go' -File -ErrorAction SilentlyContinue)
        }
    }
    foreach ($name in @('go.mod', 'go.sum')) {
        $path = Join-Path $Root $name
        if (Test-Path $path) { $files += @(Get-Item $path) }
    }
    ($files | Sort-Object FullName | ForEach-Object {
        '{0}|{1}|{2}' -f $_.FullName, $_.LastWriteTimeUtc.Ticks, $_.Length
    }) -join "`n"
}

function Build-Backend {
    # 编译报错是正常路径（改代码改到一半就会遇到），不能让它变成终止错误：
    # $ErrorActionPreference='Stop' 时，go 写到 stderr 的报错会被 PowerShell 包成
    # NativeCommandError 抛出来，把整个监视进程带走（后端也会被 finally 关掉）。
    # 所以这里临时放行，并把 ErrorRecord 降级成纯文本，报错由我们统一打印。
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $output = & $goExe build -o $exe ./cmd/markpic 2>&1
        $ok = ($LASTEXITCODE -eq 0)
        $lines = @($output | ForEach-Object { $_.ToString() })
        return @{ Ok = $ok; Output = $lines }
    }
    finally {
        $ErrorActionPreference = $previous
    }
}

# 用 .NET 直接起：Windows PowerShell 5.1 的 Start-Process 在本机可能因为环境变量里
# 同时存在 NO_PROXY / no_proxy 而报 ArgumentException。日志也不重定向，直接进控制台。
function Start-Backend {
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $exe
    $psi.Arguments = "--port $Port --no-open"
    $psi.WorkingDirectory = $Root
    $psi.UseShellExecute = $false
    return [System.Diagnostics.Process]::Start($psi)
}

function Stop-Backend($proc) {
    if ($proc -and -not $proc.HasExited) {
        try { $proc.Kill() } catch { }
        try { $proc.WaitForExit(3000) | Out-Null } catch { }
    }
}

$proc = $null
try {
    Write-Host '==> 编译后端' -ForegroundColor Cyan
    $build = Build-Backend
    if (-not $build.Ok) {
        Write-Host '==> 编译失败：' -ForegroundColor Red
        $build.Output | ForEach-Object { Write-Host $_ }
        throw '先修掉上面的报错再重试。'
    }

    $proc = Start-Backend
    if (-not (Wait-ApiHealth -Port $Port -TimeoutSeconds 30)) {
        throw "后端 30 秒内没有就绪（端口 $Port）。"
    }
    Write-Host "==> 后端已就绪 http://127.0.0.1:$Port —— 正在监视 Go 源码，改完保存会自动重启" -ForegroundColor Green

    $stamp = Get-SourceStamp
    while ($true) {
        Start-Sleep -Milliseconds $IntervalMs
        $current = Get-SourceStamp
        if ($current -eq $stamp) { continue }
        $stamp = $current

        # 编辑器保存不是原子的，等一小会儿再读，避免拿着半个文件去编译。
        Start-Sleep -Milliseconds 150

        $watch = [System.Diagnostics.Stopwatch]::StartNew()
        $build = Build-Backend
        if (-not $build.Ok) {
            Write-Host '==> 编译失败，旧进程继续服务（改好保存后会再自动重启）：' -ForegroundColor Yellow
            $build.Output | ForEach-Object { Write-Host $_ }
            continue
        }

        Stop-Backend $proc
        $proc = Start-Backend
        if (Wait-ApiHealth -Port $Port -TimeoutSeconds 30) {
            $watch.Stop()
            Write-Host ('==> 后端已重启（{0:N1}s）http://127.0.0.1:{1}' -f $watch.Elapsed.TotalSeconds, $Port) -ForegroundColor Green
        }
        else {
            Write-Host "==> 后端重启后没能在端口 $Port 就绪，看看上面的输出" -ForegroundColor Red
        }
    }
}
finally {
    Stop-Backend $proc
    Pop-Location
}
