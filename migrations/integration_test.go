//go:build integration

package migrations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestUpAppliesEmbeddedMigrations(t *testing.T) {
	databaseURL := os.Getenv("STUTRADE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("STUTRADE_TEST_DATABASE_URL is required for integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// === 隔离测试 schema ===
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create administration pool: %v", err)
	}
	t.Cleanup(admin.Close)
	schema := "migration_test_" + randomSuffix(t)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})
	testURL := withSearchPath(t, databaseURL, schema)

	// === 执行并验证迁移 ===
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatalf("create test pool: %v", err)
	}
	t.Cleanup(pool.Close)
	checker, err := NewChecker(pool, logger)
	if err != nil {
		t.Fatalf("NewChecker() error = %v", err)
	}
	t.Cleanup(func() { _ = checker.Close() })
	pending, err := checker.HasPending(ctx)
	if err != nil || !pending {
		t.Fatalf("HasPending() before Up = (%t, %v), want (true, nil)", pending, err)
	}
	if err := Up(ctx, testURL, logger); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	pending, err = checker.HasPending(ctx)
	if err != nil || pending {
		t.Fatalf("HasPending() after Up = (%t, %v), want (false, nil)", pending, err)
	}
	var appSchemaExists bool
	if err := pool.QueryRow(ctx, "SELECT to_regclass('app_schema') IS NOT NULL").Scan(&appSchemaExists); err != nil {
		t.Fatalf("inspect app_schema: %v", err)
	}
	if appSchemaExists {
		t.Fatal("app_schema exists; Goose ledger must be the only migration state")
	}
	if err := Up(ctx, testURL, logger); err != nil {
		t.Fatalf("second Up() error = %v", err)
	}
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("generate schema suffix: %v", err)
	}
	return hex.EncodeToString(value)
}

func withSearchPath(t *testing.T, databaseURL, schema string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	if parsed.Host == "" {
		t.Fatal("test database URL has no host")
	}
	return parsed.String()
}
