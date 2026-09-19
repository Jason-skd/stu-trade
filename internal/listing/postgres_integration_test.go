//go:build integration

package listing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	engine "github.com/Jason-skd/stu-trade-engine"
	"github.com/Jason-skd/stu-trade/internal/auth"
	"github.com/Jason-skd/stu-trade/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// === 验证挂单数据库语义 ===

func TestPostgresListingLifecyclePaginationAndConstraints(t *testing.T) {
	ctx, pool := listingTestDatabase(t)
	owner := auth.User{ID: uuid.MustParse("11111111-1111-4111-8111-111111111111"), Email: "owner@example.com", Nickname: "Owner"}
	other := auth.User{ID: uuid.MustParse("22222222-2222-4222-8222-222222222222"), Email: "other@example.com", Nickname: "Other"}
	for _, user := range []auth.User{owner, other} {
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, nickname, password_hash, created_at) VALUES ($1, $2, $3, 'hash', $4)`, user.ID, user.Email, user.Nickname, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	ids := []uuid.UUID{
		uuid.MustParse("ffffffff-ffff-4fff-8fff-ffffffffffff"),
		uuid.MustParse("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"),
		uuid.MustParse("dddddddd-dddd-4ddd-8ddd-dddddddddddd"),
		uuid.MustParse("cccccccc-cccc-4ccc-8ccc-cccccccccccc"),
	}
	base := time.Date(2026, 9, 18, 13, 0, 0, 0, time.UTC)
	store := NewPostgresStore(pool)
	next := 0
	service := NewService(store, func() time.Time { return base }, func() uuid.UUID {
		id := ids[next]
		next++
		return id
	})
	create := func(user auth.User, side engine.Side, title string, condition Condition) View {
		t.Helper()
		view, err := service.Create(ctx, PublicUser{ID: user.ID, Nickname: user.Nickname}, CreateInput{
			Book:      Book{ISBN13: "0-306-40615-2", Title: title, Author: "Author"},
			Condition: condition, PriceCents: 1200, Currency: "CNY", Side: side,
		})
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	first := create(owner, engine.SideSell, "100% Go", ConditionGood)
	second := create(owner, engine.SideSell, "Distributed Systems", ConditionGood)
	third := create(other, engine.SideSell, "Operating Systems", ConditionFair)
	buy := create(owner, engine.SideBuy, "100% Go", ConditionGood)

	restored, err := engine.ReadListing(ctx, store, first.Detail.ID.String())
	if err != nil || !sameListingRecord(restored.Record(), first.Listing.Record()) {
		t.Fatalf("restored = %#v, error = %v", restored, err)
	}
	got, err := service.Get(ctx, first.Detail.ID.String(), engine.SideSell)
	if err != nil || got.Detail.Book.ISBN13 != "9780306406157" || got.Owner.Nickname != owner.Nickname {
		t.Fatalf("Get() = (%#v, %v)", got, err)
	}
	if _, err := service.Get(ctx, first.Detail.ID.String(), engine.SideBuy); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-side Get() error = %v", err)
	}

	limit := 2
	page1, err := service.List(ctx, ListInput{Side: engine.SideSell, Limit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Items) != 2 || page1.NextCursor == nil || page1.Items[0].Detail.ID != first.Detail.ID || page1.Items[1].Detail.ID != second.Detail.ID {
		t.Fatalf("page1 = %#v", page1)
	}
	page2, err := service.List(ctx, ListInput{Side: engine.SideSell, Limit: &limit, Cursor: page1.NextCursor})
	if err != nil || len(page2.Items) != 1 || page2.Items[0].Detail.ID != third.Detail.ID || page2.NextCursor != nil {
		t.Fatalf("page2 = %#v, error = %v", page2, err)
	}
	query := "%"
	filtered, err := service.List(ctx, ListInput{Side: engine.SideSell, Query: &query})
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].Detail.ID != first.Detail.ID {
		t.Fatalf("literal title filter = %#v, error = %v", filtered, err)
	}
	isbn := "978-0-306-40615-7"
	condition := ConditionGood
	filtered, err = service.List(ctx, ListInput{Side: engine.SideBuy, Query: &isbn, Condition: &condition})
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].Detail.ID != buy.Detail.ID {
		t.Fatalf("ISBN filter = %#v, error = %v", filtered, err)
	}
	mine, err := service.List(ctx, ListInput{Side: engine.SideSell, OwnerID: &owner.ID})
	if err != nil || len(mine.Items) != 2 {
		t.Fatalf("mine = %#v, error = %v", mine, err)
	}

	service.now = func() time.Time { return base.Add(time.Minute) }
	if err := service.Withdraw(ctx, first.Detail.ID.String(), other.ID, engine.SideSell); !errors.Is(err, engine.ErrListingNotOwned) {
		t.Fatalf("non-owner error = %v", err)
	}
	unchanged, err := engine.ReadListing(ctx, store, first.Detail.ID.String())
	if err != nil || unchanged.Status() != engine.ListingStatusOpen {
		t.Fatalf("non-owner changed listing = (%v, %v)", unchanged, err)
	}

	errorsByWorker := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			errorsByWorker <- service.Withdraw(ctx, first.Detail.ID.String(), owner.ID, engine.SideSell)
		}()
	}
	workers.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		if err != nil {
			t.Fatalf("concurrent withdrawal error = %v", err)
		}
	}
	withdrawn, err := engine.ReadListing(ctx, store, first.Detail.ID.String())
	if err != nil || withdrawn.Status() != engine.ListingStatusWithdrawn || !withdrawn.UpdatedAt().Equal(base.Add(time.Minute)) {
		t.Fatalf("withdrawn = (%#v, %v)", withdrawn, err)
	}

	if _, err := pool.Exec(ctx, `UPDATE listings SET status = 'reserved' WHERE id = $1`, second.Detail.ID); err != nil {
		t.Fatal(err)
	}
	before, err := engine.ReadListing(ctx, store, second.Detail.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Withdraw(ctx, second.Detail.ID.String(), owner.ID, engine.SideSell); !errors.Is(err, engine.ErrInvalidTransition) {
		t.Fatalf("reserved error = %v", err)
	}
	after, err := engine.ReadListing(ctx, store, second.Detail.ID.String())
	if err != nil || !sameListingRecord(after.Record(), before.Record()) {
		t.Fatalf("reserved listing changed = (%#v, %v)", after, err)
	}

	rollbackID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	service.newID = func() uuid.UUID { return rollbackID }
	_, err = service.Create(ctx, PublicUser{ID: uuid.New(), Nickname: "Missing"}, CreateInput{
		Book:      Book{ISBN13: "9780306406157", Title: "Rollback", Author: "Author"},
		Condition: ConditionGood, PriceCents: 100, Currency: "CNY", Side: engine.SideSell,
	})
	if err == nil {
		t.Fatal("engine foreign-key failure succeeded")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM book_listing_details WHERE id = $1`, rollbackID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled-back detail count = %d, error = %v", count, err)
	}

	invalidID := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	if _, err := pool.Exec(ctx, `INSERT INTO book_listing_details (id, isbn_13, title, author, condition) VALUES ($1, '9780306406157', 'Invalid', 'Author', 'good')`, invalidID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO listings (id, owner_id, side, price_cents, status, created_at, updated_at) VALUES ($1, $2, 'sell', 0, 'open', $3, $3)`, invalidID, other.ID, base); err == nil {
		t.Fatal("non-positive price constraint accepted invalid listing")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO book_listing_details (id, isbn_13, title, author, condition) VALUES ($1, '978030640615X', 'Bad ISBN', 'Author', 'good')`, uuid.New()); err == nil {
		t.Fatal("ISBN storage constraint accepted invalid characters")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO book_listing_details (id, isbn_13, title, author, condition) VALUES ($1, '9780306406158', 'Bad checksum', 'Author', 'good')`, uuid.New()); err == nil {
		t.Fatal("ISBN storage constraint accepted an invalid checksum")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO book_listing_details (id, isbn_13, title, author, condition) VALUES ($1, '1234567890128', 'Bad prefix', 'Author', 'good')`, uuid.New()); err == nil {
		t.Fatal("ISBN storage constraint accepted an invalid prefix")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO book_listing_details (id, isbn_13, title, author, condition) VALUES ($1, '9780306406157', ' Padded title ', 'Author', 'good')`, uuid.New()); err == nil {
		t.Fatal("title storage constraint accepted a non-canonical value")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO book_listing_details (id, isbn_13, title, author, condition) VALUES ($1, '9780306406157', 'Title', ' Padded author ', 'good')`, uuid.New()); err == nil {
		t.Fatal("author storage constraint accepted a non-canonical value")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO book_listing_details (id, isbn_13, title, author, cover_image_url, condition) VALUES ($1, '9780306406157', 'Title', 'Author', '/cover.jpg', 'good')`, uuid.New()); err == nil {
		t.Fatal("cover storage constraint accepted a relative URI")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO book_listing_details (id, isbn_13, title, author, condition, note) VALUES ($1, '9780306406157', 'Title', 'Author', 'good', '  ')`, uuid.New()); err == nil {
		t.Fatal("note storage constraint accepted a blank value")
	}
}

// === 比较引擎持久化记录 ===

func sameListingRecord(left, right engine.ListingRecord) bool {
	return left.ID == right.ID && left.OwnerID == right.OwnerID && left.ResourceRef == right.ResourceRef &&
		left.Side == right.Side && left.PriceCents == right.PriceCents && left.Status == right.Status &&
		left.CreatedAt.Equal(right.CreatedAt) && left.UpdatedAt.Equal(right.UpdatedAt)
}

// === 创建挂单测试数据库 ===

func listingTestDatabase(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	databaseURL := os.Getenv("STUTRADE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("STUTRADE_TEST_DATABASE_URL is required for integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	schema := "listing_test_" + hex.EncodeToString(suffix)
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
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	testURL := parsed.String()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := migrations.Up(ctx, testURL, logger); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return ctx, pool
}
