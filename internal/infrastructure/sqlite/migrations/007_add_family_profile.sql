-- +goose Up
-- +goose StatementBegin
-- 家庭风险画像：单行表，id 固定 'default'。
-- monthly_expense_fen 可空 = 用近 4 个季度流水自动折算月均支出。
CREATE TABLE family_profile (
    id                   TEXT PRIMARY KEY CHECK (id = 'default'),
    family_structure     TEXT NOT NULL DEFAULT '',
    main_age             INTEGER NOT NULL DEFAULT 35,
    income_stability     TEXT NOT NULL DEFAULT 'medium',
    annual_income_fen    INTEGER NOT NULL DEFAULT 0,
    mortgage_monthly_fen INTEGER NOT NULL DEFAULT 0,
    monthly_expense_fen  INTEGER,
    emergency_months     INTEGER NOT NULL DEFAULT 6,
    risk_appetite        TEXT NOT NULL DEFAULT 'balanced',
    horizon              TEXT NOT NULL DEFAULT 'medium',
    updated_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS family_profile;
-- +goose StatementEnd
