<#
.SYNOPSIS
  编译并启动 MarkPic 的桌面窗口。

.DESCRIPTION
  这是个纯桌面程序：没有本地服务，也没有端口。脚本做的事只有两件 —— 编译
  bin\MarkPic.exe，然后把它拉起来。窗口关掉，程序就退出。

  MarkPic 自己是单实例的：已经在跑的时候再启动一次，只会把现有窗口提到前面。

.PARAMETER Build
  先重新编译一次（默认仅在 exe 不存在时编译）。改过 Go 代码就带上它。

.PARAMETER DataDir
  指定数据目录。默认用 exe 旁边的 data/（绿色版），那里不可写时回落 %APPDATA%。

.PARAMETER Go
  go.exe 的路径。默认自动查找（PATH → GOROOT → 常见安装位置）。

.EXAMPLE
  pnpm start

.EXAMPLE
  .\scripts\start.ps1 -Build
#>
[CmdletBinding()]
param(
    [switch]$Build,
    [string]$DataDir = '',
    [string]$Go = ''
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '_tools.ps1')

$root = Split-Path -Parent $PSScriptRoot
Push-Location $root

try {
    $exe = Join-Path $root 'bin\MarkPic.exe'

    if ($Build -or -not (Test-Path $exe)) {
        $goExe = Resolve-GoCommand -Hint $Go
        Write-Host '==> 编译 exe' -ForegroundColor Cyan
        if ((Invoke-Native -Command $goExe -Arguments @('build', '-o', $exe, './cmd/markpic')) -ne 0) {
            throw '编译失败。'
        }
    }

    if ($DataDir -ne '') {
        $env:MARKPIC_DATA_DIR = (Resolve-Path -LiteralPath $DataDir).Path
        Write-Host ("    数据目录：{0}" -f $env:MARKPIC_DATA_DIR) -ForegroundColor DarkGray
    }

    Write-Host '==> 启动窗口' -ForegroundColor Cyan
    $proc = Start-Process -FilePath $exe -PassThru
    Write-Host ("    pid {0}（关掉窗口即退出）" -f $proc.Id) -ForegroundColor DarkGray
}
finally {
    Pop-Location
}