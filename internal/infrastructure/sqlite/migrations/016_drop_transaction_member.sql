-- +goose Up
-- +goose StatementBegin
-- 成员标注字段未接入任何统计聚合，仅流水页做前端筛选，价值不大，去除。
DROP INDEX IF EXISTS idx_tx_member;
ALTER TABLE transactions DROP COLUMN member;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE transactions ADD COLUMN member TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_tx_member ON transactions (member);
-- +goose StatementEnd
