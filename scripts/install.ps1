# install.ps1 -- build agents-cli and install it to C:\Tools, the one place on PATH.
#
# Same shape as oneshot-cli / tasks-cli: stamp the commit into the binary, build
# to a staging file so a failed build never leaves a half-written exe on PATH,
# then swap it in with a retry because an agent may be mid-call on the old one.

[CmdletBinding()]
param(
    [string]$Destination = 'C:\Tools\agents-cli.exe',
    [switch]$SkipTests
)

$ErrorActionPreference = 'Stop'
$env:GOTOOLCHAIN = 'local'
$env:GOPROXY = 'off'
$env:GOSUMDB = 'off'
$repo = Split-Path -Parent $PSScriptRoot
Push-Location $repo
try {
    if (-not $SkipTests) {
        Write-Host 'running tests...'
        & go test ./...
        if ($LASTEXITCODE -ne 0) { throw 'tests failed; not installing' }
    }

    $commit = 'nogit'
    if (Test-Path (Join-Path $repo '.git')) {
        $commit = (& git rev-parse --short HEAD 2>$null)
        if (-not $commit) { $commit = 'nogit' } else { $commit = $commit.Trim() }
        if ((& git status --porcelain)) { $commit += '-dirty' }
    }
    $built = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
    $ldflags = "-X main.buildCommit=$commit -X main.buildTime=$built"

    $staging = Join-Path ([System.IO.Path]::GetTempPath()) 'agents-cli.build.exe'
    & go build -ldflags $ldflags -o $staging ./cmd/agents-cli
    if ($LASTEXITCODE -ne 0) { throw 'build failed' }

    $destDir = Split-Path -Parent $Destination
    if (-not (Test-Path -LiteralPath $destDir)) { New-Item -ItemType Directory -Path $destDir -Force | Out-Null }

    for ($attempt = 1; ; $attempt++) {
        try {
            if (Test-Path -LiteralPath $Destination) { Remove-Item -LiteralPath $Destination -Force }
            Move-Item -LiteralPath $staging -Destination $Destination -Force
            break
        } catch {
            if ($attempt -ge 5) { throw }
            Start-Sleep -Milliseconds 400
        }
    }
    & $Destination version
} finally {
    Pop-Location
}
