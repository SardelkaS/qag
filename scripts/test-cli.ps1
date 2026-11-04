param([switch]$SkipInstall,[string]$BinaryPath='.local/verify/qag-yaml.exe')
$ErrorActionPreference='Stop'
$qagRoot=Split-Path -Parent $PSScriptRoot
Push-Location $qagRoot
$qagNames=@('GOPROXY','GOSUMDB','GOMODCACHE','GOCACHE','GOBIN','GOPRIVATE','GONOPROXY','QAG_CLI_BINARY')
$qagPrevious=@{}
foreach($qagName in $qagNames){$qagPrevious[$qagName]=[Environment]::GetEnvironmentVariable($qagName,'Process')}
try {
    if(-not $SkipInstall){
        # Windows PowerShell otherwise treats informational native stderr
        # ("go: downloading ...") as a terminating error when redirected.
        $qagErrorPreference=$ErrorActionPreference
        try { $ErrorActionPreference='Continue'; & go mod download all }
        finally { $ErrorActionPreference=$qagErrorPreference }
        if($LASTEXITCODE -ne 0){throw 'Dependency cache preparation failed'}
        $qagInstallRoot=Join-Path $qagRoot ('.local/verify/install-'+[Guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Path $qagInstallRoot | Out-Null
        $qagMetadata=& go run ./cmd/module-fixture -output (Join-Path $qagInstallRoot 'proxy')
        if($LASTEXITCODE -ne 0){throw 'Module proxy preparation failed'}
        $qagInfo=$qagMetadata | ConvertFrom-Json
        $env:GOPROXY=$qagInfo.proxy
        $env:GOSUMDB='off'
        $env:GOPRIVATE=''
        $env:GONOPROXY=''
        $env:GOMODCACHE=Join-Path $qagInstallRoot 'modules'
        $env:GOCACHE=Join-Path $qagInstallRoot 'cache'
        $env:GOBIN=Join-Path $qagInstallRoot 'bin'
        $qagWorking=Join-Path $qagInstallRoot 'work'
        New-Item -ItemType Directory -Path $qagWorking | Out-Null
        Push-Location $qagWorking
        try {
            try { $ErrorActionPreference='Continue'; & go install 'github.com/SardelkaS/qag/cmd/qag@latest' }
            finally { $ErrorActionPreference=$qagErrorPreference }
            if($LASTEXITCODE -ne 0){throw 'Isolated go install failed'}
        } finally {Pop-Location}
        $BinaryPath=Join-Path $env:GOBIN 'qag.exe'
        foreach($qagName in $qagNames){[Environment]::SetEnvironmentVariable($qagName,$qagPrevious[$qagName],'Process')}
    }
    if(-not [IO.Path]::IsPathRooted($BinaryPath)){$BinaryPath=Join-Path $qagRoot $BinaryPath}
    $env:QAG_CLI_BINARY=$BinaryPath
    & go test -tags=cli -count=1 -run 'TestInstalledCLI' -v ./internal/deployment
    if($LASTEXITCODE -ne 0){throw 'Installed CLI smoke failed'}
} finally {
    foreach($qagName in $qagNames){[Environment]::SetEnvironmentVariable($qagName,$qagPrevious[$qagName],'Process')}
    Pop-Location
}
