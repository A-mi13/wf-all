// Package store — доступ модуля identity к своей схеме (спека бэкенда §3): sqlc-пакет identitydb
// (запросы — backend/internal/identity/queries, элемент identity в backend/sqlc.yaml). Репозитория поверх
// identitydb нет намеренно: сценарии app работают с запросами в своей транзакции (identitydb.New(tx)) — порядок
// замков R30 и одна закоммиченная tx под Idempotency-Key видны в коде сценария, а не за обёрткой. Общие
// помощники доступа к таблицам identity, когда понадобятся нескольким пакетам модуля (app, jobs, rekey), — здесь.
package store
