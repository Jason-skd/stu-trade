// Package migrations 提供内嵌 PostgreSQL schema migration。
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var files embed.FS

// === 执行数据库迁移 ===

// Up 连接数据库并执行所有待应用的向前 migration。
func Up(ctx context.Context, databaseURL string, logger *slog.Logger) error {
	// === 打开数据库 ===
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open migration database: %w", err)
	}
	defer database.Close()
	if err := database.PingContext(ctx); err != nil {
		return fmt.Errorf("ping migration database: %w", err)
	}

	// === 执行迁移 ===
	provider, err := newProvider(database, logger)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

// Checker 使用 Goose 自身的版本账本检查内嵌 migration 是否全部应用。
type Checker struct {
	provider *goose.Provider
}

// NewChecker 创建复用应用连接池的 migration 状态检查器。
func NewChecker(pool *pgxpool.Pool, logger *slog.Logger) (*Checker, error) {
	database := stdlib.OpenDBFromPool(pool)
	provider, err := newProvider(database, logger)
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("create migration status provider: %w", err)
	}
	return &Checker{provider: provider}, nil
}

// HasPending 返回内嵌 migration 中是否有尚未应用的版本。
func (c *Checker) HasPending(ctx context.Context) (bool, error) {
	pending, err := c.provider.HasPending(ctx)
	if err != nil {
		return false, fmt.Errorf("inspect migration status: %w", err)
	}
	return pending, nil
}

// Close 释放 Checker 使用的 database/sql 适配器，不关闭底层 pgxpool。
func (c *Checker) Close() error {
	return c.provider.Close()
}

// === 创建迁移提供器 ===

func newProvider(database *sql.DB, logger *slog.Logger) (*goose.Provider, error) {
	return goose.NewProvider(goose.DialectPostgres, database, files, goose.WithSlog(logger))
}
