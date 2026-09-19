package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	engine "github.com/Jason-skd/stu-trade-engine"
	"github.com/Jason-skd/stu-trade/internal/auth"
	"github.com/Jason-skd/stu-trade/internal/listing"
	orderdomain "github.com/Jason-skd/stu-trade/internal/order"
	"github.com/google/uuid"
)

// === 验证订单 HTTP 契约 ===

func TestOrderHTTPPurchaseProblemsAndParticipantIsolation(t *testing.T) {
	buyer := auth.User{ID: uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"), Email: "buyer@example.com", Nickname: "Buyer"}
	seller := auth.User{ID: uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"), Email: "seller@example.com", Nickname: "Seller"}
	outsider := auth.User{ID: uuid.MustParse("cccccccc-cccc-4ccc-8ccc-cccccccccccc"), Email: "outsider@example.com", Nickname: "Outsider"}
	listingID := uuid.MustParse("dddddddd-dddd-4ddd-8ddd-dddddddddddd")
	now := time.Date(2026, 9, 19, 7, 0, 0, 0, time.UTC)
	sessions := newListingSessionStore(t, buyer, seller, outsider)
	authService := auth.NewService(sessions, time.Now, httpRandom)
	store := newHTTPOrderStore(buyer, seller, outsider, listingID, now)
	ids := []uuid.UUID{
		uuid.MustParse("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"),
		uuid.MustParse("ffffffff-ffff-4fff-8fff-ffffffffffff"),
		uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(),
	}
	service := orderdomain.NewService(store, func() time.Time { return now }, func() uuid.UUID {
		id := ids[0]
		ids = ids[1:]
		return id
	})
	router := NewRouter(
		NewReadiness(&stubDatabasePinger{}, &stubMigrationChecker{}, discardLogger()), nil, nil,
		NewOrderServer(service, authService, discardLogger()),
	)
	path := "/api/v1/sell-listings/" + listingID.String() + "/purchase"
	buyerCookie := sessionCookieName + "=" + sessions.tokens[buyer.ID]
	sellerCookie := sessionCookieName + "=" + sessions.tokens[seller.ID]
	outsiderCookie := sessionCookieName + "=" + sessions.tokens[outsider.ID]

	if response := authRequest(router, http.MethodPost, path, "", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated purchase = (%d, %s)", response.Code, response.Body.String())
	}
	if response := authRequest(router, http.MethodPost, path, "", sellerCookie); response.Code != http.StatusForbidden {
		t.Fatalf("self purchase = (%d, %s)", response.Code, response.Body.String())
	}
	missing := authRequest(router, http.MethodPost, "/api/v1/sell-listings/"+uuid.New().String()+"/purchase", "", buyerCookie)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing purchase = (%d, %s)", missing.Code, missing.Body.String())
	}
	success := authRequest(router, http.MethodPost, path, "", buyerCookie)
	if success.Code != http.StatusCreated || !strings.Contains(success.Body.String(), `"source":"direct_purchase"`) ||
		!strings.Contains(success.Body.String(), `"status":"pending_delivery"`) ||
		!strings.Contains(success.Body.String(), `"email":"seller@example.com"`) {
		t.Fatalf("purchase = (%d, %s)", success.Code, success.Body.String())
	}
	repeated := authRequest(router, http.MethodPost, path, "", outsiderCookie)
	if repeated.Code != http.StatusConflict || !strings.Contains(repeated.Body.String(), `"code":"listing_unavailable"`) {
		t.Fatalf("repeated purchase = (%d, %s)", repeated.Code, repeated.Body.String())
	}

	if response := authRequest(router, http.MethodGet, "/api/v1/orders", "", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated orders = (%d, %s)", response.Code, response.Body.String())
	}
	for name, cookie := range map[string]string{"buyer": buyerCookie, "seller": sellerCookie} {
		response := authRequest(router, http.MethodGet, "/api/v1/orders", "", cookie)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"items":[{`) {
			t.Fatalf("%s orders = (%d, %s)", name, response.Code, response.Body.String())
		}
	}
	outside := authRequest(router, http.MethodGet, "/api/v1/orders", "", outsiderCookie)
	if outside.Code != http.StatusOK || !strings.Contains(outside.Body.String(), `"items":[]`) {
		t.Fatalf("outsider orders = (%d, %s)", outside.Code, outside.Body.String())
	}
	invalid := authRequest(router, http.MethodGet, "/api/v1/orders?limit=0", "", buyerCookie)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"code":"validation_failed"`) {
		t.Fatalf("invalid orders = (%d, %s)", invalid.Code, invalid.Body.String())
	}
}

// === 提供订单 HTTP 存储替身 ===

type httpOrderStore struct {
	listings map[string]engine.ListingRecord
	requests map[string]engine.MatchRequestRecord
	orders   map[uuid.UUID]orderdomain.Order
	details  map[uuid.UUID]listing.Detail
	users    map[uuid.UUID]auth.User
	orderIDs []uuid.UUID
}

type httpOrderTransaction struct {
	store *httpOrderStore
	seed  orderdomain.TransactionSeed
}

func newHTTPOrderStore(buyer, seller, outsider auth.User, listingID uuid.UUID, now time.Time) *httpOrderStore {
	return &httpOrderStore{
		listings: map[string]engine.ListingRecord{listingID.String(): {
			ID: listingID.String(), OwnerID: seller.ID.String(), ResourceRef: listingID.String(),
			Side: engine.SideSell, PriceCents: 1800, Status: engine.ListingStatusOpen,
			CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
		}},
		requests: map[string]engine.MatchRequestRecord{}, orders: map[uuid.UUID]orderdomain.Order{},
		details: map[uuid.UUID]listing.Detail{listingID: {
			ID: listingID, Book: listing.Book{ISBN13: "9780306406157", Title: "Distributed Systems", Author: "Author"},
			Condition: listing.ConditionGood,
		}},
		users: map[uuid.UUID]auth.User{buyer.ID: buyer, seller.ID: seller, outsider.ID: outsider},
	}
}

func (s *httpOrderStore) Transact(_ context.Context, seed orderdomain.TransactionSeed, operation func(engine.DirectTradeTransaction) error) error {
	return operation(&httpOrderTransaction{store: s, seed: seed})
}

func (s *httpOrderStore) ReadListings(_ context.Context, ids []string) ([]engine.ListingRecord, error) {
	result := make([]engine.ListingRecord, 0, len(ids))
	for _, id := range ids {
		if record, ok := s.listings[id]; ok {
			result = append(result, record)
		}
	}
	return result, nil
}

func (s *httpOrderStore) ReadMatchRequests(_ context.Context, ids []string) ([]engine.MatchRequestRecord, error) {
	result := make([]engine.MatchRequestRecord, 0, len(ids))
	for _, id := range ids {
		if record, ok := s.requests[id]; ok {
			result = append(result, record)
		}
	}
	return result, nil
}

func (s *httpOrderStore) FindOrderIDs(_ context.Context, filter orderdomain.OrderFilter) ([]uuid.UUID, error) {
	result := make([]uuid.UUID, 0, len(s.orderIDs))
	for _, id := range s.orderIDs {
		order := s.orders[id]
		request := s.requests[order.MatchRequestID.String()]
		if request.BuyerID == filter.ParticipantID.String() || request.SellerID == filter.ParticipantID.String() {
			result = append(result, id)
		}
	}
	return result, nil
}

func (s *httpOrderStore) ReadOrders(_ context.Context, ids []uuid.UUID) ([]orderdomain.Order, error) {
	result := make([]orderdomain.Order, 0, len(ids))
	for _, id := range ids {
		if order, ok := s.orders[id]; ok {
			result = append(result, order)
		}
	}
	return result, nil
}

func (s *httpOrderStore) ReadDetails(_ context.Context, ids []uuid.UUID) ([]listing.Detail, error) {
	result := make([]listing.Detail, 0, len(ids))
	for _, id := range ids {
		if detail, ok := s.details[id]; ok {
			result = append(result, detail)
		}
	}
	return result, nil
}

func (s *httpOrderStore) ReadUsers(_ context.Context, ids []uuid.UUID) ([]auth.User, error) {
	result := make([]auth.User, 0, len(ids))
	for _, id := range ids {
		if user, ok := s.users[id]; ok {
			result = append(result, user)
		}
	}
	return result, nil
}

func (tx *httpOrderTransaction) ReadListingsForUpdate(ctx context.Context, ids []string) ([]engine.ListingRecord, error) {
	records, err := tx.store.ReadListings(ctx, ids)
	if len(records) != len(ids) {
		return nil, engine.ErrListingNotFound
	}
	return records, err
}

func (tx *httpOrderTransaction) ValidateMatch(_ context.Context, proposal engine.MatchProposal) (bool, error) {
	if proposal.Buyer.ParticipantID == proposal.Seller.ParticipantID {
		return false, orderdomain.ErrSelfPurchase
	}
	return true, nil
}

func (tx *httpOrderTransaction) CreateMatchRequest(_ context.Context, record engine.MatchRequestRecord) error {
	tx.store.requests[record.ID] = record
	return nil
}

func (tx *httpOrderTransaction) CreateTrade(_ context.Context, input engine.TradeInput) (string, error) {
	request, ok := tx.store.requests[input.MatchRequestID]
	if !ok {
		return "", errors.New("missing match request")
	}
	tx.store.orders[tx.seed.OrderID] = orderdomain.Order{
		ID: tx.seed.OrderID, MatchRequestID: tx.seed.MatchRequestID, Status: orderdomain.OrderStatusPendingDelivery,
		CreatedAt: request.CreatedAt, UpdatedAt: request.CreatedAt,
	}
	tx.store.orderIDs = append(tx.store.orderIDs, tx.seed.OrderID)
	return tx.seed.OrderID.String(), nil
}

func (tx *httpOrderTransaction) UpdateMatchRequest(_ context.Context, record engine.MatchRequestRecord) error {
	tx.store.requests[record.ID] = record
	return nil
}

func (tx *httpOrderTransaction) UpdateListing(_ context.Context, record engine.ListingRecord) error {
	tx.store.listings[record.ID] = record
	return nil
}
