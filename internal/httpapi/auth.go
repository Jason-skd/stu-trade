package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Jason-skd/stu-trade/internal/auth"
	"github.com/gin-gonic/gin"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const sessionCookieName = "stu_trade_session"

// === 处理认证请求 ===

// AuthServer 将认证用例映射到生成的 strict HTTP 契约。
type AuthServer struct {
	service      *auth.Service
	logger       *slog.Logger
	secureCookie bool
}

// NewAuthServer 创建认证 HTTP 边界。
func NewAuthServer(service *auth.Service, logger *slog.Logger, secureCookie bool) *AuthServer {
	return &AuthServer{service: service, logger: logger, secureCookie: secureCookie}
}

// Signup 处理注册并设置新会话 Cookie。
func (s *AuthServer) Signup(ctx context.Context, request SignupRequestObject) (SignupResponseObject, error) {
	if request.Body == nil {
		return signupValidation([]auth.FieldError{{Field: "body", Code: "required", Detail: "request body is required"}}), nil
	}
	password := ""
	if request.Body.Password != nil {
		password = *request.Body.Password
	}
	session, err := s.service.Signup(ctx, string(request.Body.Email), request.Body.Nickname, password)
	if err != nil {
		var validation *auth.ValidationError
		switch {
		case errors.As(err, &validation):
			return signupValidation(validation.Fields), nil
		case errors.Is(err, auth.ErrEmailTaken):
			p := problem(http.StatusConflict, EmailTaken, "Email already registered", "an account with this email already exists")
			return Signup409ApplicationProblemPlusJSONResponse{ConflictProblemApplicationProblemPlusJSONResponse(p)}, nil
		default:
			s.logger.Error("signup failed", "error", err)
			return Signup500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}, nil
		}
	}
	cookie := s.sessionCookie(session.Token, int(auth.SessionLifetime/time.Second))
	return Signup201JSONResponse{Body: transportSession(session.User), Headers: Signup201ResponseHeaders{SetCookie: &cookie}}, nil
}

// Login 处理凭据登录并设置新会话 Cookie。
func (s *AuthServer) Login(ctx context.Context, request LoginRequestObject) (LoginResponseObject, error) {
	if request.Body == nil {
		return loginValidation([]auth.FieldError{{Field: "body", Code: "required", Detail: "request body is required"}}), nil
	}
	password := ""
	if request.Body.Password != nil {
		password = *request.Body.Password
	}
	session, err := s.service.Login(ctx, string(request.Body.Email), password)
	if err != nil {
		var validation *auth.ValidationError
		switch {
		case errors.As(err, &validation):
			return loginValidation(validation.Fields), nil
		case errors.Is(err, auth.ErrInvalidCredentials):
			p := problem(http.StatusUnauthorized, InvalidCredentials, "Invalid credentials", "email or password is incorrect")
			return Login401ApplicationProblemPlusJSONResponse{UnauthenticatedProblemApplicationProblemPlusJSONResponse(p)}, nil
		default:
			s.logger.Error("login failed", "error", err)
			return Login500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}, nil
		}
	}
	cookie := s.sessionCookie(session.Token, int(auth.SessionLifetime/time.Second))
	return Login200JSONResponse{Body: transportSession(session.User), Headers: Login200ResponseHeaders{SetCookie: &cookie}}, nil
}

// GetCurrentSession 恢复 Cookie 对应的当前会话。
func (s *AuthServer) GetCurrentSession(ctx context.Context, _ GetCurrentSessionRequestObject) (GetCurrentSessionResponseObject, error) {
	token := requestCookie(ctx)
	user, err := s.service.Current(ctx, token)
	if err != nil {
		if errors.Is(err, auth.ErrUnauthenticated) {
			p := problem(http.StatusUnauthorized, Unauthenticated, "Authentication required", "a valid session is required")
			return GetCurrentSession401ApplicationProblemPlusJSONResponse{UnauthenticatedProblemApplicationProblemPlusJSONResponse(p)}, nil
		}
		s.logger.Error("restore session failed", "error", err)
		return GetCurrentSession500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}, nil
	}
	return GetCurrentSession200JSONResponse(transportSession(user)), nil
}

// Logout 幂等删除会话并清除 Cookie。
func (s *AuthServer) Logout(ctx context.Context, _ LogoutRequestObject) (LogoutResponseObject, error) {
	if err := s.service.Logout(ctx, requestCookie(ctx)); err != nil {
		s.logger.Error("logout failed", "error", err)
		return Logout500ApplicationProblemPlusJSONResponse{InternalProblemApplicationProblemPlusJSONResponse(internalProblem())}, nil
	}
	cookie := s.sessionCookie("", -1)
	return Logout204Response{Headers: Logout204ResponseHeaders{SetCookie: &cookie}}, nil
}

// === 映射会话传输 ===

func (s *AuthServer) sessionCookie(value string, maxAge int) string {
	c := &http.Cookie{Name: sessionCookieName, Value: value, Path: "/api/v1", MaxAge: maxAge, HttpOnly: true, Secure: s.secureCookie, SameSite: http.SameSiteLaxMode}
	if maxAge < 0 {
		c.Expires = time.Unix(1, 0).UTC()
	}
	return c.String()
}
func requestCookie(ctx context.Context) string {
	c, ok := ctx.(*gin.Context)
	if !ok {
		return ""
	}
	cookie, err := c.Request.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}
func transportSession(user auth.User) Session {
	return Session{User: OrderUser{Id: user.ID.String(), Email: openapi_types.Email(user.Email), Nickname: user.Nickname}}
}

// === 映射认证错误 ===

func problem(status int, code ProblemCode, title, detail string) Problem {
	return Problem{Type: "about:blank", Title: title, Status: status, Code: code, Detail: detail}
}
func internalProblem() Problem {
	return problem(http.StatusInternalServerError, InternalError, "Internal server error", "the request could not be completed")
}
func fieldProblems(fields []auth.FieldError) *[]FieldProblem {
	result := make([]FieldProblem, len(fields))
	for i, field := range fields {
		result[i] = FieldProblem{Field: field.Field, Code: field.Code, Detail: field.Detail}
	}
	return &result
}
func signupValidation(fields []auth.FieldError) SignupResponseObject {
	p := problem(http.StatusBadRequest, ValidationFailed, "Validation failed", "one or more fields are invalid")
	p.Fields = fieldProblems(fields)
	return Signup400ApplicationProblemPlusJSONResponse{ValidationProblemApplicationProblemPlusJSONResponse(p)}
}
func loginValidation(fields []auth.FieldError) LoginResponseObject {
	p := problem(http.StatusBadRequest, ValidationFailed, "Validation failed", "one or more fields are invalid")
	p.Fields = fieldProblems(fields)
	return Login400ApplicationProblemPlusJSONResponse{ValidationProblemApplicationProblemPlusJSONResponse(p)}
}
