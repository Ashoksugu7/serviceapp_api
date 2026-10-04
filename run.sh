#!/usr/bin/env bash
# Start the API from this repository and append its output to logs/api.log.
set -euo pipefail

cd "$(dirname "$0")"
mkdir -p logs

if [[ -f .env ]]; then
  set -a
  source .env
  set +a
fi

echo "[$(date '+%Y-%m-%d %H:%M:%S')] Starting ServiceOps360 API" | tee -a logs/api.log
go run ./cmd/api 2>&1 | tee -a logs/api.log
