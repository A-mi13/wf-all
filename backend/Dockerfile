# syntax=docker/dockerfile:1
# Образ бэкенда — один на все процессы: api, admin-api, worker, migrate. Какой запускать, решает
# команда контейнера (CMD по умолчанию — api). Конфигурация — только переменные окружения
# (internal/platform/config), миграции вшиты в бинарники. Тот же образ работает на dev-стенде
# Render и на своём сервере — docs/deploy-dev.md.
#
# Контекст сборки — корень репозитория (`docker build -f backend/Dockerfile .`): стадии админки нужен
# воркспейс pnpm. Что входит в контекст — список разрешённого в корневом .dockerignore.

# Собранная apps/admin (план C, Task 1, R1): admin-api отдаёт её с того же origin, если сервис задал
# ADMIN_STATIC_DIR=/app/admin; в образе каталог не задаётся — по умолчанию статики нет, как в dev.
# Node — версия devEngines корневого package.json (docs/versions.md).
FROM node:26.10.0-alpine3.24 AS admin
WORKDIR /src
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
# pnpm — ровно версия packageManager: другая переключилась бы на неё и захотела записать переход в
# lockfile (урок PR #13). corepack в Node 26 не входит — ставим через npm образа.
RUN npm install --global --no-fund --no-audit \
      "pnpm@$(node -p "require('./package.json').packageManager.replace(/^pnpm@/, '')")" \
    && pnpm --version
# Node из devEngines pnpm ставит в node_modules/.bin (runtime-зависимость лок-файла) и запускает им postinstall
# пакетов; этому бинарнику на Alpine нужна libatomic (первый прогон image.yml, PR #14: libatomic.so.1 not found).
RUN apk add --no-cache libatomic
# без backend/: правка Go не сбрасывает кэш слоёв админки
COPY apps/ apps/
COPY packages/ packages/
COPY contracts/ contracts/
COPY scripts/ scripts/
# Только админка и её зависимости по воркспейсу. CI=true — строго по lockfile, как bootstrap.sh и
# admin-visual (явный --frozen-lockfile при смене версии pnpm ронял установку, PR #13); лок-файл
# устарел — установка падает и здесь.
RUN --mount=type=cache,id=wf-pnpm-store,target=/pnpm/store \
    CI=true pnpm install --store-dir /pnpm/store --filter "@wf/admin..."
# Скрипт build из apps/admin/package.json через node --run: pnpm run проверял бы (verifyDepsBeforeRun)
# весь воркспейс, а установлена только админка. Затем — проверка сборки, как в admin:ci.
RUN cd apps/admin && node --run build \
    && node /src/scripts/check-admin-dist.mjs /src/apps/admin/dist

FROM golang:1.27.1-alpine3.24 AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY backend/ .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/ \
    ./cmd/api ./cmd/admin-api ./cmd/worker ./cmd/migrate

FROM alpine:3.24.2
# ca-certificates — TLS до управляемого Postgres (sslmode=require) и SMTP; tzdata — таймзоны городов и полей.
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -u 10001 wf
COPY --from=build /out/ /app/
COPY --from=admin /src/apps/admin/dist/ /app/admin/
# migrate-then-api — для хостинга без отдельного шага миграций (Render free, render.yaml): goose
# держит блокировку, двойного наката не будет. Команда контейнера на Render не проходит через
# shell (`sh -c '…'` там ломается), поэтому сценарий лежит в образе.
COPY --chmod=0755 <<'EOF' /app/migrate-then-api
#!/bin/sh
set -e
migrate up
exec api
EOF
ENV PATH=/app:$PATH
USER wf
CMD ["api"]
