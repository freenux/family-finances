-- +goose Up
-- +goose StatementBegin
-- 摘要推送设置：单行表，id 固定 'default'
CREATE TABLE digest_settings (
    id           TEXT PRIMARY KEY CHECK (id = 'default'),
    enabled      INTEGER NOT NULL DEFAULT 0,
    cadence      TEXT NOT NULL DEFAULT 'weekly',    -- weekly | quarterly
    channel      TEXT NOT NULL DEFAULT 'email',     -- email | webhook
    target       TEXT NOT NULL DEFAULT '',          -- 收件地址或 webhook URL
    modules      TEXT NOT NULL DEFAULT '[]',        -- JSON 数组：启用的模块
    last_sent_at DATETIME
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS digest_settings;
-- +goose StatementEnd
