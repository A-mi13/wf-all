# syntax=docker/dockerfile:1
# Образ бэкенда — один на все процессы: api, admin-api, worker, migrate. Какой запускать, решает
# команда контейнера (CMD по умолчанию — api). Конфигурация — только переменные окружения
# (internal/platform/config), миграции вшиты в бинарники. Тот же образ работает на dev-стенде
# Render и на своём сервере — docs/deploy-dev.md.

FROM golang:1.27.1-alpine3.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/ \
    ./cmd/api ./cmd/admin-api ./cmd/worker ./cmd/migrate

FROM alpine:3.24.2
# ca-certificates — TLS до управляемого Postgres (sslmode=require); tzdata — таймзоны городов и полей.
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -u 10001 wf
COPY --from=build /out/ /app/
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
