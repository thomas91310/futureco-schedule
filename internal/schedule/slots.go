// Package schedule calculates bookable slots and stores appointments.
package schedule

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // embed zoneinfo so America/Los_Angeles works without the system database

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	// SlotLength is the only appointment duration the schema allows.
	SlotLength = 30 * time.Minute
	// MaxWindow is the longest availability query the API accepts.
	MaxWindow = 31 * 24 * time.Hour

	halfHourSeconds int64 = 1800

	invalidCursorMessage = "invalid cursor"

	// OutsideBusinessHoursMessage is the 400 body for a slot outside Monday–Friday 8:00am–5:00pm Pacific.
	OutsideBusinessHoursMessage = "appointments must be Monday through Friday, 8:00am to 5:00pm Pacific"
)

var pacific = func() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		panic(err)
	}

	return loc
}()

var tracer = otel.Tracer("futureco-schedule/schedule")

// Slot is one bookable half-hour.
type Slot struct {
	StartsAt time.Time `json:"starts_at" example:"2026-01-15T10:00:00Z"`
	EndsAt   time.Time `json:"ends_at" example:"2026-01-15T10:30:00Z"`
}

// Error is a request failure that already has an HTTP status.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	return e.Message
}

// ValidateWindow rejects a range that ends before it starts or is longer than MaxWindow.
func ValidateWindow(start, end time.Time) error {
	if end.Before(start) {
		return &Error{Status: 400, Message: "ends_at must be after starts_at"}
	}

	if end.Sub(start) > MaxWindow {
		return &Error{Status: 400, Message: "window must be 31 days or less"}
	}

	return nil
}

// FreeSlots returns unbooked half-hour slots inside the window and inside Pacific business hours.
func FreeSlots(ctx context.Context, start, end time.Time, booked []time.Time) ([]Slot, error) {
	_, span := tracer.Start(ctx, "schedule.FreeSlots")
	defer span.End()

	if err := ValidateWindow(start, end); err != nil {
		return nil, err
	}

	taken := make(map[int64]struct{}, len(booked))
	for _, b := range booked {
		taken[b.UTC().Unix()] = struct{}{}
	}

	slots := make([]Slot, 0)

	for slotStart := alignUp(start); ; slotStart = slotStart.Add(SlotLength) {
		slotEnd := slotStart.Add(SlotLength)
		if slotEnd.After(end) {
			break
		}

		if _, ok := taken[slotStart.Unix()]; ok {
			continue
		}

		if !DuringBusinessHours(slotStart, slotEnd) {
			continue
		}

		slots = append(slots, Slot{StartsAt: slotStart, EndsAt: slotEnd})
	}

	return slots, nil
}

// alignUp returns the earliest half-hour instant at or after t.
func alignUp(t time.Time) time.Time {
	t = t.UTC()
	sec := t.Unix()
	rem := sec % halfHourSeconds

	floor := sec - rem
	if rem < 0 {
		floor -= halfHourSeconds
	}

	aligned := time.Unix(floor, 0).UTC()
	if aligned.Before(t) {
		return aligned.Add(SlotLength)
	}

	return aligned
}

// DuringBusinessHours reports whether the span is on a weekday and fits in 8:00am–5:00pm Pacific.
// The appointment must start at or after 8:00am and end at or before 5:00pm, so 4:30pm is the last start.
func DuringBusinessHours(start, end time.Time) bool {
	localStart := start.In(pacific)
	localEnd := end.In(pacific)

	if localStart.Weekday() == time.Saturday || localStart.Weekday() == time.Sunday {
		return false
	}

	if localEnd.Year() != localStart.Year() || localEnd.YearDay() != localStart.YearDay() {
		return false
	}

	open := time.Date(localStart.Year(), localStart.Month(), localStart.Day(), 8, 0, 0, 0, pacific)
	closeAt := time.Date(localStart.Year(), localStart.Month(), localStart.Day(), 17, 0, 0, 0, pacific)

	return !localStart.Before(open) && !localEnd.After(closeAt)
}

// ValidAppointment reports whether the span is exactly one half-hour slot.
func ValidAppointment(start, end time.Time) bool {
	start = start.UTC()
	if start.Nanosecond() != 0 || start.Unix()%halfHourSeconds != 0 {
		return false
	}

	return end.Equal(start.Add(SlotLength))
}

// Cursor is the keyset position of an appointment, ordered by start then id.
type Cursor struct {
	StartsAt time.Time
	ID       int64
}

// EncodeCursor returns an opaque cursor for the appointment list.
func EncodeCursor(startsAt time.Time, id int64) string {
	raw := startsAt.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(id, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor parses a cursor from EncodeCursor.
func DecodeCursor(encoded string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return Cursor{}, &Error{Status: 400, Message: invalidCursorMessage}
	}

	startsRaw, idRaw, ok := strings.Cut(string(raw), "|")
	if !ok {
		return Cursor{}, &Error{Status: 400, Message: invalidCursorMessage}
	}

	startsAt, err := time.Parse(time.RFC3339Nano, startsRaw)
	if err != nil {
		return Cursor{}, &Error{Status: 400, Message: invalidCursorMessage}
	}

	id, err := strconv.ParseInt(idRaw, 10, 64)
	if err != nil {
		return Cursor{}, &Error{Status: 400, Message: invalidCursorMessage}
	}

	return Cursor{StartsAt: startsAt, ID: id}, nil
}

// recordDBError marks an unexpected database failure on the current span.
func recordDBError(span trace.Span, err error) {
	span.RecordError(err)
	span.SetStatus(codes.Error, "database request failed")
}
