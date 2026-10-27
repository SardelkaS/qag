$ErrorActionPreference = 'Stop'
$qagRoot = Split-Path -Parent $PSScriptRoot
$qagEnvPath = Join-Path $qagRoot '.local/dev.env'
if (-not (Test-Path -LiteralPath $qagEnvPath)) { throw 'Сначала выполните scripts/init-local.ps1 и scripts/database.ps1 -IncludeTests' }
foreach ($qagLine in [IO.File]::ReadAllLines($qagEnvPath)) {
    $qagParts = $qagLine.Split('=', 2)
    if ($qagParts.Length -eq 2 -and $qagParts[0] -in @('QAG_TEST_DATABASE_URL', 'QAG_SECRET_KEY')) { Set-Item -LiteralPath "Env:$($qagParts[0])" -Value $qagParts[1] }
}
Push-Location $qagRoot
try {
    $env:QAG_TEST_PKI_DIR=Join-Path $qagRoot '.local/pki'
    & go test -p=1 -tags=integration -count=1 ./...
    if ($LASTEXITCODE -ne 0) { throw 'Integration tests failed' }
} finally { Pop-Location }
