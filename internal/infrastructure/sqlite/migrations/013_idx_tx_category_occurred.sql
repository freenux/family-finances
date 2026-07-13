-- +goose Up
-- +goose StatementBegin
-- 性能：AggregateByCategory 的 LEFT JOIN 对每个二级科目重扫 occurred_at 范围（×30）。
-- 覆盖索引 (category_id, occurred_at, status, amount) 让每科目走索引区间扫描且免回表：
-- 100k 行基准实测季度聚合 165ms→2.9ms、年度 664ms→12ms。
CREATE INDEX idx_tx_category_occurred ON transactions (category_id, occurred_at, status, amount);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_tx_category_occurred;
-- +goose StatementEnd
