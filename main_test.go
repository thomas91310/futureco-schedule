package main

import (
	"encoding/json"
	"futureco-schedule/internal/httpapi"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMux_unknownPath(t *testing.T) {
	t.Parallel()

	mux := newMux(nil)

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "unknown path", method: http.MethodGet, path: "/no-such-resource"},
		{name: "unknown api path", method: http.MethodPost, path: "/api/v1/nope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			is.Equal(http.StatusNotFound, rec.Code)
			is.Equal("application/json", rec.Header().Get("Content-Type"))

			var body httpapi.ErrorResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			is.Equal(http.StatusNotFound, body.StatusCode)
			is.Equal("resource not found", body.Error)
		})
	}
}

func TestNewMux_wrongMethod(t *testing.T) {
	t.Parallel()
	is := assert.New(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/health", nil)
	rec := httptest.NewRecorder()
	newMux(nil).ServeHTTP(rec, req)

	is.Equal(http.StatusMethodNotAllowed, rec.Code)
	is.Equal("application/json", rec.Header().Get("Content-Type"))
	is.Contains(rec.Header().Get("Allow"), http.MethodGet)

	var body httpapi.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	is.Equal(http.StatusMethodNotAllowed, body.StatusCode)
	is.Equal("method not allowed", body.Error)
}

func TestNewMux_knownRouteStaysAvailable(t *testing.T) {
	t.Parallel()
	is := assert.New(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	newMux(nil).ServeHTTP(rec, req)
	is.Equal(http.StatusOK, rec.Code)
	is.Contains(rec.Body.String(), "go_goroutines")
}
