<#
.SYNOPSIS
  跑 Go 测试（和 build.ps1 / web.ps1 用同一套 Go 查找逻辑）。

.DESCRIPTION
  默认跑全部包的测试：go test ./... -count=1。
  -count=1 是为了不吃缓存，保证每次都真跑。

.PARAMETER Go
  go.exe 的路径。默认自动查找。

.PARAMETER Pattern
  传给 go test 的包模式，默认 ./...（全部包）。

.EXAMPLE
  pnpm test

.EXAMPLE
  .\scripts\test.ps1 -Pattern ./internal/conv/
#>
[CmdletBinding()]
param(
    [string]$Go = '',
    [string]$Pattern = './...'
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '_tools.ps1')

$root = Split-Path -Parent $PSScriptRoot
Push-Location $root

try {
    $goExe = Resolve-GoCommand -Hint $Go
    Write-Host "==> go test $Pattern -count=1" -ForegroundColor Cyan
    & $goExe test $Pattern -count=1
    if ($LASTEXITCODE -ne 0) { throw '测试未通过。' }
    Write-Host '测试通过。' -ForegroundColor Green
}
finally {
    Pop-Location
}
