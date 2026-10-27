param([Parameter(Mandatory=$true)][string]$Directory)
$ErrorActionPreference = 'Stop'
$qagRoot = Split-Path -Parent $PSScriptRoot
$qagDocker = & "$PSScriptRoot/docker-path.ps1"
$qagDestination = [IO.Path]::GetFullPath($Directory)
if (Test-Path -LiteralPath $qagDestination) { throw 'Export destination exists; overwriting is forbidden.' }
$qagContainer = 'qag-export-' + [guid]::NewGuid().ToString('N')
Push-Location $qagRoot
try {
    # Use a unique run container: the closed export survives until docker cp.
    & $qagDocker compose --profile tools run --no-deps --name $qagContainer export-config
    if ($LASTEXITCODE -ne 0) { throw 'Configuration export failed.' }
    New-Item -ItemType Directory -Path $qagDestination | Out-Null
    $qagIdentity = [Security.Principal.WindowsIdentity]::GetCurrent().Name
    & icacls.exe $qagDestination /inheritance:r /grant:r "${qagIdentity}:(OI)(CI)F" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Cannot protect private export directory.' }
    & $qagDocker cp "${qagContainer}:/export/." $qagDestination
    if ($LASTEXITCODE -ne 0) { throw 'Cannot copy private export.' }
} finally {
    & $qagDocker rm $qagContainer 2>$null | Out-Null
    Pop-Location
}
Write-Host "Private configuration exported: $qagDestination"
