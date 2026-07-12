-- +goose Up
-- +goose StatementBegin
-- 季度预算：每个二级支出科目一行（只做季度，月度不做）
CREATE TABLE budgets (
    id             TEXT PRIMARY KEY,
    category_id    TEXT NOT NULL UNIQUE REFERENCES categories(id),
    quarter_amount INTEGER NOT NULL,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS budgets;
-- +goose StatementEnd
