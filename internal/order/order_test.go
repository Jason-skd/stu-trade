package order

import (
	"context"
	"errors"
	"slices"
	"sort"
	"testing"
	"time"

	engine "github.com/Jason-skd/stu-trade-engine"
	"github.com/Jason-skd/stu-trade/internal/auth"
	"github.com/Jason-skd/stu-trade/internal/listing"
	"github.com/google/uuid"
)

// === 验证主动购买编排 ===

func TestPurchaseCommitsAndRereadsAuthoritativeView(t *testing.T) {
	t.Parallel()
	store, buyer, _, listingID, now := purchaseFixture()
	matchID := uuid.MustParse("dddddddd-dddd-4ddd-8ddd-dddddddddddd")
	orderID := uuid.MustParse("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee")
	ids := []uuid.UUID{matchID, orderID}
	service := NewService(store, func() time.Time { return now }, func() uuid.UUID {
		id := ids[0]
		ids = ids[1:]
		return id
	})

	view, err := service.Purchase(context.Background(), listingID.String(), buyer)
	if err != nil {
		t.Fatalf("Purchase() error = %v", err)
	}
	wantCalls := []string{
		"transact", "read_listings_for_update", "validate_match", "create_match_request",
		"create_trade", "update_match_request", "update_listing", "commit",
	}
	if !slices.Equal(store.calls, wantCalls) {
		t.Fatalf("transaction calls = %v, want %v", store.calls, wantCalls)
	}
	if view.Order.ID != orderID || view.Order.MatchRequestID != matchID || view.Order.Status != OrderStatusPendingDelivery {
		t.Fatalf("order = %+v", view.Order)
	}
	if view.MatchRequest.Status() != engine.MatchRequestStatusCompleted || view.MatchRequest.OrderRef() != orderID.String() {
		t.Fatalf("match request = %+v", view.MatchRequest.Record())
	}
	if view.SellListing.Listing.Status() != engine.ListingStatusMatched || view.Buyer.ID != buyer.ID || view.Seller.ID != store.listingOwner {
		t.Fatalf("view = %+v", view)
	}
	if view.Order.SellerConfirmedCompletionAt != nil || view.Order.BuyerConfirmedCompletionAt != nil {
		t.Fatal("new order unexpectedly has completion confirmations")
	}
}

func TestPurchaseRollsBackEveryWriteFailure(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"validate_match", "create_match_request", "create_trade", "update_match_request", "update_listing"} {
		stage := stage
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			store, buyer, _, listingID, now := purchaseFixture()
			store.failStage = stage
			service := NewService(store, func() time.Time { return now }, sequenceIDs(uuid.New(), uuid.New()))
			_, err := service.Purchase(context.Background(), listingID.String(), buyer)
			if !errors.Is(err, errInjected) {
				t.Fatalf("Purchase() error = %v, want injected failure", err)
			}
			if len(store.requests) != 0 || len(store.orders) != 0 || store.listings[listingID.String()].Status != engine.ListingStatusOpen {
				t.Fatalf("partial state = requests:%d orders:%d listing:%s", len(store.requests), len(store.orders), store.listings[listingID.String()].Status)
			}
			if store.calls[len(store.calls)-1] != "rollback" {
				t.Fatalf("calls = %v", store.calls)
			}
		})
	}
}

func TestPurchaseRejectsSelfMissingAndRepeatedListings(t *testing.T) {
	t.Parallel()
	store, buyer, seller, listingID, now := purchaseFixture()
	service := NewService(store, func() time.Time { return now }, sequenceIDs(
		uuid.New(), uuid.New(), uuid.New(), uuid.New(),
		uuid.New(), uuid.New(), uuid.New(), uuid.New(),
	))

	if _, err := service.Purchase(context.Background(), listingID.String(), seller); !errors.Is(err, ErrSelfPurchase) {
		t.Fatalf("self purchase error = %v", err)
	}
	if len(store.requests) != 0 || store.listings[listingID.String()].Status != engine.ListingStatusOpen {
		t.Fatal("self purchase changed state")
	}
	if _, err := service.Purchase(context.Background(), uuid.New().String(), buyer); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing listing error = %v", err)
	}
	if _, err := service.Purchase(context.Background(), listingID.String(), buyer); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Purchase(context.Background(), listingID.String(), buyer); !errors.Is(err, engine.ErrListingUnavailable) {
		t.Fatalf("repeated purchase error = %v", err)
	}
	if len(store.requests) != 1 || len(store.orders) != 1 {
		t.Fatalf("repeated purchase created requests:%d orders:%d", len(store.requests), len(store.orders))
	}
}

func TestListFiltersParticipantAndValidatesInput(t *testing.T) {
	t.Parallel()
	store, buyer, _, listingID, now := purchaseFixture()
	service := NewService(store, func() time.Time { return now }, sequenceIDs(uuid.New(), uuid.New()))
	if _, err := service.Purchase(context.Background(), listingID.String(), buyer); err != nil {
		t.Fatal(err)
	}
	limit := 1
	page, err := service.List(context.Background(), ListInput{ParticipantID: buyer.ID, Limit: &limit})
	if err != nil || len(page.Items) != 1 || store.lastFilter.ParticipantID != buyer.ID {
		t.Fatalf("List() = (%+v, %v), filter = %+v", page, err, store.lastFilter)
	}
	otherPage, err := service.List(context.Background(), ListInput{ParticipantID: uuid.New()})
	if err != nil || len(otherPage.Items) != 0 {
		t.Fatalf("other participant page = (%+v, %v)", otherPage, err)
	}
	badLimit, badCursor := 0, "bad"
	_, err = service.List(context.Background(), ListInput{Limit: &badLimit, Cursor: &badCursor})
	var validation *ValidationError
	if !errors.As(err, &validation) || len(validation.Fields) != 3 {
		t.Fatalf("validation error = %#v", err)
	}
}

// === 提供订单存储替身 ===

var errInjected = errors.New("injected failure")

type fakeStore struct {
	listings     map[string]engine.ListingRecord
	requests     map[string]engine.MatchRequestRecord
	orders       map[uuid.UUID]Order
	details      map[uuid.UUID]listing.Detail
	users        map[uuid.UUID]auth.User
	orderIDs     []uuid.UUID
	listingOwner uuid.UUID
	calls        []string
	failStage    string
	lastFilter   OrderFilter
}

type fakeTransaction struct {
	store *fakeStore
	seed  TransactionSeed
}

func purchaseFixture() (*fakeStore, auth.User, auth.User, uuid.UUID, time.Time) {
	now := time.Date(2026, 9, 19, 6, 0, 0, 0, time.UTC)
	buyer := auth.User{ID: uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"), Email: "buyer@example.com", Nickname: "Buyer"}
	seller := auth.User{ID: uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"), Email: "seller@example.com", Nickname: "Seller"}
	listingID := uuid.MustParse("cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	return &fakeStore{
		listings: map[string]engine.ListingRecord{listingID.String(): {
			ID: listingID.String(), OwnerID: seller.ID.String(), ResourceRef: listingID.String(),
			Side: engine.SideSell, PriceCents: 2599, Status: engine.ListingStatusOpen,
			CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
		}},
		requests: map[string]engine.MatchRequestRecord{}, orders: map[uuid.UUID]Order{},
		details: map[uuid.UUID]listing.Detail{listingID: {
			ID: listingID, Book: listing.Book{ISBN13: "9780306406157", Title: "Distributed Systems", Author: "Author"},
			Condition: listing.ConditionGood,
		}},
		users: map[uuid.UUID]auth.User{buyer.ID: buyer, seller.ID: seller}, listingOwner: seller.ID,
	}, buyer, seller, listingID, now
}

func (s *fakeStore) Transact(_ context.Context, seed TransactionSeed, operation func(engine.DirectTradeTransaction) error) error {
	s.calls = append(s.calls, "transact")
	listings, requests, orders := cloneListings(s.listings), cloneRequests(s.requests), cloneOrders(s.orders)
	orderIDs := slices.Clone(s.orderIDs)
	if err := operation(&fakeTransaction{store: s, seed: seed}); err != nil {
		s.listings, s.requests, s.orders, s.orderIDs = listings, requests, orders, orderIDs
		s.calls = append(s.calls, "rollback")
		return err
	}
	s.calls = append(s.calls, "commit")
	return nil
}

func (s *fakeStore) ReadListings(_ context.Context, ids []string) ([]engine.ListingRecord, error) {
	result := make([]engine.ListingRecord, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		if record, ok := s.listings[ids[i]]; ok {
			result = append(result, record)
		}
	}
	return result, nil
}

func (s *fakeStore) ReadMatchRequests(_ context.Context, ids []string) ([]engine.MatchRequestRecord, error) {
	result := make([]engine.MatchRequestRecord, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		if record, ok := s.requests[ids[i]]; ok {
			result = append(result, record)
		}
	}
	return result, nil
}

func (s *fakeStore) FindOrderIDs(_ context.Context, filter OrderFilter) ([]uuid.UUID, error) {
	s.lastFilter = filter
	result := make([]uuid.UUID, 0, len(s.orderIDs))
	for _, id := range s.orderIDs {
		order := s.orders[id]
		request := s.requests[order.MatchRequestID.String()]
		if request.BuyerID != filter.ParticipantID.String() && request.SellerID != filter.ParticipantID.String() {
			continue
		}
		if filter.Status != nil && order.Status != *filter.Status {
			continue
		}
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := s.orders[result[i]], s.orders[result[j]]
		if left.CreatedAt.Equal(right.CreatedAt) {
			return result[i].String() > result[j].String()
		}
		return left.CreatedAt.After(right.CreatedAt)
	})
	if len(result) > filter.PageSize {
		result = result[:filter.PageSize]
	}
	return result, nil
}

func (s *fakeStore) ReadOrders(_ context.Context, ids []uuid.UUID) ([]Order, error) {
	result := make([]Order, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		if order, ok := s.orders[ids[i]]; ok {
			result = append(result, order)
		}
	}
	return result, nil
}

func (s *fakeStore) ReadDetails(_ context.Context, ids []uuid.UUID) ([]listing.Detail, error) {
	result := make([]listing.Detail, 0, len(ids))
	for _, id := range ids {
		if detail, ok := s.details[id]; ok {
			result = append(result, detail)
		}
	}
	return result, nil
}

func (s *fakeStore) ReadUsers(_ context.Context, ids []uuid.UUID) ([]auth.User, error) {
	result := make([]auth.User, 0, len(ids))
	for _, id := range ids {
		if user, ok := s.users[id]; ok {
			result = append(result, user)
		}
	}
	return result, nil
}

func (tx *fakeTransaction) ReadListingsForUpdate(_ context.Context, ids []string) ([]engine.ListingRecord, error) {
	tx.store.calls = append(tx.store.calls, "read_listings_for_update")
	result, err := tx.store.ReadListings(context.Background(), ids)
	if len(result) != len(ids) {
		return nil, engine.ErrListingNotFound
	}
	return result, err
}

func (tx *fakeTransaction) ValidateMatch(_ context.Context, proposal engine.MatchProposal) (bool, error) {
	tx.store.calls = append(tx.store.calls, "validate_match")
	if tx.store.failStage == "validate_match" {
		return false, errInjected
	}
	if proposal.Buyer.ParticipantID == proposal.Seller.ParticipantID {
		return false, ErrSelfPurchase
	}
	return true, nil
}

func (tx *fakeTransaction) CreateMatchRequest(_ context.Context, record engine.MatchRequestRecord) error {
	tx.store.calls = append(tx.store.calls, "create_match_request")
	if tx.store.failStage == "create_match_request" {
		return errInjected
	}
	tx.store.requests[record.ID] = record
	return nil
}

func (tx *fakeTransaction) CreateTrade(_ context.Context, input engine.TradeInput) (string, error) {
	tx.store.calls = append(tx.store.calls, "create_trade")
	if tx.store.failStage == "create_trade" {
		return "", errInjected
	}
	request := tx.store.requests[input.MatchRequestID]
	tx.store.orders[tx.seed.OrderID] = Order{
		ID: tx.seed.OrderID, MatchRequestID: tx.seed.MatchRequestID, Status: OrderStatusPendingDelivery,
		CreatedAt: request.CreatedAt, UpdatedAt: request.CreatedAt,
	}
	tx.store.orderIDs = append(tx.store.orderIDs, tx.seed.OrderID)
	return tx.seed.OrderID.String(), nil
}

func (tx *fakeTransaction) UpdateMatchRequest(_ context.Context, record engine.MatchRequestRecord) error {
	tx.store.calls = append(tx.store.calls, "update_match_request")
	if tx.store.failStage == "update_match_request" {
		return errInjected
	}
	tx.store.requests[record.ID] = record
	return nil
}

func (tx *fakeTransaction) UpdateListing(_ context.Context, record engine.ListingRecord) error {
	tx.store.calls = append(tx.store.calls, "update_listing")
	if tx.store.failStage == "update_listing" {
		return errInjected
	}
	tx.store.listings[record.ID] = record
	return nil
}

func sequenceIDs(ids ...uuid.UUID) func() uuid.UUID {
	return func() uuid.UUID {
		id := ids[0]
		ids = ids[1:]
		return id
	}
}

func cloneListings(source map[string]engine.ListingRecord) map[string]engine.ListingRecord {
	result := make(map[string]engine.ListingRecord, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneRequests(source map[string]engine.MatchRequestRecord) map[string]engine.MatchRequestRecord {
	result := make(map[string]engine.MatchRequestRecord, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneOrders(source map[uuid.UUID]Order) map[uuid.UUID]Order {
	result := make(map[uuid.UUID]Order, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
