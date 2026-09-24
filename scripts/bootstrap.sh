#!/usr/bin/env bash
# Первая команда на чистой машине. Ничего не ставит глобально:
# Go-инструменты собираются в .tools/bin по пинам из tools/go.mod (проверка go.sum).
set -euo pipefail
cd "$(dirname "$0")/.."
BIN="$PWD/.tools/bin"
mkdir -p "$BIN"
go -C tools build -o "$BIN/" \
  github.com/go-task/task/v3/cmd/task \
  github.com/rhysd/actionlint/cmd/actionlint \
  github.com/axllent/mailpit
# gitleaks не встраивает версию без -ldflags (version/version.go: Version по
# умолчанию — плейсхолдер "version is set by build process"). Версию берём из
# пина в tools/go.mod, а не хардкодим второй раз.
GITLEAKS_VERSION=$(go -C tools list -m -f '{{.Version}}' github.com/zricethezav/gitleaks/v8)
go -C tools build -ldflags "-X github.com/zricethezav/gitleaks/v8/version.Version=${GITLEAKS_VERSION}" \
  -o "$BIN/" github.com/zricethezav/gitleaks/v8
go -C tools/lint build -o "$BIN/" github.com/golangci/golangci-lint/v2/cmd/golangci-lint
pnpm install
echo "Готово. Дальше: ./task setup"
