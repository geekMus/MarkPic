<#
.SYNOPSIS
  打一个版本：编译 exe、算出校验值、更新 version.json。

.DESCRIPTION
  「检查更新」读的就是仓库根的 version.json（原始文件，不是 GitHub Releases API）。
  这个脚本按发布号编译一份 exe，把它的 SHA-256 与大小写进 version.json，
  剩下的就是把它发出去：

      git tag v1.0.1
      git commit -am "release v1.0.1" ; git push --follow-tags
      gh release create v1.0.1 bin\MarkPic-v1.0.1-windows-amd64.exe --title v1.0.1

  发版顺序很重要：先把 exe 传上 release，再推 version.json 所在的分支 ——
  否则老版本会看到一个指向还不存在的下载地址的清单。

.PARAMETER Version
  发布号，必须是 1.2.3 这种形式。

.PARAMETER Notes
  更新说明，会显示在「关于」弹窗里。

.PARAMETER SkipBuild
  不重新编译，只按现有产物重写 version.json。

.PARAMETER Output
  产物路径，默认 bin\MarkPic-v<版本>-windows-amd64.exe。

.EXAMPLE
  .\scripts\release.ps1 -Version 1.0.1 -Notes "修了几个对齐问题"
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Version,
    [string]$Notes = '',
    [switch]$SkipBuild,
    [string]$Output = '',
    [string]$Go = '',
    [string]$Pnpm = ''
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '_tools.ps1')

$root = Split-Path -Parent $PSScriptRoot
Push-Location $root

try {
    if ($Version -notmatch '^\d+\.\d+\.\d+$') {
        throw "发布号得写成 1.2.3 这种形式，收到的是：$Version"
    }
    $tag = "v$Version"
    if (-not $Output) {
        $Output = "bin\MarkPic-$tag-windows-amd64.exe"
    }

    if (-not $SkipBuild) {
        Write-Host "==> 编译 $tag" -ForegroundColor Cyan
        & (Join-Path $PSScriptRoot 'build.ps1') -Version $Version -Output $Output -Go $Go -Pnpm $Pnpm
    }

    $file = Join-Path $root $Output
    if (-not (Test-Path $file)) {
        throw "没有构建产物：$file"
    }

    $item = Get-Item $file
    $hash = (Get-FileHash -Algorithm SHA256 -Path $file).Hash.ToLowerInvariant()
    $asset = $item.Name
    $url = "https://github.com/geekMus/markpic/releases/download/$tag/$asset"

    $manifest = [ordered]@{
        version   = $Version
        tag       = $tag
        published = (Get-Date -Format 'yyyy-MM-dd')
        notes     = $Notes
        windows   = [ordered]@{
            amd64 = [ordered]@{
                url    = $url
                sha256 = $hash
                size   = $item.Length
            }
        }
    }

    $json = ($manifest | ConvertTo-Json -Depth 6) -replace "`r`n", "`n"
    $manifestPath = Join-Path $root 'version.json'
    [IO.File]::WriteAllText($manifestPath, $json + "`n", (New-Object System.Text.UTF8Encoding($false)))

    Write-Host ''
    Write-Host "==> 已更新 version.json（版本 $Version）" -ForegroundColor Green
    Write-Host "    产物   $file"
    Write-Host ("    大小   {0:N0} 字节" -f $item.Length)
    Write-Host "    SHA256 $hash"
    Write-Host ''
    Write-Host '接下来把它发出去（先传 exe，再推清单）：' -ForegroundColor Cyan
    Write-Host "    gh release create $tag `"$file`" --title $tag --notes `"$Notes`""
    Write-Host ('    git add version.json ; git commit -m "release {0}"' -f $tag)
    Write-Host "    git tag $tag ; git push --follow-tags"
}
finally {
    Pop-Location
}
