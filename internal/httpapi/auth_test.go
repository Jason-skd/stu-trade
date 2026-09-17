package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Jason-skd/stu-trade/internal/auth"
	"github.com/google/uuid"
)

func TestAuthHTTPFlowAndCookiePolicy(t *testing.T) {
	store := &authHTTPStore{}
	now := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	service := auth.NewService(store, func() time.Time { return now }, httpRandom)
	router := NewRouter(NewReadiness(&stubDatabasePinger{}, &stubMigrationChecker{}, discardLogger()), NewAuthServer(service, discardLogger(), true))

	signup := authRequest(router, http.MethodPost, "/api/v1/signup", "{\"email\":\" USER@example.com \",\"nickname\":\" User \",\"password\":\"password123\"}", "")
	if signup.Code != http.StatusCreated || !strings.Contains(signup.Body.String(), "\"email\":\"user@example.com\"") {
		t.Fatalf("signup = (%d, %s)", signup.Code, signup.Body.String())
	}
	cookies := signup.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("signup cookies = %#v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != sessionCookieName || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/api/v1" || cookie.MaxAge != 604800 {
		t.Fatalf("cookie = %#v", cookie)
	}

	current := authRequest(router, http.MethodGet, "/api/v1/sessions/current", "", cookie.String())
	if current.Code != http.StatusOK {
		t.Fatalf("current = (%d, %s)", current.Code, current.Body.String())
	}
	logout := authRequest(router, http.MethodDelete, "/api/v1/sessions/current", "", cookie.String())
	logoutCookies := logout.Result().Cookies()
	if logout.Code != http.StatusNoContent || len(logoutCookies) != 1 || logoutCookies[0].MaxAge >= 0 {
		t.Fatalf("logout = (%d, %#v)", logout.Code, logoutCookies)
	}
	again := authRequest(router, http.MethodDelete, "/api/v1/sessions/current", "", "")
	if again.Code != http.StatusNoContent {
		t.Fatalf("idempotent logout = %d", again.Code)
	}
}

func TestAuthHTTPProblems(t *testing.T) {
	service := auth.NewService(&authHTTPStore{}, time.Now, httpRandom)
	router := NewRouter(NewReadiness(&stubDatabasePinger{}, &stubMigrationChecker{}, discardLogger()), NewAuthServer(service, discardLogger(), false))
	invalid := authRequest(router, http.MethodPost, "/api/v1/signup", "{\"email\":\"bad\",\"nickname\":\"\",\"password\":\"short\"}", "")
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "\"code\":\"validation_failed\"") {
		t.Fatalf("invalid = (%d, %s)", invalid.Code, invalid.Body.String())
	}
	missing := authRequest(router, http.MethodGet, "/api/v1/sessions/current", "", "")
	if missing.Code != http.StatusUnauthorized || !strings.Contains(missing.Body.String(), "\"code\":\"unauthenticated\"") {
		t.Fatalf("missing = (%d, %s)", missing.Code, missing.Body.String())
	}
}

func authRequest(handler http.Handler, method, target, body, cookie string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		request.Header.Set("Cookie", cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func httpRandom(value []byte) (int, error) {
	for i := range value {
		value[i] = byte(i + 11)
	}
	return len(value), nil
}

type authHTTPStore struct {
	user   auth.StoredUser
	digest []byte
}

func (s *authHTTPStore) CreateUserSession(_ context.Context, user auth.StoredUser, digest []byte, _ time.Time, _ time.Time) error {
	s.user = user
	s.digest = append([]byte(nil), digest...)
	return nil
}
func (s *authHTTPStore) FindUserByEmail(context.Context, string) (auth.StoredUser, error) {
	if s.user.ID == uuid.Nil {
		return auth.StoredUser{}, auth.ErrUnauthenticated
	}
	return s.user, nil
}
func (s *authHTTPStore) CreateSession(context.Context, uuid.UUID, []byte, time.Time, time.Time) error {
	return nil
}
func (s *authHTTPStore) FindSessionUser(_ context.Context, digest []byte, _ time.Time) (auth.User, error) {
	if string(digest) != string(s.digest) {
		return auth.User{}, auth.ErrUnauthenticated
	}
	return s.user.User, nil
}
func (s *authHTTPStore) DeleteSession(_ context.Context, digest []byte) error {
	if string(digest) == string(s.digest) {
		s.digest = nil
	}
	return nil
}
