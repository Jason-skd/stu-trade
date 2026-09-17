package httpapi

import (
	"context"
	"errors"
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
	invalidCookie := authRequest(router, http.MethodDelete, "/api/v1/sessions/current", "", sessionCookieName+"=invalid")
	if invalidCookie.Code != http.StatusNoContent {
		t.Fatalf("invalid-cookie logout = %d", invalidCookie.Code)
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
	unknown := authRequest(router, http.MethodPost, "/api/v1/signup", "{\"email\":\"user@example.com\",\"nickname\":\"User\",\"password\":\"password123\",\"role\":\"admin\"}", "")
	if unknown.Code != http.StatusBadRequest || !strings.Contains(unknown.Body.String(), "\"code\":\"validation_failed\"") {
		t.Fatalf("unknown field = (%d, %s)", unknown.Code, unknown.Body.String())
	}
	malformed := authRequest(router, http.MethodPost, "/api/v1/login", "{", "")
	if malformed.Code != http.StatusBadRequest || !strings.Contains(malformed.Body.String(), "\"code\":\"validation_failed\"") {
		t.Fatalf("malformed JSON = (%d, %s)", malformed.Code, malformed.Body.String())
	}
}

func TestAuthHTTPLoginConflictAndInternalProblems(t *testing.T) {
	hash, err := auth.HashPassword("password123", httpRandom)
	if err != nil {
		t.Fatal(err)
	}
	store := &authHTTPStore{user: auth.StoredUser{User: auth.User{ID: uuid.New(), Email: "user@example.com", Nickname: "User"}, PasswordHash: hash}}
	router := NewRouter(NewReadiness(&stubDatabasePinger{}, &stubMigrationChecker{}, discardLogger()), NewAuthServer(auth.NewService(store, time.Now, httpRandom), discardLogger(), false))

	login := authRequest(router, http.MethodPost, "/api/v1/login", "{\"email\":\"user@example.com\",\"password\":\"password123\"}", "")
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 || login.Result().Cookies()[0].Secure {
		t.Fatalf("login = (%d, %#v)", login.Code, login.Result().Cookies())
	}
	wrong := authRequest(router, http.MethodPost, "/api/v1/login", "{\"email\":\"user@example.com\",\"password\":\"wrongpass\"}", "")
	if wrong.Code != http.StatusUnauthorized || !strings.Contains(wrong.Body.String(), "\"code\":\"invalid_credentials\"") {
		t.Fatalf("wrong credentials = (%d, %s)", wrong.Code, wrong.Body.String())
	}

	store.createUserErr = auth.ErrEmailTaken
	conflict := authRequest(router, http.MethodPost, "/api/v1/signup", "{\"email\":\"user@example.com\",\"nickname\":\"Other\",\"password\":\"password456\"}", "")
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "\"code\":\"email_taken\"") {
		t.Fatalf("email conflict = (%d, %s)", conflict.Code, conflict.Body.String())
	}

	store.findErr = errors.New("database unavailable")
	internal := authRequest(router, http.MethodPost, "/api/v1/login", "{\"email\":\"user@example.com\",\"password\":\"password123\"}", "")
	if internal.Code != http.StatusInternalServerError || !strings.Contains(internal.Body.String(), "\"code\":\"internal_error\"") || strings.Contains(internal.Body.String(), "database unavailable") {
		t.Fatalf("internal error = (%d, %s)", internal.Code, internal.Body.String())
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
	user          auth.StoredUser
	digest        []byte
	createUserErr error
	findErr       error
}

func (s *authHTTPStore) CreateUserSession(_ context.Context, user auth.StoredUser, digest []byte, _ time.Time, _ time.Time) error {
	if s.createUserErr != nil {
		return s.createUserErr
	}
	s.user = user
	s.digest = append([]byte(nil), digest...)
	return nil
}
func (s *authHTTPStore) FindUserByEmail(context.Context, string) (auth.StoredUser, error) {
	if s.findErr != nil {
		return auth.StoredUser{}, s.findErr
	}
	if s.user.ID == uuid.Nil {
		return auth.StoredUser{}, auth.ErrUnauthenticated
	}
	return s.user, nil
}

func (s *authHTTPStore) CreateSession(_ context.Context, _ uuid.UUID, digest []byte, _ time.Time, _ time.Time) error {
	s.digest = append([]byte(nil), digest...)
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
