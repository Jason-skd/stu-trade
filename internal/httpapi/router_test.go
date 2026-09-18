package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthDoesNotCheckDependencies(t *testing.T) {
	database := &stubDatabasePinger{err: errors.New("database unavailable")}
	migrations := &stubMigrationChecker{err: errors.New("schema unavailable")}
	router := NewRouter(NewReadiness(database, migrations, discardLogger()), nil, nil)

	response := performRequest(router, http.MethodGet, "/health")
	if response.Code != http.StatusOK || database.called || migrations.called {
		t.Fatalf("health response = %d, database called = %t, migrations called = %t", response.Code, database.called, migrations.called)
	}
}

func TestReadinessStates(t *testing.T) {
	tests := []struct {
		name       string
		database   *stubDatabasePinger
		migrations *stubMigrationChecker
		wantStatus int
		wantBody   string
	}{
		{name: "ready", database: &stubDatabasePinger{}, migrations: &stubMigrationChecker{}, wantStatus: http.StatusOK, wantBody: `"status":"ready"`},
		{name: "schema outdated", database: &stubDatabasePinger{}, migrations: &stubMigrationChecker{pending: true}, wantStatus: http.StatusServiceUnavailable, wantBody: `"reason":"schema_outdated"`},
		{name: "schema unavailable", database: &stubDatabasePinger{}, migrations: &stubMigrationChecker{err: errors.New("unavailable")}, wantStatus: http.StatusServiceUnavailable, wantBody: `"reason":"schema_unavailable"`},
		{name: "database unavailable", database: &stubDatabasePinger{err: errors.New("unavailable")}, migrations: &stubMigrationChecker{}, wantStatus: http.StatusServiceUnavailable, wantBody: `"reason":"database_unavailable"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := NewRouter(NewReadiness(tt.database, tt.migrations, discardLogger()), nil, nil)
			response := performRequest(router, http.MethodGet, "/ready")
			if response.Code != tt.wantStatus || !strings.Contains(response.Body.String(), tt.wantBody) {
				t.Fatalf("response = (%d, %q), want status %d containing %q", response.Code, response.Body.String(), tt.wantStatus, tt.wantBody)
			}
		})
	}
}

func TestReadinessTimesOutDependencyChecks(t *testing.T) {
	database := &stubDatabasePinger{waitForCancellation: true}
	readiness := NewReadiness(database, &stubMigrationChecker{}, discardLogger())
	readiness.timeout = 10 * time.Millisecond

	started := time.Now()
	response := performRequest(NewRouter(readiness, nil, nil), http.MethodGet, "/ready")
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"reason":"database_unavailable"`) {
		t.Fatalf("response = (%d, %q)", response.Code, response.Body.String())
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("readiness elapsed = %v, want bounded check", elapsed)
	}
}

func TestUnknownRouteReturnsBackendNotFound(t *testing.T) {
	router := NewRouter(NewReadiness(&stubDatabasePinger{}, &stubMigrationChecker{}, discardLogger()), nil, nil)
	response := performRequest(router, http.MethodGet, "/missing")
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"error":"not_found"`) {
		t.Fatalf("response = (%d, %q)", response.Code, response.Body.String())
	}
}

type stubDatabasePinger struct {
	err                 error
	called              bool
	waitForCancellation bool
}

func (r *stubDatabasePinger) Ping(ctx context.Context) (int32, error) {
	r.called = true
	if r.waitForCancellation {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	return 1, r.err
}

type stubMigrationChecker struct {
	pending bool
	err     error
	called  bool
}

func (c *stubMigrationChecker) HasPending(context.Context) (bool, error) {
	c.called = true
	return c.pending, c.err
}

func performRequest(handler http.Handler, method, target string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
