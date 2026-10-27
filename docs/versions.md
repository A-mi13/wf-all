# Версии

Правило: последние стабильные. Если экосистема не поддерживает последнюю — последняя
совместимая, с причиной и условием апгрейда в таблице исключений. Проверять по официальным
источникам: go.dev/dl, nodejs.org/dist/index.json, `npm view`, GitHub Releases, postgresql.org.

| Что | Версия | Где закреплено |
| --- | --- | --- |
| Go | 1.27.1 | `toolchain` в `backend/go.mod`, `backend/tools/go.mod`, `tools/go.mod`, `tools/lint/go.mod`, `tools/oasdiff/go.mod` |
| Node.js | 26.10.0 | `devEngines.runtime` в корневом `package.json` |
| pnpm | 12.6.0 | `packageManager` в корневом `package.json` |
| PostgreSQL / PostGIS | 18.6 / 3.6.2 | `scripts/pg.sh` (локально, zip), `postgis/postgis:18-3.6` в CI |
| Next.js / React | 16.4.3 / 19.3.1 | `apps/web/package.json`, catalog (`pnpm-workspace.yaml`) |
| Vite / Vitest | 8.3.5 / 5.0.5 | catalog (`pnpm-workspace.yaml`) |
| Playwright | 1.64.1 (Chromium 156.0.8078.4, WebKit 27.2) | `apps/web/package.json`, `apps/admin/package.json` (одна версия на репо: CI-кеш браузеров — по ней) |
| TypeScript | 6.0.3 (приложения, пакеты) / 5.9.3 (генератор) | catalog и `catalogs.contracts` (`pnpm-workspace.yaml`); почему не 7 — в исключениях |
| ESLint / typescript-eslint / eslint-plugin-boundaries | 9.39.5 / 8.72.0 / 7.3.0 | catalog; boundaries — `packages/config/package.json` |
| Prettier | 3.9.10 | catalog (`pnpm-workspace.yaml`) |
| openapi-typescript / openapi-fetch | 7.13.0 / 0.17.0 | `contracts/package.json` / `apps/{web,admin}/package.json` |
| next-intl / use-intl | 4.14.9 / 4.14.9 | `apps/web/package.json` / `apps/admin/package.json` |
| MSW / openapi-msw | 3.0.3 / 2.1.0 | `apps/{web,admin}/package.json` (в msw 3 опция `onUnhandledFrame`, не `onUnhandledRequest`: старое имя молча игнорируется — необработанный запрос уходит в сеть) |
| TanStack Query / TanStack Router | 5.104.5 / 1.170.46 | `apps/admin/package.json`, Query ещё и `apps/web/package.json` (одна версия); Router — маршруты кодом |
| react-hook-form | 7.91.0 | `apps/admin/package.json` |
| uqr | 0.1.3 | `apps/admin/package.json` (QR секрета 2FA — SVG на клиенте, без сети и зависимостей; R11) |
| @fontsource/oswald / @fontsource/manrope | 5.3.0 / 5.3.0 | `apps/admin/package.json` (шрифты админки без внешних запросов, `src/app/fonts.ts`) |
| GitHub Actions: checkout / setup-go / pnpm action-setup | 7.0.1 / 7.0.0 / 6.1.0 | полный SHA в `.github/workflows/*.yml`; плавающий тег `pnpm/action-setup@v6` указывал на 6.0.10 без поддержки pnpm 12 |
| Flutter / Dart | фиксируется в wf-native | в репо wf-native проекта Flutter пока нет — версии появятся с мобильной спекой |
| chi / pgx | 5.5.0 / 5.11.0 | `backend/go.mod` |
| oapi-codegen / sqlc | 2.8.1 / 1.31.1 | `backend/tools/go.mod` |
| oapi-codegen runtime | 1.8.0 | `backend/go.mod` (биндинг параметров запроса в сгенерированном коде `internal/httpapi/*/oapi`) |
| River | 0.49.0 | `backend/go.mod` (рантайм-библиотека воркера и admin-api), CLI — `backend/tools/go.mod`; почему не 0.50.0 — в исключениях |
| goose / pgtestdb / kin-openapi | 3.29.0 / 0.1.1 / 0.152.0 | `backend/go.mod` |
| google/uuid | 1.6.0 | `backend/go.mod` (UUIDv7 — `internal/platform/id`) |
| golang-jwt/jwt | 5.3.1 | `backend/go.mod` (access-токены, EdDSA — `internal/platform/token`) |
| golang.org/x/crypto / x/net / x/sync / x/text | 0.58.0 / 0.61.0 / 0.24.0 / 0.43.0 | `backend/go.mod` (argon2id и NFKC пароля — `internal/platform/password`; singleflight — `internal/platform/auth`, `internal/platform/health`; IDNA домена почты сотрудника (UTS #46) и NFKC кодов — `internal/staff/internal/domain`) |
| messageformat-go/mf1 | 0.8.6 | `backend/go.mod` (ICU MessageFormat v1 серверных локалей — `internal/platform/i18n`) |
| go-mail | 0.8.1 | `backend/go.mod` (SMTP — `internal/platform/mail`) |
| altcha-lib-go | 1.0.0 | `backend/go.mod` (только тесты совместимости ALTCHA — `internal/platform/humancheck`) |
| go-task / gitleaks / actionlint / Mailpit | 3.54.0 / 8.30.1 / 1.7.12 / 1.32.0 | `tools/go.mod` |
| golangci-lint | 2.15.0 | `tools/lint/go.mod` |
| lefthook | 2.2.2 | корневой `package.json` |
| Образы Docker: golang / alpine | 1.27.1-alpine3.24 / 3.24.2 | `backend/Dockerfile` |
| Swagger UI (swagger-ui-dist, jsDelivr + SRI) | 5.33.1 | `SwaggerUIVersion` и хэши в `backend/internal/platform/apidocs/apidocs.go` |
| Redocly CLI | 2.71.0 | `contracts/package.json` (сборка бандлов контракта) |
| oasdiff | 1.34.1 | `tools/oasdiff/go.mod` (отдельный модуль, см. ловушки) |
| go-pgquery (pg_query в wasm) | v0.0.0-20250409022910-10ac41983c07 | `backend/go.mod` — та же версия, что у sqlc в `backend/tools/go.mod` |

## Исключения

| Что | Стоит | Последняя | Почему | Когда апгрейдить |
| --- | --- | --- | --- | --- |
| TypeScript (приложения) | 6.0.3 | 7.0.2 | typescript-eslint 8.72 поддерживает TS < 6.1 (проверено 27.10.2026); у 7.x нет классического JS API | когда typescript-eslint и eslint-config-next объявят поддержку TS 7 |
| TypeScript (генератор) | 5.9.3 | 7.0.2 | openapi-typescript 7.13 требует `typescript ^5.x` | когда openapi-typescript поднимет peer |
| River | 0.49.0 | 0.50.0 | 0.50.0 опубликована 27.10.2026 00:53 UTC — моложе суток на момент подъёма Go-зависимостей ветки (27.10.2026; та же защита, что `minimumReleaseAge` у pnpm); 0.x — минорный подъём River может ломать API, а заглушки чужих видов (`queue.AddForeign`) опираются на его внутренности | после 28.10.2026 01:00 UTC — поднять River, riverpgxv5, rivertype и CLI до последней 0.x, прогнать `go test ./cmd/... ./internal/platform/queue/...` и тесты рескьюера |
| ESLint | 9.39.5 | 10.13.0 | eslint-config-next 16.4.3 тянет eslint-plugin-react 7.37.5 (peer ESLint ≤ 9.7; проверено 27.10.2026) | когда eslint-config-next перейдёт на ESLint 10 |

## Ловушки

- pnpm по умолчанию не ставит версии младше суток (`minimumReleaseAge`) — это защита, не баг.
- sqlc разбирает грамматику PostgreSQL 17: синтаксис PG18 в SQL не используем.
- Глобальный pnpm должен быть ≥ 10.34.5, иначе он не переключается на 12 по `packageManager`.
- pnpm 12 по умолчанию сам доустанавливает воркспейс при `pnpm run`/`pnpm exec`. У нас
  `verifyDepsBeforeRun: error` (`pnpm-workspace.yaml`): устаревший `node_modules` — ошибка
  `ERR_PNPM_VERIFY_DEPS_BEFORE_RUN`, чинится одним `pnpm install`. Сам `pnpm install`
  параллельно ни с чем не запускать. Хук pre-commit зовёт `node_modules/.bin/prettier` без pnpm.
- oasdiff — отдельный модуль `tools/oasdiff`: общий `tools/go.mod` с ним не собирается (yaml/v4 rc); `replace` не использовать.
