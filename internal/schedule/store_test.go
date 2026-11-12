package schedule

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func TestMapDBErr(t *testing.T) {
	t.Parallel()

	span := trace.SpanFromContext(context.Background())

	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantMessage string
	}{
		{
			name:        "unique violation",
			err:         &pgconn.PgError{Code: "23505"},
			wantStatus:  409,
			wantMessage: "slot already booked",
		},
		{
			name:        "foreign key violation",
			err:         &pgconn.PgError{Code: "23503"},
			wantStatus:  404,
			wantMessage: "trainer or user not found",
		},
		{
			name:        "check violation",
			err:         &pgconn.PgError{Code: "23514"},
			wantStatus:  400,
			wantMessage: "appointment must be 30 minutes and start on a half-hour",
		},
		{
			name: "unexpected database error",
			err:  &pgconn.PgError{Code: "42P01"},
		},
		{
			name: "non postgres error",
			err:  errors.New("connection reset"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)
			err := mapDBErr(span, tt.err)

			var statusErr *Error
			if tt.wantStatus == 0 {
				is.NotErrorAs(err, &statusErr)
				is.ErrorContains(err, "writing appointment")

				return
			}

			require.ErrorAs(t, err, &statusErr)
			is.Equal(tt.wantStatus, statusErr.Status)
			is.Equal(tt.wantMessage, statusErr.Message)
		})
	}
}
