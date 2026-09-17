package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Jason-skd/stu-trade/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool    *pgxpool.Pool
	queries *db.Queries
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool, queries: db.New(pool)}
}
func (s *PostgresStore) CreateUserSession(ctx context.Context, user StoredUser, digest []byte, expiresAt, createdAt time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin signup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.queries.WithTx(tx)
	_, err = q.CreateUser(ctx, db.CreateUserParams{ID: pgUUID(user.ID), Email: user.Email, Nickname: user.Nickname, PasswordHash: user.PasswordHash, CreatedAt: pgTime(createdAt)})
	if err != nil {
		return translateDBError("create user", err)
	}
	if err := q.CreateSession(ctx, db.CreateSessionParams{TokenDigest: digest, UserID: pgUUID(user.ID), ExpiresAt: pgTime(expiresAt), CreatedAt: pgTime(createdAt)}); err != nil {
		return fmt.Errorf("create signup session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit signup: %w", err)
	}
	return nil
}
func (s *PostgresStore) FindUserByEmail(ctx context.Context, email string) (StoredUser, error) {
	row, err := s.queries.FindUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredUser{}, ErrUnauthenticated
	}
	if err != nil {
		return StoredUser{}, fmt.Errorf("find user: %w", err)
	}
	return StoredUser{User: User{ID: uuid.UUID(row.ID.Bytes), Email: row.Email, Nickname: row.Nickname}, PasswordHash: row.PasswordHash}, nil
}
func (s *PostgresStore) CreateSession(ctx context.Context, id uuid.UUID, digest []byte, expiresAt, createdAt time.Time) error {
	if err := s.queries.CreateSession(ctx, db.CreateSessionParams{TokenDigest: digest, UserID: pgUUID(id), ExpiresAt: pgTime(expiresAt), CreatedAt: pgTime(createdAt)}); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}
func (s *PostgresStore) FindSessionUser(ctx context.Context, digest []byte, now time.Time) (User, error) {
	row, err := s.queries.FindSessionUser(ctx, db.FindSessionUserParams{TokenDigest: digest, ExpiresAt: pgTime(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUnauthenticated
	}
	if err != nil {
		return User{}, fmt.Errorf("find session: %w", err)
	}
	return User{ID: uuid.UUID(row.ID.Bytes), Email: row.Email, Nickname: row.Nickname}, nil
}
func (s *PostgresStore) DeleteSession(ctx context.Context, digest []byte) error {
	if err := s.queries.DeleteSession(ctx, digest); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
func pgUUID(value uuid.UUID) pgtype.UUID        { return pgtype.UUID{Bytes: value, Valid: true} }
func pgTime(value time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: value, Valid: true} }
func translateDBError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "users_email_key" {
		return ErrEmailTaken
	}
	return fmt.Errorf("%s: %w", operation, err)
}
