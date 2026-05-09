-- +goose Up
-- +goose StatementBegin
ALTER TABLE transactions ADD COLUMN note TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- SQLite 不支持 DROP COLUMN（旧版本），这里直接清空并留列；若回滚到更早版本请手工处理。
UPDATE transactions SET note = '';
-- +goose StatementEnd
