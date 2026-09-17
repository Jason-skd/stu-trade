package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthDoesNotCheckDependencies(t *testing.T) {
	reader := &stubSchemaVersionReader{err: errors.New("database unavailable")}
	router := NewRouter(NewReadiness(reader, 1))

	response := performRequest(router, http.MethodGet, "/health")
	if response.Code != http.StatusOK || reader.called {
		t.Fatalf("health response = %d, database called = %t", response.Code, reader.called)
	}
}

func TestReadinessStates(t *testing.T) {
	tests := []struct {
		name       string
		reader     *stubSchemaVersionReader
		wantStatus int
		wantBody   string
	}{
		{name: "ready", reader: &stubSchemaVersionReader{version: 2}, wantStatus: http.StatusOK, wantBody: `"status":"ready"`},
		{name: "schema outdated", reader: &stubSchemaVersionReader{version: 1}, wantStatus: http.StatusServiceUnavailable, wantBody: `"reason":"schema_outdated"`},
		{name: "database unavailable", reader: &stubSchemaVersionReader{err: errors.New("unavailable")}, wantStatus: http.StatusServiceUnavailable, wantBody: `"reason":"database_unavailable"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := NewRouter(NewReadiness(tt.reader, 2))
			response := performRequest(router, http.MethodGet, "/ready")
			if response.Code != tt.wantStatus || !strings.Contains(response.Body.String(), tt.wantBody) {
				t.Fatalf("response = (%d, %q), want status %d containing %q", response.Code, response.Body.String(), tt.wantStatus, tt.wantBody)
			}
		})
	}
}

func TestUnknownRouteReturnsBackendNotFound(t *testing.T) {
	router := NewRouter(NewReadiness(&stubSchemaVersionReader{version: 1}, 1))
	response := performRequest(router, http.MethodGet, "/missing")
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"error":"not_found"`) {
		t.Fatalf("response = (%d, %q)", response.Code, response.Body.String())
	}
}

type stubSchemaVersionReader struct {
	version int64
	err     error
	called  bool
}

func (r *stubSchemaVersionReader) GetSchemaVersion(context.Context) (int64, error) {
	r.called = true
	return r.version, r.err
}

func performRequest(handler http.Handler, method, target string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
