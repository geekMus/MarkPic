<#
.SYNOPSIS
  构建并运行打包好的单文件 exe —— 和最终用户双击 exe 的效果一样。

.DESCRIPTION
  · 默认先编译一次 Go，产出 bin\convert-md-picture.exe（前端产物 internal/webui/dist 已提交在仓库里）
  · 然后以图形界面模式启动它：起本机服务并打开浏览器，界面就是交付给用户的那个
  · 想连前端一起重新构建（改过 web/）就加 -Web
  · 想直接用现有的 exe（不重新编译）就加 -NoBuild
  · 停止：Ctrl+C，或者直接关掉这个窗口

  和 pnpm web 的区别：web 是开发环境（Vite 起前端、改代码热更新、端口 5173），
  start 跑的是打包后的形态（单进程、端口 17860、界面由 exe 自己提供）。

.PARAMETER Port
  端口，默认 17860。被占用时程序会自动往后试 20 个端口。

.PARAMETER Web
  先把前端也重新构建一遍（改了 web/ 下的代码之后用）。

.PARAMETER NoBuild
  跳过构建，直接运行现有的 bin\convert-md-picture.exe。

.PARAMETER NoOpen
  不自动打开浏览器（等价于给 exe 传 --no-open）。

.PARAMETER Go
  go.exe 的路径。默认自动查找（PATH → GOROOT → 常见安装位置）。

.PARAMETER Pnpm
  pnpm 的路径。仅 -Web 时需要，默认自动查找。

.EXAMPLE
  pnpm start

.EXAMPLE
  pnpm start -- -Web -NoOpen

.EXAMPLE
  .\scripts\start.ps1 -NoBuild -Port 17900
#>
[CmdletBinding()]
param(
    [int]$Port = 17860,
    [switch]$Web,
    [switch]$NoBuild,
    [switch]$NoOpen,
    [string]$Go = '',
    [string]$Pnpm = ''
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '_tools.ps1')

$root = Split-Path -Parent $PSScriptRoot
Push-Location $root

$code = 0
try {
    $exe = Join-Path $root 'bin\convert-md-picture.exe'

    if ($NoBuild) {
        if (-not (Test-Path $exe)) {
            throw "没有找到 $exe —— 去掉 -NoBuild 先构建一次。"
        }
    }
    else {
        $buildArgs = @{}
        if ($Web) { $buildArgs['Web'] = $true }
        if ($Go) { $buildArgs['Go'] = $Go }
        if ($Pnpm) { $buildArgs['Pnpm'] = $Pnpm }
        & (Join-Path $PSScriptRoot 'build.ps1') @buildArgs
        if (-not (Test-Path $exe)) { throw '构建没有产出 exe，先看看上面的报错。' }
    }

    # 数组的每个元素就是一个独立参数：写成 "--port $Port" 会被当成一个带空格的参数传给 exe。
    $arguments = @('--port', "$Port")
    if ($NoOpen) { $arguments += '--no-open' }

    Write-Host ''
    Write-Host "==> 运行打包版（端口 $Port）" -ForegroundColor Cyan
    if (-not $NoOpen) {
        Write-Host '    启动后会打开浏览器；不想自动打开就加 -NoOpen' -ForegroundColor DarkGray
    }
    if (-not $Web) {
        Write-Host '    界面用的是仓库里已经构建好的前端产物；改过 web/ 就加 -Web 一起重建' -ForegroundColor DarkGray
    }

    # 直接前台运行：它的日志进当前控制台，Ctrl+C 就能结束。
    # 不能用 Start-Process —— 本机 Windows PowerShell 5.1 会因为环境变量里同时存在
    # NO_PROXY / no_proxy 报 ArgumentException。
    & $exe @arguments
    # 把 exe 的退出码带回去：0 成功、1 有文件失败、2 参数有误（见 README）
    $code = $LASTEXITCODE
}
finally {
    Pop-Location
}

exit $code
