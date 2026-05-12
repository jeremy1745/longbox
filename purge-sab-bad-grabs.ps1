# purge-sab-bad-grabs.ps1
#
# Reads the SAB API key + URL from longbox.db, lists SAB history, identifies
# non-comic releases (XXX / TV episodes / video tags), and deletes them
# from SAB — along with the downloaded files on disk (del_files=1).
#
# Usage:
#   .\purge-sab-bad-grabs.ps1 -DryRun     # report only
#   .\purge-sab-bad-grabs.ps1             # actually delete from SAB
#
# Reject criteria mirror the Go-side isComicRelease filter (whole-word tokens):
#   TV episode markers (SxxExx / NxX)
#   Video resolution + container tags (720p/1080p/2160p, WEB-DL, BluRay, HDTV, BDRip)
#   Adult-content keywords (XXX, Porn, Hentai, Brazzers, ManyVids, OnlyFans, …)
# Plain "Webrip" / "Digital" are intentionally NOT rejected — both are
# legitimate scene tags on comic releases.

param(
    [switch]$DryRun,
    [string]$DbPath = "$PSScriptRoot\longbox.db"
)

if (-not (Test-Path $DbPath)) {
    Write-Error "longbox.db not found at $DbPath"
    exit 1
}

# Locate sqlite3.exe — try PATH, then a few common spots
$sqlite = (Get-Command sqlite3.exe -ErrorAction SilentlyContinue).Source
if (-not $sqlite) {
    foreach ($candidate in @(
        "C:\Program Files\sqlite\sqlite3.exe",
        "C:\sqlite\sqlite3.exe",
        "$PSScriptRoot\sqlite3.exe"
    )) {
        if (Test-Path $candidate) { $sqlite = $candidate; break }
    }
}
if (-not $sqlite) {
    Write-Error "sqlite3.exe not found in PATH or common locations. Install from https://www.sqlite.org/download.html or place sqlite3.exe next to this script."
    exit 1
}

# Read the first enabled SAB download client
$row = & $sqlite -readonly $DbPath "SELECT url, api_key FROM download_clients WHERE type='sabnzbd' AND enabled=1 ORDER BY id LIMIT 1;"
if (-not $row) {
    Write-Error "no enabled sabnzbd download_clients row in $DbPath"
    exit 1
}
$parts = $row -split '\|'
if ($parts.Length -ne 2) {
    Write-Error "unexpected download_clients row shape: $row"
    exit 1
}
$sabUrl = $parts[0].Trim()
$sabKey = $parts[1].Trim()

# Normalize URL
if ($sabUrl -notmatch '^https?://') { $sabUrl = "http://$sabUrl" }
$sabUrl = $sabUrl.TrimEnd('/')

Write-Host "SAB endpoint: $sabUrl"
Write-Host "Mode        : $(if ($DryRun) { 'DRY RUN' } else { 'LIVE — will delete from SAB' })"
Write-Host ("=" * 60)

# Pull history (last 500)
$historyURL = "$sabUrl/api?mode=history&output=json&apikey=$sabKey&limit=500"
try {
    $resp = Invoke-RestMethod -Uri $historyURL -TimeoutSec 15
}
catch {
    Write-Error "failed to query SAB history: $($_.Exception.Message)"
    exit 1
}

$slots = $resp.history.slots
if (-not $slots) {
    Write-Host "no history entries returned"
    exit 0
}

$reject = '(?i)(?:^|[\W_])(s\d{2}e\d{2}|s\d{4}e\d{2}|\d{1,2}x\d{2}|480p|720p|1080p|2160p|web-dl|bluray|bdrip|brrip|hdtv|hdrip|xxx|porn|hentai|brazzers|manyvids|onlyfans|loveherboobs|latinacasting|ultimatesurrender|enjoyx)(?:[\W_]|$)'

$bad = $slots | Where-Object { $_.name -match $reject }
Write-Host "history entries: $($slots.Count)"
Write-Host "non-comic hits : $($bad.Count)"
Write-Host ("=" * 60)

$ok = 0
$failed = 0
foreach ($s in $bad) {
    $line = "  $($s.name)  [nzo:$($s.nzo_id), $($s.size)]"
    if ($DryRun) {
        Write-Host "DRY: $line"
        $ok++
        continue
    }
    try {
        $deleteURL = "$sabUrl/api?mode=history&name=delete&value=$($s.nzo_id)&del_files=1&apikey=$sabKey"
        $r = Invoke-RestMethod -Uri $deleteURL -TimeoutSec 15
        if ($r.status -eq $true) {
            Write-Host "OK:  $line"
            $ok++
        } else {
            Write-Host "FAIL: $line  -  $($r | ConvertTo-Json -Compress)" -ForegroundColor Red
            $failed++
        }
    } catch {
        Write-Host "FAIL: $line  -  $($_.Exception.Message)" -ForegroundColor Red
        $failed++
    }
}

Write-Host ("=" * 60)
Write-Host "done — ok: $ok, failed: $failed"
