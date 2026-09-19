package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	engine "github.com/Jason-skd/stu-trade-engine"
	"github.com/Jason-skd/stu-trade/internal/auth"
	orderdomain "github.com/Jason-skd/stu-trade/internal/order"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// === 处理订单请求 ===

// OrderServer 将主动购买和订单查询映射到生成的 strict HTTP 契约。
type OrderServer struct {
	orders   *orderdomain.Service
	sessions *auth.Service
	logger   *slog.Logger
}

// NewOrderServer 创建订单 HTTP 边界。
func NewOrderServer(orders *orderdomain.Service, sessions *auth.Service, logger *slog.Logger) *OrderServer {
	return &OrderServer{orders: orders, sessions: sessions, logger: logger}
}

// PurchaseSellListing 以当前用户身份购买指定卖单。
func (s *OrderServer) PurchaseSellListing(ctx context.Context, request PurchaseSellListingRequestObject) (PurchaseSellListingResponseObject, error) {
	user, err := s.currentUser(ctx)
	if err != nil {
		if !errors.Is(err, auth.ErrUnauthenticated) {
			s.logger.Error("authenticate direct purchase failed", "error", err)
			return PurchaseSellListing500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}, nil
		}
		return PurchaseSellListing401ApplicationProblemPlusJSONResponse{UnauthenticatedProblemApplicationProblemPlusJSONResponse(authenticationProblem())}, nil
	}
	view, err := s.orders.Purchase(ctx, request.SellListingId, user)
	if err != nil {
		return s.purchaseFailure(err), nil
	}
	return PurchaseSellListing201JSONResponse(transportDirectTrade(view, &user)), nil
}

// ListOrders 返回当前用户作为买方或卖方参与的订单。
func (s *OrderServer) ListOrders(ctx context.Context, request ListOrdersRequestObject) (ListOrdersResponseObject, error) {
	user, err := s.currentUser(ctx)
	if err != nil {
		if !errors.Is(err, auth.ErrUnauthenticated) {
			s.logger.Error("authenticate order query failed", "error", err)
			return ListOrders500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}, nil
		}
		return ListOrders401ApplicationProblemPlusJSONResponse{UnauthenticatedProblemApplicationProblemPlusJSONResponse(authenticationProblem())}, nil
	}
	input := orderdomain.ListInput{ParticipantID: user.ID, Cursor: (*string)(request.Params.Cursor), Limit: (*int)(request.Params.Limit)}
	if request.Params.Status != nil {
		status := orderdomain.OrderStatus(*request.Params.Status)
		input.Status = &status
	}
	page, err := s.orders.List(ctx, input)
	if err != nil {
		var validation *orderdomain.ValidationError
		if errors.As(err, &validation) {
			return ListOrders400ApplicationProblemPlusJSONResponse{ValidationProblemApplicationProblemPlusJSONResponse(orderValidationProblem(validation.Fields))}, nil
		}
		s.logger.Error("list orders failed", "error", err)
		return ListOrders500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}, nil
	}
	items := make([]Order, len(page.Items))
	for i := range page.Items {
		items[i] = transportOrder(page.Items[i])
	}
	return ListOrders200JSONResponse{OrderPageJSONResponse{Items: items, NextCursor: page.NextCursor}}, nil
}

func (s *OrderServer) currentUser(ctx context.Context) (auth.User, error) {
	token := requestCookie(ctx)
	if token == "" {
		return auth.User{}, auth.ErrUnauthenticated
	}
	return s.sessions.Current(ctx, token)
}

// === 映射订单输出 ===

func transportDirectTrade(view orderdomain.View, current *auth.User) DirectTradeResult {
	return DirectTradeResult{Match: transportMatch(view, current), Order: transportOrder(view)}
}

func transportMatch(view orderdomain.View, current *auth.User) Match {
	request := view.MatchRequest
	orderID := view.Order.ID.String()
	sellListing := transportSellListing(view.SellListing, current)
	result := Match{
		Id: request.ID(), Source: transportMatchSource(request.Source()), Status: MatchStatus(request.Status().String()),
		SellListing: &sellListing, AgreedPrice: Money{Amount: int(request.TradePrice().Cents()), Currency: CNY},
		SellerConfirmedAt: optionalTime(request.SellerConfirmedAt()), BuyerConfirmedAt: optionalTime(request.BuyerConfirmedAt()),
		OrderId: &orderID, CreatedAt: request.CreatedAt(), UpdatedAt: request.UpdatedAt(),
	}
	_ = result.FromMatch1(Match1{Source: DirectPurchase, SellListing: sellListing})
	return result
}

func transportOrder(view orderdomain.View) Order {
	request := view.MatchRequest
	return Order{
		Id: view.Order.ID.String(), MatchId: request.ID(), Book: transportBook(view.SellListing.Detail.Book),
		Seller: transportOrderUser(view.Seller), Buyer: transportOrderUser(view.Buyer),
		AgreedPrice:                 Money{Amount: int(request.TradePrice().Cents()), Currency: CNY},
		Status:                      OrderStatus(view.Order.Status),
		SellerConfirmedCompletionAt: view.Order.SellerConfirmedCompletionAt,
		BuyerConfirmedCompletionAt:  view.Order.BuyerConfirmedCompletionAt,
		CreatedAt:                   view.Order.CreatedAt, UpdatedAt: view.Order.UpdatedAt,
	}
}

func transportOrderUser(user auth.User) OrderUser {
	return OrderUser{Id: user.ID.String(), Email: openapi_types.Email(user.Email), Nickname: user.Nickname}
}

func transportMatchSource(source engine.MatchSource) MatchSource {
	switch source {
	case engine.MatchSourceAutomatic:
		return Automatic
	case engine.MatchSourceSellerAccept:
		return DirectAcceptance
	case engine.MatchSourceBuyerPurchase:
		return DirectPurchase
	default:
		return MatchSource("unknown")
	}
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

// === 映射订单错误 ===

func (s *OrderServer) purchaseFailure(err error) PurchaseSellListingResponseObject {
	switch {
	case errors.Is(err, orderdomain.ErrSelfPurchase):
		return PurchaseSellListing403ApplicationProblemPlusJSONResponse{ForbiddenProblemApplicationProblemPlusJSONResponse(selfPurchaseProblem())}
	case errors.Is(err, orderdomain.ErrNotFound):
		return PurchaseSellListing404ApplicationProblemPlusJSONResponse{NotFoundProblemApplicationProblemPlusJSONResponse(notFoundProblem())}
	case errors.Is(err, engine.ErrListingUnavailable), errors.Is(err, engine.ErrMatchRejected), errors.Is(err, engine.ErrInvalidTransition):
		return PurchaseSellListing409ApplicationProblemPlusJSONResponse{ConflictProblemApplicationProblemPlusJSONResponse(purchaseUnavailableProblem())}
	default:
		s.logger.Error("purchase sell listing failed", "error", err)
		return PurchaseSellListing500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}
	}
}

func orderValidationProblem(fields []orderdomain.FieldError) Problem {
	p := problem(http.StatusBadRequest, ValidationFailed, "Validation failed", "one or more fields are invalid")
	result := make([]FieldProblem, len(fields))
	for i, field := range fields {
		result[i] = FieldProblem{Field: field.Field, Code: field.Code, Detail: field.Detail}
	}
	p.Fields = &result
	return p
}

func selfPurchaseProblem() Problem {
	return problem(http.StatusForbidden, Forbidden, "Forbidden", "a listing owner cannot purchase their own listing")
}

func purchaseUnavailableProblem() Problem {
	return problem(http.StatusConflict, ListingUnavailable, "Listing unavailable", "the listing is no longer available for purchase")
}
