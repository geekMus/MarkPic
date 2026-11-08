<#
  脚本之间共用的小工具：定位工具链、探测本地服务。
  单独成文件是为了让 build.ps1 / web.ps1 / test.ps1 报一样的错、用一样的查找顺序。

  注意：本文件必须存成「UTF-8 with BOM」。
  Windows PowerShell 5.1 会把无 BOM 的 .ps1 当 GBK 读，中文会变乱码并报语法错误。
#>

# 找 go.exe：先看 -Go 参数，再看 PATH，再看 GOROOT，最后看几个常见安装位置。
function Resolve-GoCommand {
    [CmdletBinding()]
    param([string]$Hint = '')

    if ($Hint) {
        if (Test-Path $Hint) { return (Resolve-Path $Hint).Path }
        throw "找不到 go：-Go 指定的路径不存在（$Hint）"
    }

    $cmd = Get-Command go -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }

    foreach ($groot in @($env:GOROOT, $env:GO_ROOT)) {
        if ($groot) {
            $p = Join-Path $groot 'bin\go.exe'
            if (Test-Path $p) { return $p }
        }
    }

    # 免安装（zip 解压）版的常见位置。开发机上 Go 就装在 D:\Env\Go，靠这条兜底。
    $candidates = @('D:\Env\Go\bin\go.exe')
    if ($env:ProgramFiles) { $candidates += (Join-Path $env:ProgramFiles 'Go\bin\go.exe') }
    if (${env:ProgramFiles(x86)}) { $candidates += (Join-Path ${env:ProgramFiles(x86)} 'Go\bin\go.exe') }
    if ($env:LOCALAPPDATA) { $candidates += (Join-Path $env:LOCALAPPDATA 'Programs\Go\bin\go.exe') }
    foreach ($p in $candidates) {
        if (Test-Path $p) { return $p }
    }

    throw '找不到 go 命令。请安装 Go（https://go.dev/dl/）、把它的 bin 目录加入 PATH，或用 -Go <go.exe 路径> 指定。'
}

# 找 pnpm：先看 -Pnpm 参数，再看 PATH，最后看 npm 全局目录。
function Resolve-PnpmCommand {
    [CmdletBinding()]
    param([string]$Hint = '')

    if ($Hint) {
        if (Test-Path $Hint) { return (Resolve-Path $Hint).Path }
        throw "找不到 pnpm：-Pnpm 指定的路径不存在（$Hint）"
    }

    $cmd = Get-Command pnpm -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }

    if ($env:APPDATA) {
        $npmDir = Join-Path $env:APPDATA 'npm'
        foreach ($name in @('pnpm.cmd', 'pnpm.exe')) {
            $p = Join-Path $npmDir $name
            if (Test-Path $p) { return $p }
        }
    }

    throw '找不到 pnpm 命令。请安装 Node.js 与 pnpm（npm i -g pnpm），或把 pnpm 加入 PATH。'
}

# 探一次本地服务：返回 $true 表示「这个端口上跑的是本项目」。
# 用 curl.exe 而不是 Invoke-WebRequest，是为了绕开系统代理设置。
function Test-ApiHealth {
    [CmdletBinding()]
    param([int]$Port)

    $url = "http://127.0.0.1:$Port/api/health"
    if (Get-Command curl.exe -ErrorAction SilentlyContinue) {
        $body = & curl.exe -s --noproxy '*' --max-time 2 $url 2>$null
        if ($LASTEXITCODE -eq 0 -and $body -match 'convert-md-picture') { return $true }
        return $false
    }
    try {
        $resp = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 2
        return ($resp.Content -match 'convert-md-picture')
    }
    catch { return $false }
}

# 轮询等待服务就绪。
function Wait-ApiHealth {
    [CmdletBinding()]
    param(
        [int]$Port,
        [int]$TimeoutSeconds = 30
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        if (Test-ApiHealth -Port $Port) { return $true }
        Start-Sleep -Milliseconds 300
    }
    return $false
}

# 读一次 /api/health 并解析成对象（拿不到就返回 $null）。
function Get-ApiHealth {
    [CmdletBinding()]
    param([int]$Port)

    try {
        if (Get-Command curl.exe -ErrorAction SilentlyContinue) {
            $body = & curl.exe -s --noproxy '*' --max-time 2 "http://127.0.0.1:$Port/api/health" 2>$null
        }
        else {
            $body = (Invoke-WebRequest -Uri "http://127.0.0.1:$Port/api/health" -UseBasicParsing -TimeoutSec 2).Content
        }
        if (-not $body) { return $null }
        return ($body | ConvertFrom-Json)
    }
    catch { return $null }
}

# 等一个 TCP 端口有人监听（Vite 没有 /api/health 之类的探针，只能连端口）。
function Wait-TcpPort {
    [CmdletBinding()]
    param(
        [int]$Port,
        [int]$TimeoutSeconds = 30
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $client = New-Object System.Net.Sockets.TcpClient
        try {
            $client.Connect('127.0.0.1', $Port)
            $client.Close()
            return $true
        }
        catch {
            $client.Dispose()
            Start-Sleep -Milliseconds 250
        }
    }
    return $false
}

# 用系统默认浏览器打开一个网址。
# 不用 Start-Process：Windows PowerShell 5.1 在本机可能因为环境变量里同时存在
# NO_PROXY / no_proxy 而抛 ArgumentException；交给 cmd 的 start 更稳。
function Open-Url {
    [CmdletBinding()]
    param([string]$Url)

    try {
        & cmd.exe /c start '' $Url 2>$null | Out-Null
        return $true
    }
    catch {
        try {
            Start-Process $Url | Out-Null
            return $true
        }
        catch { return $false }
    }
}
