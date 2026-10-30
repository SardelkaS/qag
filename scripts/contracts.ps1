$ErrorActionPreference = 'Stop'
$qagRoot = Split-Path -Parent $PSScriptRoot
Push-Location $qagRoot
try {
    & go run ./cmd/contract
    if ($LASTEXITCODE -ne 0) { throw 'OpenAPI generation failed' }
    $qagMcpHash=(Get-FileHash -LiteralPath api/mcp-tools.json -Algorithm SHA256).Hash
    $qagOpenApiHash=(Get-FileHash -LiteralPath api/openapi.json -Algorithm SHA256).Hash
    & go run ./cmd/contract
    if ($LASTEXITCODE -ne 0 -or (Get-FileHash -LiteralPath api/mcp-tools.json -Algorithm SHA256).Hash -ne $qagMcpHash -or (Get-FileHash -LiteralPath api/openapi.json -Algorithm SHA256).Hash -ne $qagOpenApiHash) {throw 'Contract generation is not deterministic'}
    & node scripts/validate-mcp.mjs
    if ($LASTEXITCODE -ne 0) {throw 'MCP contract validation failed'}
    Push-Location (Join-Path $qagRoot 'web')
    try {
        & npx.cmd --no-install redocly lint ../api/openapi.json
        if ($LASTEXITCODE -ne 0) { throw 'OpenAPI validation failed' }
        & npx.cmd --no-install openapi-typescript ../api/openapi.json -o src/generated/api.ts
        if ($LASTEXITCODE -ne 0) { throw 'TypeScript API generation failed' }
    } finally { Pop-Location }
} finally { Pop-Location }
