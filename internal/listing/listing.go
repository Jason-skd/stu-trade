// Package listing 实现二手书挂单的宿主资料、查询与 engine 编排。
package listing

import (
	"context"
	"errors"
	"fmt"
	"time"

	engine "github.com/Jason-skd/stu-trade-engine"
	"github.com/google/uuid"
)

const defaultLimit = 20

// ErrNotFound 表示请求的宿主挂单不存在或方向不匹配。
var ErrNotFound = errors.New("listing not found")

// Condition 是宿主保存的书籍成色快照。
type Condition string

const (
	ConditionNew     Condition = "new"
	ConditionLikeNew Condition = "like_new"
	ConditionGood    Condition = "good"
	ConditionFair    Condition = "fair"
	ConditionPoor    Condition = "poor"
)

func (c Condition) valid() bool {
	switch c {
	case ConditionNew, ConditionLikeNew, ConditionGood, ConditionFair, ConditionPoor:
		return true
	default:
		return false
	}
}

// Book 是挂单创建时保存的书籍资料快照。
type Book struct {
	ISBN13, Title, Author string
	CoverImageURL         *string
}

// Detail 是与 engine Listing 共享标识的宿主资料。
type Detail struct {
	ID        uuid.UUID
	Book      Book
	Condition Condition
	Note      *string
}

// PublicUser 是挂单响应所需的最小公开用户资料。
type PublicUser struct {
	ID       uuid.UUID
	Nickname string
}

// View 组合经 engine 恢复的通用状态与宿主资料。
type View struct {
	Listing *engine.Listing
	Detail  Detail
	Owner   PublicUser
}

// CreateInput 是发布买卖挂单的宿主输入。
type CreateInput struct {
	Book       Book
	Condition  Condition
	Note       *string
	PriceCents int64
	Currency   string
	Side       engine.Side
}

// ListInput 是公开市场或本人挂单列表的筛选输入。
type ListInput struct {
	Side      engine.Side
	Query     *string
	Condition *Condition
	OwnerID   *uuid.UUID
	Cursor    *string
	Limit     *int
}

// Page 是稳定 keyset 分页结果。
type Page struct {
	Items      []View
	NextCursor *string
}

// FieldError 描述一个挂单请求字段的稳定校验失败。
type FieldError struct{ Field, Code, Detail string }

// ValidationError 汇总挂单请求中的字段错误。
type ValidationError struct{ Fields []FieldError }

func (e *ValidationError) Error() string { return "validation failed" }

// CandidateFilter 是 PostgreSQL 候选 ID 查询所需的已校验条件。
type CandidateFilter struct {
	Side            engine.Side
	OwnerID         *uuid.UUID
	Condition       *Condition
	ISBN13          *string
	TitleQuery      *string
	BeforeCreatedAt *time.Time
	BeforeID        *uuid.UUID
	PageSize        int
}

// Store 定义挂单服务所需的最小事务、批量查询与 engine 读取能力。
type Store interface {
	engine.ListingReader
	Transact(context.Context, func(Transaction) error) error
	FindListingIDs(context.Context, CandidateFilter) ([]uuid.UUID, error)
	ReadDetails(context.Context, []uuid.UUID) ([]Detail, error)
	ReadUsers(context.Context, []uuid.UUID) ([]PublicUser, error)
}

// Transaction 是发布和撤回时绑定同一数据库事务的能力。
type Transaction interface {
	engine.ListingCreator
	engine.ListingReader
	engine.ListingWithdrawer
	CreateDetail(context.Context, Detail) error
}

// Service 编排宿主资料和 engine Listing 的原子操作。
type Service struct {
	store Store
	now   func() time.Time
	newID func() uuid.UUID
}

// NewService 创建挂单服务；nil 时间或标识生成器使用安全默认值。
func NewService(store Store, now func() time.Time, newID func() uuid.UUID) *Service {
	if now == nil {
		now = time.Now
	}
	if newID == nil {
		newID = uuid.New
	}
	return &Service{store: store, now: now, newID: newID}
}

// Create 在同一事务内先保存书籍资料，再通过 engine 发布 Listing。
func (s *Service) Create(ctx context.Context, owner PublicUser, input CreateInput) (View, error) {
	normalized, err := validateCreate(input)
	if err != nil {
		return View{}, err
	}
	amount, err := engine.NewAmount(normalized.PriceCents)
	if err != nil {
		return View{}, validation("price.amount", "invalid", "must be a positive integer amount in fen")
	}
	id, now := s.newID(), s.now().UTC()
	detail := Detail{ID: id, Book: normalized.Book, Condition: normalized.Condition, Note: normalized.Note}
	var entity *engine.Listing
	err = s.store.Transact(ctx, func(tx Transaction) error {
		if err := tx.CreateDetail(ctx, detail); err != nil {
			return fmt.Errorf("create listing detail: %w", err)
		}
		entity, err = engine.PublishListing(ctx, tx, engine.PublishListingInput{
			ID: id.String(), OwnerID: owner.ID.String(), ResourceRef: id.String(),
			Side: normalized.Side, Price: amount, CreatedAt: now,
		})
		return err
	})
	if err != nil {
		return View{}, err
	}
	return View{Listing: entity, Detail: detail, Owner: owner}, nil
}

// Get 读取一个指定方向的挂单并始终通过 engine 恢复通用状态。
func (s *Service) Get(ctx context.Context, id string, side engine.Side) (View, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return View{}, ErrNotFound
	}
	views, err := s.readViews(ctx, s.store, []uuid.UUID{parsed})
	if err != nil {
		return View{}, err
	}
	if views[0].Listing.Side() != side {
		return View{}, ErrNotFound
	}
	return views[0], nil
}

// List 先筛选宿主候选 ID，再批量恢复 engine Listing 和用户资料。
func (s *Service) List(ctx context.Context, input ListInput) (Page, error) {
	filter, limit, err := validateList(input)
	if err != nil {
		return Page{}, err
	}
	ids, err := s.store.FindListingIDs(ctx, filter)
	if err != nil {
		return Page{}, fmt.Errorf("find listing ids: %w", err)
	}
	if len(ids) == 0 {
		return Page{Items: []View{}}, nil
	}
	views, err := s.readViews(ctx, s.store, ids)
	if err != nil {
		return Page{}, err
	}
	items := make([]View, 0, limit)
	lastScanned := -1
	for i, view := range views {
		lastScanned = i
		if input.OwnerID == nil && view.Listing.Status() != engine.ListingStatusOpen {
			continue
		}
		items = append(items, view)
		if len(items) == limit {
			break
		}
	}
	page := Page{Items: items}
	if lastScanned < len(views)-1 || len(ids) == filter.PageSize {
		cursorView := views[lastScanned]
		cursor := encodeCursor(cursorView.Listing.CreatedAt(), cursorView.Detail.ID)
		page.NextCursor = &cursor
	}
	return page, nil
}

// Withdraw 在事务内核对路由方向，并始终通过 engine 撤回挂单。
func (s *Service) Withdraw(ctx context.Context, id string, ownerID uuid.UUID, side engine.Side) error {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return s.store.Transact(ctx, func(tx Transaction) error {
		entity, err := engine.ReadListing(ctx, tx, parsed.String())
		if err != nil {
			return translateEngineNotFound(err)
		}
		if entity.Side() != side {
			return ErrNotFound
		}
		_, err = engine.WithdrawListing(ctx, tx, engine.WithdrawListingInput{
			ID: parsed.String(), OwnerID: ownerID.String(), UpdatedAt: s.now().UTC(),
		})
		return translateEngineNotFound(err)
	})
}

func (s *Service) readViews(ctx context.Context, reader engine.ListingReader, ids []uuid.UUID) ([]View, error) {
	textIDs := make([]string, len(ids))
	for i := range ids {
		textIDs[i] = ids[i].String()
	}
	entities, err := engine.ReadListings(ctx, reader, textIDs)
	if err != nil {
		return nil, translateEngineNotFound(err)
	}
	details, err := s.store.ReadDetails(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read listing details: %w", err)
	}
	detailByID := make(map[uuid.UUID]Detail, len(details))
	for _, detail := range details {
		if _, duplicate := detailByID[detail.ID]; duplicate {
			return nil, fmt.Errorf("duplicate listing detail %s", detail.ID)
		}
		detailByID[detail.ID] = detail
	}
	ownerIDs := make([]uuid.UUID, 0, len(entities))
	seenOwners := make(map[uuid.UUID]struct{}, len(entities))
	for _, entity := range entities {
		ownerID, err := uuid.Parse(entity.OwnerID())
		if err != nil {
			return nil, fmt.Errorf("parse listing owner %q: %w", entity.OwnerID(), err)
		}
		if _, seen := seenOwners[ownerID]; !seen {
			seenOwners[ownerID] = struct{}{}
			ownerIDs = append(ownerIDs, ownerID)
		}
	}
	users, err := s.store.ReadUsers(ctx, ownerIDs)
	if err != nil {
		return nil, fmt.Errorf("read listing owners: %w", err)
	}
	userByID := make(map[uuid.UUID]PublicUser, len(users))
	for _, user := range users {
		if _, duplicate := userByID[user.ID]; duplicate {
			return nil, fmt.Errorf("duplicate listing owner %s", user.ID)
		}
		userByID[user.ID] = user
	}
	views := make([]View, len(entities))
	for i, entity := range entities {
		id := ids[i]
		detail, ok := detailByID[id]
		if !ok || entity.ResourceRef() != id.String() {
			return nil, fmt.Errorf("listing %s has no matching detail", id)
		}
		ownerID, _ := uuid.Parse(entity.OwnerID())
		owner, ok := userByID[ownerID]
		if !ok {
			return nil, fmt.Errorf("listing %s has no owner profile", id)
		}
		views[i] = View{Listing: entity, Detail: detail, Owner: owner}
	}
	return views, nil
}

func validation(field, code, detail string) error {
	return &ValidationError{Fields: []FieldError{{Field: field, Code: code, Detail: detail}}}
}

func translateEngineNotFound(err error) error {
	if errors.Is(err, engine.ErrListingNotFound) {
		return ErrNotFound
	}
	return err
}
