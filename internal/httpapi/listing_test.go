package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	engine "github.com/Jason-skd/stu-trade-engine"
	"github.com/Jason-skd/stu-trade/internal/auth"
	listingdomain "github.com/Jason-skd/stu-trade/internal/listing"
	"github.com/google/uuid"
)

// === 验证挂单 HTTP 契约 ===

func TestListingHTTPCreateBrowseAndWithdrawFlow(t *testing.T) {
	owner := auth.User{ID: uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"), Email: "owner@example.com", Nickname: "Owner"}
	stranger := auth.User{ID: uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"), Email: "stranger@example.com", Nickname: "Stranger"}
	sessions := newListingSessionStore(t, owner, stranger)
	authService := auth.NewService(sessions, time.Now, httpRandom)
	store := newHTTPListingStore(owner, stranger)
	now := time.Date(2026, 9, 18, 11, 0, 0, 0, time.UTC)
	nextID := uuid.MustParse("ffffffff-ffff-4fff-8fff-ffffffffffff")
	listingService := listingdomain.NewService(store, func() time.Time { return now }, func() uuid.UUID { return nextID })
	router := NewRouter(
		NewReadiness(&stubDatabasePinger{}, &stubMigrationChecker{}, discardLogger()), nil,
		NewListingServer(listingService, authService, discardLogger()),
	)
	ownerCookie := sessionCookieName + "=" + sessions.tokens[owner.ID]
	strangerCookie := sessionCookieName + "=" + sessions.tokens[stranger.ID]
	body := `{"book":{"isbn":"0-306-40615-2","title":"Distributed Systems","author":"Tanenbaum"},"condition":"good","price":{"amount":2599,"currency":"CNY"},"note":"Clean copy"}`

	unauthenticated := authRequest(router, http.MethodPost, "/api/v1/sell-listings", body, "")
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated create = (%d, %s)", unauthenticated.Code, unauthenticated.Body.String())
	}
	created := authRequest(router, http.MethodPost, "/api/v1/sell-listings", body, ownerCookie)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"isbn":"9780306406157"`) || !strings.Contains(created.Body.String(), `"availableActions":["withdraw"]`) {
		t.Fatalf("create = (%d, %s)", created.Code, created.Body.String())
	}

	publicPage := authRequest(router, http.MethodGet, "/api/v1/sell-listings?q=distributed&condition=good", "", "")
	if publicPage.Code != http.StatusOK || !strings.Contains(publicPage.Body.String(), `"items":[{`) || !strings.Contains(publicPage.Body.String(), `"availableActions":[]`) {
		t.Fatalf("public list = (%d, %s)", publicPage.Code, publicPage.Body.String())
	}
	mineMissing := authRequest(router, http.MethodGet, "/api/v1/sell-listings?mine=true", "", "")
	if mineMissing.Code != http.StatusUnauthorized {
		t.Fatalf("mine without session = (%d, %s)", mineMissing.Code, mineMissing.Body.String())
	}
	mine := authRequest(router, http.MethodGet, "/api/v1/sell-listings?mine=true", "", ownerCookie)
	if mine.Code != http.StatusOK || !strings.Contains(mine.Body.String(), `"availableActions":["withdraw"]`) {
		t.Fatalf("mine = (%d, %s)", mine.Code, mine.Body.String())
	}
	detail := authRequest(router, http.MethodGet, "/api/v1/sell-listings/"+nextID.String(), "", ownerCookie)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"nickname":"Owner"`) {
		t.Fatalf("detail = (%d, %s)", detail.Code, detail.Body.String())
	}
	wrongRoute := authRequest(router, http.MethodGet, "/api/v1/buy-requests/"+nextID.String(), "", "")
	if wrongRoute.Code != http.StatusNotFound {
		t.Fatalf("wrong route = (%d, %s)", wrongRoute.Code, wrongRoute.Body.String())
	}

	forbidden := authRequest(router, http.MethodDelete, "/api/v1/sell-listings/"+nextID.String(), "", strangerCookie)
	if forbidden.Code != http.StatusForbidden || store.records[nextID.String()].Status != engine.ListingStatusOpen {
		t.Fatalf("forbidden = (%d, %s), status = %s", forbidden.Code, forbidden.Body.String(), store.records[nextID.String()].Status.String())
	}
	withdrawn := authRequest(router, http.MethodDelete, "/api/v1/sell-listings/"+nextID.String(), "", ownerCookie)
	again := authRequest(router, http.MethodDelete, "/api/v1/sell-listings/"+nextID.String(), "", ownerCookie)
	if withdrawn.Code != http.StatusNoContent || again.Code != http.StatusNoContent {
		t.Fatalf("withdraw = %d, again = %d", withdrawn.Code, again.Code)
	}
	publicAfter := authRequest(router, http.MethodGet, "/api/v1/sell-listings", "", "")
	if publicAfter.Code != http.StatusOK || !strings.Contains(publicAfter.Body.String(), `"items":[]`) {
		t.Fatalf("public after withdrawal = (%d, %s)", publicAfter.Code, publicAfter.Body.String())
	}
	mineAfter := authRequest(router, http.MethodGet, "/api/v1/sell-listings?mine=true", "", ownerCookie)
	if mineAfter.Code != http.StatusOK || !strings.Contains(mineAfter.Body.String(), `"status":"withdrawn"`) || !strings.Contains(mineAfter.Body.String(), `"availableActions":[]`) {
		t.Fatalf("mine after withdrawal = (%d, %s)", mineAfter.Code, mineAfter.Body.String())
	}
}

func TestListingHTTPBuyMappingAndProblems(t *testing.T) {
	owner := auth.User{ID: uuid.New(), Email: "owner@example.com", Nickname: "Owner"}
	sessions := newListingSessionStore(t, owner)
	authService := auth.NewService(sessions, time.Now, httpRandom)
	store := newHTTPListingStore(owner)
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	listingService := listingdomain.NewService(store, func() time.Time { return now }, uuid.New)
	router := NewRouter(
		NewReadiness(&stubDatabasePinger{}, &stubMigrationChecker{}, discardLogger()), nil,
		NewListingServer(listingService, authService, discardLogger()),
	)
	cookie := sessionCookieName + "=" + sessions.tokens[owner.ID]
	invalid := `{"book":{"isbn":"bad","title":"","author":""},"condition":"worn","maximumPrice":{"amount":0,"currency":"USD"}}`
	response := authRequest(router, http.MethodPost, "/api/v1/buy-requests", invalid, cookie)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"field":"maximumPrice.amount"`) || !strings.Contains(response.Body.String(), `"field":"maximumPrice.currency"`) {
		t.Fatalf("invalid buy = (%d, %s)", response.Code, response.Body.String())
	}
	malformedQuery := authRequest(router, http.MethodGet, "/api/v1/buy-requests?limit=0&cursor=bad", "", "")
	if malformedQuery.Code != http.StatusBadRequest || !strings.Contains(malformedQuery.Body.String(), `"code":"validation_failed"`) {
		t.Fatalf("invalid query = (%d, %s)", malformedQuery.Code, malformedQuery.Body.String())
	}
	missing := authRequest(router, http.MethodDelete, "/api/v1/buy-requests/"+uuid.New().String(), "", cookie)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing delete = (%d, %s)", missing.Code, missing.Body.String())
	}

	reservedID := uuid.New()
	store.details[reservedID] = listingdomain.Detail{ID: reservedID, Book: listingdomain.Book{ISBN13: "9780306406157", Title: "Book", Author: "Author"}, Condition: listingdomain.ConditionGood}
	store.records[reservedID.String()] = httpListingRecord(reservedID, owner.ID, engine.SideBuy, engine.ListingStatusReserved, now)
	conflict := authRequest(router, http.MethodDelete, "/api/v1/buy-requests/"+reservedID.String(), "", cookie)
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), `"code":"listing_unavailable"`) || store.records[reservedID.String()].Status != engine.ListingStatusReserved {
		t.Fatalf("conflict = (%d, %s)", conflict.Code, conflict.Body.String())
	}
}

// === 提供会话存储替身 ===

type listingSessionStore struct {
	users       map[string]auth.User
	tokens      map[uuid.UUID]string
	pendingUser auth.User
}

func newListingSessionStore(t *testing.T, users ...auth.User) *listingSessionStore {
	t.Helper()
	store := &listingSessionStore{users: map[string]auth.User{}, tokens: map[uuid.UUID]string{}}
	for i, user := range users {
		store.pendingUser = user
		random := func(value []byte) (int, error) {
			for j := range value {
				value[j] = byte(i + j + 1)
			}
			return len(value), nil
		}
		session, err := auth.NewService(store, time.Now, random).Signup(context.Background(), user.Email, user.Nickname, "password123")
		if err != nil {
			t.Fatal(err)
		}
		store.tokens[user.ID] = session.Token
	}
	store.pendingUser = auth.User{}
	return store
}
func (s *listingSessionStore) CreateUserSession(_ context.Context, _ auth.StoredUser, digest []byte, _ time.Time, _ time.Time) error {
	s.users[string(digest)] = s.pendingUser
	return nil
}
func (*listingSessionStore) FindUserByEmail(context.Context, string) (auth.StoredUser, error) {
	return auth.StoredUser{}, auth.ErrUnauthenticated
}
func (*listingSessionStore) CreateSession(context.Context, uuid.UUID, []byte, time.Time, time.Time) error {
	return errors.New("not implemented")
}
func (s *listingSessionStore) FindSessionUser(_ context.Context, digest []byte, _ time.Time) (auth.User, error) {
	user, ok := s.users[string(digest)]
	if !ok {
		return auth.User{}, auth.ErrUnauthenticated
	}
	return user, nil
}
func (*listingSessionStore) DeleteSession(context.Context, []byte) error { return nil }

// === 提供挂单存储替身 ===

type httpListingStore struct {
	records map[string]engine.ListingRecord
	details map[uuid.UUID]listingdomain.Detail
	users   map[uuid.UUID]listingdomain.PublicUser
}

func newHTTPListingStore(users ...auth.User) *httpListingStore {
	store := &httpListingStore{records: map[string]engine.ListingRecord{}, details: map[uuid.UUID]listingdomain.Detail{}, users: map[uuid.UUID]listingdomain.PublicUser{}}
	for _, user := range users {
		store.users[user.ID] = listingdomain.PublicUser{ID: user.ID, Nickname: user.Nickname}
	}
	return store
}
func (s *httpListingStore) Transact(_ context.Context, operation func(listingdomain.Transaction) error) error {
	return operation(s)
}
func (s *httpListingStore) CreateDetail(_ context.Context, detail listingdomain.Detail) error {
	s.details[detail.ID] = detail
	return nil
}
func (s *httpListingStore) CreateListing(_ context.Context, entity *engine.Listing) error {
	s.records[entity.ID()] = entity.Record()
	return nil
}
func (s *httpListingStore) ReadListings(_ context.Context, ids []string) ([]engine.ListingRecord, error) {
	result := make([]engine.ListingRecord, 0, len(ids))
	for _, id := range ids {
		if record, ok := s.records[id]; ok {
			result = append(result, record)
		}
	}
	return result, nil
}
func (s *httpListingStore) ReadListingForUpdate(_ context.Context, id string) (engine.ListingRecord, error) {
	record, ok := s.records[id]
	if !ok {
		return engine.ListingRecord{}, engine.ErrListingNotFound
	}
	return record, nil
}
func (s *httpListingStore) UpdateListing(_ context.Context, record engine.ListingRecord) error {
	s.records[record.ID] = record
	return nil
}
func (s *httpListingStore) FindListingIDs(_ context.Context, filter listingdomain.CandidateFilter) ([]uuid.UUID, error) {
	result := make([]uuid.UUID, 0, len(s.records))
	for id, record := range s.records {
		parsed := uuid.MustParse(id)
		detail := s.details[parsed]
		if record.Side != filter.Side || (filter.OwnerID == nil && record.Status != engine.ListingStatusOpen) || (filter.OwnerID != nil && record.OwnerID != filter.OwnerID.String()) {
			continue
		}
		if filter.Condition != nil && detail.Condition != *filter.Condition {
			continue
		}
		if filter.ISBN13 != nil && detail.Book.ISBN13 != *filter.ISBN13 {
			continue
		}
		if filter.TitleQuery != nil && !strings.Contains(strings.ToLower(detail.Book.Title), strings.ToLower(strings.ReplaceAll(*filter.TitleQuery, `\`, ""))) {
			continue
		}
		if filter.BeforeCreatedAt != nil && !(record.CreatedAt.Before(*filter.BeforeCreatedAt) || (record.CreatedAt.Equal(*filter.BeforeCreatedAt) && parsed.String() < filter.BeforeID.String())) {
			continue
		}
		result = append(result, parsed)
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := s.records[result[i].String()], s.records[result[j].String()]
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
func (s *httpListingStore) ReadDetails(_ context.Context, ids []uuid.UUID) ([]listingdomain.Detail, error) {
	result := make([]listingdomain.Detail, 0, len(ids))
	for _, id := range ids {
		if detail, ok := s.details[id]; ok {
			result = append(result, detail)
		}
	}
	return result, nil
}
func (s *httpListingStore) ReadUsers(_ context.Context, ids []uuid.UUID) ([]listingdomain.PublicUser, error) {
	result := make([]listingdomain.PublicUser, 0, len(ids))
	for _, id := range ids {
		if user, ok := s.users[id]; ok {
			result = append(result, user)
		}
	}
	return result, nil
}
func httpListingRecord(id, ownerID uuid.UUID, side engine.Side, status engine.ListingStatus, now time.Time) engine.ListingRecord {
	return engine.ListingRecord{ID: id.String(), OwnerID: ownerID.String(), ResourceRef: id.String(), Side: side, PriceCents: 1000, Status: status, CreatedAt: now, UpdatedAt: now}
}
