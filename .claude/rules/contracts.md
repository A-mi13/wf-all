---
paths:
  - "contracts/**"
---

# Контракты

- Править исходники: `openapi/{public,admin}/root.yaml` (общие компоненты, реестр схем) и `openapi/{public,admin}/<модуль>/{paths,schemas}.yaml`. Бандлы `openapi/{public,admin}.yaml` — сгенерированные, руками не правятся.
- Изменил YAML → `./task gen` (бандлы redocly, Go, TS-типы) → закоммить сгенерированное в `contracts/`, `backend/`, `apps/`.
- Новая или изменённая ручка — сначала контракт, в том же коммите, что и код: Swagger (`/docs`, `backend/internal/platform/apidocs`) и клиенты читают его, а не код. Документация полная, чтобы у потребителя не осталось вопросов (страж `src/docs.mjs`, тест `test/docs.test.mjs`): у операции `summary` и `description` (что делает, кто вызывает, побочные эффекты, коды ошибок операции и когда они бывают), у тега — запись с `description` в корневом `tags`, у каждого параметра — `description` и пример, у тела запроса и каждого ответа — `description`, у каждой схемы и каждого поля — `description` и `example` (`enum` заменяет пример; поле-`$ref` описывает сама схема). Примеры — правдоподобные значения домена, а не `string`/`0`; правила, общие для всех операций, — в `info.description`.
- Конвенции (тест `test/conventions.test.mjs`): у операции ровно один тег (= модуль-реализатор, это проверяет `apitest.TagViolations` в `internal/httpapi/{public,admin}`; `platform` — операции платформы), `operationId` lowerCamel, у операции `x-error-codes`, у мутирующей операции с аутентификацией — `$ref: '#/components/parameters/IdempotencyKey'`; анонимная операция — `security: []`; каждый `enum` — с `x-extensible-enum: true` (enum'ы открытые, спека §8.3).
- Анонимная мутирующая операция — только из закрытого корневого списка `x-anonymous-mutations` в `root.yaml` (в админке список пуст, спека §6.4). Корневая `security` обязательна; компонент `IdempotencyKey` — name `Idempotency-Key`, in header, required true.
- Коды ошибок операций — минимум два сегмента `<модуль>.<ошибка>`; `x-error-codes-common` — общие коды платформы.
- Новый код ошибки → текст `errors.<код>` в `apps/web/messages` (публичный контракт) или `apps/admin/messages` (админский), `ru` и `en`. Тексты новых кодов в `en.json` пока пустые строки (fallback на ru, тест `messages.test.ts`), перевод — отдельная работа.
- Схема `Problem` правится в `public/root.yaml` и `admin/root.yaml` одинаково (тест `test/contracts.test.mjs`).
- Ломающее изменение `public.yaml` — только в `/v2` и с новым major тега `contracts-vX.Y.Z`; проверка — `./task contracts:breaking` (oasdiff).
- `admin.yaml` oasdiff проверяет так же; осознанная ломающая правка админского контракта (выкатывается вместе с `apps/admin`) — только явной строкой `<МЕТОД> <путь> <текст изменения>` в `contracts/oasdiff-err-ignore-admin.txt` (без комментариев) и с причиной в сообщении коммита; у `public.yaml` исключений нет.
- Код ошибки платформы (`x-error-codes-common`) — только вместе с константой в `backend/internal/platform/httpx/codes.go` (`PlatformCodes`): `TestPlatformCodesDocumented` сверяет в обе стороны.
- `contracts/oasdiff-severity.txt` — понижение уровней oasdiff (сейчас только `response-property-enum-value-added info`: enum'ы ответа открытые, спека §8.3); формат — две колонки без комментариев, обоснование — в `scripts/contracts-breaking.sh`.
- Изменил `tokens/tokens.json` → `pnpm --filter @wf/tokens build` (входит в `./task gen`).
