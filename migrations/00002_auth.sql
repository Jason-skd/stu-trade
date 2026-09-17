-- +goose Up
CREATE TABLE users (
    id uuid PRIMARY KEY,
    email text NOT NULL UNIQUE,
    nickname text NOT NULL,
    password_hash text NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE TABLE sessions (
    token_digest bytea PRIMARY KEY CHECK (octet_length(token_digest) = 32),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- +goose Down
DROP TABLE sessions;
DROP TABLE users;
