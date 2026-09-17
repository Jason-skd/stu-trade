-- name: CreateUser :one
INSERT INTO users (id, email, nickname, password_hash, created_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, email, nickname, password_hash, created_at;

-- name: FindUserByEmail :one
SELECT id, email, nickname, password_hash, created_at
FROM users
WHERE email = $1;

-- name: CreateSession :exec
INSERT INTO sessions (token_digest, user_id, expires_at, created_at)
VALUES ($1, $2, $3, $4);

-- name: FindSessionUser :one
SELECT u.id, u.email, u.nickname, s.expires_at
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_digest = $1 AND s.expires_at > $2;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_digest = $1;
