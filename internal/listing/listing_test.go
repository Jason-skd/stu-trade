package listing

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	engine "github.com/Jason-skd/stu-trade-engine"
	"github.com/google/uuid"
)

func TestNormalizeISBN(t *testing.T) {
	tests := []struct {
		name, input, want string
		valid             bool
	}{
		{name: "ISBN-10", input: "0-306-40615-2", want: "9780306406157", valid: true},
		{name: "ISBN-10 X", input: " 0 8044 2957 X ", want: "9780804429573", valid: true},
		{name: "ISBN-13", input: "978-0-306-40615-7", want: "9780306406157", valid: true},
		{name: "bad checksum", input: "9780306406158"},
		{name: "EAN without book prefix", input: "4006381333931"},
		{name: "bad character", input: "97803064061A7"},
		{name: "bad length", input: "123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeISBN(tt.input)
			if tt.valid && (err != nil || got != tt.want) {
				t.Fatalf("NormalizeISBN() = (%q, %v), want (%q, nil)", got, err, tt.want)
			}
			if !tt.valid && err == nil {
				t.Fatalf("NormalizeISBN() = %q, want error", got)
			}
		})
	}
}

func TestCreatePublishesNormalizedListingAtomically(t *testing.T) {
	store := newFakeStore()
	id := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	owner := PublicUser{ID: uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"), Nickname: "Owner"}
	now := time.Date(2026, 9, 18, 8, 0, 0, 0, time.UTC)
	service := NewService(store, func() time.Time { return now }, func() uuid.UUID { return id })
	note, cover := "  clean copy  ", "https://example.com/cover.jpg"
	view, err := service.Create(context.Background(), owner, CreateInput{
		Book:      Book{ISBN13: "0-306-40615-2", Title: "  Book  ", Author: "  Author ", CoverImageURL: &cover},
		Condition: ConditionGood, Note: &note, PriceCents: 2599, Currency: "CNY", Side: engine.SideSell,
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Detail.ID != id || view.Detail.Book.ISBN13 != "9780306406157" || view.Detail.Book.Title != "Book" || *view.Detail.Note != "clean copy" {
		t.Fatalf("view detail = %#v", view.Detail)
	}
	record := store.records[id.String()]
	if record.ID != id.String() || record.ResourceRef != id.String() || record.OwnerID != owner.ID.String() || record.Side != engine.SideSell || record.PriceCents != 2599 || record.Status != engine.ListingStatusOpen {
		t.Fatalf("record = %#v", record)
	}

	store.createListingErr = errors.New("engine write failed")
	rollbackID := uuid.MustParse("cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	service.newID = func() uuid.UUID { return rollbackID }
	if _, err := service.Create(context.Background(), owner, CreateInput{
		Book:      Book{ISBN13: "9780306406157", Title: "Book", Author: "Author"},
		Condition: ConditionGood, PriceCents: 100, Currency: "CNY", Side: engine.SideBuy,
	}); err == nil {
		t.Fatal("Create() succeeded with engine write failure")
	}
	if _, exists := store.details[rollbackID]; exists {
		t.Fatal("detail remained after transaction rollback")
	}
}

func TestCreateValidation(t *testing.T) {
	service := NewService(newFakeStore(), time.Now, uuid.New)
	empty := " "
	_, err := service.Create(context.Background(), PublicUser{ID: uuid.New()}, CreateInput{
		Book:      Book{ISBN13: "bad", Title: " ", Author: "", CoverImageURL: &empty},
		Condition: "unknown", PriceCents: 0, Currency: "USD",
	})
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("Create() error = %v", err)
	}
	want := []string{"book.isbn", "book.title", "book.author", "book.coverImageUrl", "condition", "price.amount", "price.currency", "side"}
	got := make([]string, len(validation.Fields))
	for i := range validation.Fields {
		got[i] = validation.Fields[i].Field
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
}

func TestListRestoresBatchInCandidateOrderAndBuildsCursor(t *testing.T) {
	store := newFakeStore()
	ownerID := uuid.MustParse("dddddddd-dddd-4ddd-8ddd-dddddddddddd")
	store.users[ownerID] = PublicUser{ID: ownerID, Nickname: "Owner"}
	created := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)
	ids := []uuid.UUID{
		uuid.MustParse("ffffffff-ffff-4fff-8fff-ffffffffffff"),
		uuid.MustParse("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"),
		uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
	}
	for i, id := range ids {
		store.details[id] = Detail{ID: id, Book: Book{ISBN13: "9780306406157", Title: "Go % Book", Author: "Author"}, Condition: ConditionGood}
		store.records[id.String()] = listingRecordForTest(id, ownerID, engine.SideSell, engine.ListingStatusOpen, created.Add(-time.Duration(i)*time.Minute))
	}
	store.candidateIDs = ids
	query, limit := " Go % ", 2
	condition := ConditionGood
	page, err := NewService(store, time.Now, uuid.New).List(context.Background(), ListInput{
		Side: engine.SideSell, Query: &query, Condition: &condition, Limit: &limit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].Detail.ID != ids[0] || page.Items[1].Detail.ID != ids[1] || page.NextCursor == nil {
		t.Fatalf("page = %#v", page)
	}
	if store.lastFilter.TitleQuery == nil || *store.lastFilter.TitleQuery != `Go \%` || store.lastFilter.PageSize != 3 {
		t.Fatalf("filter = %#v", store.lastFilter)
	}
	store.candidateIDs = nil
	_, err = NewService(store, time.Now, uuid.New).List(context.Background(), ListInput{
		Side: engine.SideSell, Cursor: page.NextCursor,
	})
	if err != nil || store.lastFilter.BeforeCreatedAt == nil || !store.lastFilter.BeforeCreatedAt.Equal(page.Items[1].Listing.CreatedAt()) || *store.lastFilter.BeforeID != ids[1] {
		t.Fatalf("decoded cursor filter = %#v, error = %v", store.lastFilter, err)
	}
}

func TestListNormalizesISBNSearchAndRejectsPaginationErrors(t *testing.T) {
	store := newFakeStore()
	service := NewService(store, time.Now, uuid.New)
	query := "0-306-40615-2"
	if _, err := service.List(context.Background(), ListInput{Side: engine.SideBuy, Query: &query}); err != nil {
		t.Fatal(err)
	}
	if store.lastFilter.ISBN13 == nil || *store.lastFilter.ISBN13 != "9780306406157" {
		t.Fatalf("ISBN filter = %#v", store.lastFilter)
	}
	badCursor, badLimit := "not-a-cursor", 51
	_, err := service.List(context.Background(), ListInput{Side: engine.SideBuy, Cursor: &badCursor, Limit: &badLimit})
	var validation *ValidationError
	if !errors.As(err, &validation) || len(validation.Fields) != 2 {
		t.Fatalf("List() error = %#v", err)
	}
	badISBN := "9780306406158"
	_, err = service.List(context.Background(), ListInput{Side: engine.SideBuy, Query: &badISBN})
	if !errors.As(err, &validation) || len(validation.Fields) != 1 || validation.Fields[0].Field != "q" {
		t.Fatalf("invalid ISBN query error = %#v", err)
	}
}

func TestPublicListRechecksRestoredOpenStateWithoutLosingCursor(t *testing.T) {
	store := newFakeStore()
	ownerID := uuid.New()
	store.users[ownerID] = PublicUser{ID: ownerID, Nickname: "Owner"}
	created := time.Date(2026, 9, 18, 9, 30, 0, 0, time.UTC)
	ids := []uuid.UUID{uuid.New(), uuid.New()}
	for i, id := range ids {
		store.details[id] = Detail{ID: id}
		store.records[id.String()] = listingRecordForTest(id, ownerID, engine.SideSell, engine.ListingStatusWithdrawn, created.Add(-time.Duration(i)*time.Minute))
	}
	store.candidateIDs = ids
	limit := 1
	page, err := NewService(store, time.Now, uuid.New).List(context.Background(), ListInput{Side: engine.SideSell, Limit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 || page.NextCursor == nil {
		t.Fatalf("page = %#v, want empty page with continuation", page)
	}
	ownerPage, err := NewService(store, time.Now, uuid.New).List(context.Background(), ListInput{Side: engine.SideSell, OwnerID: &ownerID, Limit: &limit})
	if err != nil || len(ownerPage.Items) != 1 || ownerPage.Items[0].Listing.Status() != engine.ListingStatusWithdrawn {
		t.Fatalf("owner page = %#v, error = %v", ownerPage, err)
	}
}

func TestGetDetectsMissingAndCorruptRecords(t *testing.T) {
	store := newFakeStore()
	service := NewService(store, time.Now, uuid.New)
	id := uuid.New()
	if _, err := service.Get(context.Background(), id.String(), engine.SideSell); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing error = %v", err)
	}
	ownerID := uuid.New()
	store.details[id] = Detail{ID: id}
	store.users[ownerID] = PublicUser{ID: ownerID}
	record := listingRecordForTest(id, ownerID, engine.SideSell, engine.ListingStatusOpen, time.Now().UTC())
	record.Status = 0
	store.records[id.String()] = record
	if _, err := service.Get(context.Background(), id.String(), engine.SideSell); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("corrupt error = %v", err)
	}
}

func TestWithdrawEnforcesSideOwnershipStateAndIdempotence(t *testing.T) {
	store := newFakeStore()
	ownerID, strangerID := uuid.New(), uuid.New()
	id := uuid.New()
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	store.records[id.String()] = listingRecordForTest(id, ownerID, engine.SideSell, engine.ListingStatusOpen, now)
	service := NewService(store, func() time.Time { return now.Add(time.Minute) }, uuid.New)
	if err := service.Withdraw(context.Background(), id.String(), ownerID, engine.SideBuy); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-side error = %v", err)
	}
	if err := service.Withdraw(context.Background(), id.String(), strangerID, engine.SideSell); !errors.Is(err, engine.ErrListingNotOwned) {
		t.Fatalf("wrong-owner error = %v", err)
	}
	if store.records[id.String()].Status != engine.ListingStatusOpen {
		t.Fatal("unauthorized withdrawal changed state")
	}
	if err := service.Withdraw(context.Background(), id.String(), ownerID, engine.SideSell); err != nil {
		t.Fatal(err)
	}
	if store.records[id.String()].Status != engine.ListingStatusWithdrawn {
		t.Fatalf("status = %s", store.records[id.String()].Status.String())
	}
	if err := service.Withdraw(context.Background(), id.String(), ownerID, engine.SideSell); err != nil {
		t.Fatalf("idempotent withdrawal error = %v", err)
	}

	reservedID := uuid.New()
	store.records[reservedID.String()] = listingRecordForTest(reservedID, ownerID, engine.SideSell, engine.ListingStatusReserved, now)
	if err := service.Withdraw(context.Background(), reservedID.String(), ownerID, engine.SideSell); !errors.Is(err, engine.ErrInvalidTransition) {
		t.Fatalf("reserved error = %v", err)
	}
	if store.records[reservedID.String()].Status != engine.ListingStatusReserved {
		t.Fatal("reserved withdrawal changed state")
	}
}

type fakeStore struct {
	records          map[string]engine.ListingRecord
	details          map[uuid.UUID]Detail
	users            map[uuid.UUID]PublicUser
	candidateIDs     []uuid.UUID
	lastFilter       CandidateFilter
	createListingErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{records: map[string]engine.ListingRecord{}, details: map[uuid.UUID]Detail{}, users: map[uuid.UUID]PublicUser{}}
}

func (s *fakeStore) Transact(_ context.Context, operation func(Transaction) error) error {
	records, details := cloneRecords(s.records), cloneDetails(s.details)
	if err := operation(s); err != nil {
		s.records, s.details = records, details
		return err
	}
	return nil
}
func (s *fakeStore) CreateDetail(_ context.Context, detail Detail) error {
	s.details[detail.ID] = detail
	return nil
}
func (s *fakeStore) CreateListing(_ context.Context, entity *engine.Listing) error {
	if s.createListingErr != nil {
		return s.createListingErr
	}
	s.records[entity.ID()] = entity.Record()
	return nil
}
func (s *fakeStore) ReadListings(_ context.Context, ids []string) ([]engine.ListingRecord, error) {
	result := make([]engine.ListingRecord, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		if record, ok := s.records[ids[i]]; ok {
			result = append(result, record)
		}
	}
	return result, nil
}
func (s *fakeStore) ReadListingForUpdate(_ context.Context, id string) (engine.ListingRecord, error) {
	record, ok := s.records[id]
	if !ok {
		return engine.ListingRecord{}, engine.ErrListingNotFound
	}
	return record, nil
}
func (s *fakeStore) UpdateListing(_ context.Context, record engine.ListingRecord) error {
	s.records[record.ID] = record
	return nil
}
func (s *fakeStore) FindListingIDs(_ context.Context, filter CandidateFilter) ([]uuid.UUID, error) {
	s.lastFilter = filter
	return append([]uuid.UUID(nil), s.candidateIDs...), nil
}
func (s *fakeStore) ReadDetails(_ context.Context, ids []uuid.UUID) ([]Detail, error) {
	result := make([]Detail, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		if detail, ok := s.details[ids[i]]; ok {
			result = append(result, detail)
		}
	}
	return result, nil
}
func (s *fakeStore) ReadUsers(_ context.Context, ids []uuid.UUID) ([]PublicUser, error) {
	result := make([]PublicUser, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		if user, ok := s.users[ids[i]]; ok {
			result = append(result, user)
		}
	}
	return result, nil
}

func listingRecordForTest(id, ownerID uuid.UUID, side engine.Side, status engine.ListingStatus, createdAt time.Time) engine.ListingRecord {
	return engine.ListingRecord{
		ID: id.String(), OwnerID: ownerID.String(), ResourceRef: id.String(), Side: side,
		PriceCents: 1000, Status: status, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
}
func cloneRecords(source map[string]engine.ListingRecord) map[string]engine.ListingRecord {
	result := make(map[string]engine.ListingRecord, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
func cloneDetails(source map[uuid.UUID]Detail) map[uuid.UUID]Detail {
	result := make(map[uuid.UUID]Detail, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
