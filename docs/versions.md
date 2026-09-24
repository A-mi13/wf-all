# Версии

Правило: последние стабильные. Если экосистема не поддерживает последнюю — последняя
совместимая, с причиной и условием апгрейда в таблице исключений. Проверять по официальным
источникам: go.dev/dl, nodejs.org/dist/index.json, `npm view`, GitHub Releases, postgresql.org.

| Что | Версия | Где закреплено |
| --- | --- | --- |
| Go | 1.27.1 | `toolchain` в `backend/go.mod`, `backend/tools/go.mod`, `tools/go.mod`, `tools/lint/go.mod` |
| Node.js | 26.10.0 | `devEngines.runtime` в корневом `package.json` |
| pnpm | 12.6.0 | `packageManager` в корневом `package.json` |
| PostgreSQL / PostGIS | 18.6 / 3.6.2 | `scripts/pg.sh` (локально, zip), `postgis/postgis:18-3.6` в CI |
| Next.js / React | 16.3.6 / 19.3.0 | `apps/web/package.json`, catalog (`pnpm-workspace.yaml`) |
| Vite / Vitest | 8.3.1 / 5.0.1 | catalog (`pnpm-workspace.yaml`) |
| Playwright | 1.63.0 | `apps/web/package.json` |
| chi / pgx | 5.3.2 / 5.11.0 | `backend/go.mod` |
| oapi-codegen / sqlc | 2.8.0 / 1.31.1 | `backend/tools/go.mod` |
| River | 0.47.0 | `backend/go.mod` (рантайм-библиотека воркера), CLI — `backend/tools/go.mod` |
| goose / pgtestdb / kin-openapi | 3.28.0 / 0.1.1 / 0.149.0 | `backend/go.mod` |
| go-task / gitleaks / actionlint / Mailpit | 3.53.1 / 8.30.1 / 1.7.12 / 1.31.2 | `tools/go.mod` |
| golangci-lint | 2.14.0 | `tools/lint/go.mod` |
| lefthook | 2.1.14 | корневой `package.json` |

## Исключения

| Что | Стоит | Последняя | Почему | Когда апгрейдить |
| --- | --- | --- | --- | --- |
| TypeScript (приложения) | 6.0.3 | 7.0.2 | typescript-eslint 8.70 поддерживает TS < 6.1; у 7.x нет классического JS API | когда typescript-eslint и eslint-config-next объявят поддержку TS 7 |
| TypeScript (генератор) | 5.9.3 | 7.0.2 | openapi-typescript 7.13 требует `typescript ^5.x` | когда openapi-typescript поднимет peer |
| ESLint | 9.39.5 | 10.11.0 | eslint-config-next 16.3.6 тянет eslint-plugin-react 7.37.5 (ESLint ≤ 9) | когда eslint-config-next перейдёт на ESLint 10 |

## Ловушки

- pnpm по умолчанию не ставит версии младше суток (`minimumReleaseAge`) — это защита, не баг.
- sqlc разбирает грамматику PostgreSQL 17: синтаксис PG18 в SQL не используем.
- Глобальный pnpm должен быть ≥ 10.34.5, иначе он не переключается на 12 по `packageManager`.
- pnpm 12 при запуске скриптов (`pnpm --filter X test`) может сам доустанавливать воркспейс —
  не запускать такие команды параллельно с `pnpm install`.
