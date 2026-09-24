-- 0012_river_v3.sql
-- Схема очереди River, версия 3. Не править руками — сгенерировано:
-- go tool -modfile=tools/go.mod river migrate-get --version 3 --up|--down

-- +goose Up
-- +goose StatementBegin
-- River main migration 003 [up]
ALTER TABLE river_job ALTER COLUMN tags SET DEFAULT '{}';
UPDATE river_job SET tags = '{}' WHERE tags IS NULL;
ALTER TABLE river_job ALTER COLUMN tags SET NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- River main migration 003 [down]
ALTER TABLE river_job
    ALTER COLUMN tags DROP NOT NULL,
    ALTER COLUMN tags DROP DEFAULT;
-- +goose StatementEnd
