#!/usr/bin/env bash
# Проверка версий окружения. Используется как тест фазы 0 и в README.
set -uo pipefail
cd "$(dirname "$0")/.."
fail=0
check() { # имя, ожидаемая версия, фактический вывод
  if [[ "$3" == *"$2"* ]]; then echo "ok   $1 $2"; else echo "FAIL $1: ждали $2, получили: $3"; fail=1; fi
}
what="${1:-all}"
if [[ "$what" == pnpm || "$what" == all ]]; then
  check pnpm 12.6.0 "$(pnpm --version 2>&1)"
  check node v26.10.0 "$(pnpm exec node --version 2>&1)"
fi
if [[ "$what" == go || "$what" == all ]]; then
  check go go1.27.1 "$(cd backend 2>/dev/null && go version 2>&1)"
fi
if [[ "$what" == tools || "$what" == all ]]; then
  check task 3.53.1 "$(.tools/bin/task --version 2>&1)"
  check golangci-lint 2.14.0 "$(.tools/bin/golangci-lint version 2>&1)"
  check gitleaks 8.30.1 "$(.tools/bin/gitleaks version 2>&1)"
  check actionlint 1.7.12 "$(.tools/bin/actionlint -version 2>&1)"
fi
exit $fail
