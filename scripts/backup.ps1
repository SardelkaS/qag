param([string]$Directory)
$ErrorActionPreference = 'Stop'
$qagRoot = Split-Path -Parent $PSScriptRoot
if (-not $Directory) { $Directory = Join-Path $qagRoot ('.local/backups/' + (Get-Date -Format 'yyyyMMdd-HHmmss')) }
$qagBackupDir = [IO.Path]::GetFullPath($Directory)
if (Test-Path -LiteralPath $qagBackupDir) { throw 'Backup destination exists; choose a new directory.' }
$qagDocker = & "$PSScriptRoot/docker-path.ps1"
# Init/import reconciles the actual Docker source, never replaces its key.
& $qagDocker volume inspect qag_runtime-config --format '{{.Name}}' *> $null
if ($LASTEXITCODE -ne 0) { & "$PSScriptRoot/init-local.ps1" }
& "$PSScriptRoot/export-config.ps1" -Directory $qagBackupDir
$qagDumpPath = Join-Path $qagBackupDir 'qag.dump'
$qagRemote = '/tmp/qag-backup-' + [guid]::NewGuid().ToString('N') + '.dump'
try {
    & $qagDocker exec qag-postgres-1 pg_dump -U qag -d qag --schema=qag --schema=qag_demo --format=custom --no-owner --no-acl -f $qagRemote
    if ($LASTEXITCODE -ne 0) { throw 'pg_dump failed.' }
    & $qagDocker cp "qag-postgres-1:$qagRemote" $qagDumpPath
    if ($LASTEXITCODE -ne 0) { throw 'Backup copy failed.' }
    $qagConfig = [IO.File]::ReadAllText((Join-Path $qagBackupDir 'installation.json')) | ConvertFrom-Json
    [IO.File]::WriteAllText((Join-Path $qagBackupDir 'qag-secret-key.txt'), $qagConfig.values.QAG_SECRET_KEY, [Text.UTF8Encoding]::new($false))
} finally { & $qagDocker exec qag-postgres-1 rm -f -- $qagRemote | Out-Null }
Write-Host "Backup saved: $qagBackupDir (dump + original configuration/PKI)."
