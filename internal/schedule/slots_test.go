package schedule

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestValidateWindow(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		start   time.Time
		end     time.Time
		wantErr string
	}{
		{name: "within 31 days", start: start, end: start.Add(MaxWindow)},
		{name: "equal bounds", start: start, end: start},
		{name: "ends before start", start: start, end: start.Add(-time.Minute), wantErr: "ends_at must be after starts_at"},
		{name: "longer than 31 days", start: start, end: start.Add(MaxWindow + time.Second), wantErr: "window must be 31 days or less"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			err := ValidateWindow(tt.start, tt.end)
			if tt.wantErr == "" {
				is.NoError(err)
				return
			}

			is.EqualError(err, tt.wantErr)
		})
	}
}

func TestFreeSlots(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 1, 15, 10, 0, 0, 0, pacific).UTC()

	tests := []struct {
		name    string
		start   time.Time
		end     time.Time
		booked  []time.Time
		want    []Slot
		wantErr string
	}{
		{
			name:  "half-hour slots with a booking removed",
			start: day,
			end:   day.Add(time.Hour),
			booked: []time.Time{
				day.Add(30 * time.Minute),
			},
			want: []Slot{
				{StartsAt: day, EndsAt: day.Add(30 * time.Minute)},
			},
		},
		{
			name:  "unaligned start moves to the next half-hour",
			start: day.Add(15 * time.Minute),
			end:   day.Add(time.Hour),
			want: []Slot{
				{StartsAt: day.Add(30 * time.Minute), EndsAt: day.Add(time.Hour)},
			},
		},
		{
			name:  "slot that would end after the window is excluded",
			start: day,
			end:   day.Add(45 * time.Minute),
			want: []Slot{
				{StartsAt: day, EndsAt: day.Add(30 * time.Minute)},
			},
		},
		{
			name:  "slot ending on the window bound is included",
			start: day,
			end:   day.Add(SlotLength),
			want: []Slot{
				{StartsAt: day, EndsAt: day.Add(SlotLength)},
			},
		},
		{
			name:  "fractional start waits for the next half-hour",
			start: day.Add(time.Nanosecond),
			end:   day.Add(time.Hour),
			want: []Slot{
				{StartsAt: day.Add(SlotLength), EndsAt: day.Add(time.Hour)},
			},
		},
		{
			name:  "booking outside the generated slots is ignored",
			start: day,
			end:   day.Add(SlotLength),
			booked: []time.Time{
				day.Add(-SlotLength),
			},
			want: []Slot{
				{StartsAt: day, EndsAt: day.Add(SlotLength)},
			},
		},
		{
			name:  "equal bounds yield no slots",
			start: day,
			end:   day,
			want:  []Slot{},
		},
		{
			name:    "ends before start",
			start:   day,
			end:     day.Add(-time.Minute),
			wantErr: "ends_at must be after starts_at",
		},
		{
			name:    "window longer than 31 days",
			start:   day,
			end:     day.Add(MaxWindow + time.Second),
			wantErr: "window must be 31 days or less",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			got, err := FreeSlots(context.Background(), tt.start, tt.end, tt.booked)
			if tt.wantErr != "" {
				is.EqualError(err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			is.Equal(tt.want, got)
		})
	}
}

func TestFreeSlots_businessHours(t *testing.T) {
	t.Parallel()

	thursday := func(hour, minute int) time.Time {
		return time.Date(2026, time.January, 15, hour, minute, 0, 0, pacific)
	}
	saturday := time.Date(2026, time.January, 17, 8, 0, 0, 0, pacific)
	summer := time.Date(2026, time.July, 2, 16, 30, 0, 0, pacific)

	tests := []struct {
		name  string
		start time.Time
		end   time.Time
		want  []Slot
	}{
		{
			name:  "before 8am Pacific is excluded",
			start: thursday(7, 0),
			end:   thursday(9, 0),
			want:  slotsBetween(thursday(8, 0), thursday(9, 0)),
		},
		{
			name:  "last slot ends at 5pm Pacific",
			start: thursday(16, 0),
			end:   thursday(17, 30),
			want:  slotsBetween(thursday(16, 0), thursday(17, 0)),
		},
		{
			name:  "saturday has no slots",
			start: saturday,
			end:   time.Date(2026, time.January, 17, 17, 0, 0, 0, pacific),
			want:  []Slot{},
		},
		{
			name:  "summer afternoon Pacific stays bookable",
			start: summer,
			end:   time.Date(2026, time.July, 2, 17, 0, 0, 0, pacific),
			want:  slotsBetween(summer, time.Date(2026, time.July, 2, 17, 0, 0, 0, pacific)),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			got, err := FreeSlots(context.Background(), tt.start, tt.end, nil)
			require.NoError(t, err)
			is.Equal(tt.want, got)
		})
	}
}

func slotsBetween(start, end time.Time) []Slot {
	slots := make([]Slot, 0)
	for at := start; !at.Add(SlotLength).After(end); at = at.Add(SlotLength) {
		slots = append(slots, Slot{StartsAt: at.UTC(), EndsAt: at.Add(SlotLength).UTC()})
	}

	return slots
}

func ExampleFreeSlots() {
	start := time.Date(2026, 1, 15, 10, 0, 0, 0, pacific)
	end := time.Date(2026, 1, 15, 11, 30, 0, 0, pacific)
	booked := []time.Time{time.Date(2026, 1, 15, 10, 30, 0, 0, pacific)}

	slots, err := FreeSlots(context.Background(), start, end, booked)
	if err != nil {
		panic(err)
	}

	for _, slot := range slots {
		fmt.Printf("%s %s\n", slot.StartsAt.Format(time.RFC3339), slot.EndsAt.Format(time.RFC3339))
	}
	// Output:
	// 2026-01-15T18:00:00Z 2026-01-15T18:30:00Z
	// 2026-01-15T19:00:00Z 2026-01-15T19:30:00Z
}

func TestDuringBusinessHours(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		start time.Time
		want  bool
	}{
		{name: "Thursday 10:00am Pacific", start: time.Date(2026, 1, 15, 10, 0, 0, 0, pacific), want: true},
		{name: "Thursday 8:00am Pacific", start: time.Date(2026, 1, 15, 8, 0, 0, 0, pacific), want: true},
		{name: "Thursday 7:30am Pacific", start: time.Date(2026, 1, 15, 7, 30, 0, 0, pacific), want: false},
		{name: "Thursday 4:30pm Pacific", start: time.Date(2026, 1, 15, 16, 30, 0, 0, pacific), want: true},
		{name: "Thursday 5:00pm Pacific", start: time.Date(2026, 1, 15, 17, 0, 0, 0, pacific), want: false},
		{name: "Monday 8:00am Pacific", start: time.Date(2026, 1, 12, 8, 0, 0, 0, pacific), want: true},
		{name: "Friday 4:30pm Pacific", start: time.Date(2026, 1, 16, 16, 30, 0, 0, pacific), want: true},
		{name: "Saturday 10:00am Pacific", start: time.Date(2026, 1, 17, 10, 0, 0, 0, pacific), want: false},
		{name: "Sunday 10:00am Pacific", start: time.Date(2026, 1, 18, 10, 0, 0, 0, pacific), want: false},
		{name: "Thursday 4:30pm Pacific daylight time", start: time.Date(2026, 7, 2, 16, 30, 0, 0, pacific), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)
			is.Equal(tt.want, DuringBusinessHours(tt.start, tt.start.Add(SlotLength)))
		})
	}
}

func TestValidAppointment(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		start time.Time
		end   time.Time
		want  bool
	}{
		{name: "half-hour slot", start: start, end: start.Add(SlotLength), want: true},
		{name: "offset that lands on a unix half-hour", start: start.In(time.FixedZone("CET", 3600)), end: start.Add(SlotLength), want: true},
		{name: "hour-long span", start: start, end: start.Add(time.Hour), want: false},
		{name: "unaligned start", start: start.Add(15 * time.Minute), end: start.Add(45 * time.Minute), want: false},
		{name: "fractional second", start: start.Add(time.Nanosecond), end: start.Add(time.Nanosecond).Add(SlotLength), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)
			is.Equal(tt.want, ValidAppointment(tt.start, tt.end))
		})
	}
}

func TestEncodeCursor(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		id   int64
	}{
		{name: "round trip", id: 42},
		{name: "zero id", id: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)
			got, err := DecodeCursor(EncodeCursor(start, tt.id))
			require.NoError(t, err)
			is.True(got.StartsAt.Equal(start))
			is.Equal(tt.id, got.ID)
		})
	}
}

func TestDecodeCursor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		encoded string
	}{
		{name: "not base64", encoded: "%%%"},
		{name: "missing separator", encoded: "bm9wZQ"},
		{name: "bad timestamp", encoded: "bm90LWEtdGltZXwx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)
			_, err := DecodeCursor(tt.encoded)
			is.EqualError(err, invalidCursorMessage)
		})
	}
}
