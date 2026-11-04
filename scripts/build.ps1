param([switch]$SkipInstall, [string]$OutputPath = 'dist/qag.exe')
$ErrorActionPreference = 'Stop'
$qagRoot = Split-Path -Parent $PSScriptRoot
foreach ($qagAsset in @('assets/branding/qag-master.png','assets/branding/qag.ico','web/public/favicon.ico','web/public/qag-32.png','web/public/qag-256.png')) {
    if (-not (Test-Path -LiteralPath (Join-Path $qagRoot $qagAsset))) { throw "Отсутствует иконка: $qagAsset" }
}
Push-Location (Join-Path $qagRoot 'web')
try {
    if (-not $SkipInstall) { & npm.cmd ci; if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' } }
    & npm.cmd run build
    if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' }
} finally { Pop-Location }
Push-Location $qagRoot
try {
    foreach ($qagArch in @('amd64','arm64')) {
        & go run github.com/akavel/rsrc@v0.10.2 -ico assets/branding/qag.ico -arch $qagArch -o "cmd/qag/icon_windows_$qagArch.syso"
        if ($LASTEXITCODE -ne 0) { throw 'Не удалось подготовить Windows icon resource' }
    }
    New-Item -ItemType Directory -Path (Join-Path $qagRoot 'dist') -Force | Out-Null
    if (-not [IO.Path]::IsPathRooted($OutputPath)) { $OutputPath = Join-Path $qagRoot $OutputPath }
    New-Item -ItemType Directory -Path (Split-Path -Parent $OutputPath) -Force | Out-Null
    & go build -trimpath -o $OutputPath ./cmd/qag
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed' }
} finally { Pop-Location }
