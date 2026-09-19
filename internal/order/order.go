// Package order 编排主动购买、订单查询与成交结果组装。
package order

import (
	"context"
	"errors"
	"fmt"
	"time"

	engine "github.com/Jason-skd/stu-trade-engine"
	"github.com/Jason-skd/stu-trade/internal/auth"
	"github.com/Jason-skd/stu-trade/internal/listing"
	"github.com/Jason-skd/stu-trade/internal/pagecursor"
	"github.com/google/uuid"
)

// === 定义订单模型 ===

const defaultLimit = 20

var (
	// ErrNotFound 表示目标挂单或成交结果不存在。
	ErrNotFound = errors.New("trade not found")
	// ErrSelfPurchase 表示买方试图购买自己的挂单。
	ErrSelfPurchase = errors.New("self purchase")
)

// OrderStatus 表示宿主订单履约状态。
type OrderStatus string

const (
	OrderStatusPendingDelivery OrderStatus = "pending_delivery"
	OrderStatusCompleted       OrderStatus = "completed"
)

func (s OrderStatus) valid() bool {
	return s == OrderStatusPendingDelivery || s == OrderStatusCompleted
}

// Order 保存成交后的宿主履约状态；成交事实由关联 MatchRequest 持有。
type Order struct {
	// === persistent 字段 ===
	ID                          uuid.UUID
	MatchRequestID              uuid.UUID
	Status                      OrderStatus
	SellerConfirmedCompletionAt *time.Time
	BuyerConfirmedCompletionAt  *time.Time
	CreatedAt                   time.Time
	UpdatedAt                   time.Time
}

// TransactionSeed 提供一次直接成交事务预生成的稳定标识。
type TransactionSeed struct {
	// === runtime 字段 ===
	MatchRequestID uuid.UUID
	OrderID        uuid.UUID
}

// OrderFilter 是参与者订单 ID 查询所需的已校验条件。
type OrderFilter struct {
	// === runtime 字段 ===
	ParticipantID   uuid.UUID
	Status          *OrderStatus
	BeforeCreatedAt *time.Time
	BeforeID        *uuid.UUID
	PageSize        int
}

// ListInput 是当前参与者订单列表的筛选输入。
type ListInput struct {
	// === runtime 字段 ===
	ParticipantID uuid.UUID
	Status        *OrderStatus
	Cursor        *string
	Limit         *int
}

// View 组合订单、Engine 成交事实与宿主资料。
type View struct {
	// === runtime 字段 ===
	Order        Order
	MatchRequest *engine.MatchRequest
	SellListing  listing.View
	Buyer        auth.User
	Seller       auth.User
}

// Page 是稳定 keyset 分页结果。
type Page struct {
	// === runtime 字段 ===
	Items      []View
	NextCursor *string
}

// FieldError 描述一个订单查询字段的稳定校验失败。
type FieldError struct {
	// === runtime 字段 ===
	Field  string
	Code   string
	Detail string
}

// ValidationError 汇总订单查询输入错误。
type ValidationError struct {
	// === runtime 字段 ===
	Fields []FieldError
}

// Error 返回稳定的订单查询校验错误身份。
func (e *ValidationError) Error() string { return "validation failed" }

// Store 定义主动购买与订单查询所需的最小宿主能力。
type Store interface {
	engine.ListingReader
	engine.MatchRequestReader

	Transact(
		context.Context,
		TransactionSeed,
		func(engine.DirectTradeTransaction) error,
	) error

	FindOrderIDs(context.Context, OrderFilter) ([]uuid.UUID, error)
	ReadOrders(context.Context, []uuid.UUID) ([]Order, error)
	ReadDetails(context.Context, []uuid.UUID) ([]listing.Detail, error)
	ReadUsers(context.Context, []uuid.UUID) ([]auth.User, error)
}

// === 执行订单用例 ===

// Service 编排直接成交与成交后的权威结果读取。
type Service struct {
	store Store
	now   func() time.Time
	newID func() uuid.UUID
}

// NewService 创建订单服务；nil 时间或标识生成器使用安全默认值。
func NewService(store Store, now func() time.Time, newID func() uuid.UUID) *Service {
	if now == nil {
		now = time.Now
	}
	if newID == nil {
		newID = uuid.New
	}
	return &Service{store: store, now: now, newID: newID}
}

// Purchase 原子购买一个 open Sell Listing，并在提交后重新读取成交事实。
func (s *Service) Purchase(ctx context.Context, sellListingID string, buyer auth.User) (View, error) {
	listingID, err := uuid.Parse(sellListingID)
	if err != nil || listingID == uuid.Nil {
		return View{}, ErrNotFound
	}
	seed := TransactionSeed{MatchRequestID: s.newID(), OrderID: s.newID()}
	createdAt := s.now().UTC()
	err = s.store.Transact(ctx, seed, func(tx engine.DirectTradeTransaction) error {
		_, purchaseErr := engine.PurchaseSellListing(ctx, boundUnitOfWork{tx: tx}, engine.PurchaseSellListingInput{
			MatchRequestID: seed.MatchRequestID.String(), SellListingID: listingID.String(),
			BuyerID: buyer.ID.String(), CreatedAt: createdAt,
		})
		return purchaseErr
	})
	if err != nil {
		if errors.Is(err, engine.ErrListingNotFound) {
			return View{}, ErrNotFound
		}
		return View{}, err
	}
	views, err := s.readViews(ctx, []uuid.UUID{seed.OrderID})
	if err != nil {
		return View{}, fmt.Errorf("read committed purchase: %w", err)
	}
	return views[0], nil
}

// List 返回当前参与者可见的订单。
func (s *Service) List(ctx context.Context, input ListInput) (Page, error) {
	filter, limit, err := validateList(input)
	if err != nil {
		return Page{}, err
	}
	ids, err := s.store.FindOrderIDs(ctx, filter)
	if err != nil {
		return Page{}, fmt.Errorf("find order ids: %w", err)
	}
	if len(ids) == 0 {
		return Page{Items: []View{}}, nil
	}
	hasMore := len(ids) > limit
	if hasMore {
		ids = ids[:limit]
	}
	items, err := s.readViews(ctx, ids)
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: items}
	if hasMore {
		last := items[len(items)-1].Order
		cursor := pagecursor.Encode(last.CreatedAt, last.ID)
		page.NextCursor = &cursor
	}
	return page, nil
}

type boundUnitOfWork struct {
	// === runtime 字段 ===
	tx engine.DirectTradeTransaction
}

func (u boundUnitOfWork) WithTx(_ context.Context, operation func(engine.DirectTradeTransaction) error) error {
	return operation(u.tx)
}

// === 组装订单视图 ===

func (s *Service) readViews(ctx context.Context, ids []uuid.UUID) ([]View, error) {
	orders, err := s.store.ReadOrders(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read orders: %w", err)
	}
	ordered, err := restoreOrders(ids, orders)
	if err != nil {
		return nil, err
	}

	requestIDs := make([]string, len(ordered))
	for i := range ordered {
		requestIDs[i] = ordered[i].MatchRequestID.String()
	}
	requests, err := engine.ReadMatchRequests(ctx, s.store, requestIDs)
	if err != nil {
		return nil, fmt.Errorf("read order match requests: %w", err)
	}

	listingIDs := make([]string, len(requests))
	detailIDs := make([]uuid.UUID, len(requests))
	userIDs := make([]uuid.UUID, 0, len(requests)*2)
	seenUsers := make(map[uuid.UUID]struct{}, len(requests)*2)
	for i, request := range requests {
		if request.Status() != engine.MatchRequestStatusCompleted || request.OrderRef() != ordered[i].ID.String() {
			return nil, fmt.Errorf("order %s has inconsistent match request", ordered[i].ID)
		}
		listingID, parseErr := uuid.Parse(request.SellListingID())
		if parseErr != nil || listingID == uuid.Nil {
			return nil, fmt.Errorf("order %s has invalid sell listing", ordered[i].ID)
		}
		resourceID, parseErr := uuid.Parse(request.ResourceRef())
		if parseErr != nil || resourceID == uuid.Nil || resourceID != listingID {
			return nil, fmt.Errorf("order %s has inconsistent resource", ordered[i].ID)
		}
		listingIDs[i], detailIDs[i] = listingID.String(), resourceID
		for _, participant := range []string{request.BuyerID(), request.SellerID()} {
			userID, participantErr := uuid.Parse(participant)
			if participantErr != nil || userID == uuid.Nil {
				return nil, fmt.Errorf("order %s has invalid participant", ordered[i].ID)
			}
			if _, seen := seenUsers[userID]; !seen {
				seenUsers[userID] = struct{}{}
				userIDs = append(userIDs, userID)
			}
		}
	}

	listings, err := engine.ReadListings(ctx, s.store, listingIDs)
	if err != nil {
		return nil, fmt.Errorf("read order listings: %w", err)
	}
	details, err := s.store.ReadDetails(ctx, detailIDs)
	if err != nil {
		return nil, fmt.Errorf("read order details: %w", err)
	}
	detailByID, err := indexDetails(details)
	if err != nil {
		return nil, err
	}
	users, err := s.store.ReadUsers(ctx, userIDs)
	if err != nil {
		return nil, fmt.Errorf("read order users: %w", err)
	}
	userByID, err := indexUsers(users)
	if err != nil {
		return nil, err
	}

	views := make([]View, len(ordered))
	for i, order := range ordered {
		request, entity := requests[i], listings[i]
		detail, ok := detailByID[detailIDs[i]]
		if !ok || entity.ID() != detail.ID.String() || entity.OwnerID() != request.SellerID() {
			return nil, fmt.Errorf("order %s has no matching sell listing detail", order.ID)
		}
		buyerID, _ := uuid.Parse(request.BuyerID())
		sellerID, _ := uuid.Parse(request.SellerID())
		buyer, buyerOK := userByID[buyerID]
		seller, sellerOK := userByID[sellerID]
		if !buyerOK || !sellerOK {
			return nil, fmt.Errorf("order %s has missing participant", order.ID)
		}
		views[i] = View{
			Order: order, MatchRequest: request,
			SellListing: listing.View{Listing: entity, Detail: detail, Owner: listing.PublicUser{ID: seller.ID, Nickname: seller.Nickname}},
			Buyer:       buyer, Seller: seller,
		}
	}
	return views, nil
}

func restoreOrders(ids []uuid.UUID, records []Order) ([]Order, error) {
	wanted := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	byID := make(map[uuid.UUID]Order, len(records))
	for _, record := range records {
		if _, ok := wanted[record.ID]; !ok {
			return nil, fmt.Errorf("orders contain unexpected id %s", record.ID)
		}
		if _, duplicate := byID[record.ID]; duplicate {
			return nil, fmt.Errorf("orders contain duplicate id %s", record.ID)
		}
		if err := validateOrder(record); err != nil {
			return nil, fmt.Errorf("restore order %s: %w", record.ID, err)
		}
		byID[record.ID] = record
	}
	result := make([]Order, len(ids))
	for i, id := range ids {
		record, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("read order %s: %w", id, ErrNotFound)
		}
		result[i] = record
	}
	return result, nil
}

func validateOrder(order Order) error {
	if order.ID == uuid.Nil || order.MatchRequestID == uuid.Nil {
		return errors.New("ids must not be nil")
	}
	if !order.Status.valid() {
		return errors.New("status must be pending_delivery or completed")
	}
	if order.CreatedAt.IsZero() || order.UpdatedAt.Before(order.CreatedAt) {
		return errors.New("timestamps are invalid")
	}
	for _, confirmation := range []*time.Time{order.SellerConfirmedCompletionAt, order.BuyerConfirmedCompletionAt} {
		if confirmation != nil && (confirmation.Before(order.CreatedAt) || confirmation.After(order.UpdatedAt)) {
			return errors.New("completion timestamp is invalid")
		}
	}
	if order.Status == OrderStatusCompleted && (order.SellerConfirmedCompletionAt == nil || order.BuyerConfirmedCompletionAt == nil) {
		return errors.New("completed order requires both confirmations")
	}
	return nil
}

func indexDetails(records []listing.Detail) (map[uuid.UUID]listing.Detail, error) {
	result := make(map[uuid.UUID]listing.Detail, len(records))
	for _, record := range records {
		if _, duplicate := result[record.ID]; duplicate {
			return nil, fmt.Errorf("duplicate order detail %s", record.ID)
		}
		result[record.ID] = record
	}
	return result, nil
}

func indexUsers(records []auth.User) (map[uuid.UUID]auth.User, error) {
	result := make(map[uuid.UUID]auth.User, len(records))
	for _, record := range records {
		if _, duplicate := result[record.ID]; duplicate {
			return nil, fmt.Errorf("duplicate order user %s", record.ID)
		}
		result[record.ID] = record
	}
	return result, nil
}

// === 校验订单查询 ===

func validateList(input ListInput) (OrderFilter, int, error) {
	filter := OrderFilter{ParticipantID: input.ParticipantID, Status: input.Status, PageSize: defaultLimit + 1}
	var fields []FieldError
	if input.ParticipantID == uuid.Nil {
		fields = append(fields, FieldError{Field: "participant", Code: "required", Detail: "must not be empty"})
	}
	if input.Status != nil && !input.Status.valid() {
		fields = append(fields, FieldError{Field: "status", Code: "invalid", Detail: "must be pending_delivery or completed"})
	}
	limit := defaultLimit
	if input.Limit != nil {
		limit = *input.Limit
		if limit < 1 || limit > 50 {
			fields = append(fields, FieldError{Field: "limit", Code: "out_of_range", Detail: "must be between 1 and 50"})
		}
	}
	filter.PageSize = limit + 1
	if input.Cursor != nil {
		createdAt, id, err := pagecursor.Decode(*input.Cursor)
		if err != nil {
			fields = append(fields, FieldError{Field: "cursor", Code: "invalid", Detail: "must be a continuation cursor returned by this API"})
		} else {
			filter.BeforeCreatedAt, filter.BeforeID = &createdAt, &id
		}
	}
	if len(fields) != 0 {
		return OrderFilter{}, 0, &ValidationError{Fields: fields}
	}
	return filter, limit, nil
}
