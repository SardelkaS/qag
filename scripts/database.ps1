param([switch]$IncludeTests)
$ErrorActionPreference = 'Stop'
$qagRoot = Split-Path -Parent $PSScriptRoot
$qagDocker = & "$PSScriptRoot/docker-path.ps1"
Push-Location $qagRoot
try {
    & "$PSScriptRoot/init-local.ps1"
    & $qagDocker compose up --build -d --wait postgres
    if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL startup failed.' }
    & $qagDocker compose run --build --rm db-setup
    if ($LASTEXITCODE -ne 0) { throw 'Demo role / mTLS group preparation failed.' }
} finally { Pop-Location }
