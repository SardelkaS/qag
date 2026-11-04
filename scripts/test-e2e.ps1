param([string]$BinaryPath = '.local/verify/qag.exe', [switch]$SkipBuild)
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
    if (-not [IO.Path]::IsPathRooted($BinaryPath)) { $BinaryPath=Join-Path $qagRoot $BinaryPath }
    if (-not $SkipBuild) {
        New-Item -ItemType Directory -Path (Split-Path -Parent $BinaryPath) -Force | Out-Null
        & go build -o $BinaryPath ./cmd/qag
        if ($LASTEXITCODE -ne 0) { throw 'Application build failed' }
    }
    if (-not (Test-Path -LiteralPath $BinaryPath)) { throw 'Нет проверяемого бинарника' }
    $env:QAG_E2E_BINARY=$BinaryPath
    $env:QAG_TEST_PKI_DIR=Join-Path $qagRoot '.local/pki'
    Push-Location web
    try {
        $qagE2EErrorPreference=$ErrorActionPreference
        try { $ErrorActionPreference='Continue'; & npm.cmd run test:e2e }
        finally { $ErrorActionPreference=$qagE2EErrorPreference }
        if ($LASTEXITCODE -ne 0) { throw 'E2E tests failed' }
    } finally { Pop-Location }
} finally { Pop-Location }
