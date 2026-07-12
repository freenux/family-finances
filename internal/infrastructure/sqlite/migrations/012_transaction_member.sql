-- +goose Up
-- +goose StatementBegin
-- 流水加成员标注（自由文本，空 = 未标注）。不做账号权限体系。
ALTER TABLE transactions ADD COLUMN member TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_tx_member ON transactions (member);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_tx_member;
ALTER TABLE transactions DROP COLUMN member;
-- +goose StatementEnd
