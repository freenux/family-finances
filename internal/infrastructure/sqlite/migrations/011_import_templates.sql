-- +goose Up
-- +goose StatementBegin
-- 通用 CSV 导入的列映射模板
CREATE TABLE import_templates (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    mapping    TEXT NOT NULL,   -- CSVMapping JSON
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS import_templates;
-- +goose StatementEnd
