//go:build integration

package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Jason-skd/stu-trade/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresStorePersistsAuthSafelyAndAtomically(t *testing.T) {
	databaseURL := os.Getenv("STUTRADE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("STUTRADE_TEST_DATABASE_URL is required for integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := "auth_test_" + integrationSuffix(t)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop schema: %v", err)
		}
	})
	testURL := integrationSearchPath(t, databaseURL, schema)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := migrations.Up(ctx, testURL, logger); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := NewPostgresStore(pool)
	now := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	service := NewService(store, func() time.Time { return now }, nil)
	session, err := service.Signup(ctx, "user@example.com", "User", "password123")
	if err != nil {
		t.Fatal(err)
	}
	digest, ok := TokenDigest(session.Token)
	if !ok {
		t.Fatal("issued token is invalid")
	}

	var passwordHash string
	var storedDigest []byte
	if err := pool.QueryRow(ctx, "SELECT u.password_hash, s.token_digest FROM users u JOIN sessions s ON s.user_id = u.id WHERE u.id = $1", session.User.ID).Scan(&passwordHash, &storedDigest); err != nil {
		t.Fatal(err)
	}
	if string(storedDigest) != string(digest) {
		t.Fatalf("stored credentials do not match digests")
	}
	if passwordHash == "password123" || string(storedDigest) == session.Token {
		t.Fatal("plaintext credential stored")
	}

	if _, err := service.Signup(ctx, " USER@example.com ", "Other", "password456"); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("duplicate email error = %v", err)
	}

	rollbackUser := StoredUser{User: User{ID: uuid.New(), Email: "rollback@example.com", Nickname: "Rollback"}, PasswordHash: passwordHash}
	if err := store.CreateUserSession(ctx, rollbackUser, digest, now.Add(SessionLifetime), now); err == nil {
		t.Fatal("session collision succeeded")
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE id = $1", rollbackUser.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled back user count = %d, err = %v", count, err)
	}

	found, err := store.FindSessionUser(ctx, digest, now)
	if err != nil || found.ID != session.User.ID {
		t.Fatalf("FindSessionUser() = (%v, %v)", found, err)
	}
	if _, err := store.FindSessionUser(ctx, digest, now.Add(SessionLifetime)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired session error = %v", err)
	}
	if err := store.DeleteSession(ctx, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FindSessionUser(ctx, digest, now); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("deleted session error = %v", err)
	}
}

func integrationSuffix(t *testing.T) string {
	t.Helper()
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(value)
}

func integrationSearchPath(t *testing.T, databaseURL, schema string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
