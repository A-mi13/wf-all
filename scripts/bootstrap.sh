#!/usr/bin/env bash
# Первая команда на чистой машине. Ничего не ставит глобально:
# Go-инструменты собираются в .tools/bin по пинам из tools/go.mod (проверка go.sum).
set -euo pipefail
cd "$(dirname "$0")/.."
BIN="$PWD/.tools/bin"
mkdir -p "$BIN"
go -C tools build -o "$BIN/" \
  github.com/go-task/task/v3/cmd/task \
  github.com/zricethezav/gitleaks/v8 \
  github.com/rhysd/actionlint/cmd/actionlint \
  github.com/axllent/mailpit
go -C tools/lint build -o "$BIN/" github.com/golangci/golangci-lint/v2/cmd/golangci-lint
pnpm install
echo "Готово. Дальше: ./task setup"
