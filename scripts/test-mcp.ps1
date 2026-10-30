param([string]$BinaryPath='.local/verify/qag-mcp.exe',[switch]$SkipBuild)
$ErrorActionPreference='Stop'
$qagRoot=Split-Path -Parent $PSScriptRoot
Push-Location $qagRoot
try {
    foreach($qagLine in [IO.File]::ReadAllLines((Join-Path $qagRoot '.local/dev.env'))) {
        $qagParts=$qagLine.Split('=',2)
        if($qagParts.Length -eq 2 -and $qagParts[0] -in @('QAG_TEST_DATABASE_URL','QAG_SECRET_KEY')) {Set-Item -LiteralPath "Env:$($qagParts[0])" -Value $qagParts[1]}
    }
    $env:QAG_TEST_PKI_DIR=Join-Path $qagRoot '.local/pki'
    if(-not [IO.Path]::IsPathRooted($BinaryPath)) {$BinaryPath=Join-Path $qagRoot $BinaryPath}
    if(-not $SkipBuild) {
        $env:CGO_ENABLED='0'
        & go build -o $BinaryPath ./cmd/qag
        if($LASTEXITCODE -ne 0) {throw 'Native build failed'}
    }
    if(-not (Test-Path -LiteralPath $BinaryPath)) {throw 'Native binary missing'}
    $env:QAG_NATIVE_BINARY=$BinaryPath
    $env:QAG_RENDER_FIXTURE_DIR=Join-Path $qagRoot '.local/verify/mcp-render'
    & go test -count=1 ./internal/mcpserver ./internal/httpapi ./internal/runtime ./internal/reporting
    if($LASTEXITCODE -ne 0) {throw 'MCP unit tests failed'}
    & node scripts/verify-server-report.mjs
    if($LASTEXITCODE -ne 0) {throw 'PDF verification failed'}
    & go test -p=1 -tags=integration -count=1 -run 'TestMCP' -v ./internal/service ./internal/deployment
    if($LASTEXITCODE -ne 0) {throw 'MCP integration/native tests failed'}
} finally {Pop-Location}
