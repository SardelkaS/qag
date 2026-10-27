param([switch]$IncludeTests)
$ErrorActionPreference = 'Stop'
$qagRoot = Split-Path -Parent $PSScriptRoot
$qagEnvPath = Join-Path $qagRoot '.local/dev.env'
if (-not (Test-Path -LiteralPath $qagEnvPath)) { throw 'Сначала выполните scripts/init-local.ps1' }
$qagDockerCandidates = @(
    (Get-Command docker -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Source)
    (Join-Path $env:LOCALAPPDATA 'Programs/DockerDesktop/resources/bin/docker.exe')
    'C:\Program Files\Docker\Docker\resources\bin\docker.exe'
) | Where-Object { $_ -and (Test-Path -LiteralPath $_) } | Select-Object -Unique
$qagDockerPath = $qagDockerCandidates | Select-Object -First 1
if (-not $qagDockerPath) { throw 'Docker Desktop не установлен или недоступен. Установите его и запустите Docker Engine.' }
Push-Location $qagRoot
try {
    & $qagDockerPath info --format '{{.ServerVersion}}'
    if ($LASTEXITCODE -ne 0) { throw 'Docker Engine не запущен' }
    New-Item -ItemType Directory -Path (Join-Path $qagRoot '.tools') -Force | Out-Null
    $qagTestContext = Join-Path $qagRoot '.tools/qag-test-context.exe'
    & go build -o $qagTestContext ./cmd/test-setup
    if ($LASTEXITCODE -ne 0) { throw 'Не удалось собрать подготовку тестового контекста' }
    & $qagTestContext -pki-only
    if ($LASTEXITCODE -ne 0) { throw 'Не удалось подготовить локальную PKI' }
    & $qagDockerPath compose --env-file $qagEnvPath config --quiet
    if ($LASTEXITCODE -ne 0) { throw 'Некорректная конфигурация Compose' }
    & $qagDockerPath compose --env-file $qagEnvPath up -d --wait postgres
    if ($LASTEXITCODE -ne 0) { throw 'Не удалось запустить PostgreSQL через Compose' }
    & "$PSScriptRoot/init-local.ps1"
    foreach ($qagLine in [IO.File]::ReadAllLines($qagEnvPath)) {
        $qagParts=$qagLine.Split('=',2)
        if($qagParts.Length -eq 2 -and $qagParts[0] -in @('QAG_DATABASE_URL','QAG_DEMO_DATABASE_URL')) { Set-Item -LiteralPath "Env:$($qagParts[0])" -Value $qagParts[1] }
    }
    New-Item -ItemType Directory -Path (Join-Path $qagRoot '.tools') -Force | Out-Null
    & $qagTestContext
    if ($LASTEXITCODE -ne 0) { throw 'Не удалось подготовить тестовую mTLS-группу' }
    $qagProvision = Join-Path $qagRoot '.tools/qag-demo-provision.exe'
    & go build -o $qagProvision ./cmd/demo-setup
    if ($LASTEXITCODE -ne 0) { throw 'Не удалось собрать подготовку demo-роли' }
    & $qagProvision
    if ($LASTEXITCODE -ne 0) { throw 'Не удалось подготовить демонстрационную роль' }
} finally { Pop-Location }
