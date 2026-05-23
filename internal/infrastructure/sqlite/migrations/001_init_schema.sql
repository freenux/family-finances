-- +goose Up
-- +goose StatementBegin
CREATE TABLE categories (
    id          TEXT PRIMARY KEY,
    parent_id   TEXT,
    level       INTEGER NOT NULL,
    name        TEXT NOT NULL,
    group_emoji TEXT,
    type        TEXT NOT NULL,
    sort_order  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE transactions (
    id              TEXT PRIMARY KEY,
    source          TEXT NOT NULL,
    import_batch_id TEXT,
    occurred_at     DATETIME NOT NULL,
    counterparty    TEXT,
    description     TEXT,
    amount          INTEGER NOT NULL,
    direction       TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'confirmed',
    category_id     TEXT,
    raw_row         TEXT,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (category_id) REFERENCES categories(id)
);

CREATE INDEX idx_tx_occurred_direction_status ON transactions (occurred_at, direction, status);
CREATE INDEX idx_tx_category                  ON transactions (category_id);
CREATE INDEX idx_tx_batch                     ON transactions (import_batch_id);
CREATE INDEX idx_tx_status                    ON transactions (status);

CREATE TABLE import_batches (
    id              TEXT PRIMARY KEY,
    source          TEXT NOT NULL,
    filename        TEXT,
    period_start    DATETIME,
    period_end      DATETIME,
    total_rows      INTEGER NOT NULL DEFAULT 0,
    imported_rows   INTEGER NOT NULL DEFAULT 0,
    skipped_rows    INTEGER NOT NULL DEFAULT 0,
    pending_rows    INTEGER NOT NULL DEFAULT 0,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE imported_transaction_keys (
    source          TEXT NOT NULL,
    transaction_no  TEXT NOT NULL,
    PRIMARY KEY (source, transaction_no)
);

CREATE TABLE category_rules (
    id            TEXT PRIMARY KEY,
    pattern       TEXT NOT NULL,
    pattern_type  TEXT NOT NULL,
    field         TEXT NOT NULL,
    category_id   TEXT,
    priority      INTEGER NOT NULL DEFAULT 10,
    source        TEXT NOT NULL DEFAULT 'builtin',
    is_active     INTEGER NOT NULL DEFAULT 1,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (category_id) REFERENCES categories(id)
);

CREATE INDEX idx_rules_priority_active ON category_rules (priority, is_active);

CREATE TABLE asset_snapshots (
    id            TEXT PRIMARY KEY,
    period        TEXT NOT NULL UNIQUE,
    snapshot_date DATETIME NOT NULL,
    data          TEXT NOT NULL,
    net_worth     INTEGER NOT NULL DEFAULT 0,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE financial_goals (
    id                TEXT PRIMARY KEY,
    category          TEXT NOT NULL,
    description       TEXT NOT NULL,
    target_amount     INTEGER NOT NULL,
    years_to_achieve  TEXT,
    priority          TEXT NOT NULL,
    expected_return   REAL NOT NULL DEFAULT 0,
    annual_saving     INTEGER,
    notes             TEXT,
    sort_order        INTEGER NOT NULL DEFAULT 0,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE insurance_policies (
    id              TEXT PRIMARY KEY,
    insured_person  TEXT NOT NULL,
    insurance_type  TEXT NOT NULL,
    company_product TEXT,
    coverage        TEXT,
    coverage_amount INTEGER,
    annual_premium  INTEGER,
    renewal_date    DATETIME,
    notes           TEXT,
    sort_order      INTEGER NOT NULL DEFAULT 0,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE reports (
    id            TEXT PRIMARY KEY,
    period        TEXT NOT NULL,
    period_type   TEXT NOT NULL,
    generated_at  DATETIME NOT NULL,
    income_data   TEXT,
    expense_data  TEXT,
    kpi_data      TEXT,
    comparison    TEXT,
    ai_prompt     TEXT,
    ai_analysis   TEXT,
    ai_model      TEXT,
    status        TEXT NOT NULL DEFAULT 'draft',
    is_frozen     INTEGER NOT NULL DEFAULT 0,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (period, period_type)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS reports;
DROP TABLE IF EXISTS insurance_policies;
DROP TABLE IF EXISTS financial_goals;
DROP TABLE IF EXISTS asset_snapshots;
DROP TABLE IF EXISTS category_rules;
DROP TABLE IF EXISTS imported_transaction_keys;
DROP TABLE IF EXISTS import_batches;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS categories;
-- +goose StatementEnd
