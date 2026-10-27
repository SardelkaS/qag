param(
    [Parameter(Mandatory=$true)][string]$PayloadPath,
    [Parameter(Mandatory=$true)][int]$ApplicationPid,
    [Parameter(Mandatory=$true)][string]$BinaryPath,
    [Parameter(Mandatory=$true)][string]$ReadyPath,
    [Parameter(Mandatory=$true)][string]$DonePath
)
$ErrorActionPreference='Stop'
[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)
Add-Type -AssemblyName System.Windows.Forms
Add-Type -Path (Join-Path $PSScriptRoot 'WordPasteCheck.cs') -ReferencedAssemblies 'Microsoft.CSharp','System.Core'
$qagRoot=[IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$qagPrivate=[IO.Path]::GetFullPath((Join-Path $qagRoot '.local'))+[IO.Path]::DirectorySeparatorChar
foreach($qagPath in @($PayloadPath,$ReadyPath,$DonePath)) {
    if(-not ([IO.Path]::GetFullPath($qagPath)).StartsWith($qagPrivate,[StringComparison]::OrdinalIgnoreCase)){throw 'Тестовые файлы должны находиться в .local'}
}
$qagProcess=Get-Process -Id $ApplicationPid
$qagExpected=[IO.Path]::GetFullPath($BinaryPath)
if(-not $qagExpected.StartsWith($qagPrivate,[StringComparison]::OrdinalIgnoreCase) -or -not [string]::Equals($qagProcess.Path,$qagExpected,[StringComparison]::OrdinalIgnoreCase)){throw 'Разрешено остановить только переданный проверяемый бинарник в .local'}
$qagInput=Get-Content -LiteralPath $PayloadPath -Raw -Encoding UTF8 | ConvertFrom-Json
$qagEndpoint=[Uri]$qagInput.url
if($qagEndpoint.Host -ne '127.0.0.1' -or $qagEndpoint.Port -ne 18080 -or $qagEndpoint.Scheme -ne 'http' -or $qagEndpoint.AbsolutePath -notmatch '^/api/v1/runs/[0-9a-f-]+/report/clipboard$'){throw 'Разрешён только локальный E2E endpoint отчёта'}

# Materialize the previous clipboard before the app publishes anything. No
# clipboard contents are written to files or logs.
$qagOriginal=[Windows.Forms.Clipboard]::GetDataObject()
$qagBackup=New-Object Windows.Forms.DataObject
$qagFormats=0
if($null -ne $qagOriginal){foreach($qagFormat in $qagOriginal.GetFormats($false)){
    $qagValue=$qagOriginal.GetData($qagFormat,$false)
    if($null -eq $qagValue){throw 'Не удалось полностью сохранить прежний буфер; проверка не начиналась'}
    if($qagValue -is [IO.Stream]){$qagClone=New-Object IO.MemoryStream;$qagValue.Position=0;$qagValue.CopyTo($qagClone);$qagClone.Position=0;$qagValue=$qagClone}
    elseif($qagValue -is [Drawing.Image]){$qagValue=$qagValue.Clone()}
    elseif($qagValue -is [Array]){$qagValue=$qagValue.Clone()}
    $qagBackup.SetData($qagFormat,$false,$qagValue);$qagFormats++
}}
$qagWord=$null
$qagDocuments=New-Object Collections.Generic.List[object]
try {
    $qagBody=$qagInput.body | ConvertTo-Json -Depth 40 -Compress
    $qagResult=Invoke-RestMethod -Uri $qagEndpoint -Method Post -ContentType 'application/json' -Headers @{Origin='http://127.0.0.1:18080';'X-QAG-CSRF'=[string]$qagInput.csrf} -Body ([Text.Encoding]::UTF8.GetBytes($qagBody))
    if(-not $qagResult.copied){throw 'Native clipboard не подтвердил копирование'}
    $qagClip=[Windows.Forms.Clipboard]::GetDataObject()
    foreach($qagFormat in @('UnicodeText','Rich Text Format','HTML Format')){if(-not $qagClip.GetDataPresent($qagFormat,$false)){throw 'В буфере отсутствует обязательное представление'}}
    [IO.File]::WriteAllText($ReadyPath,'ready')
    $qagDeadline=[DateTime]::UtcNow.AddSeconds(45)
    while(-not (Test-Path -LiteralPath $DonePath)){if([DateTime]::UtcNow -gt $qagDeadline){throw 'Браузер не подтвердил вставку'};Start-Sleep -Milliseconds 100}
    $qagDocPath=Join-Path $qagPrivate ('word-paste-'+[guid]::NewGuid().ToString('N')+'.docx')
    $qagImageCount=[QagWordPasteCheck]::Verify([string]$qagInput.expectedText,$qagDocPath,$qagInput.body.parts.Count,$ApplicationPid,$qagExpected)
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $qagZip=[IO.Compression.ZipFile]::OpenRead($qagDocPath)
    try {
        if(@($qagZip.Entries | Where-Object {$_.FullName.StartsWith('word/media/')}).Count -lt 1){throw 'DOCX не содержит встроенных изображений'}
        foreach($qagEntry in $qagZip.Entries | Where-Object {$_.FullName.EndsWith('.rels')}){
            $qagReader=New-Object IO.StreamReader($qagEntry.Open())
            try{$qagRel=$qagReader.ReadToEnd()}finally{$qagReader.Dispose()}
            if($qagRel.Contains('TargetMode="External"')){throw 'Документ содержит внешние ссылки вместо самостоятельных данных'}
        }
    } finally {$qagZip.Dispose()}
    Write-Output ('Word: русский текст редактируется, встроенных изображений — '+$qagImageCount+'; вставка без qag прошла.')
} catch {
    [Console]::Error.WriteLine('Word verification: '+$_.Exception.Message+' '+$_.ScriptStackTrace)
    throw
} finally {
    foreach($qagDoc in $qagDocuments){try{$qagDoc.Close(0)}catch{};[void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($qagDoc)}
    if($null -ne $qagWord){$qagWord.Quit();[void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($qagWord)}
    if($qagFormats -eq 0){[Windows.Forms.Clipboard]::Clear()}else{[Windows.Forms.Clipboard]::SetDataObject($qagBackup,$true)}
}
