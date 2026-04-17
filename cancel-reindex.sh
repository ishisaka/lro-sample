#!/usr/bin/env bash
#
# cancel-reindex.sh - AIP-151 LRO サンプルサーバーに再インデックスタスクを登録し、
#                     指定秒数経過後にキャンセルする
#
# Usage:
#   ./cancel-reindex.sh
#   ./cancel-reindex.sh --cancel-after 3

set -euo pipefail

# --- デフォルト値 ------------------------------------------------------------
BASE_URL="${BASE_URL:-http://localhost:8080}"
PUBLISHER="lacroix"
CANCEL_AFTER_SEC=2

# --- 引数パース --------------------------------------------------------------
while [[ $# -gt 0 ]]; do
    case "$1" in
        --base-url)      BASE_URL="$2"; shift 2 ;;
        --publisher)     PUBLISHER="$2"; shift 2 ;;
        --cancel-after)  CANCEL_AFTER_SEC="$2"; shift 2 ;;
        -h|--help)
            grep '^#' "$0" | sed 's/^# \{0,1\}//' | head -20
            exit 0
            ;;
        *) echo "Unknown option: $1" >&2; exit 1 ;;
    esac
done

# --- 色定義 ------------------------------------------------------------------
CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RED='\033[0;31m'
RESET='\033[0m'

# --- 事前チェック ------------------------------------------------------------
command -v jq   >/dev/null 2>&1 || { echo "Error: jq is required"   >&2; exit 1; }
command -v curl >/dev/null 2>&1 || { echo "Error: curl is required" >&2; exit 1; }

# --- 1. タスク登録 -----------------------------------------------------------
START_URI="${BASE_URL}/v1/publishers/${PUBLISHER}/books:reindex"
echo -e "${CYAN}POST ${START_URI}${RESET}"

response=$(curl -fsS -X POST "$START_URI")
op_name=$(echo "$response" | jq -r '.name')

echo -e "${GREEN}Operation started: ${op_name}${RESET}"
echo -e "${YELLOW}Waiting ${CANCEL_AFTER_SEC} seconds before cancelling...${RESET}"

sleep "$CANCEL_AFTER_SEC"

# --- 2. キャンセルリクエスト送信 ---------------------------------------------
CANCEL_URI="${BASE_URL}/v1/${op_name}:cancel"
echo -e "${CYAN}POST ${CANCEL_URI}${RESET}"

curl -fsS -X POST "$CANCEL_URI" >/dev/null
echo -e "${YELLOW}Cancel request sent${RESET}"

# --- 3. 最終状態を確認 -------------------------------------------------------
# サーバー側がキャンセルを受けて状態を更新するのをわずかに待つ
sleep 0.5

GET_URI="${BASE_URL}/v1/${op_name}"
response=$(curl -fsS "$GET_URI")

echo -e "${CYAN}Final state:${RESET}"
echo "$response" | jq .

# --- 4. 結果判定 -------------------------------------------------------------
done_flag=$(echo "$response" | jq -r '.done')
error=$(echo    "$response" | jq -r '.error // empty')

if [[ "$done_flag" == "true" && -n "$error" ]]; then
    err_code=$(echo "$response" | jq -r '.error.code')
    err_msg=$(echo  "$response" | jq -r '.error.message')
    echo -e "${GREEN}Operation was cancelled (code=${err_code}: ${err_msg})${RESET}"
elif [[ "$done_flag" == "true" ]]; then
    echo -e "${YELLOW}Operation had already completed before cancellation took effect${RESET}"
else
    echo -e "${RED}Operation still running - cancellation not yet reflected${RESET}" >&2
    exit 1
fi
