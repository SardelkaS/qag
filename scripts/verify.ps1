$ErrorActionPreference = 'Stop'
$qagRoot = Split-Path -Parent $PSScriptRoot
Push-Location $qagRoot
try {
    & "$PSScriptRoot/proto.ps1"
    $qagVerifyBinary = Join-Path $qagRoot '.local/verify/qag.exe'
    & "$PSScriptRoot/build.ps1" -OutputPath $qagVerifyBinary
    & "$PSScriptRoot/contracts.ps1"
    & go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
    & go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go vet failed' }
    Push-Location web
    try {
        & npm.cmd run typecheck
        if ($LASTEXITCODE -ne 0) { throw 'TypeScript checks failed' }
        & npm.cmd test
        if ($LASTEXITCODE -ne 0) { throw 'Frontend tests failed' }
        & npx.cmd playwright install chromium
        if ($LASTEXITCODE -ne 0) { throw 'Chromium installation failed' }
    } finally { Pop-Location }
    & "$PSScriptRoot/test-integration.ps1"
    & "$PSScriptRoot/test-e2e.ps1" -BinaryPath $qagVerifyBinary -SkipBuild
    & openspec.cmd validate --all --strict --no-interactive
    if ($LASTEXITCODE -ne 0) { throw 'OpenSpec validation failed' }
    Write-Host 'Все проверки пройдены.'
} finally { Pop-Location }
