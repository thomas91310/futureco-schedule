package httpapi

import (
	"encoding/json"
	"futureco-schedule/internal/schedule"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestHandler_AvailableTimes(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

	tests := []trainerRequestCase{
		{
			name:       "invalid trainer id",
			trainerID:  "abc",
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid trainer_id",
		},
		{
			name:       "missing window",
			trainerID:  "1",
			wantStatus: http.StatusBadRequest,
			wantError:  "starts_at and ends_at are required",
		},
		{
			name:       "window is not RFC3339",
			trainerID:  "1",
			query:      "starts_at=tomorrow&ends_at=later",
			wantStatus: http.StatusBadRequest,
			wantError:  "starts_at and ends_at must be RFC3339",
		},
		{
			name:       "ends before start",
			trainerID:  "1",
			query:      "starts_at=" + day.Format(time.RFC3339) + "&ends_at=" + day.Add(-time.Hour).Format(time.RFC3339),
			wantStatus: http.StatusBadRequest,
			wantError:  "ends_at must be after starts_at",
		},
		{
			name:       "window longer than 31 days",
			trainerID:  "1",
			query:      "starts_at=" + day.Format(time.RFC3339) + "&ends_at=" + day.Add(32*24*time.Hour).Format(time.RFC3339),
			wantStatus: http.StatusBadRequest,
			wantError:  "window must be 31 days or less",
		},
	}
	assertTrainerGet(t, "/available-times", (&Handler{}).AvailableTimes, tests)
}

func TestHandler_CreateAppointment(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	validEnd := start.Add(30 * time.Minute).Format(time.RFC3339)

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantError  string
	}{
		{
			name:       "invalid JSON",
			body:       "{",
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid JSON",
		},
		{
			name:       "missing ids",
			body:       `{"trainer_id":0,"user_id":1,"starts_at":"` + start.Format(time.RFC3339) + `","ends_at":"` + validEnd + `"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "trainer_id and user_id are required",
		},
		{
			name:       "not a half-hour",
			body:       `{"trainer_id":1,"user_id":2,"starts_at":"` + start.Add(15*time.Minute).Format(time.RFC3339) + `","ends_at":"` + start.Add(45*time.Minute).Format(time.RFC3339) + `"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "appointment must be 30 minutes and start on a half-hour",
		},
		{
			name:       "wrong duration",
			body:       `{"trainer_id":1,"user_id":2,"starts_at":"` + start.Format(time.RFC3339) + `","ends_at":"` + start.Add(time.Hour).Format(time.RFC3339) + `"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "appointment must be 30 minutes and start on a half-hour",
		},
		{
			name:       "outside Pacific business hours",
			body:       `{"trainer_id":1,"user_id":2,"starts_at":"` + start.Format(time.RFC3339) + `","ends_at":"` + validEnd + `"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  schedule.OutsideBusinessHoursMessage,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/appointments", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			(&Handler{}).CreateAppointment(rec, req)
			is.Equal(tt.wantStatus, rec.Code)
			body := decodeError(t, rec)
			is.Equal(tt.wantStatus, body.StatusCode)
			is.Equal(tt.wantError, body.Error)
		})
	}
}

func TestHandler_ListAppointments(t *testing.T) {
	t.Parallel()

	tests := []trainerRequestCase{
		{
			name:       "invalid trainer id",
			trainerID:  "0",
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid trainer_id",
		},
		{
			name:       "non-positive limit",
			trainerID:  "1",
			query:      "limit=0",
			wantStatus: http.StatusBadRequest,
			wantError:  invalidLimitMessage,
		},
		{
			name:       "invalid cursor",
			trainerID:  "1",
			query:      "cursor=%25%25%25",
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid cursor",
		},
	}
	assertTrainerGet(t, "/appointments", (&Handler{}).ListAppointments, tests)
}

func TestJSONErrors(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/trainers/{trainer_id}/appointments", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("trainer_id") != "1" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		_, _ = w.Write([]byte("ok"))
	})
	handler := JSONErrors(mux)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantBody   string
	}{
		{
			name:       "trainer id from the path",
			method:     http.MethodGet,
			path:       "/api/v1/trainers/1/appointments",
			wantStatus: http.StatusOK,
			wantBody:   "ok",
		},
		{
			name:       "unknown path",
			method:     http.MethodGet,
			path:       "/f",
			wantStatus: http.StatusNotFound,
			wantBody:   "resource not found",
		},
		{
			name:       "wrong method",
			method:     http.MethodPost,
			path:       "/api/v1/trainers/1/appointments",
			wantStatus: http.StatusMethodNotAllowed,
			wantBody:   "method not allowed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			is.Equal(tt.wantStatus, rec.Code)
			is.Contains(rec.Body.String(), tt.wantBody)
		})
	}
}

func TestParseLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    int
		wantErr string
	}{
		{name: "default", raw: "", want: defaultLimit},
		{name: "explicit", raw: "20", want: 20},
		{name: "capped at 100", raw: "1000", want: maxLimit},
		{name: "zero", raw: "0", wantErr: invalidLimitMessage},
		{name: "negative", raw: "-1", wantErr: invalidLimitMessage},
		{name: "not a number", raw: "many", wantErr: invalidLimitMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			got, err := parseLimit(tt.raw)
			if tt.wantErr != "" {
				is.EqualError(err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			is.Equal(tt.want, got)
		})
	}
}

type trainerRequestCase struct {
	name       string
	trainerID  string
	query      string
	wantStatus int
	wantError  string
}

func assertTrainerGet(t *testing.T, suffix string, handle func(http.ResponseWriter, *http.Request), tests []trainerRequestCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			is := assert.New(t)

			target := "/api/v1/trainers/" + tt.trainerID + suffix
			if tt.query != "" {
				target += "?" + tt.query
			}

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
			req.SetPathValue("trainer_id", tt.trainerID)

			rec := httptest.NewRecorder()
			handle(rec, req)
			is.Equal(tt.wantStatus, rec.Code)

			body := decodeError(t, rec)
			is.Equal(tt.wantStatus, body.StatusCode)
			is.Equal(tt.wantError, body.Error)
		})
	}
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) ErrorResponse {
	t.Helper()

	var body ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding error body: %v", err)
	}

	return body
}
