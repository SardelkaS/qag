$qagDockerCandidates = @(
    (Get-Command docker -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Source)
    (Join-Path $env:LOCALAPPDATA 'Programs/DockerDesktop/resources/bin/docker.exe')
    'C:\Program Files\Docker\Docker\resources\bin\docker.exe'
) | Where-Object { $_ -and (Test-Path -LiteralPath $_) } | Select-Object -Unique
$qagDockerPath = $qagDockerCandidates | Select-Object -First 1
if (-not $qagDockerPath) { throw 'Docker Desktop не найден' }
$qagDockerPath
