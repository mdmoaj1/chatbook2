-- +goose Up
-- +goose StatementBegin
ALTER TABLE users ADD COLUMN phone_number VARCHAR(20) DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN phone_number;
-- +goose StatementEnd
