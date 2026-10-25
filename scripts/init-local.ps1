$ErrorActionPreference = 'Stop'
$qagRoot = Split-Path -Parent $PSScriptRoot
$qagLocal = Join-Path $qagRoot '.local'
$qagEnvPath = Join-Path $qagLocal 'dev.env'
if (Test-Path -LiteralPath $qagEnvPath) { Write-Output 'Локальная конфигурация уже существует: .local/dev.env'; exit 0 }
New-Item -ItemType Directory -Path $qagLocal -Force | Out-Null
$qagGenerator = [System.Security.Cryptography.RandomNumberGenerator]::Create()
try {
    $qagBytes = New-Object byte[] 32
    $qagGenerator.GetBytes($qagBytes)
    $qagPassword = [BitConverter]::ToString($qagBytes).Replace('-', '').ToLowerInvariant()
    $qagGenerator.GetBytes($qagBytes)
    $qagTestPassword = [BitConverter]::ToString($qagBytes).Replace('-', '').ToLowerInvariant()
    $qagGenerator.GetBytes($qagBytes)
    $qagKey = [Convert]::ToBase64String($qagBytes)
    $qagLines = @(
        "QAG_POSTGRES_PASSWORD=$qagPassword"
        "QAG_TEST_POSTGRES_PASSWORD=$qagTestPassword"
        "QAG_SECRET_KEY=$qagKey"
        "QAG_DATABASE_URL=postgresql://qag:${qagPassword}@127.0.0.1:15432/qag?sslmode=disable"
        "QAG_TEST_DATABASE_URL=postgresql://qag_test:${qagTestPassword}@127.0.0.1:15433/qag_test?sslmode=disable"
    )
    [IO.File]::WriteAllLines($qagEnvPath, $qagLines, [Text.UTF8Encoding]::new($false))
} finally { $qagGenerator.Dispose() }
Write-Output 'Создана .local/dev.env с отдельными случайными паролями и ключом. Файл исключён из Git.'
