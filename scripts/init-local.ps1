$ErrorActionPreference = 'Stop'
$qagRoot = Split-Path -Parent $PSScriptRoot
$qagLocal = Join-Path $qagRoot '.local'
$qagEnvPath = Join-Path $qagLocal 'dev.env'
$qagDocker = & "$PSScriptRoot/docker-path.ps1"
Push-Location $qagRoot
try {
    if (Test-Path -LiteralPath $qagEnvPath) {
        & $qagDocker compose --profile tools run --build --rm --no-deps import-local
    } else {
        & $qagDocker compose run --build --rm --no-deps init
    }
    if ($LASTEXITCODE -ne 0) { throw 'Cannot prepare shared installation state; no new host key was generated.' }
    $qagExport = Join-Path $qagLocal ('native-export-' + [guid]::NewGuid().ToString('N'))
    & "$PSScriptRoot/export-config.ps1" -Directory $qagExport
    $qagExportEnv = Join-Path $qagExport 'dev.env'
    if (Test-Path -LiteralPath $qagEnvPath) {
        # Import already checked exact credentials and PKI against the shared state.
        Write-Host 'Existing native configuration agrees with Docker state.'
    } else {
        if (Test-Path -LiteralPath (Join-Path $qagLocal 'pki')) { throw 'Native PKI exists without dev.env; restore the matching configuration.' }
        [IO.File]::Copy($qagExportEnv, $qagEnvPath, $false)
        Copy-Item -LiteralPath (Join-Path $qagExport 'pki') -Destination $qagLocal
        & icacls.exe $qagEnvPath /inheritance:r /grant:r "$([Security.Principal.WindowsIdentity]::GetCurrent().Name):F" | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'Cannot protect native credentials.' }
    }
} finally { Pop-Location }
Write-Host 'Shared Docker/native configuration prepared; original credentials retained.'
