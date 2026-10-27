$ErrorActionPreference = 'Stop'
$qagRoot = Split-Path -Parent $PSScriptRoot
$env:QAG_DOCKER = & "$PSScriptRoot/docker-path.ps1"
foreach ($qagLine in [IO.File]::ReadAllLines((Join-Path $qagRoot '.local/dev.env'))) {
    $qagParts = $qagLine.Split('=',2)
    if ($qagParts.Length -eq 2 -and $qagParts[0] -eq 'QAG_TEST_DATABASE_URL') { $env:QAG_TEST_DATABASE_URL = $qagParts[1] }
}
Push-Location $qagRoot
try {
    & $env:QAG_DOCKER compose build
    if ($LASTEXITCODE -ne 0) { throw 'Container build failed.' }
    & go test -tags=container -count=1 -timeout=5m -v ./internal/deployment
    if ($LASTEXITCODE -ne 0) { throw 'Container verification failed.' }
} finally { Pop-Location }
