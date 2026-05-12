# cleanup-bad-grabs.ps1
#
# Reads cleanup-bad-grabs-list.txt (one folder name per line, relative to E:\Comics)
# and moves each entry to the Windows Recycle Bin (NOT permanent delete — recoverable).
#
# Usage:
#   1. Review cleanup-bad-grabs-list.txt — DELETE any lines for folders you want to KEEP.
#   2. Optional dry run: .\cleanup-bad-grabs.ps1 -DryRun
#   3. Real run:        .\cleanup-bad-grabs.ps1
#
# Safety: every removal goes through Microsoft.VisualBasic.FileIO with
# RecycleOption.SendToRecycleBin. Files end up in the Windows Recycle Bin
# and can be restored from there.

param(
    [switch]$DryRun,
    [string]$LibraryDir = "E:\Comics",
    [string]$ListFile  = "$PSScriptRoot\cleanup-bad-grabs-list.txt"
)

if (-not (Test-Path $ListFile)) {
    Write-Error "List file not found: $ListFile"
    exit 1
}

Add-Type -AssemblyName Microsoft.VisualBasic

$entries = Get-Content $ListFile | Where-Object { $_ -and -not $_.StartsWith('#') }
Write-Host ("=" * 60)
Write-Host "cleanup-bad-grabs.ps1"
Write-Host "  library: $LibraryDir"
Write-Host "  entries: $($entries.Count)"
Write-Host "  mode   : $(if ($DryRun) { 'DRY RUN — no changes' } else { 'LIVE — sending to Recycle Bin' })"
Write-Host ("=" * 60)

$ok = 0
$skipped = 0
$failed = 0

foreach ($name in $entries) {
    $name = $name.Trim()
    if (-not $name) { continue }
    $path = Join-Path $LibraryDir $name

    if (-not (Test-Path -LiteralPath $path)) {
        Write-Host "  SKIP (not found): $name"
        $skipped++
        continue
    }

    if ($DryRun) {
        Write-Host "  DRY:  $name"
        $ok++
        continue
    }

    try {
        [Microsoft.VisualBasic.FileIO.FileSystem]::DeleteDirectory(
            $path,
            [Microsoft.VisualBasic.FileIO.UIOption]::OnlyErrorDialogs,
            [Microsoft.VisualBasic.FileIO.RecycleOption]::SendToRecycleBin
        )
        Write-Host "  OK:   $name"
        $ok++
    }
    catch {
        Write-Host "  FAIL: $name  -  $($_.Exception.Message)" -ForegroundColor Red
        $failed++
    }
}

Write-Host ("=" * 60)
Write-Host "done — ok: $ok, skipped: $skipped, failed: $failed"
if (-not $DryRun) {
    Write-Host "all removed folders are recoverable from the Windows Recycle Bin"
}
