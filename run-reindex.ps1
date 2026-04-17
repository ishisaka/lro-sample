<#
.SYNOPSIS
  AIP-151 LRO サンプルサーバーに再インデックスタスクを登録し、完了まで待機します。

.EXAMPLE
  ./run-reindex.ps1
  ./run-reindex.ps1 -Publisher "oreilly" -PollIntervalSec 2
#>
[CmdletBinding()]
param(
    [string]$BaseUrl = "http://localhost:8080",
    [string]$Publisher = "lacroix",
    [int]   $PollIntervalSec = 1,
    [int]   $TimeoutSec = 60
)

$ErrorActionPreference = 'Stop'

# --- 1. タスク登録 -----------------------------------------------------------
$startUri = "$BaseUrl/v1/publishers/$Publisher/books:reindex"
Write-Host "POST $startUri" -ForegroundColor Cyan

$op = Invoke-RestMethod -Method Post -Uri $startUri
Write-Host "Operation started: $($op.name)" -ForegroundColor Green

# --- 2. 完了までポーリング ---------------------------------------------------
$getUri = "$BaseUrl/v1/$($op.name)"
$deadline = (Get-Date).AddSeconds($TimeoutSec)

while (-not $op.done) {
    if ((Get-Date) -gt $deadline) {
        throw "Timeout after $TimeoutSec seconds waiting for $($op.name)"
    }

    Start-Sleep -Seconds $PollIntervalSec
    $op = Invoke-RestMethod -Method Get -Uri $getUri

    $progress = [int]$op.metadata.progress
    Write-Progress -Activity "Reindexing books" -Status "$progress%" -PercentComplete $progress
    Write-Host ("  progress: {0,3}%  updated: {1}" -f $progress, $op.metadata.updateTime)
}

Write-Progress -Activity "Reindexing books" -Completed

# --- 3. 結果判定 -------------------------------------------------------------
if ($op.error) {
    Write-Host "Operation FAILED (code=$($op.error.code)): $($op.error.message)" -ForegroundColor Red
    exit 1
}

Write-Host "Operation SUCCEEDED" -ForegroundColor Green
Write-Host "Response:" -ForegroundColor Cyan
$op.response | ConvertTo-Json -Depth 5
