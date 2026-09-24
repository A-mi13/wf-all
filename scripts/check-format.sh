#!/usr/bin/env bash
# Проверка форматирования для pre-commit (lefthook.yml): только проверяет, файлы не правит.
# Вызов: bash scripts/check-format.sh prettier|gofmt <файлы…>
# Логика — здесь, а не строкой в lefthook.yml: на Windows lefthook передаёт run в sh.exe
# одной командной строкой, и двойные кавычки внутри неё теряются.
set -uo pipefail
cd "$(dirname "$0")/.."

tool=${1:-}
shift || true
[ "$#" -gt 0 ] || exit 0

case "$tool" in
  prettier)
    # напрямую, без pnpm exec: хуки параллельных коммитов не запускают pnpm (и его проверку установки)
    bad=$(node_modules/.bin/prettier --list-different --ignore-unknown "$@")
    rc=$?
    fix='node_modules/.bin/prettier --write'
    ;;
  gofmt)
    bad=$(gofmt -l "$@")
    rc=$?
    fix='gofmt -w'
    ;;
  *)
    echo "check-format.sh: неизвестный инструмент '$tool' (prettier | gofmt)" >&2
    exit 2
    ;;
esac

if [ "$rc" -ne 0 ] && [ -z "$bad" ]; then
  echo "pre-commit: $tool не смог разобрать файлы — ошибка выше" >&2
  exit 1
fi
if [ -n "$bad" ]; then
  echo "pre-commit: $tool — файлы не отформатированы:" >&2
  printf '  %s\n' $bad >&2
  echo "Отформатировать и добавить заново: $fix" $bad >&2
  [ "$tool" = prettier ] && echo '(весь репозиторий — pnpm format)' >&2
  exit 1
fi
