param([switch]$SkipInstall)
$ErrorActionPreference = 'Stop'
$qagRoot = Split-Path -Parent $PSScriptRoot
Push-Location (Join-Path $qagRoot 'web')
try {
    if (-not $SkipInstall) { & npm.cmd ci; if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' } }
    & npm.cmd run build
    if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' }
} finally { Pop-Location }
Push-Location $qagRoot
try {
    New-Item -ItemType Directory -Path (Join-Path $qagRoot 'dist') -Force | Out-Null
    & go build -trimpath -o (Join-Path $qagRoot 'dist/qag.exe') ./cmd/qag
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed' }
} finally { Pop-Location }
