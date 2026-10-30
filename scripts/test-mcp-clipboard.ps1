param([Parameter(Mandatory=$true)][string]$RunId,[Parameter(Mandatory=$true)][int]$ApplicationPid,[Parameter(Mandatory=$true)][string]$BinaryPath)
$ErrorActionPreference='Stop'
[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)
Add-Type -AssemblyName System.Windows.Forms
Add-Type -Path (Join-Path $PSScriptRoot 'WordPasteCheck.cs') -ReferencedAssemblies 'Microsoft.CSharp','System.Core'
$qagRoot=[IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$qagPrivate=[IO.Path]::GetFullPath((Join-Path $qagRoot '.local'))+[IO.Path]::DirectorySeparatorChar
if(-not ([IO.Path]::GetFullPath($BinaryPath)).StartsWith($qagPrivate,[StringComparison]::OrdinalIgnoreCase) -or (Get-Process -Id $ApplicationPid).Path -ne [IO.Path]::GetFullPath($BinaryPath)) {throw 'Only an explicit .local fixture executable is allowed'}
if($RunId -notmatch '^[0-9a-f-]{36}$') {throw 'Invalid fixture run ID'}
function Invoke-QagMCP([string]$Name,$Arguments) {
    $qagBody=@{jsonrpc='2.0';id=1;method='tools/call';params=@{name=$Name;arguments=$Arguments}} | ConvertTo-Json -Depth 30 -Compress
    $qagHttp=Invoke-WebRequest -UseBasicParsing -Uri 'http://127.0.0.1:18088/mcp' -Method Post -ContentType 'application/json' -Headers @{Accept='application/json, text/event-stream';'MCP-Protocol-Version'='2025-11-25'} -Body ([Text.Encoding]::UTF8.GetBytes($qagBody))
    $qagResponse=[Text.Encoding]::UTF8.GetString($qagHttp.RawContentStream.ToArray()) | ConvertFrom-Json
    if($qagResponse.error -or $qagResponse.result.isError -or $qagResponse.result.structuredContent.error) {throw 'MCP clipboard tool failed'}
    return $qagResponse.result.structuredContent.data
}
# Materialize all existing formats before publishing any test value.
$qagOriginal=[Windows.Forms.Clipboard]::GetDataObject()
$qagBackup=New-Object Windows.Forms.DataObject
$qagFormats=0
if($null -ne $qagOriginal){foreach($qagFormat in $qagOriginal.GetFormats($false)){
    $qagValue=$qagOriginal.GetData($qagFormat,$false)
    if($null -eq $qagValue){throw 'Cannot preserve the original clipboard'}
    if($qagValue -is [IO.Stream]){$qagClone=New-Object IO.MemoryStream;$qagValue.Position=0;$qagValue.CopyTo($qagClone);$qagClone.Position=0;$qagValue=$qagClone}
    elseif($qagValue -is [Drawing.Image]){$qagValue=$qagValue.Clone()}
    elseif($qagValue -is [Array]){$qagValue=$qagValue.Clone()}
    $qagBackup.SetData($qagFormat,$false,$qagValue);$qagFormats++
}}
try {
    $qagReport=Invoke-QagMCP 'qag_report_prepare' @{runId=$RunId;includeExpected=$true}
    $qagArgs=@{runId=$RunId;snapshotId=$qagReport.snapshotId;hash=$qagReport.hash;includeExpected=$true;format='text'}
    foreach($qagMode in @('text','markdown','rich')) {
        $qagArgs.format=$qagMode
        $qagResult=Invoke-QagMCP 'qag_report_copy' $qagArgs
        if(-not $qagResult.copied){throw 'Native MCP clipboard did not confirm copying'}
        $qagClip=[Windows.Forms.Clipboard]::GetDataObject()
        if(-not $qagClip.GetDataPresent('UnicodeText',$false)){throw 'Unicode text missing'}
        if(-not ([string]$qagClip.GetData('UnicodeText',$false)).Contains([string]$qagReport.title)){throw 'Clipboard title differs from UTF-8 MCP response'}
        if($qagMode -eq 'rich') {
            foreach($qagFormat in @('HTML Format','Rich Text Format')){if(-not $qagClip.GetDataPresent($qagFormat,$false)){throw 'Rich representation missing'}}
            if(-not ([string]$qagClip.GetData('HTML Format',$false)).Contains('data:image/png;base64,')){throw 'No inline PNG in MCP HTML'}
        } elseif($qagClip.GetDataPresent('HTML Format',$false)){throw 'Text mode unexpectedly publishes rich formats'}
    }
    $qagDocPath=Join-Path $qagPrivate ('mcp-word-'+[guid]::NewGuid().ToString('N')+'.docx')
    $qagImages=[QagWordPasteCheck]::Verify([string]$qagReport.title,$qagDocPath,1,$ApplicationPid,[IO.Path]::GetFullPath($BinaryPath))
    if($qagImages -lt 1){throw 'Word lost MCP raster calls'}
    Write-Output ('MCP clipboard: Unicode/Markdown/HTML/RTF; Word images '+$qagImages+'; original clipboard restored.')
} finally {
    if($qagFormats -eq 0){[Windows.Forms.Clipboard]::Clear()}else{[Windows.Forms.Clipboard]::SetDataObject($qagBackup,$true)}
}
