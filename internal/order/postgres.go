package order

import (
	"context"
	"errors"
	"fmt"
	"time"

	engine "github.com/Jason-skd/stu-trade-engine"
	"github.com/Jason-skd/stu-trade/internal/auth"
	"github.com/Jason-skd/stu-trade/internal/db"
	"github.com/Jason-skd/stu-trade/internal/listing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// === 实现订单存储 ===

// PostgresStore 使用 PostgreSQL 事务实现直接成交协议与订单查询。
type PostgresStore struct {
	pool         *pgxpool.Pool
	queries      *db.Queries
	listingStore *listing.PostgresStore
}

// NewPostgresStore 创建复用应用连接池的订单存储。
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool, queries: db.New(pool), listingStore: listing.NewPostgresStore(pool)}
}

// Transact 将 engine 直接成交调用绑定到同一 PostgreSQL 事务。
func (s *PostgresStore) Transact(
	ctx context.Context,
	seed TransactionSeed,
	operation func(engine.DirectTradeTransaction) error,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin direct trade transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)
	adapter := &postgresAdapter{
		PostgresAdapter: listing.NewPostgresAdapter(queries),
		queries:         queries,
		seed:            seed,
	}
	if err := operation(adapter); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit direct trade transaction: %w", err)
	}
	return nil
}

// ReadListings 委托挂单 adapter 恢复所需的物理记录。
func (s *PostgresStore) ReadListings(ctx context.Context, ids []string) ([]engine.ListingRecord, error) {
	return s.listingStore.ReadListings(ctx, ids)
}

// ReadMatchRequests 读取 engine MatchRequest 逻辑记录。
func (s *PostgresStore) ReadMatchRequests(ctx context.Context, ids []string) ([]engine.MatchRequestRecord, error) {
	parsed, err := parseUUIDStrings(ids, "match request")
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ReadMatchRequests(ctx, parsed)
	if err != nil {
		return nil, err
	}
	result := make([]engine.MatchRequestRecord, len(rows))
	for i := range rows {
		result[i] = matchRequestRecord(rows[i])
	}
	return result, nil
}

// FindOrderIDs 返回参与者可见且稳定排序的订单标识。
func (s *PostgresStore) FindOrderIDs(ctx context.Context, filter OrderFilter) ([]uuid.UUID, error) {
	params := db.FindOrderIDsParams{
		ParticipantID: pgUUID(filter.ParticipantID),
		Status:        optionalOrderStatus(filter.Status),
		PageSize:      int32(filter.PageSize),
	}
	if filter.BeforeCreatedAt != nil {
		params.BeforeCreatedAt = pgTime(*filter.BeforeCreatedAt)
	}
	params.BeforeID = optionalUUID(filter.BeforeID)
	rows, err := s.queries.FindOrderIDs(ctx, params)
	if err != nil {
		return nil, err
	}
	result := make([]uuid.UUID, len(rows))
	for i := range rows {
		result[i] = uuid.UUID(rows[i].Bytes)
	}
	return result, nil
}

// ReadOrders 批量读取宿主订单记录。
func (s *PostgresStore) ReadOrders(ctx context.Context, ids []uuid.UUID) ([]Order, error) {
	rows, err := s.queries.ReadOrders(ctx, pgUUIDs(ids))
	if err != nil {
		return nil, err
	}
	result := make([]Order, len(rows))
	for i, row := range rows {
		result[i] = orderRecord(row)
	}
	return result, nil
}

// ReadDetails 批量读取成交书籍资料。
func (s *PostgresStore) ReadDetails(ctx context.Context, ids []uuid.UUID) ([]listing.Detail, error) {
	return s.listingStore.ReadDetails(ctx, ids)
}

// ReadUsers 批量读取订单参与者的完整联系资料。
func (s *PostgresStore) ReadUsers(ctx context.Context, ids []uuid.UUID) ([]auth.User, error) {
	rows, err := s.queries.ReadOrderUsers(ctx, pgUUIDs(ids))
	if err != nil {
		return nil, err
	}
	result := make([]auth.User, len(rows))
	for i, row := range rows {
		result[i] = auth.User{ID: uuid.UUID(row.ID.Bytes), Email: row.Email, Nickname: row.Nickname}
	}
	return result, nil
}

type postgresAdapter struct {
	// === runtime 字段 ===
	*listing.PostgresAdapter
	queries        *db.Queries
	seed           TransactionSeed
	createdRequest *engine.MatchRequestRecord
	tradeInput     *engine.TradeInput
}

// === 适配直接成交协议 ===

func (a *postgresAdapter) ValidateMatch(ctx context.Context, proposal engine.MatchProposal) (bool, error) {
	if proposal.Source != engine.MatchSourceBuyerPurchase {
		return false, errors.New("direct purchase adapter received another match source")
	}
	if proposal.Buyer.ParticipantID == proposal.Seller.ParticipantID {
		return false, ErrSelfPurchase
	}
	if proposal.Buyer.ListingID != "" || proposal.Buyer.ResourceRef != "" ||
		proposal.Seller.ListingID == "" || proposal.Seller.ResourceRef != proposal.Seller.ListingID {
		return false, errors.New("direct purchase proposal is inconsistent")
	}
	buyerID, err := uuid.Parse(proposal.Buyer.ParticipantID)
	if err != nil || buyerID == uuid.Nil {
		return false, errors.New("direct purchase buyer is invalid")
	}
	sellerID, err := uuid.Parse(proposal.Seller.ParticipantID)
	if err != nil || sellerID == uuid.Nil {
		return false, errors.New("direct purchase seller is invalid")
	}
	resourceID, err := uuid.Parse(proposal.Seller.ResourceRef)
	if err != nil || resourceID == uuid.Nil {
		return false, errors.New("direct purchase resource is invalid")
	}
	users, err := a.queries.ReadOrderUsers(ctx, pgUUIDs([]uuid.UUID{buyerID, sellerID}))
	if err != nil {
		return false, fmt.Errorf("read direct trade users: %w", err)
	}
	if len(users) != 2 {
		return false, errors.New("direct purchase participant no longer exists")
	}
	foundUsers := map[uuid.UUID]struct{}{}
	for _, user := range users {
		foundUsers[uuid.UUID(user.ID.Bytes)] = struct{}{}
	}
	if _, ok := foundUsers[buyerID]; !ok {
		return false, errors.New("direct purchase buyer no longer exists")
	}
	if _, ok := foundUsers[sellerID]; !ok {
		return false, errors.New("direct purchase seller no longer exists")
	}
	details, err := a.queries.ReadBookListingDetails(ctx, pgUUIDs([]uuid.UUID{resourceID}))
	if err != nil {
		return false, fmt.Errorf("read direct trade detail: %w", err)
	}
	if len(details) != 1 || uuid.UUID(details[0].ID.Bytes) != resourceID {
		return false, errors.New("direct purchase resource is unavailable")
	}
	return true, nil
}

func (a *postgresAdapter) CreateMatchRequest(ctx context.Context, record engine.MatchRequestRecord) error {
	if record.ID != a.seed.MatchRequestID.String() {
		return errors.New("match request id differs from transaction seed")
	}
	if _, err := engine.RestoreMatchRequest(record); err != nil {
		return err
	}
	params, err := createMatchRequestParams(record)
	if err != nil {
		return err
	}
	if err := a.queries.CreateMatchRequest(ctx, params); err != nil {
		return err
	}
	copy := record
	a.createdRequest = &copy
	return nil
}

func (a *postgresAdapter) CreateTrade(ctx context.Context, input engine.TradeInput) (string, error) {
	if a.createdRequest == nil || !tradeMatchesRequest(input, *a.createdRequest) {
		return "", errors.New("trade input differs from created match request")
	}
	if a.tradeInput != nil {
		if *a.tradeInput != input {
			return "", errors.New("transaction attempted to create different trades")
		}
		return a.seed.OrderID.String(), nil
	}
	createdAt := a.createdRequest.CreatedAt
	err := a.queries.CreateOrder(ctx, db.CreateOrderParams{
		ID: pgUUID(a.seed.OrderID), MatchRequestID: pgUUID(a.seed.MatchRequestID),
		Status: string(OrderStatusPendingDelivery), CreatedAt: pgTime(createdAt), UpdatedAt: pgTime(createdAt),
	})
	if err != nil {
		return "", err
	}
	copy := input
	a.tradeInput = &copy
	return a.seed.OrderID.String(), nil
}

func (a *postgresAdapter) UpdateMatchRequest(ctx context.Context, record engine.MatchRequestRecord) error {
	if a.createdRequest == nil || record.ID != a.createdRequest.ID || record.OrderRef != a.seed.OrderID.String() {
		return errors.New("completed match request differs from transaction seed")
	}
	request, err := engine.RestoreMatchRequest(record)
	if err != nil {
		return err
	}
	if request.Status() != engine.MatchRequestStatusCompleted {
		return errors.New("direct trade match request is not completed")
	}
	params, err := updateMatchRequestParams(record)
	if err != nil {
		return err
	}
	updated, err := a.queries.UpdateMatchRequest(ctx, params)
	if err != nil {
		return err
	}
	if updated != 1 {
		return engine.ErrMatchRequestNotFound
	}
	return nil
}

// === 转换订单记录 ===

func createMatchRequestParams(record engine.MatchRequestRecord) (db.CreateMatchRequestParams, error) {
	values, err := matchRequestValues(record)
	if err != nil {
		return db.CreateMatchRequestParams{}, err
	}
	return db.CreateMatchRequestParams(values), nil
}

func updateMatchRequestParams(record engine.MatchRequestRecord) (db.UpdateMatchRequestParams, error) {
	values, err := matchRequestValues(record)
	if err != nil {
		return db.UpdateMatchRequestParams{}, err
	}
	return db.UpdateMatchRequestParams(values), nil
}

type matchRequestParams struct {
	ID                pgtype.UUID
	BuyerID           pgtype.UUID
	SellerID          pgtype.UUID
	ResourceRef       pgtype.UUID
	BuyListingID      pgtype.UUID
	SellListingID     pgtype.UUID
	TradePriceCents   int64
	Source            string
	BuyerConfirmedAt  pgtype.Timestamptz
	SellerConfirmedAt pgtype.Timestamptz
	Status            string
	OrderRef          pgtype.UUID
	CreatedAt         pgtype.Timestamptz
	UpdatedAt         pgtype.Timestamptz
}

func matchRequestValues(record engine.MatchRequestRecord) (matchRequestParams, error) {
	id, err := uuid.Parse(record.ID)
	if err != nil {
		return matchRequestParams{}, fmt.Errorf("parse match request id: %w", err)
	}
	buyerID, err := uuid.Parse(record.BuyerID)
	if err != nil {
		return matchRequestParams{}, fmt.Errorf("parse match buyer id: %w", err)
	}
	sellerID, err := uuid.Parse(record.SellerID)
	if err != nil {
		return matchRequestParams{}, fmt.Errorf("parse match seller id: %w", err)
	}
	resourceRef, err := uuid.Parse(record.ResourceRef)
	if err != nil {
		return matchRequestParams{}, fmt.Errorf("parse match resource ref: %w", err)
	}
	buyListingID, err := parseOptionalUUIDString(record.BuyListingID, "buy listing id")
	if err != nil {
		return matchRequestParams{}, err
	}
	sellListingID, err := parseOptionalUUIDString(record.SellListingID, "sell listing id")
	if err != nil {
		return matchRequestParams{}, err
	}
	orderRef, err := parseOptionalUUIDString(record.OrderRef, "order ref")
	if err != nil {
		return matchRequestParams{}, err
	}
	return matchRequestParams{
		ID: pgUUID(id), BuyerID: pgUUID(buyerID), SellerID: pgUUID(sellerID), ResourceRef: pgUUID(resourceRef),
		BuyListingID: buyListingID, SellListingID: sellListingID,
		TradePriceCents: record.TradePriceCents, Source: record.Source.String(),
		BuyerConfirmedAt: optionalTime(record.BuyerConfirmedAt), SellerConfirmedAt: optionalTime(record.SellerConfirmedAt),
		Status: record.Status.String(), OrderRef: orderRef,
		CreatedAt: pgTime(record.CreatedAt), UpdatedAt: pgTime(record.UpdatedAt),
	}, nil
}

func matchRequestRecord(row db.MatchRequest) engine.MatchRequestRecord {
	return engine.MatchRequestRecord{
		ID: uuid.UUID(row.ID.Bytes).String(), BuyerID: uuid.UUID(row.BuyerID.Bytes).String(),
		SellerID: uuid.UUID(row.SellerID.Bytes).String(), ResourceRef: uuid.UUID(row.ResourceRef.Bytes).String(),
		BuyListingID: optionalUUIDText(row.BuyListingID), SellListingID: optionalUUIDText(row.SellListingID),
		TradePriceCents: row.TradePriceCents, Source: parseMatchSource(row.Source),
		BuyerConfirmedAt: optionalTimeValue(row.BuyerConfirmedAt), SellerConfirmedAt: optionalTimeValue(row.SellerConfirmedAt),
		Status: parseMatchStatus(row.Status), OrderRef: optionalUUIDText(row.OrderRef),
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
}

func orderRecord(row db.Order) Order {
	return Order{
		ID: uuid.UUID(row.ID.Bytes), MatchRequestID: uuid.UUID(row.MatchRequestID.Bytes), Status: OrderStatus(row.Status),
		SellerConfirmedCompletionAt: timePointer(row.SellerConfirmedCompletionAt),
		BuyerConfirmedCompletionAt:  timePointer(row.BuyerConfirmedCompletionAt),
		CreatedAt:                   row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
}

func tradeMatchesRequest(input engine.TradeInput, record engine.MatchRequestRecord) bool {
	return input.MatchRequestID == record.ID && input.BuyerID == record.BuyerID &&
		input.SellerID == record.SellerID && input.ResourceRef == record.ResourceRef &&
		input.TradePrice.Cents() == record.TradePriceCents && input.Source == record.Source
}

func parseMatchSource(value string) engine.MatchSource {
	switch value {
	case engine.MatchSourceAutomatic.String():
		return engine.MatchSourceAutomatic
	case engine.MatchSourceSellerAccept.String():
		return engine.MatchSourceSellerAccept
	case engine.MatchSourceBuyerPurchase.String():
		return engine.MatchSourceBuyerPurchase
	default:
		return 0
	}
}

func parseMatchStatus(value string) engine.MatchRequestStatus {
	switch value {
	case engine.MatchRequestStatusPending.String():
		return engine.MatchRequestStatusPending
	case engine.MatchRequestStatusCompleted.String():
		return engine.MatchRequestStatusCompleted
	case engine.MatchRequestStatusDeclined.String():
		return engine.MatchRequestStatusDeclined
	case engine.MatchRequestStatusCancelled.String():
		return engine.MatchRequestStatusCancelled
	default:
		return 0
	}
}

// === 转换数据库值 ===

func parseUUIDStrings(ids []string, entity string) ([]pgtype.UUID, error) {
	result := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return nil, fmt.Errorf("parse %s id %q: %w", entity, id, err)
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
func pgTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}
func optionalUUID(value *uuid.UUID) pgtype.UUID {
	if value == nil {
		return pgtype.UUID{}
	}
	return pgUUID(*value)
}
func parseOptionalUUIDString(value, field string) (pgtype.UUID, error) {
	if value == "" {
		return pgtype.UUID{}, nil
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("parse %s: %w", field, err)
	}
	return pgUUID(parsed), nil
}
func optionalUUIDText(value pgtype.UUID) string {
	if !value.Valid {
		return ""
	}
	return uuid.UUID(value.Bytes).String()
}
func optionalTime(value time.Time) pgtype.Timestamptz {
	if value.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgTime(value)
}
func optionalTimeValue(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time
}
func timePointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}
func optionalOrderStatus(value *OrderStatus) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: string(*value), Valid: true}
}
