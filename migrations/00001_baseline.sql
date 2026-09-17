-- +goose Up
CREATE TABLE app_schema (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    version BIGINT NOT NULL CHECK (version > 0)
);

INSERT INTO app_schema (version) VALUES (1);

-- +goose Down
DROP TABLE app_schema;
