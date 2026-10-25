$ErrorActionPreference = 'Stop'
$qagRoot = Split-Path -Parent $PSScriptRoot
$qagEnvPath = Join-Path $qagRoot '.local/dev.env'
if (-not (Test-Path -LiteralPath $qagEnvPath)) { throw 'Сначала выполните scripts/init-local.ps1' }
foreach ($qagLine in [IO.File]::ReadAllLines($qagEnvPath)) {
    $qagParts = $qagLine.Split('=', 2)
    if ($qagParts.Length -eq 2 -and $qagParts[0] -in @('QAG_DATABASE_URL', 'QAG_SECRET_KEY')) { Set-Item -LiteralPath "Env:$($qagParts[0])" -Value $qagParts[1] }
}
$qagBinary = Join-Path $qagRoot 'dist/qag.exe'
if (-not (Test-Path -LiteralPath $qagBinary)) { throw 'Сначала выполните scripts/build.ps1' }
& $qagBinary
exit $LASTEXITCODE
