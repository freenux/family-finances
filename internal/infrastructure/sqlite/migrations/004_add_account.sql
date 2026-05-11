-- +goose Up
-- +goose StatementBegin

-- 引入账户维度（husband/wife）。用户明确要求：清空既有流水重新导入。
DELETE FROM transactions;
DELETE FROM imported_transaction_keys;
DELETE FROM import_batches;

ALTER TABLE transactions ADD COLUMN account TEXT NOT NULL DEFAULT 'husband';
CREATE INDEX idx_tx_account_occurred ON transactions (account, occurred_at);

-- imported_transaction_keys 主键改为 (source, account, transaction_no)，
-- 允许夫妻刷同一张卡时用不同账户标签分别导入而不被去重。
DROP TABLE imported_transaction_keys;
CREATE TABLE imported_transaction_keys (
    source          TEXT NOT NULL,
    account         TEXT NOT NULL,
    transaction_no  TEXT NOT NULL,
    PRIMARY KEY (source, account, transaction_no)
);

-- import_batches 也补 account 以便追溯
ALTER TABLE import_batches ADD COLUMN account TEXT NOT NULL DEFAULT 'husband';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_tx_account_occurred;
-- account 列暂不支持 DROP（SQLite 限制），回滚仅清数据即可
DELETE FROM transactions;
DELETE FROM imported_transaction_keys;
DELETE FROM import_batches;
-- +goose StatementEnd
