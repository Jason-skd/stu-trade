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

// === 实现认证存储 ===

// PostgresStore 使用 PostgreSQL 和 sqlc 实现认证持久化。
type PostgresStore struct {
	pool    *pgxpool.Pool
	queries *db.Queries
}

// NewPostgresStore 创建复用应用连接池的认证存储。
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool, queries: db.New(pool)}
}

// CreateUserSession 在一个事务中创建用户及其首个会话。
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

// FindUserByEmail 按规范化邮箱读取认证用户。
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

// CreateSession 为现有用户保存一个固定期限会话。
func (s *PostgresStore) CreateSession(ctx context.Context, id uuid.UUID, digest []byte, expiresAt, createdAt time.Time) error {
	if err := s.queries.CreateSession(ctx, db.CreateSessionParams{TokenDigest: digest, UserID: pgUUID(id), ExpiresAt: pgTime(expiresAt), CreatedAt: pgTime(createdAt)}); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// FindSessionUser 读取尚未过期的会话用户。
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

// DeleteSession 幂等删除会话摘要对应的记录。
func (s *PostgresStore) DeleteSession(ctx context.Context, digest []byte) error {
	if err := s.queries.DeleteSession(ctx, digest); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// === 转换数据库值 ===

func pgUUID(value uuid.UUID) pgtype.UUID        { return pgtype.UUID{Bytes: value, Valid: true} }
func pgTime(value time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: value, Valid: true} }
func translateDBError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "users_email_key" {
		return ErrEmailTaken
	}
	return fmt.Errorf("%s: %w", operation, err)
}
