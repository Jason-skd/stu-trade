//go:build integration

package order

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

// === 验证主动购买数据库语义 ===

func TestPostgresPurchaseAtomicityConcurrencyAndVisibility(t *testing.T) {
	ctx, pool := orderTestDatabase(t)
	store := NewPostgresStore(pool)
	now := time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC)
	seller := orderTestUser(t, ctx, pool, "seller@example.com")
	buyerOne := orderTestUser(t, ctx, pool, "buyer-one@example.com")
	buyerTwo := orderTestUser(t, ctx, pool, "buyer-two@example.com")
	outsider := orderTestUser(t, ctx, pool, "outsider@example.com")

	listingID := orderTestListing(t, ctx, pool, seller.ID, now.Add(-time.Hour))
	service := NewService(store, func() time.Time { return now }, nil)
	view, err := service.Purchase(ctx, listingID.String(), buyerOne)
	if err != nil {
		t.Fatalf("Purchase() error = %v", err)
	}
	if view.MatchRequest.Status() != engine.MatchRequestStatusCompleted || view.Order.Status != OrderStatusPendingDelivery ||
		view.Buyer.ID != buyerOne.ID || view.Seller.ID != seller.ID || view.SellListing.Listing.Status() != engine.ListingStatusMatched {
		t.Fatalf("purchase view = %+v", view)
	}
	if _, err := service.Purchase(ctx, listingID.String(), buyerOne); !errors.Is(err, engine.ErrListingUnavailable) {
		t.Fatalf("repeated purchase error = %v", err)
	}
	assertOrderCounts(t, ctx, pool, listingID, 1, 1)
	for _, participant := range []auth.User{buyerOne, seller} {
		page, listErr := service.List(ctx, ListInput{ParticipantID: participant.ID})
		if listErr != nil || len(page.Items) != 1 || page.Items[0].Order.ID != view.Order.ID {
			t.Fatalf("participant %s orders = (%+v, %v)", participant.ID, page, listErr)
		}
	}
	page, err := service.List(ctx, ListInput{ParticipantID: outsider.ID})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("outsider orders = (%+v, %v)", page, err)
	}

	rollbackListing := orderTestListing(t, ctx, pool, seller.ID, now.Add(-time.Hour))
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_listing_match() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'late failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER reject_listing_match BEFORE UPDATE ON listings FOR EACH ROW WHEN (NEW.id = '`+rollbackListing.String()+`') EXECUTE FUNCTION reject_listing_match()`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Purchase(ctx, rollbackListing.String(), buyerOne); err == nil {
		t.Fatal("purchase with late database failure succeeded")
	}
	assertOrderCounts(t, ctx, pool, rollbackListing, 0, 0)
	var rollbackStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM listings WHERE id = $1`, rollbackListing).Scan(&rollbackStatus); err != nil || rollbackStatus != "open" {
		t.Fatalf("rolled-back listing status = %q, error = %v", rollbackStatus, err)
	}

	concurrentListing := orderTestListing(t, ctx, pool, seller.ID, now.Add(-time.Hour))
	errorsByBuyer := make(chan error, 2)
	var buyers sync.WaitGroup
	for _, buyer := range []auth.User{buyerOne, buyerTwo} {
		buyer := buyer
		buyers.Add(1)
		go func() {
			defer buyers.Done()
			_, purchaseErr := service.Purchase(ctx, concurrentListing.String(), buyer)
			errorsByBuyer <- purchaseErr
		}()
	}
	buyers.Wait()
	close(errorsByBuyer)
	successes, conflicts := 0, 0
	for purchaseErr := range errorsByBuyer {
		switch {
		case purchaseErr == nil:
			successes++
		case errors.Is(purchaseErr, engine.ErrListingUnavailable):
			conflicts++
		default:
			t.Fatalf("concurrent purchase error = %v", purchaseErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results = %d successes, %d conflicts", successes, conflicts)
	}
	assertOrderCounts(t, ctx, pool, concurrentListing, 1, 1)
}

// === 构造订单测试数据 ===

func orderTestUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email string) auth.User {
	t.Helper()
	user := auth.User{ID: uuid.New(), Email: email, Nickname: email[:len(email)-12]}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, nickname, password_hash, created_at) VALUES ($1, $2, $3, 'hash', now())`, user.ID, user.Email, user.Nickname); err != nil {
		t.Fatal(err)
	}
	return user
}

func orderTestListing(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner uuid.UUID, createdAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO book_listing_details (id, isbn_13, title, author, condition) VALUES ($1, '9780306406157', 'Distributed Systems', 'Author', 'good')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO listings (id, owner_id, side, price_cents, status, created_at, updated_at) VALUES ($1, $2, 'sell', 2599, 'open', $3, $3)`, id, owner, createdAt); err != nil {
		t.Fatal(err)
	}
	return id
}

func assertOrderCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, listingID uuid.UUID, requests, orders int) {
	t.Helper()
	var requestCount, orderCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM match_requests WHERE sell_listing_id = $1`, listingID).Scan(&requestCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orders o JOIN match_requests m ON m.id = o.match_request_id WHERE m.sell_listing_id = $1`, listingID).Scan(&orderCount); err != nil {
		t.Fatal(err)
	}
	if requestCount != requests || orderCount != orders {
		t.Fatalf("listing %s has %d requests and %d orders, want %d and %d", listingID, requestCount, orderCount, requests, orders)
	}
}

// === 创建订单测试数据库 ===

func orderTestDatabase(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	databaseURL := os.Getenv("STUTRADE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("STUTRADE_TEST_DATABASE_URL is required for integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	schema := "order_test_" + hex.EncodeToString(suffix)
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
	if err := migrations.Up(ctx, testURL, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return ctx, pool
}
