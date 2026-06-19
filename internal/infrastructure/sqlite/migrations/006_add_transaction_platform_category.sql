-- +goose Up
-- +goose StatementBegin
ALTER TABLE transactions ADD COLUMN platform_category TEXT;

UPDATE transactions
SET platform_category = CASE
    WHEN source = 'alipay' AND instr(raw_row, ',') > 0 THEN
        substr(raw_row, instr(raw_row, ',') + 1, instr(substr(raw_row, instr(raw_row, ',') + 1), ',') - 1)
    WHEN source = 'wechat' AND instr(raw_row, ' | ') > 0 THEN
        substr(raw_row, instr(raw_row, ' | ') + 3, instr(substr(raw_row, instr(raw_row, ' | ') + 3), ' | ') - 1)
    ELSE platform_category
END
WHERE COALESCE(platform_category, '') = ''
  AND raw_row IS NOT NULL
  AND (
    (source = 'alipay' AND instr(raw_row, ',') > 0)
    OR (source = 'wechat' AND instr(raw_row, ' | ') > 0)
  );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE transactions_old_platform_category (
    id              TEXT PRIMARY KEY,
    source          TEXT NOT NULL,
    account         TEXT NOT NULL DEFAULT 'husband',
    import_batch_id TEXT,
    occurred_at     DATETIME NOT NULL,
    counterparty    TEXT,
    description     TEXT,
    note            TEXT,
    amount          INTEGER NOT NULL,
    direction       TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'confirmed',
    category_id     TEXT,
    raw_row         TEXT,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (category_id) REFERENCES categories(id)
);

INSERT INTO transactions_old_platform_category (
    id, source, account, import_batch_id, occurred_at, counterparty, description, note,
    amount, direction, status, category_id, raw_row, created_at, updated_at
)
SELECT
    id, source, account, import_batch_id, occurred_at, counterparty, description, note,
    amount, direction, status, category_id, raw_row, created_at, updated_at
FROM transactions;

DROP TABLE transactions;
ALTER TABLE transactions_old_platform_category RENAME TO transactions;

CREATE INDEX idx_tx_occurred_direction_status ON transactions (occurred_at, direction, status);
CREATE INDEX idx_tx_category                  ON transactions (category_id);
CREATE INDEX idx_tx_batch                     ON transactions (import_batch_id);
CREATE INDEX idx_tx_status                    ON transactions (status);
CREATE INDEX idx_tx_account_occurred          ON transactions (account, occurred_at);
-- +goose StatementEnd
