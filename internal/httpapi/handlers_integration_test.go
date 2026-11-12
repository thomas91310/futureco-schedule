//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"futureco-schedule/internal/schedule"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandler_Health(t *testing.T) {
	handler, _ := newHandler(t)

	t.Run("database answers", func(t *testing.T) {
		is := assert.New(t)
		req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		rec := httptest.NewRecorder()
		handler.Health(rec, req)
		is.Equal(http.StatusOK, rec.Code)
		var body HealthResponse
		is.NoError(json.Unmarshal(rec.Body.Bytes(), &body))
		is.Equal(http.StatusOK, body.StatusCode)
		is.Equal("ok", body.Status)
	})

	t.Run("canceled request", func(t *testing.T) {
		is := assert.New(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		req := httptest.NewRequest(http.MethodGet, "/api/health", nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		handler.Health(rec, req)
		is.Equal(http.StatusServiceUnavailable, rec.Code)
		body := decodeError(t, rec)
		is.Equal(http.StatusServiceUnavailable, body.StatusCode)
		is.Equal("database unavailable", body.Error)
	})
}

func TestHandler_AvailableTimes_omitsBooked(t *testing.T) {
	is := assert.New(t)
	handler, tx := newHandler(t)
	trainerID := insertPerson(t, tx, "trainers", "Trainer")
	userID := insertPerson(t, tx, "users", "User")
	start := time.Date(2031, time.January, 16, 10, 0, 0, 0, pacificTime(t))
	booked := start.Add(30 * time.Minute)
	insertAppointment(t, tx, trainerID, userID, booked)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/trainers/"+strconv.FormatInt(trainerID, 10)+"/available-times?starts_at="+start.Format(time.RFC3339)+"&ends_at="+start.Add(90*time.Minute).Format(time.RFC3339), nil)
	req.SetPathValue("trainer_id", strconv.FormatInt(trainerID, 10))
	rec := httptest.NewRecorder()
	handler.AvailableTimes(rec, req)

	is.Equal(http.StatusOK, rec.Code)
	var body AvailableTimesResponse
	is.NoError(json.Unmarshal(rec.Body.Bytes(), &body))
	is.Equal(http.StatusOK, body.StatusCode)
	is.Equal([]schedule.Slot{
		{StartsAt: start.UTC(), EndsAt: start.Add(30 * time.Minute).UTC()},
		{StartsAt: start.Add(time.Hour).UTC(), EndsAt: start.Add(90 * time.Minute).UTC()},
	}, body.Items)
}

func TestHandler_CreateAppointment_conflict(t *testing.T) {
	handler, tx := newHandler(t)
	trainerID := insertPerson(t, tx, "trainers", "Trainer")
	userID := insertPerson(t, tx, "users", "User")
	start := time.Date(2031, time.January, 16, 15, 0, 0, 0, pacificTime(t))
	body := appointmentJSON(trainerID, userID, start)

	rec := postAppointment(handler, body)
	is := assert.New(t)
	is.Equal(http.StatusCreated, rec.Code)
	var created AppointmentResponse
	is.NoError(json.Unmarshal(rec.Body.Bytes(), &created))
	is.Equal(http.StatusCreated, created.StatusCode)
	is.Equal("/api/v1/appointments/"+strconv.FormatInt(created.ID, 10), rec.Header().Get("Location"))
	is.True(created.StartsAt.Equal(start))
	is.True(created.EndsAt.Equal(start.Add(30 * time.Minute)))

	again := postAppointment(handler, body)
	is.Equal(http.StatusConflict, again.Code)
	conflict := decodeError(t, again)
	is.Equal(http.StatusConflict, conflict.StatusCode)
	is.Equal("slot already booked", conflict.Error)
}

func TestHandler_CreateAppointment_missingPerson(t *testing.T) {
	is := assert.New(t)
	handler, _ := newHandler(t)
	start := time.Date(2031, time.January, 16, 16, 0, 0, 0, pacificTime(t))
	rec := postAppointment(handler, appointmentJSON(9_000_000_000_000, 9_000_000_000_001, start))
	is.Equal(http.StatusNotFound, rec.Code)
	body := decodeError(t, rec)
	is.Equal(http.StatusNotFound, body.StatusCode)
	is.Equal("trainer or user not found", body.Error)
}

func TestHandler_ListAppointments_cursor(t *testing.T) {
	is := assert.New(t)
	handler, tx := newHandler(t)
	trainerID := insertPerson(t, tx, "trainers", "Trainer")
	userID := insertPerson(t, tx, "users", "User")
	start := time.Date(2031, time.January, 16, 9, 0, 0, 0, pacificTime(t))
	insertAppointment(t, tx, trainerID, userID, start.Add(time.Hour))
	insertAppointment(t, tx, trainerID, userID, start)
	insertAppointment(t, tx, trainerID, userID, start.Add(30*time.Minute))

	firstCode, first := listAppointments(t, handler, trainerID, "limit=1")
	is.Equal(http.StatusOK, firstCode)
	is.Equal(http.StatusOK, first.StatusCode)
	is.True(first.HasMore)
	is.NotNil(first.NextCursor)
	is.Len(first.Items, 1)
	is.True(first.Items[0].StartsAt.Equal(start))

	secondCode, second := listAppointments(t, handler, trainerID, "limit=1&cursor="+*first.NextCursor)
	is.Equal(http.StatusOK, secondCode)
	is.Equal(http.StatusOK, second.StatusCode)
	is.True(second.HasMore)
	is.True(second.Items[0].StartsAt.Equal(start.Add(30 * time.Minute)))

	thirdCode, third := listAppointments(t, handler, trainerID, "limit=1&cursor="+*second.NextCursor)
	is.Equal(http.StatusOK, thirdCode)
	is.Equal(http.StatusOK, third.StatusCode)
	is.False(third.HasMore)
	is.Nil(third.NextCursor)
	is.True(third.Items[0].StartsAt.Equal(start.Add(time.Hour)))
}

func TestHandler_UnknownTrainer(t *testing.T) {
	is := assert.New(t)
	handler, _ := newHandler(t)
	const missing int64 = 9_000_000_000_000
	start := time.Date(2031, time.January, 16, 10, 0, 0, 0, pacificTime(t))

	available := httptest.NewRequest(http.MethodGet, "/available-times?starts_at="+start.Format(time.RFC3339)+"&ends_at="+start.Add(time.Hour).Format(time.RFC3339), nil)
	available.SetPathValue("trainer_id", strconv.FormatInt(missing, 10))
	availableRec := httptest.NewRecorder()
	handler.AvailableTimes(availableRec, available)
	is.Equal(http.StatusNotFound, availableRec.Code)
	availableBody := decodeError(t, availableRec)
	is.Equal(http.StatusNotFound, availableBody.StatusCode)
	is.Equal("trainer not found", availableBody.Error)

	listed := listRaw(handler, missing, "")
	is.Equal(http.StatusNotFound, listed.Code)
	listedBody := decodeError(t, listed)
	is.Equal(http.StatusNotFound, listedBody.StatusCode)
	is.Equal("trainer not found", listedBody.Error)
}

func newHandler(t *testing.T) (*Handler, pgx.Tx) {
	t.Helper()
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = tx.Rollback(context.Background())
	})
	return &Handler{Store: schedule.NewStore(tx)}, tx
}

func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://futureco:futureco@localhost:5433/futureco?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, pool.Ping(ctx))
	return pool
}

func pacificTime(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	return loc
}

func insertPerson(t *testing.T, tx pgx.Tx, table, firstName string) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	var err error
	switch table {
	case "trainers":
		err = tx.QueryRow(ctx, `
			INSERT INTO trainers (first_name, last_name, dob)
			VALUES ($1, 'Local', '1990-01-01')
			RETURNING id
		`, firstName).Scan(&id)
	case "users":
		err = tx.QueryRow(ctx, `
			INSERT INTO users (first_name, last_name, dob)
			VALUES ($1, 'Local', '1990-01-01')
			RETURNING id
		`, firstName).Scan(&id)
	default:
		t.Fatalf("unexpected table %s", table)
	}
	require.NoError(t, err)
	return id
}

func insertAppointment(t *testing.T, tx pgx.Tx, trainerID, userID int64, start time.Time) {
	t.Helper()
	_, err := tx.Exec(context.Background(), `
		INSERT INTO appointments (trainer_id, user_id, started_at, ended_at)
		VALUES ($1, $2, $3, $4)
	`, trainerID, userID, start, start.Add(30*time.Minute))
	require.NoError(t, err)
}

func appointmentJSON(trainerID, userID int64, start time.Time) string {
	return `{"trainer_id":` + strconv.FormatInt(trainerID, 10) +
		`,"user_id":` + strconv.FormatInt(userID, 10) +
		`,"starts_at":"` + start.Format(time.RFC3339) +
		`","ends_at":"` + start.Add(30*time.Minute).Format(time.RFC3339) + `"}`
}

func postAppointment(handler *Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/appointments", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.CreateAppointment(rec, req)
	return rec
}

func listAppointments(t *testing.T, handler *Handler, trainerID int64, query string) (int, AppointmentListResponse) {
	t.Helper()
	rec := listRaw(handler, trainerID, query)
	var body AppointmentListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return rec.Code, body
}

func listRaw(handler *Handler, trainerID int64, query string) *httptest.ResponseRecorder {
	target := "/api/v1/trainers/" + strconv.FormatInt(trainerID, 10) + "/appointments"
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.SetPathValue("trainer_id", strconv.FormatInt(trainerID, 10))
	rec := httptest.NewRecorder()
	handler.ListAppointments(rec, req)
	return rec
}
