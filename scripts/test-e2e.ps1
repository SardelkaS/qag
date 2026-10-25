$ErrorActionPreference = 'Stop'
$qagRoot = Split-Path -Parent $PSScriptRoot
foreach ($qagLine in [IO.File]::ReadAllLines((Join-Path $qagRoot '.local/dev.env'))) {
    $qagParts = $qagLine.Split('=', 2)
    if ($qagParts.Length -eq 2 -and $qagParts[0] -in @('QAG_TEST_DATABASE_URL', 'QAG_SECRET_KEY')) { Set-Item -LiteralPath "Env:$($qagParts[0])" -Value $qagParts[1] }
}
Push-Location $qagRoot
try {
    & go build -o dist/testfixture.exe ./cmd/testfixture
    if ($LASTEXITCODE -ne 0) { throw 'Fixture build failed' }
    & go build -o dist/qag.exe ./cmd/qag
    if ($LASTEXITCODE -ne 0) { throw 'Application build failed' }
    Push-Location web
    try { & npm.cmd run test:e2e; if ($LASTEXITCODE -ne 0) { throw 'E2E tests failed' } } finally { Pop-Location }
} finally { Pop-Location }
