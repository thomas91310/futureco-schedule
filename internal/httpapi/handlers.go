// Package httpapi serves the schedule JSON API.
package httpapi

import (
	"encoding/json"
	"errors"
	"futureco-schedule/internal/schedule"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

const (
	defaultLimit        = 50
	maxLimit            = 100
	invalidLimitMessage = "invalid limit"
)

// Handler serves the schedule API.
type Handler struct {
	Store *schedule.Store
}

// ErrorResponse is the flat error body. status_code matches the HTTP status.
type ErrorResponse struct {
	StatusCode int    `json:"status_code"`
	Error      string `json:"error"`
}

// ErrorBadRequest is the 400 error body documented in Swagger.
type ErrorBadRequest struct {
	StatusCode int    `json:"status_code" example:"400"`
	Error      string `json:"error" example:"ends_at must be after starts_at"`
}

// ErrorNotFound is the 404 error body documented in Swagger.
type ErrorNotFound struct {
	StatusCode int    `json:"status_code" example:"404"`
	Error      string `json:"error" example:"trainer not found"`
}

// ErrorConflict is the 409 error body documented in Swagger.
type ErrorConflict struct {
	StatusCode int    `json:"status_code" example:"409"`
	Error      string `json:"error" example:"slot already booked"`
}

// ErrorInternal is the 500 error body documented in Swagger.
type ErrorInternal struct {
	StatusCode int    `json:"status_code" example:"500"`
	Error      string `json:"error" example:"internal error"`
}

// ErrorUnavailable is the 503 error body documented in Swagger.
type ErrorUnavailable struct {
	StatusCode int    `json:"status_code" example:"503"`
	Error      string `json:"error" example:"database unavailable"`
}

// HealthResponse is the body of a successful health check.
type HealthResponse struct {
	StatusCode int    `json:"status_code" example:"200"`
	Status     string `json:"status" example:"ok"`
}

// AvailableTimesResponse lists free slots in the requested window.
type AvailableTimesResponse struct {
	StatusCode int             `json:"status_code" example:"200"`
	Items      []schedule.Slot `json:"items"`
}

// CreateAppointmentRequest is the JSON body for a new appointment.
type CreateAppointmentRequest struct {
	TrainerID int64     `json:"trainer_id" example:"1"`
	UserID    int64     `json:"user_id" example:"2"`
	StartsAt  time.Time `json:"starts_at" example:"2026-01-15T10:00:00Z"`
	EndsAt    time.Time `json:"ends_at" example:"2026-01-15T10:30:00Z"`
}

// AppointmentResponse is the created appointment.
type AppointmentResponse struct {
	StatusCode int       `json:"status_code" example:"201"`
	ID         int64     `json:"id" example:"1"`
	TrainerID  int64     `json:"trainer_id" example:"1"`
	UserID     int64     `json:"user_id" example:"2"`
	StartsAt   time.Time `json:"starts_at" example:"2026-01-15T10:00:00Z"`
	EndsAt     time.Time `json:"ends_at" example:"2026-01-15T10:30:00Z"`
}

// AppointmentListResponse is one page of a trainer's appointments.
type AppointmentListResponse struct {
	StatusCode int                    `json:"status_code" example:"200"`
	Items      []schedule.Appointment `json:"items"`
	NextCursor *string                `json:"next_cursor"`
	HasMore    bool                   `json:"has_more" example:"false"`
}

// Health checks that the database answers SELECT 1.
//
// Health godoc
// @Summary      Database health
// @Description  Runs SELECT 1. Mounted at GET /api/health, outside /api/v1, so the e2e warm-up path stays stable.
// @Tags         health
// @Produce      json
// @Success      200  {object}  HealthResponse
// @Failure      503  {object}  ErrorUnavailable
// @Router       /api/health [get]
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{
			StatusCode: http.StatusServiceUnavailable,
			Error:      "database unavailable",
		})

		return
	}

	writeJSON(w, http.StatusOK, HealthResponse{StatusCode: http.StatusOK, Status: "ok"})
}

// AvailableTimes lists unbooked 30-minute slots for one trainer.
//
// AvailableTimes godoc
// @Summary      List available times
// @Description  Returns unbooked 30-minute slots that start on a half-hour, start at or after starts_at, end at or before ends_at, and fall on Monday through Friday between 8:00am and 5:00pm Pacific. The window must be 31 days or less.
// @Tags         appointments
// @Produce      json
// @Param        trainer_id  path   int     true  "Trainer ID"
// @Param        starts_at   query  string  true  "Window start (RFC3339)"
// @Param        ends_at     query  string  true  "Window end (RFC3339)"
// @Success      200  {object}  AvailableTimesResponse
// @Failure      400  {object}  ErrorBadRequest
// @Failure      404  {object}  ErrorNotFound
// @Failure      500  {object}  ErrorInternal
// @Router       /api/v1/trainers/{trainer_id}/available-times [get]
func (h *Handler) AvailableTimes(w http.ResponseWriter, r *http.Request) {
	trainerID, err := pathID(r, "trainer_id")
	if err != nil {
		h.writeErr(w, err)
		return
	}

	start, end, err := queryWindow(r)
	if err != nil {
		h.writeErr(w, err)
		return
	}

	if err := schedule.ValidateWindow(start, end); err != nil {
		h.writeErr(w, err)
		return
	}

	exists, err := h.Store.TrainerExists(r.Context(), trainerID)
	if err != nil {
		h.writeErr(w, err)
		return
	}

	if !exists {
		h.writeErr(w, &schedule.Error{Status: http.StatusNotFound, Message: "trainer not found"})
		return
	}

	booked, err := h.Store.BookedStarts(r.Context(), trainerID, start, end)
	if err != nil {
		h.writeErr(w, err)
		return
	}

	slots, err := schedule.FreeSlots(r.Context(), start, end, booked)
	if err != nil {
		h.writeErr(w, err)
		return
	}

	if slots == nil {
		slots = []schedule.Slot{}
	}

	writeJSON(w, http.StatusOK, AvailableTimesResponse{StatusCode: http.StatusOK, Items: slots})
}

// CreateAppointment books one 30-minute slot.
//
// CreateAppointment godoc
// @Summary      Create an appointment
// @Description  Books one half-hour slot during Monday through Friday, 8:00am to 5:00pm Pacific. The trainer and user must exist, and the trainer must be free at starts_at.
// @Tags         appointments
// @Accept       json
// @Produce      json
// @Param        appointment  body  CreateAppointmentRequest  true  "Appointment"
// @Success      201  {object}  AppointmentResponse
// @Failure      400  {object}  ErrorBadRequest
// @Failure      404  {object}  ErrorNotFound
// @Failure      409  {object}  ErrorConflict
// @Failure      500  {object}  ErrorInternal
// @Router       /api/v1/appointments [post]
func (h *Handler) CreateAppointment(w http.ResponseWriter, r *http.Request) {
	var req CreateAppointmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeErr(w, &schedule.Error{Status: http.StatusBadRequest, Message: "invalid JSON"})
		return
	}

	if req.TrainerID == 0 || req.UserID == 0 {
		h.writeErr(w, &schedule.Error{Status: http.StatusBadRequest, Message: "trainer_id and user_id are required"})
		return
	}

	if !schedule.ValidAppointment(req.StartsAt, req.EndsAt) {
		h.writeErr(w, &schedule.Error{Status: http.StatusBadRequest, Message: "appointment must be 30 minutes and start on a half-hour"})
		return
	}

	if !schedule.DuringBusinessHours(req.StartsAt, req.EndsAt) {
		h.writeErr(w, &schedule.Error{Status: http.StatusBadRequest, Message: schedule.OutsideBusinessHoursMessage})
		return
	}

	created, err := h.Store.CreateAppointment(r.Context(), req.TrainerID, req.UserID, req.StartsAt, req.EndsAt)
	if err != nil {
		h.writeErr(w, err)
		return
	}

	w.Header().Set("Location", "/api/v1/appointments/"+strconv.FormatInt(created.ID, 10))
	writeJSON(w, http.StatusCreated, AppointmentResponse{
		StatusCode: http.StatusCreated,
		ID:         created.ID,
		TrainerID:  created.TrainerID,
		UserID:     created.UserID,
		StartsAt:   created.StartsAt,
		EndsAt:     created.EndsAt,
	})
}

// ListAppointments returns a trainer's bookings, oldest first.
//
// ListAppointments godoc
// @Summary      List scheduled appointments
// @Description  Returns a trainer's appointments ordered by start time. Use cursor from next_cursor to read the next page.
// @Tags         appointments
// @Produce      json
// @Param        trainer_id  path   int     true   "Trainer ID"
// @Param        limit       query  int     false  "Page size, default 50, maximum 100"
// @Param        cursor      query  string  false  "Opaque cursor from the previous page"
// @Success      200  {object}  AppointmentListResponse
// @Failure      400  {object}  ErrorBadRequest
// @Failure      404  {object}  ErrorNotFound
// @Failure      500  {object}  ErrorInternal
// @Router       /api/v1/trainers/{trainer_id}/appointments [get]
func (h *Handler) ListAppointments(w http.ResponseWriter, r *http.Request) {
	trainerID, err := pathID(r, "trainer_id")
	if err != nil {
		h.writeErr(w, err)
		return
	}

	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		h.writeErr(w, err)
		return
	}

	var cursor *schedule.Cursor

	if raw := r.URL.Query().Get("cursor"); raw != "" {
		decoded, err := schedule.DecodeCursor(raw)
		if err != nil {
			h.writeErr(w, err)
			return
		}

		cursor = &decoded
	}

	exists, err := h.Store.TrainerExists(r.Context(), trainerID)
	if err != nil {
		h.writeErr(w, err)
		return
	}

	if !exists {
		h.writeErr(w, &schedule.Error{Status: http.StatusNotFound, Message: "trainer not found"})
		return
	}

	items, hasMore, err := h.Store.ListAppointments(r.Context(), trainerID, cursor, limit)
	if err != nil {
		h.writeErr(w, err)
		return
	}

	if items == nil {
		items = []schedule.Appointment{}
	}

	var next *string

	if hasMore && len(items) > 0 {
		encoded := schedule.EncodeCursor(items[len(items)-1].StartsAt, items[len(items)-1].ID)
		next = &encoded
	}

	writeJSON(w, http.StatusOK, AppointmentListResponse{
		StatusCode: http.StatusOK,
		Items:      items,
		NextCursor: next,
		HasMore:    hasMore,
	})
}

// NotFound writes a JSON 404 for a path this API does not serve.
func NotFound(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotFound, ErrorResponse{
		StatusCode: http.StatusNotFound,
		Error:      "resource not found",
	})
}

// JSONErrors serves mux and replaces the router's plain-text 404 and 405.
// ServeMux reports an empty pattern for both an unmatched path and a wrong method.
// A registered handler, including one that writes a JSON 404, is called unchanged.
func JSONErrors(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := mux.Handler(r)
		if pattern != "" {
			// ServeHTTP fills the wildcard values. Handler leaves PathValue empty,
			// so /trainers/1/appointments would arrive as an invalid trainer id.
			mux.ServeHTTP(w, r)
			return
		}

		guard := &routerErrorGuard{ResponseWriter: w}
		h.ServeHTTP(guard, r)

		switch guard.status {
		case http.StatusNotFound:
			w.Header().Del("X-Content-Type-Options")
			NotFound(w, r)
		case http.StatusMethodNotAllowed:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Del("X-Content-Type-Options")
			writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{
				StatusCode: http.StatusMethodNotAllowed,
				Error:      "method not allowed",
			})
		}
	})
}

// routerErrorGuard holds back the router's plain-text 404 and 405 bodies.
type routerErrorGuard struct {
	http.ResponseWriter
	status int
	hold   bool
}

func (g *routerErrorGuard) WriteHeader(code int) {
	g.status = code
	if code == http.StatusNotFound || code == http.StatusMethodNotAllowed {
		g.hold = true
		return
	}

	g.ResponseWriter.WriteHeader(code)
}

func (g *routerErrorGuard) Write(b []byte) (int, error) {
	if g.hold {
		return len(b), nil
	}

	return g.ResponseWriter.Write(b)
}

func (h *Handler) writeErr(w http.ResponseWriter, err error) {
	if statusErr, ok := errors.AsType[*schedule.Error](err); ok {
		writeJSON(w, statusErr.Status, ErrorResponse{StatusCode: statusErr.Status, Error: statusErr.Message})
		return
	}

	writeJSON(w, http.StatusInternalServerError, ErrorResponse{
		StatusCode: http.StatusInternalServerError,
		Error:      "internal error",
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("writing response", "error", err)
	}
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id < 1 {
		return 0, &schedule.Error{Status: http.StatusBadRequest, Message: "invalid " + name}
	}

	return id, nil
}

func queryWindow(r *http.Request) (time.Time, time.Time, error) {
	startRaw := r.URL.Query().Get("starts_at")

	endRaw := r.URL.Query().Get("ends_at")
	if startRaw == "" || endRaw == "" {
		return time.Time{}, time.Time{}, &schedule.Error{Status: http.StatusBadRequest, Message: "starts_at and ends_at are required"}
	}

	start, err := time.Parse(time.RFC3339Nano, startRaw)
	if err != nil {
		return time.Time{}, time.Time{}, &schedule.Error{Status: http.StatusBadRequest, Message: "starts_at and ends_at must be RFC3339"}
	}

	end, err := time.Parse(time.RFC3339Nano, endRaw)
	if err != nil {
		return time.Time{}, time.Time{}, &schedule.Error{Status: http.StatusBadRequest, Message: "starts_at and ends_at must be RFC3339"}
	}

	return start, end, nil
}

func parseLimit(raw string) (int, error) {
	if raw == "" {
		return defaultLimit, nil
	}

	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 {
		return 0, &schedule.Error{Status: http.StatusBadRequest, Message: invalidLimitMessage}
	}

	if limit > maxLimit {
		return maxLimit, nil
	}

	return limit, nil
}
