package planning

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// The unbooked list (design S13, §13 amendment 9): every person in
// employment within the horizon whose confirmed booked / available over it is
// under the threshold. A person with no available hours in the horizon —
// away throughout, or starting after it — is away, not unbooked, and is not
// listed. The stats count it and the attention items list it too.

// unbookedPerson is one person on the list: the load and the free hours over
// the horizon, and the roll-off — the last day of their latest confirmed row
// ending inside it, nil when none does.
type unbookedPerson struct {
	Person      contracts.PersonEntry
	LoadPercent float64
	FreeHours   float64
	RollOff     *time.Time
}

// horizonOf is the unbooked horizon from today (S13): today to today plus
// the setting's weeks.
func horizonOf(today time.Time, row store.PlanningSetting) (time.Time, time.Time) {
	return today, addDays(today, int(row.UnbookedHorizonWeeks)*7)
}

// unbookedIn is the unbooked list over from..to, which w covers, under
// thresholdPercent, in amendment 9's order: no roll-off first, then by
// roll-off, by load and by display name.
func unbookedIn(w *window, from, to time.Time, thresholdPercent int16) []unbookedPerson {
	var out []unbookedPerson
	for _, e := range w.People {
		in := w.inputOf(e.ID, false, nil)
		in.From, in.To = from, to
		var available, booked, free float64
		for _, week := range capacityWeeks(in) {
			available += week.Available
			booked += week.Booked
			free += week.Free
		}
		if hundredths(available) <= 0 || booked*100 >= float64(thresholdPercent)*available {
			continue
		}
		u := unbookedPerson{Person: e, LoadPercent: roundHours(booked / available * 100), FreeHours: roundHours(free)}
		for _, a := range w.rowsOf(e.ID) {
			end := civil(a.ToDate)
			if a.Status != statusConfirmed || end.Before(from) || end.After(to) {
				continue
			}
			if u.RollOff == nil || end.After(*u.RollOff) {
				u.RollOff = ptrTo(end)
			}
		}
		out = append(out, u)
	}
	slices.SortStableFunc(out, func(a, b unbookedPerson) int {
		switch {
		case a.RollOff == nil && b.RollOff != nil:
			return -1
		case a.RollOff != nil && b.RollOff == nil:
			return 1
		case a.RollOff != nil && b.RollOff != nil:
			if c := a.RollOff.Compare(*b.RollOff); c != 0 {
				return c
			}
		}
		return cmp.Or(cmp.Compare(a.LoadPercent, b.LoadPercent), strings.Compare(a.Person.DisplayName, b.Person.DisplayName))
	})
	return out
}

// ListPlanningUnbooked The unbooked list
// (GET /api/v1/planning/unbooked)
//
// planning:view.
func (s *server) ListPlanningUnbooked(ctx context.Context, _ gen.ListPlanningUnbookedRequestObject) (gen.ListPlanningUnbookedResponseObject, error) {
	out, err := s.unbooked(ctx)
	if status, problem := readProblem(err); status == http.StatusServiceUnavailable {
		return gen.ListPlanningUnbooked503ApplicationProblemPlusJSONResponse(problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.ListPlanningUnbooked200JSONResponse(out), nil
}

// unbooked builds GET /unbooked's answer.
func (s *server) unbooked(ctx context.Context) (gen.PlanningUnbooked, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return gen.PlanningUnbooked{}, err
	}
	row, err := settings(ctx, store.New(s.deps.Pool))
	if err != nil {
		return gen.PlanningUnbooked{}, err
	}
	from, to := horizonOf(c.Today, row)
	w, err := s.loadWindow(ctx, from, to, nil)
	if err != nil {
		return gen.PlanningUnbooked{}, err
	}
	out := gen.PlanningUnbooked{
		Horizon: gen.PlanningDateSpan{From: apiDate(from), To: apiDate(to)},
		People:  []gen.PlanningUnbookedPerson{},
	}
	for _, u := range unbookedIn(w, from, to, row.UnbookedThresholdPercent) {
		p := gen.PlanningUnbookedPerson{Person: personView(u.Person), LoadPercent: u.LoadPercent, FreeHours: u.FreeHours}
		if u.RollOff != nil {
			p.RollOff = ptrTo(apiDate(*u.RollOff))
		}
		out.People = append(out.People, p)
	}
	return out, nil
}
