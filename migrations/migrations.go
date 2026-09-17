// Package migrations 提供内嵌 PostgreSQL schema migration。
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"strconv"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var files embed.FS

// LatestVersion 返回内嵌 migration 文件声明的最高版本。
func LatestVersion() int64 {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		panic(fmt.Sprintf("read embedded migrations: %v", err))
	}
	var latest int64
	for _, entry := range entries {
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if entry.IsDir() || !ok || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			panic(fmt.Sprintf("parse migration version %q: %v", entry.Name(), err))
		}
		latest = max(latest, version)
	}
	if latest == 0 {
		panic("no embedded migrations")
	}
	return latest
}

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
	provider, err := goose.NewProvider(goose.DialectPostgres, database, files, goose.WithSlog(logger))
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}
