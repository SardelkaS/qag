$ErrorActionPreference = 'Stop'
$qagRoot = Split-Path -Parent $PSScriptRoot
$qagTools = Join-Path $qagRoot '.tools'
$qagProtocDir = Join-Path $qagTools 'protoc-33.1'
$qagProtoc = Join-Path $qagProtocDir 'bin/protoc.exe'
New-Item -ItemType Directory -Path $qagTools -Force | Out-Null
if (-not (Test-Path -LiteralPath $qagProtoc)) {
    $qagArchive = Join-Path $qagTools 'protoc-33.1-win64.zip'
    Invoke-WebRequest -UseBasicParsing -Uri 'https://github.com/protocolbuffers/protobuf/releases/download/v33.1/protoc-33.1-win64.zip' -OutFile $qagArchive
    Expand-Archive -LiteralPath $qagArchive -DestinationPath $qagProtocDir -Force
}
$qagOldBin = $env:GOBIN
$env:GOBIN = Join-Path $qagTools 'bin'
New-Item -ItemType Directory -Path $env:GOBIN -Force | Out-Null
Push-Location $qagRoot
try {
    & go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
    if ($LASTEXITCODE -ne 0) { throw 'protobuf generator installation failed' }
    & go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
    if ($LASTEXITCODE -ne 0) { throw 'gRPC generator installation failed' }
    & $qagProtoc "--plugin=protoc-gen-go=$(Join-Path $env:GOBIN 'protoc-gen-go.exe')" "--plugin=protoc-gen-go-grpc=$(Join-Path $env:GOBIN 'protoc-gen-go-grpc.exe')" --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative internal/demo/proto/demo.proto
    if ($LASTEXITCODE -ne 0) { throw 'Protocol generation failed' }
} finally { Pop-Location; $env:GOBIN=$qagOldBin }
