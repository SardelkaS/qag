param([string]$Directory)
$ErrorActionPreference = 'Stop'
$qagRoot = Split-Path -Parent $PSScriptRoot
if (-not $Directory) { $Directory = Join-Path $qagRoot ('.local/backups/' + (Get-Date -Format 'yyyyMMdd-HHmmss')) }
$qagBackupDir = [IO.Path]::GetFullPath($Directory)
$qagDumpPath = Join-Path $qagBackupDir 'qag.dump'
$qagKeyPath = Join-Path $qagBackupDir 'qag-secret-key.txt'
if ((Test-Path -LiteralPath $qagDumpPath) -or (Test-Path -LiteralPath $qagKeyPath)) { throw 'Резервная копия уже существует; выберите другую папку' }
$qagKeyLine = [IO.File]::ReadAllLines((Join-Path $qagRoot '.local/dev.env')) | Where-Object { $_.StartsWith('QAG_SECRET_KEY=') } | Select-Object -First 1
if (-not $qagKeyLine) { throw 'Локальный ключ не найден' }
$qagDocker = & "$PSScriptRoot/docker-path.ps1"
New-Item -ItemType Directory -Path $qagBackupDir -Force | Out-Null
$qagRemote = '/tmp/qag-backup-' + [guid]::NewGuid().ToString('N') + '.dump'
try {
    & $qagDocker exec qag-postgres-1 pg_dump -U qag -d qag --format=custom --no-owner --no-acl -f $qagRemote
    if ($LASTEXITCODE -ne 0) { throw 'pg_dump failed' }
    & $qagDocker cp "qag-postgres-1:$qagRemote" $qagDumpPath
    if ($LASTEXITCODE -ne 0) { throw 'Backup copy failed' }
    [IO.File]::WriteAllText($qagKeyPath, $qagKeyLine.Substring('QAG_SECRET_KEY='.Length), (New-Object Text.UTF8Encoding($false)))
} finally { & $qagDocker exec qag-postgres-1 rm -f -- $qagRemote | Out-Null }
Write-Host "Дамп: $qagDumpPath"
Write-Host "Ключ: $qagKeyPath — перенесите его в отдельное защищённое хранилище."
