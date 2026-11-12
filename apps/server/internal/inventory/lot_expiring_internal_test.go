package inventory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/module"
)

// failingCalendar is a business calendar whose every read fails.
type failingCalendar struct{}

func (failingCalendar) Zone(context.Context) (*time.Location, error) {
	return nil, errors.New("calendar down")
}

func (failingCalendar) Today(context.Context, time.Time) (time.Time, error) {
	return time.Time{}, errors.New("calendar down")
}

func (failingCalendar) Day(context.Context, time.Time) (time.Time, error) {
	return time.Time{}, errors.New("calendar down")
}

// TestExpiringItems_CalendarFails is the dashboard's lot items left out, not
// the attention list failed, when the business day cannot be read (M72).
func TestExpiringItems_CalendarFails(t *testing.T) {
	t.Parallel()
	s := &server{deps: module.Deps{Calendar: failingCalendar{}}}
	items, err := s.expiringItems(t.Context(), nil, time.Now())
	if err != nil || len(items) != 0 {
		t.Errorf("expiringItems = %v, %v; want none and no error", items, err)
	}
}
