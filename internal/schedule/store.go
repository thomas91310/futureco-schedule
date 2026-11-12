package schedule

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/trace"
)

// Querier is the subset of pgx pool and transaction methods the store uses.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Appointment is one stored 30-minute booking.
type Appointment struct {
	ID        int64     `json:"id" example:"1"`
	TrainerID int64     `json:"trainer_id" example:"1"`
	UserID    int64     `json:"user_id" example:"2"`
	StartsAt  time.Time `json:"starts_at" example:"2026-01-15T10:00:00Z"`
	EndsAt    time.Time `json:"ends_at" example:"2026-01-15T10:30:00Z"`
}

// Store runs parameterized queries against Postgres.
type Store struct {
	db Querier
}

// NewStore returns a store that uses pool or a transaction.
func NewStore(db Querier) *Store {
	return &Store{db: db}
}

// Ping checks that the database accepts a query.
func (s *Store) Ping(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "Store.Ping")
	defer span.End()

	var one int
	if err := s.db.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		recordDBError(span, err)
		return fmt.Errorf("ping database: %w", err)
	}

	return nil
}

// TrainerExists reports whether a trainer row is present.
func (s *Store) TrainerExists(ctx context.Context, trainerID int64) (bool, error) {
	ctx, span := tracer.Start(ctx, "Store.TrainerExists")
	defer span.End()

	var exists bool

	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM trainers WHERE id = $1)`, trainerID).Scan(&exists)
	if err != nil {
		recordDBError(span, err)
		return false, fmt.Errorf("checking trainer: %w", err)
	}

	return exists, nil
}

// BookedStarts returns appointment starts for the trainer in [start, end).
func (s *Store) BookedStarts(ctx context.Context, trainerID int64, start, end time.Time) ([]time.Time, error) {
	ctx, span := tracer.Start(ctx, "Store.BookedStarts")
	defer span.End()

	rows, err := s.db.Query(ctx, `
		SELECT started_at
		FROM appointments
		WHERE trainer_id = $1
		  AND started_at >= $2
		  AND started_at < $3
		ORDER BY started_at
	`, trainerID, start.UTC(), end.UTC())
	if err != nil {
		recordDBError(span, err)
		return nil, fmt.Errorf("querying booked starts: %w", err)
	}
	defer rows.Close()

	booked := make([]time.Time, 0)

	for rows.Next() {
		var started time.Time
		if err := rows.Scan(&started); err != nil {
			recordDBError(span, err)
			return nil, fmt.Errorf("scanning booked start: %w", err)
		}

		booked = append(booked, started.UTC())
	}

	if err := rows.Err(); err != nil {
		recordDBError(span, err)
		return nil, fmt.Errorf("iterating booked starts: %w", err)
	}

	return booked, nil
}

// CreateAppointment inserts one slot. The unique index on (trainer_id, started_at) rejects races.
func (s *Store) CreateAppointment(ctx context.Context, trainerID, userID int64, start, end time.Time) (Appointment, error) {
	ctx, span := tracer.Start(ctx, "Store.CreateAppointment")
	defer span.End()

	var id int64

	err := s.db.QueryRow(ctx, `
		INSERT INTO appointments (trainer_id, user_id, started_at, ended_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, trainerID, userID, start.UTC(), end.UTC()).Scan(&id)
	if err != nil {
		return Appointment{}, mapDBErr(span, err)
	}

	return Appointment{
		ID:        id,
		TrainerID: trainerID,
		UserID:    userID,
		StartsAt:  start.UTC(),
		EndsAt:    end.UTC(),
	}, nil
}

// ListAppointments returns up to limit bookings after cursor, ordered by start then id.
// hasMore is true when another row exists beyond the page.
func (s *Store) ListAppointments(ctx context.Context, trainerID int64, cursor *Cursor, limit int) ([]Appointment, bool, error) {
	ctx, span := tracer.Start(ctx, "Store.ListAppointments")
	defer span.End()

	hasCursor := cursor != nil

	var (
		cursorTime time.Time
		cursorID   int64
	)

	if hasCursor {
		cursorTime = cursor.StartsAt.UTC()
		cursorID = cursor.ID
	}

	rows, err := s.db.Query(ctx, `
		SELECT id, trainer_id, user_id, started_at, ended_at
		FROM appointments
		WHERE trainer_id = $1
		  AND (
		    $2::boolean = false
		    OR (started_at, id) > ($3::timestamptz, $4::bigint)
		  )
		ORDER BY started_at, id
		LIMIT $5
	`, trainerID, hasCursor, cursorTime, cursorID, limit+1)
	if err != nil {
		recordDBError(span, err)
		return nil, false, fmt.Errorf("querying appointments: %w", err)
	}
	defer rows.Close()

	items := make([]Appointment, 0, limit)

	for rows.Next() {
		var (
			item           Appointment
			started, ended time.Time
		)
		if err := rows.Scan(&item.ID, &item.TrainerID, &item.UserID, &started, &ended); err != nil {
			recordDBError(span, err)
			return nil, false, fmt.Errorf("scanning appointment: %w", err)
		}

		item.StartsAt = started.UTC()
		item.EndsAt = ended.UTC()
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		recordDBError(span, err)
		return nil, false, fmt.Errorf("iterating appointments: %w", err)
	}

	hasMore := false
	if len(items) > limit {
		hasMore = true
		items = items[:limit]
	}

	return items, hasMore, nil
}

func mapDBErr(span trace.Span, err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.Code {
		case "23505":
			return &Error{Status: 409, Message: "slot already booked"}
		case "23503":
			return &Error{Status: 404, Message: "trainer or user not found"}
		case "23514":
			return &Error{Status: 400, Message: "appointment must be 30 minutes and start on a half-hour"}
		}
	}

	recordDBError(span, err)

	return fmt.Errorf("writing appointment: %w", err)
}
