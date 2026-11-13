$ErrorActionPreference = 'Stop'
$qagRoot = Split-Path -Parent $PSScriptRoot
$qagNames = @('QAG_TEST_DATABASE_URL', 'QAG_TEST_PKI_DIR', 'QAG_TEST_BACKEND')
$qagPrevious = @{}
foreach ($qagName in $qagNames) { $qagPrevious[$qagName] = [Environment]::GetEnvironmentVariable($qagName, 'Process') }
Push-Location $qagRoot
try {
    foreach ($qagLine in [IO.File]::ReadAllLines((Join-Path $qagRoot '.local/dev.env'))) {
        $qagParts = $qagLine.Split('=', 2)
        if ($qagParts.Length -eq 2 -and $qagParts[0] -eq 'QAG_TEST_DATABASE_URL') { $env:QAG_TEST_DATABASE_URL = $qagParts[1] }
    }
    $env:QAG_TEST_PKI_DIR = Join-Path $qagRoot '.local/pki'
    $env:QAG_TEST_BACKEND = 'yaml'
    # The application uses YAML. PostgreSQL is only an isolated external SQL
    # target, as in the existing HTTP/gRPC/TLS integration fixtures.
    & go test -p=1 -tags=integration -count=1 -run 'Test(MCP(NetworkResourcesProtoAndSQL|UIParityAndLifecycle|JobOwnershipCancellationAndStorageFailure|ResourceVariablesStepAndJira|EnvironmentHTTPFormAcrossProtocols|HTTPVariableProbeAndLogBothProtocols)|HTTPVariableProbeDraftAPIAndMasking|DiagnosticsStorageAndPortableSnapshot|ProbeCancellationAndResourceDraft|HistoryPoliciesAndSelectedStep|HistoryPolicyTerminalOutcomes|ResourceVariableDurabilityCancelAndRecovery|EnvironmentVariableDurabilityCancelAndRecovery)$' -v ./internal/service
    if ($LASTEXITCODE -ne 0) { throw 'YAML UI/MCP repository integration failed' }
} finally {
    foreach ($qagName in $qagNames) { [Environment]::SetEnvironmentVariable($qagName, $qagPrevious[$qagName], 'Process') }
    Pop-Location
}
