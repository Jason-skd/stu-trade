package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	engine "github.com/Jason-skd/stu-trade-engine"
	"github.com/Jason-skd/stu-trade/internal/auth"
	listingdomain "github.com/Jason-skd/stu-trade/internal/listing"
)

// === 处理挂单请求 ===

// ListingServer 将买卖挂单用例映射到生成的 strict HTTP 契约。
type ListingServer struct {
	listings *listingdomain.Service
	sessions *auth.Service
	logger   *slog.Logger
}

// NewListingServer 创建挂单 HTTP 边界。
func NewListingServer(listings *listingdomain.Service, sessions *auth.Service, logger *slog.Logger) *ListingServer {
	return &ListingServer{listings: listings, sessions: sessions, logger: logger}
}

// CreateSellListing 发布当前用户的卖单。
func (s *ListingServer) CreateSellListing(ctx context.Context, request CreateSellListingRequestObject) (CreateSellListingResponseObject, error) {
	user, err := s.currentUser(ctx, true)
	if err != nil {
		if !errors.Is(err, auth.ErrUnauthenticated) {
			s.logger.Error("authenticate sell listing creation failed", "error", err)
		}
		return createSellError(err), nil
	}
	if request.Body == nil {
		return createSellValidation([]listingdomain.FieldError{{Field: "body", Code: "required", Detail: "request body is required"}}), nil
	}
	view, err := s.listings.Create(ctx, listingOwner(*user), sellCreateInput(*request.Body))
	if err != nil {
		var validation *listingdomain.ValidationError
		if errors.As(err, &validation) {
			return createSellValidation(validation.Fields), nil
		}
		s.logger.Error("create sell listing failed", "error", err)
		return CreateSellListing500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}, nil
	}
	return CreateSellListing201JSONResponse(transportSellListing(view, user)), nil
}

// CreateBuyRequest 发布当前用户的买单。
func (s *ListingServer) CreateBuyRequest(ctx context.Context, request CreateBuyRequestRequestObject) (CreateBuyRequestResponseObject, error) {
	user, err := s.currentUser(ctx, true)
	if err != nil {
		if !errors.Is(err, auth.ErrUnauthenticated) {
			s.logger.Error("authenticate buy request creation failed", "error", err)
		}
		return createBuyError(err), nil
	}
	if request.Body == nil {
		return createBuyValidation([]listingdomain.FieldError{{Field: "body", Code: "required", Detail: "request body is required"}}), nil
	}
	view, err := s.listings.Create(ctx, listingOwner(*user), buyCreateInput(*request.Body))
	if err != nil {
		var validation *listingdomain.ValidationError
		if errors.As(err, &validation) {
			return createBuyValidation(buyFieldNames(validation.Fields)), nil
		}
		s.logger.Error("create buy request failed", "error", err)
		return CreateBuyRequest500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}, nil
	}
	return CreateBuyRequest201JSONResponse(transportBuyRequest(view, user)), nil
}

// ListSellListings 返回公开 open 卖单或当前用户全部卖单。
func (s *ListingServer) ListSellListings(ctx context.Context, request ListSellListingsRequestObject) (ListSellListingsResponseObject, error) {
	user, err := s.listUser(ctx, request.Params.Mine)
	if err != nil {
		if errors.Is(err, auth.ErrUnauthenticated) {
			return ListSellListings401ApplicationProblemPlusJSONResponse{UnauthenticatedProblemApplicationProblemPlusJSONResponse(authenticationProblem())}, nil
		}
		s.logger.Error("authenticate sell listing query failed", "error", err)
		return ListSellListings500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}, nil
	}
	page, err := s.listings.List(ctx, sellListInput(request.Params, user))
	if err != nil {
		var validation *listingdomain.ValidationError
		if errors.As(err, &validation) {
			return ListSellListings400ApplicationProblemPlusJSONResponse{ValidationProblemApplicationProblemPlusJSONResponse(listingValidationProblem(validation.Fields))}, nil
		}
		s.logger.Error("list sell listings failed", "error", err)
		return ListSellListings500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}, nil
	}
	items := make([]SellListing, len(page.Items))
	for i := range page.Items {
		items[i] = transportSellListing(page.Items[i], user)
	}
	return ListSellListings200JSONResponse{SellListingPageJSONResponse{Items: items, NextCursor: page.NextCursor}}, nil
}

// ListBuyRequests 返回公开 open 买单或当前用户全部买单。
func (s *ListingServer) ListBuyRequests(ctx context.Context, request ListBuyRequestsRequestObject) (ListBuyRequestsResponseObject, error) {
	user, err := s.listUser(ctx, request.Params.Mine)
	if err != nil {
		if errors.Is(err, auth.ErrUnauthenticated) {
			return ListBuyRequests401ApplicationProblemPlusJSONResponse{UnauthenticatedProblemApplicationProblemPlusJSONResponse(authenticationProblem())}, nil
		}
		s.logger.Error("authenticate buy request query failed", "error", err)
		return ListBuyRequests500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}, nil
	}
	page, err := s.listings.List(ctx, buyListInput(request.Params, user))
	if err != nil {
		var validation *listingdomain.ValidationError
		if errors.As(err, &validation) {
			return ListBuyRequests400ApplicationProblemPlusJSONResponse{ValidationProblemApplicationProblemPlusJSONResponse(listingValidationProblem(validation.Fields))}, nil
		}
		s.logger.Error("list buy requests failed", "error", err)
		return ListBuyRequests500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}, nil
	}
	items := make([]BuyRequest, len(page.Items))
	for i := range page.Items {
		items[i] = transportBuyRequest(page.Items[i], user)
	}
	return ListBuyRequests200JSONResponse{BuyRequestPageJSONResponse{Items: items, NextCursor: page.NextCursor}}, nil
}

// GetSellListing 返回指定卖单；有效会话只影响可用动作。
func (s *ListingServer) GetSellListing(ctx context.Context, request GetSellListingRequestObject) (GetSellListingResponseObject, error) {
	user, err := s.currentUser(ctx, false)
	if err != nil {
		s.logger.Error("authenticate sell listing read failed", "error", err)
		return GetSellListing500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}, nil
	}
	view, err := s.listings.Get(ctx, request.SellListingId, engine.SideSell)
	if err != nil {
		if errors.Is(err, listingdomain.ErrNotFound) {
			return GetSellListing404ApplicationProblemPlusJSONResponse{NotFoundProblemApplicationProblemPlusJSONResponse(notFoundProblem())}, nil
		}
		s.logger.Error("get sell listing failed", "error", err)
		return GetSellListing500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}, nil
	}
	return GetSellListing200JSONResponse(transportSellListing(view, user)), nil
}

// GetBuyRequest 返回指定买单；有效会话只影响可用动作。
func (s *ListingServer) GetBuyRequest(ctx context.Context, request GetBuyRequestRequestObject) (GetBuyRequestResponseObject, error) {
	user, err := s.currentUser(ctx, false)
	if err != nil {
		s.logger.Error("authenticate buy request read failed", "error", err)
		return GetBuyRequest500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}, nil
	}
	view, err := s.listings.Get(ctx, request.BuyRequestId, engine.SideBuy)
	if err != nil {
		if errors.Is(err, listingdomain.ErrNotFound) {
			return GetBuyRequest404ApplicationProblemPlusJSONResponse{NotFoundProblemApplicationProblemPlusJSONResponse(notFoundProblem())}, nil
		}
		s.logger.Error("get buy request failed", "error", err)
		return GetBuyRequest500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}, nil
	}
	return GetBuyRequest200JSONResponse(transportBuyRequest(view, user)), nil
}

// WithdrawSellListing 撤回当前用户的 open 卖单。
func (s *ListingServer) WithdrawSellListing(ctx context.Context, request WithdrawSellListingRequestObject) (WithdrawSellListingResponseObject, error) {
	user, err := s.currentUser(ctx, true)
	if err != nil {
		if !errors.Is(err, auth.ErrUnauthenticated) {
			s.logger.Error("authenticate sell listing withdrawal failed", "error", err)
		}
		return withdrawSellError(err), nil
	}
	if err := s.listings.Withdraw(ctx, request.SellListingId, user.ID, engine.SideSell); err != nil {
		return s.withdrawSellFailure(err), nil
	}
	return WithdrawSellListing204Response{}, nil
}

// WithdrawBuyRequest 撤回当前用户的 open 买单。
func (s *ListingServer) WithdrawBuyRequest(ctx context.Context, request WithdrawBuyRequestRequestObject) (WithdrawBuyRequestResponseObject, error) {
	user, err := s.currentUser(ctx, true)
	if err != nil {
		if !errors.Is(err, auth.ErrUnauthenticated) {
			s.logger.Error("authenticate buy request withdrawal failed", "error", err)
		}
		return withdrawBuyError(err), nil
	}
	if err := s.listings.Withdraw(ctx, request.BuyRequestId, user.ID, engine.SideBuy); err != nil {
		return s.withdrawBuyFailure(err), nil
	}
	return WithdrawBuyRequest204Response{}, nil
}

// === 识别当前用户 ===

func (s *ListingServer) currentUser(ctx context.Context, required bool) (*auth.User, error) {
	token := requestCookie(ctx)
	if token == "" {
		if required {
			return nil, auth.ErrUnauthenticated
		}
		return nil, nil
	}
	user, err := s.sessions.Current(ctx, token)
	if errors.Is(err, auth.ErrUnauthenticated) && !required {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *ListingServer) listUser(ctx context.Context, mine *Mine) (*auth.User, error) {
	required := mine != nil && bool(*mine)
	return s.currentUser(ctx, required)
}

// === 映射挂单输入 ===

func sellListInput(params ListSellListingsParams, user *auth.User) listingdomain.ListInput {
	return listInput(engine.SideSell, params.Q, params.Condition, params.Mine, params.Cursor, params.Limit, user)
}

func buyListInput(params ListBuyRequestsParams, user *auth.User) listingdomain.ListInput {
	return listInput(engine.SideBuy, params.Q, params.Condition, params.Mine, params.Cursor, params.Limit, user)
}

func listInput(side engine.Side, q *Search, condition *Condition, mine *Mine, cursor *Cursor, limit *Limit, user *auth.User) listingdomain.ListInput {
	input := listingdomain.ListInput{Side: side, Query: (*string)(q), Cursor: (*string)(cursor), Limit: (*int)(limit)}
	if condition != nil {
		value := listingdomain.Condition(*condition)
		input.Condition = &value
	}
	if mine != nil && bool(*mine) && user != nil {
		input.OwnerID = &user.ID
	}
	return input
}

func sellCreateInput(input CreateSellListingInput) listingdomain.CreateInput {
	return listingdomain.CreateInput{
		Book:      listingdomain.Book{ISBN13: input.Book.Isbn, Title: input.Book.Title, Author: input.Book.Author, CoverImageURL: input.Book.CoverImageUrl},
		Condition: listingdomain.Condition(input.Condition), Note: input.Note,
		PriceCents: int64(input.Price.Amount), Currency: string(input.Price.Currency), Side: engine.SideSell,
	}
}

func buyCreateInput(input CreateBuyRequestInput) listingdomain.CreateInput {
	return listingdomain.CreateInput{
		Book:      listingdomain.Book{ISBN13: input.Book.Isbn, Title: input.Book.Title, Author: input.Book.Author, CoverImageURL: input.Book.CoverImageUrl},
		Condition: listingdomain.Condition(input.Condition), Note: input.Note,
		PriceCents: int64(input.MaximumPrice.Amount), Currency: string(input.MaximumPrice.Currency), Side: engine.SideBuy,
	}
}

// === 映射挂单输出 ===

func transportSellListing(view listingdomain.View, current *auth.User) SellListing {
	return SellListing{
		Id: view.Detail.ID.String(), Owner: transportPublicUser(view.Owner), Book: transportBook(view.Detail.Book),
		Condition: Condition(view.Detail.Condition), Price: transportMoney(view), Note: view.Detail.Note,
		Status: ListingStatus(view.Listing.Status().String()), AvailableActions: availableActions(view, current),
		CreatedAt: view.Listing.CreatedAt(), UpdatedAt: view.Listing.UpdatedAt(),
	}
}

func transportBuyRequest(view listingdomain.View, current *auth.User) BuyRequest {
	return BuyRequest{
		Id: view.Detail.ID.String(), Owner: transportPublicUser(view.Owner), Book: transportBook(view.Detail.Book),
		Condition: Condition(view.Detail.Condition), MaximumPrice: transportMoney(view), Note: view.Detail.Note,
		Status: ListingStatus(view.Listing.Status().String()), AvailableActions: availableActions(view, current),
		CreatedAt: view.Listing.CreatedAt(), UpdatedAt: view.Listing.UpdatedAt(),
	}
}

func transportPublicUser(user listingdomain.PublicUser) PublicUser {
	return PublicUser{Id: user.ID.String(), Nickname: user.Nickname}
}
func listingOwner(user auth.User) listingdomain.PublicUser {
	return listingdomain.PublicUser{ID: user.ID, Nickname: user.Nickname}
}
func transportBook(book listingdomain.Book) Book {
	return Book{Isbn: book.ISBN13, Title: book.Title, Author: book.Author, CoverImageUrl: book.CoverImageURL}
}
func transportMoney(view listingdomain.View) Money {
	return Money{Amount: int(view.Listing.Price().Cents()), Currency: CNY}
}
func availableActions(view listingdomain.View, current *auth.User) []ListingAction {
	if current == nil || view.Listing.Status() != engine.ListingStatusOpen {
		return []ListingAction{}
	}
	if view.Listing.OwnerID() == current.ID.String() {
		return []ListingAction{Withdraw}
	}
	if view.Listing.Side() == engine.SideSell {
		return []ListingAction{Purchase}
	}
	return []ListingAction{}
}

// === 映射鉴权错误 ===

func createSellError(err error) CreateSellListingResponseObject {
	if errors.Is(err, auth.ErrUnauthenticated) {
		return CreateSellListing401ApplicationProblemPlusJSONResponse{UnauthenticatedProblemApplicationProblemPlusJSONResponse(authenticationProblem())}
	}
	return CreateSellListing500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}
}
func createBuyError(err error) CreateBuyRequestResponseObject {
	if errors.Is(err, auth.ErrUnauthenticated) {
		return CreateBuyRequest401ApplicationProblemPlusJSONResponse{UnauthenticatedProblemApplicationProblemPlusJSONResponse(authenticationProblem())}
	}
	return CreateBuyRequest500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}
}
func withdrawSellError(err error) WithdrawSellListingResponseObject {
	if errors.Is(err, auth.ErrUnauthenticated) {
		return WithdrawSellListing401ApplicationProblemPlusJSONResponse{UnauthenticatedProblemApplicationProblemPlusJSONResponse(authenticationProblem())}
	}
	return WithdrawSellListing500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}
}
func withdrawBuyError(err error) WithdrawBuyRequestResponseObject {
	if errors.Is(err, auth.ErrUnauthenticated) {
		return WithdrawBuyRequest401ApplicationProblemPlusJSONResponse{UnauthenticatedProblemApplicationProblemPlusJSONResponse(authenticationProblem())}
	}
	return WithdrawBuyRequest500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}
}

func (s *ListingServer) withdrawSellFailure(err error) WithdrawSellListingResponseObject {
	switch {
	case errors.Is(err, listingdomain.ErrNotFound):
		return WithdrawSellListing404ApplicationProblemPlusJSONResponse{NotFoundProblemApplicationProblemPlusJSONResponse(notFoundProblem())}
	case errors.Is(err, engine.ErrListingNotOwned):
		return WithdrawSellListing403ApplicationProblemPlusJSONResponse{ForbiddenProblemApplicationProblemPlusJSONResponse(forbiddenProblem())}
	case errors.Is(err, engine.ErrInvalidTransition):
		return WithdrawSellListing409ApplicationProblemPlusJSONResponse{ConflictProblemApplicationProblemPlusJSONResponse(listingUnavailableProblem())}
	default:
		s.logger.Error("withdraw sell listing failed", "error", err)
		return WithdrawSellListing500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}
	}
}

func (s *ListingServer) withdrawBuyFailure(err error) WithdrawBuyRequestResponseObject {
	switch {
	case errors.Is(err, listingdomain.ErrNotFound):
		return WithdrawBuyRequest404ApplicationProblemPlusJSONResponse{NotFoundProblemApplicationProblemPlusJSONResponse(notFoundProblem())}
	case errors.Is(err, engine.ErrListingNotOwned):
		return WithdrawBuyRequest403ApplicationProblemPlusJSONResponse{ForbiddenProblemApplicationProblemPlusJSONResponse(forbiddenProblem())}
	case errors.Is(err, engine.ErrInvalidTransition):
		return WithdrawBuyRequest409ApplicationProblemPlusJSONResponse{ConflictProblemApplicationProblemPlusJSONResponse(listingUnavailableProblem())}
	default:
		s.logger.Error("withdraw buy request failed", "error", err)
		return WithdrawBuyRequest500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}
	}
}

// === 映射校验错误 ===

func createSellValidation(fields []listingdomain.FieldError) CreateSellListingResponseObject {
	return CreateSellListing400ApplicationProblemPlusJSONResponse{ValidationProblemApplicationProblemPlusJSONResponse(listingValidationProblem(fields))}
}
func createBuyValidation(fields []listingdomain.FieldError) CreateBuyRequestResponseObject {
	return CreateBuyRequest400ApplicationProblemPlusJSONResponse{ValidationProblemApplicationProblemPlusJSONResponse(listingValidationProblem(fields))}
}
func listingValidationProblem(fields []listingdomain.FieldError) Problem {
	p := problem(http.StatusBadRequest, ValidationFailed, "Validation failed", "one or more fields are invalid")
	result := make([]FieldProblem, len(fields))
	for i, field := range fields {
		result[i] = FieldProblem{Field: field.Field, Code: field.Code, Detail: field.Detail}
	}
	p.Fields = &result
	return p
}
func buyFieldNames(fields []listingdomain.FieldError) []listingdomain.FieldError {
	result := make([]listingdomain.FieldError, len(fields))
	for i, field := range fields {
		field.Field = replacePrefix(field.Field, "price.", "maximumPrice.")
		result[i] = field
	}
	return result
}
func replacePrefix(value, old, replacement string) string {
	if len(value) >= len(old) && value[:len(old)] == old {
		return replacement + value[len(old):]
	}
	return value
}

// === 创建挂单问题响应 ===

func authenticationProblem() Problem {
	return problem(http.StatusUnauthorized, Unauthenticated, "Authentication required", "a valid session is required")
}
func forbiddenProblem() Problem {
	return problem(http.StatusForbidden, Forbidden, "Forbidden", "the listing belongs to another user")
}
func notFoundProblem() Problem {
	return problem(http.StatusNotFound, NotFound, "Not found", "the requested listing does not exist")
}
func listingUnavailableProblem() Problem {
	return problem(http.StatusConflict, ListingUnavailable, "Listing unavailable", "only an open listing can be withdrawn")
}
