-- +goose Up
ALTER TABLE users
    ADD CONSTRAINT users_email_canonical_check
        CHECK (email = lower(btrim(email)) AND char_length(email) BETWEEN 3 AND 254),
    ADD CONSTRAINT users_nickname_canonical_check
        CHECK (nickname = btrim(nickname) AND char_length(nickname) BETWEEN 1 AND 50),
    ADD CONSTRAINT users_password_hash_not_empty_check
        CHECK (char_length(password_hash) > 0);

ALTER TABLE sessions
    ADD CONSTRAINT sessions_expiry_check CHECK (expires_at > created_at);

-- +goose Down
ALTER TABLE sessions
    DROP CONSTRAINT sessions_expiry_check;

ALTER TABLE users
    DROP CONSTRAINT users_password_hash_not_empty_check,
    DROP CONSTRAINT users_nickname_canonical_check,
    DROP CONSTRAINT users_email_canonical_check;
