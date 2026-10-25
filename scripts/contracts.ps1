$ErrorActionPreference = 'Stop'
$qagRoot = Split-Path -Parent $PSScriptRoot
Push-Location $qagRoot
try {
    & go run ./cmd/contract
    if ($LASTEXITCODE -ne 0) { throw 'OpenAPI generation failed' }
    Push-Location (Join-Path $qagRoot 'web')
    try {
        & npx.cmd --no-install redocly lint ../api/openapi.json
        if ($LASTEXITCODE -ne 0) { throw 'OpenAPI validation failed' }
        & npx.cmd --no-install openapi-typescript ../api/openapi.json -o src/generated/api.ts
        if ($LASTEXITCODE -ne 0) { throw 'TypeScript API generation failed' }
    } finally { Pop-Location }
} finally { Pop-Location }
