---
paths:
  - "contracts/**"
---

# Контракты

- Править исходники: `openapi/{public,admin}/root.yaml` (общие компоненты, реестр схем) и `openapi/{public,admin}/<модуль>/{paths,schemas}.yaml`. Бандлы `openapi/{public,admin}.yaml` — сгенерированные, руками не правятся.
- Изменил YAML → `./task gen` (бандлы redocly, Go, TS-типы) → закоммить сгенерированное в `contracts/`, `backend/`, `apps/`.
- Конвенции (тест `test/conventions.test.mjs`): один тег = модуль-реализатор (`platform` — операции платформы), `operationId` lowerCamel, у операции `x-error-codes`, у мутирующей операции с аутентификацией — `$ref: '#/components/parameters/IdempotencyKey'`; анонимная операция — `security: []`.
- Анонимная мутирующая операция — только из закрытого корневого списка `x-anonymous-mutations` в `root.yaml` (в админке список пуст, спека §6.4). Корневая `security` обязательна; компонент `IdempotencyKey` — name `Idempotency-Key`, in header, required true.
- Коды ошибок операций — минимум два сегмента `<модуль>.<ошибка>`; `x-error-codes-common` — общие коды платформы.
- Новый код ошибки → текст `errors.<код>` в `apps/web/messages` (публичный контракт) или `apps/admin/messages` (админский), `ru` и `en`. Тексты новых кодов в `en.json` пока пустые строки (fallback на ru, тест `messages.test.ts`), перевод — отдельная работа.
- Схема `Problem` правится в `public/root.yaml` и `admin/root.yaml` одинаково (тест `test/contracts.test.mjs`).
- Ломающее изменение `public.yaml` — только в `/v2` и с новым major тега `contracts-vX.Y.Z`; проверка — `./task contracts:breaking` (oasdiff).
- `contracts/oasdiff-severity.txt` — понижение уровней oasdiff (сейчас только `response-property-enum-value-added info`: enum'ы ответа открытые, спека §8.3); формат — две колонки без комментариев, обоснование — в `scripts/contracts-breaking.sh`.
- Изменил `tokens/tokens.json` → `pnpm --filter @wf/tokens build` (входит в `./task gen`).
