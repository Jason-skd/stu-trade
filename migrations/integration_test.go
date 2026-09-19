//go:build integration

package migrations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// === 验证迁移数据库语义 ===

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

	// === 验证直接成交物理约束 ===
	var redundantColumnExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'listings' AND column_name = 'resource_ref'
		)
	`).Scan(&redundantColumnExists); err != nil {
		t.Fatalf("inspect listings.resource_ref: %v", err)
	}
	if redundantColumnExists {
		t.Fatal("listings.resource_ref still exists")
	}
	wantConstraints := []string{
		"match_requests_completion_check",
		"match_requests_order_pair_fkey",
		"match_requests_source_listings_check",
		"match_requests_status_check",
		"orders_match_request_id_key",
		"orders_status_check",
	}
	rows, err := pool.Query(ctx, `
		SELECT conname
		FROM pg_constraint
		WHERE connamespace = current_schema()::regnamespace
		  AND conname = ANY($1::text[])
		ORDER BY conname
	`, wantConstraints)
	if err != nil {
		t.Fatalf("inspect direct purchase constraints: %v", err)
	}
	defer rows.Close()
	var gotConstraints []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan direct purchase constraint: %v", err)
		}
		gotConstraints = append(gotConstraints, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate direct purchase constraints: %v", err)
	}
	if !slices.Equal(gotConstraints, wantConstraints) {
		t.Fatalf("direct purchase constraints = %v, want %v", gotConstraints, wantConstraints)
	}
	var orderDeleteAction, requestDeleteAction string
	if err := pool.QueryRow(ctx, `
		SELECT confdeltype::text
		FROM pg_constraint
		WHERE connamespace = current_schema()::regnamespace AND conname = 'orders_match_request_id_fkey'
	`).Scan(&orderDeleteAction); err != nil {
		t.Fatalf("inspect order match request foreign key: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT confdeltype::text
		FROM pg_constraint
		WHERE connamespace = current_schema()::regnamespace AND conname = 'match_requests_order_pair_fkey'
	`).Scan(&requestDeleteAction); err != nil {
		t.Fatalf("inspect match request order foreign key: %v", err)
	}
	if orderDeleteAction != "r" || requestDeleteAction != "r" {
		t.Fatalf("direct purchase delete actions = order:%q request:%q, want restrict", orderDeleteAction, requestDeleteAction)
	}
}

// === 构造迁移测试环境 ===

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
