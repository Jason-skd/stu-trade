package listing

import (
	"context"
	"errors"
	"fmt"
	"time"

	engine "github.com/Jason-skd/stu-trade-engine"
	"github.com/Jason-skd/stu-trade/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore 使用 sqlc 查询实现宿主存储和 engine 持久化协议。
type PostgresStore struct {
	pool    *pgxpool.Pool
	queries *db.Queries
}

// NewPostgresStore 创建复用应用连接池的挂单存储。
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool, queries: db.New(pool)}
}

// Transact 将宿主详情和 engine 协议调用绑定到同一事务。
func (s *PostgresStore) Transact(ctx context.Context, operation func(Transaction) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin listing transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	adapter := &postgresAdapter{queries: s.queries.WithTx(tx)}
	if err := operation(adapter); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit listing transaction: %w", err)
	}
	return nil
}

// ReadListings 读取 engine Listing 物理记录；合法性由 engine 恢复入口校验。
func (s *PostgresStore) ReadListings(ctx context.Context, ids []string) ([]engine.ListingRecord, error) {
	return (&postgresAdapter{queries: s.queries}).ReadListings(ctx, ids)
}

// FindListingIDs 仅返回符合宿主筛选条件的稳定排序候选标识。
func (s *PostgresStore) FindListingIDs(ctx context.Context, filter CandidateFilter) ([]uuid.UUID, error) {
	params := db.FindListingIDsParams{
		Side:       filter.Side.String(),
		OwnerID:    optionalUUID(filter.OwnerID),
		Condition:  optionalCondition(filter.Condition),
		Isbn13:     optionalString(filter.ISBN13),
		TitleQuery: optionalString(filter.TitleQuery),
		PageSize:   int32(filter.PageSize),
	}
	if filter.BeforeCreatedAt != nil {
		params.BeforeCreatedAt = pgTime(*filter.BeforeCreatedAt)
	}
	params.BeforeID = optionalUUID(filter.BeforeID)
	rows, err := s.queries.FindListingIDs(ctx, params)
	if err != nil {
		return nil, err
	}
	result := make([]uuid.UUID, len(rows))
	for i := range rows {
		result[i] = uuid.UUID(rows[i].Bytes)
	}
	return result, nil
}

// ReadDetails 批量读取宿主书籍资料。
func (s *PostgresStore) ReadDetails(ctx context.Context, ids []uuid.UUID) ([]Detail, error) {
	rows, err := s.queries.ReadBookListingDetails(ctx, pgUUIDs(ids))
	if err != nil {
		return nil, err
	}
	result := make([]Detail, len(rows))
	for i, row := range rows {
		result[i] = Detail{
			ID: uuid.UUID(row.ID.Bytes),
			Book: Book{ISBN13: row.Isbn13, Title: row.Title, Author: row.Author,
				CoverImageURL: textPointer(row.CoverImageUrl)},
			Condition: Condition(row.Condition), Note: textPointer(row.Note),
		}
	}
	return result, nil
}

// ReadUsers 批量读取响应所需的公开用户资料。
func (s *PostgresStore) ReadUsers(ctx context.Context, ids []uuid.UUID) ([]PublicUser, error) {
	rows, err := s.queries.ReadListingOwners(ctx, pgUUIDs(ids))
	if err != nil {
		return nil, err
	}
	result := make([]PublicUser, len(rows))
	for i, row := range rows {
		result[i] = PublicUser{ID: uuid.UUID(row.ID.Bytes), Nickname: row.Nickname}
	}
	return result, nil
}

type postgresAdapter struct{ queries *db.Queries }

func (a *postgresAdapter) CreateDetail(ctx context.Context, detail Detail) error {
	return a.queries.CreateBookListingDetail(ctx, db.CreateBookListingDetailParams{
		ID: pgUUID(detail.ID), Isbn13: detail.Book.ISBN13, Title: detail.Book.Title,
		Author: detail.Book.Author, CoverImageUrl: optionalText(detail.Book.CoverImageURL),
		Condition: string(detail.Condition), Note: optionalText(detail.Note),
	})
}

func (a *postgresAdapter) CreateListing(ctx context.Context, entity *engine.Listing) error {
	params, err := createListingParams(entity.Record())
	if err != nil {
		return err
	}
	return a.queries.CreateListing(ctx, params)
}

func (a *postgresAdapter) ReadListings(ctx context.Context, ids []string) ([]engine.ListingRecord, error) {
	parsed, err := parseUUIDStrings(ids)
	if err != nil {
		return nil, err
	}
	rows, err := a.queries.ReadListings(ctx, parsed)
	if err != nil {
		return nil, err
	}
	result := make([]engine.ListingRecord, len(rows))
	for i := range rows {
		result[i] = listingRecord(rows[i])
	}
	return result, nil
}

func (a *postgresAdapter) ReadListingForUpdate(ctx context.Context, id string) (engine.ListingRecord, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return engine.ListingRecord{}, engine.ErrListingNotFound
	}
	row, err := a.queries.ReadListingForUpdate(ctx, pgUUID(parsed))
	if errors.Is(err, pgx.ErrNoRows) {
		return engine.ListingRecord{}, engine.ErrListingNotFound
	}
	if err != nil {
		return engine.ListingRecord{}, err
	}
	return listingRecord(row), nil
}

func (a *postgresAdapter) UpdateListing(ctx context.Context, record engine.ListingRecord) error {
	params, err := updateListingParams(record)
	if err != nil {
		return err
	}
	updated, err := a.queries.UpdateListing(ctx, params)
	if err != nil {
		return err
	}
	if updated != 1 {
		return engine.ErrListingNotFound
	}
	return nil
}

func createListingParams(record engine.ListingRecord) (db.CreateListingParams, error) {
	id, ownerID, resourceRef, err := recordUUIDs(record)
	if err != nil {
		return db.CreateListingParams{}, err
	}
	return db.CreateListingParams{
		ID: pgUUID(id), OwnerID: pgUUID(ownerID), ResourceRef: pgUUID(resourceRef),
		Side: record.Side.String(), PriceCents: record.PriceCents, Status: record.Status.String(),
		CreatedAt: pgTime(record.CreatedAt), UpdatedAt: pgTime(record.UpdatedAt),
	}, nil
}

func updateListingParams(record engine.ListingRecord) (db.UpdateListingParams, error) {
	id, ownerID, resourceRef, err := recordUUIDs(record)
	if err != nil {
		return db.UpdateListingParams{}, err
	}
	return db.UpdateListingParams{
		ID: pgUUID(id), OwnerID: pgUUID(ownerID), ResourceRef: pgUUID(resourceRef),
		Side: record.Side.String(), PriceCents: record.PriceCents, Status: record.Status.String(),
		CreatedAt: pgTime(record.CreatedAt), UpdatedAt: pgTime(record.UpdatedAt),
	}, nil
}

func recordUUIDs(record engine.ListingRecord) (uuid.UUID, uuid.UUID, uuid.UUID, error) {
	id, err := uuid.Parse(record.ID)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, fmt.Errorf("parse listing id: %w", err)
	}
	ownerID, err := uuid.Parse(record.OwnerID)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, fmt.Errorf("parse listing owner id: %w", err)
	}
	resourceRef, err := uuid.Parse(record.ResourceRef)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, fmt.Errorf("parse listing resource ref: %w", err)
	}
	return id, ownerID, resourceRef, nil
}

func listingRecord(row db.Listing) engine.ListingRecord {
	return engine.ListingRecord{
		ID: uuid.UUID(row.ID.Bytes).String(), OwnerID: uuid.UUID(row.OwnerID.Bytes).String(),
		ResourceRef: uuid.UUID(row.ResourceRef.Bytes).String(), Side: parseSide(row.Side),
		PriceCents: row.PriceCents, Status: parseStatus(row.Status),
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
}

func parseSide(value string) engine.Side {
	switch value {
	case engine.SideBuy.String():
		return engine.SideBuy
	case engine.SideSell.String():
		return engine.SideSell
	default:
		return 0
	}
}

func parseStatus(value string) engine.ListingStatus {
	switch value {
	case engine.ListingStatusOpen.String():
		return engine.ListingStatusOpen
	case engine.ListingStatusReserved.String():
		return engine.ListingStatusReserved
	case engine.ListingStatusMatched.String():
		return engine.ListingStatusMatched
	case engine.ListingStatusWithdrawn.String():
		return engine.ListingStatusWithdrawn
	default:
		return 0
	}
}

func parseUUIDStrings(ids []string) ([]pgtype.UUID, error) {
	result := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return nil, fmt.Errorf("parse listing id %q: %w", id, err)
		}
		result[i] = pgUUID(parsed)
	}
	return result, nil
}

func pgUUID(value uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: value, Valid: true} }
func pgUUIDs(values []uuid.UUID) []pgtype.UUID {
	result := make([]pgtype.UUID, len(values))
	for i := range values {
		result[i] = pgUUID(values[i])
	}
	return result
}
func optionalUUID(value *uuid.UUID) pgtype.UUID {
	if value == nil {
		return pgtype.UUID{}
	}
	return pgUUID(*value)
}
func pgTime(value time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: value, Valid: true} }
func optionalCondition(value *Condition) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: string(*value), Valid: true}
}
func optionalString(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}
func optionalText(value *string) pgtype.Text { return optionalString(value) }
func textPointer(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}
