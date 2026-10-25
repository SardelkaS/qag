$ErrorActionPreference = 'Stop'
$qagRoot = Split-Path -Parent $PSScriptRoot
$qagLocal = Join-Path $qagRoot '.local'
$qagEnvPath = Join-Path $qagLocal 'dev.env'
if (-not (Test-Path -LiteralPath $qagEnvPath)) {
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
}

$qagExistingLines=[IO.File]::ReadAllLines($qagEnvPath)
if (-not ($qagExistingLines | Where-Object { $_.StartsWith('QAG_DEMO_DATABASE_URL=') })) {
    $qagMainLine=$qagExistingLines | Where-Object { $_.StartsWith('QAG_DATABASE_URL=') } | Select-Object -First 1
    if (-not $qagMainLine) { throw 'Нет QAG_DATABASE_URL для подготовки demo-подключения' }
    $qagDemoUri=New-Object UriBuilder($qagMainLine.Substring('QAG_DATABASE_URL='.Length))
    $qagDemoUri.UserName='qag_demo_'+[guid]::NewGuid().ToString('N')
    $qagDemoBytes=New-Object byte[] 32
    $qagDemoRandom=[Security.Cryptography.RandomNumberGenerator]::Create()
    try { $qagDemoRandom.GetBytes($qagDemoBytes) } finally { $qagDemoRandom.Dispose() }
    $qagDemoUri.Password=[BitConverter]::ToString($qagDemoBytes).Replace('-','').ToLowerInvariant()
    [IO.File]::AppendAllText($qagEnvPath,[Environment]::NewLine+'QAG_DEMO_DATABASE_URL='+$qagDemoUri.Uri.AbsoluteUri+[Environment]::NewLine,(New-Object Text.UTF8Encoding($false)))
}
Write-Output 'Локальная конфигурация подготовлена; существующие реквизиты сохранены.'