<#
.SYNOPSIS
  AIP-151 LRO サンプルサーバーに再インデックスタスクを登録し、
  指定秒数経過後にキャンセルします。

.EXAMPLE
  ./cancel-reindex.ps1
  ./cancel-reindex.ps1 -CancelAfterSec 3
#>
[CmdletBinding()]
param(
    [string]$BaseUrl = "http://localhost:8080",
    [string]$Publisher = "lacroix",
    [int]   $CancelAfterSec = 2
)

$ErrorActionPreference = 'Stop'

# --- 1. タスク登録 -----------------------------------------------------------
$startUri = "$BaseUrl/v1/publishers/$Publisher/books:reindex"
Write-Host "POST $startUri" -ForegroundColor Cyan

$op = Invoke-RestMethod -Method Post -Uri $startUri
Write-Host "Operation started: $($op.name)" -ForegroundColor Green
Write-Host "Waiting $CancelAfterSec seconds before cancelling..." -ForegroundColor Yellow

Start-Sleep -Seconds $CancelAfterSec

# --- 2. キャンセルリクエスト送信 ---------------------------------------------
$cancelUri = "$BaseUrl/v1/$($op.name):cancel"
Write-Host "POST $cancelUri" -ForegroundColor Cyan

Invoke-RestMethod -Method Post -Uri $cancelUri | Out-Null
Write-Host "Cancel request sent" -ForegroundColor Yellow

# --- 3. 最終状態を確認 -------------------------------------------------------
# サーバー側がキャンセルを受けて状態を更新するのをわずかに待つ
Start-Sleep -Milliseconds 500

$getUri = "$BaseUrl/v1/$($op.name)"
$op = Invoke-RestMethod -Method Get -Uri $getUri

Write-Host "Final state:" -ForegroundColor Cyan
$op | ConvertTo-Json -Depth 5

# --- 4. 結果判定 -------------------------------------------------------------
if ($op.done -and $op.error) {
    Write-Host ("Operation was cancelled (code={0}: {1})" -f $op.error.code, $op.error.message) -ForegroundColor Green
}
elseif ($op.done) {
    Write-Host "Operation had already completed before cancellation took effect" -ForegroundColor Yellow
}
else {
    Write-Host "Operation still running - cancellation not yet reflected" -ForegroundColor Red
    exit 1
}
