#!/usr/bin/env bash
# Первая команда на чистой машине. Ничего не ставит глобально:
# Go-инструменты собираются в .tools/bin по пинам из tools/go.mod, tools/lint/go.mod и
# tools/oasdiff/go.mod (проверка go.sum).
set -euo pipefail
cd "$(dirname "$0")/.."
BIN="$PWD/.tools/bin"
mkdir -p "$BIN"

build_tools() {
  go -C tools build -o "$BIN/" \
    github.com/go-task/task/v3/cmd/task \
    github.com/rhysd/actionlint/cmd/actionlint \
    github.com/axllent/mailpit
  # gitleaks не встраивает версию без -ldflags (version/version.go: Version по
  # умолчанию — плейсхолдер "version is set by build process"). Версию берём из
  # пина в tools/go.mod, а не хардкодим второй раз.
  local gitleaks_version oasdiff_version
  gitleaks_version=$(go -C tools list -m -f '{{.Version}}' github.com/zricethezav/gitleaks/v8)
  go -C tools build -ldflags "-X github.com/zricethezav/gitleaks/v8/version.Version=${gitleaks_version}" \
    -o "$BIN/" github.com/zricethezav/gitleaks/v8
  # oasdiff — отдельный модуль (как tools/lint): его yaml/v4 несовместим с actionlint в общем tools/go.mod.
  # Версию он тоже не встраивает (build.Version по умолчанию "main") — берём из пина.
  oasdiff_version=$(go -C tools/oasdiff list -m -f '{{.Version}}' github.com/oasdiff/oasdiff)
  go -C tools/oasdiff build -ldflags "-X github.com/oasdiff/oasdiff/build.Version=${oasdiff_version#v}" \
    -o "$BIN/" github.com/oasdiff/oasdiff
  go -C tools/lint build -o "$BIN/" github.com/golangci/golangci-lint/v2/cmd/golangci-lint
}

# Инструменты пересобираются, только когда сменились их пины, Go или этот сценарий: отметка —
# хэш входов сборки. В CI .tools/bin приходит из кеша по тем же файлам (.github/workflows/*.yml).
stamp=$({
  go version
  cat tools/go.mod tools/go.sum tools/lint/go.mod tools/lint/go.sum \
    tools/oasdiff/go.mod tools/oasdiff/go.sum scripts/bootstrap.sh
} | sha256sum | cut -d' ' -f1)
if [[ "$(cat "$BIN/.stamp" 2>/dev/null)" == "$stamp" ]]; then
  echo "Go-инструменты актуальны: $BIN"
else
  build_tools
  echo "$stamp" >"$BIN/.stamp"
fi
pnpm install
echo "Готово. Дальше: ./task setup"
