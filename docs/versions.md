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
| TypeScript | 6.0.3 (приложения, пакеты) / 5.9.3 (генератор) | catalog и `catalogs.contracts` (`pnpm-workspace.yaml`); почему не 7 — в исключениях |
| ESLint / typescript-eslint / eslint-plugin-boundaries | 9.39.5 / 8.70.1 / 7.2.0 | catalog; boundaries — `packages/config/package.json` |
| Prettier | 3.9.9 | catalog (`pnpm-workspace.yaml`) |
| openapi-typescript / openapi-fetch | 7.13.0 / 0.17.0 | `contracts/package.json` / `apps/{web,admin}/package.json` |
| next-intl / use-intl | 4.14.7 / 4.14.7 | `apps/web/package.json` / `apps/admin/package.json` |
| MSW | 2.15.0 | `apps/{web,admin}/package.json` |
| GitHub Actions: checkout / setup-go / pnpm action-setup | 7.0.1 / 7.0.0 / 6.1.0 | полный SHA в `.github/workflows/*.yml`; плавающий тег `pnpm/action-setup@v6` указывал на 6.0.10 без поддержки pnpm 12 |
| Flutter / Dart | фиксируется в wf-native | в репо wf-native проекта Flutter пока нет — версии появятся с мобильной спекой |
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
- pnpm 12 по умолчанию сам доустанавливает воркспейс при `pnpm run`/`pnpm exec`. У нас
  `verifyDepsBeforeRun: error` (`pnpm-workspace.yaml`): устаревший `node_modules` — ошибка
  `ERR_PNPM_VERIFY_DEPS_BEFORE_RUN`, чинится одним `pnpm install`. Сам `pnpm install`
  параллельно ни с чем не запускать. Хук pre-commit зовёт `node_modules/.bin/prettier` без pnpm.
