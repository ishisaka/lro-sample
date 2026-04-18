#!/usr/bin/env bash
#
# run-reindex.sh - AIP-151 LRO サンプルサーバーに再インデックスタスクを登録し、
#                  完了まで待機する
#
# Usage:
#   ./run-reindex.sh
#   ./run-reindex.sh --publisher oreilly --poll-interval 2
#   ./run-reindex.sh --base-url http://localhost:8080 --timeout 120

set -euo pipefail

# --- デフォルト値 ------------------------------------------------------------
BASE_URL="${BASE_URL:-http://localhost:8080}"
PUBLISHER="lacroix"
POLL_INTERVAL_SEC=1
TIMEOUT_SEC=60

# --- 引数パース --------------------------------------------------------------
while [[ $# -gt 0 ]]; do
    case "$1" in
        --base-url)       BASE_URL="$2"; shift 2 ;;
        --publisher)      PUBLISHER="$2"; shift 2 ;;
        --poll-interval)  POLL_INTERVAL_SEC="$2"; shift 2 ;;
        --timeout)        TIMEOUT_SEC="$2"; shift 2 ;;
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

# --- 2. 完了までポーリング ---------------------------------------------------
GET_URI="${BASE_URL}/v1/${op_name}"
deadline=$(( $(date +%s) + TIMEOUT_SEC ))

done_flag="false"
while [[ "$done_flag" != "true" ]]; do
    if (( $(date +%s) > deadline )); then
        echo "Timeout after ${TIMEOUT_SEC} seconds waiting for ${op_name}" >&2
        exit 1
    fi

    sleep "$POLL_INTERVAL_SEC"

    response=$(curl -fsS "$GET_URI")
    done_flag=$(echo    "$response" | jq -r '.done')
    progress=$(echo     "$response" | jq -r '.metadata.progress  // 0')
    update_time=$(echo  "$response" | jq -r '.metadata.updateTime // "-"')

    printf "  progress: %3s%%  updated: %s\n" "$progress" "$update_time"
done

# --- 3. 結果判定 -------------------------------------------------------------
error=$(echo "$response" | jq -r '.error // empty')
if [[ -n "$error" ]]; then
    err_code=$(echo "$response" | jq -r '.error.code')
    err_msg=$(echo  "$response" | jq -r '.error.message')
    echo -e "${RED}Operation FAILED (code=${err_code}): ${err_msg}${RESET}" >&2
    exit 1
fi

echo -e "${GREEN}Operation SUCCEEDED${RESET}"
echo -e "${CYAN}Response:${RESET}"
echo "$response" | jq '.response'
