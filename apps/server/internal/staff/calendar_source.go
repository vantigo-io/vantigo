package staff

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// calendarKindAbsence is the one kind this module projects onto the calendar
// (calendar design D1).
const calendarKindAbsence = "staff.absence"

// calendarSource is this module's contracts.CalendarSource (calendar design
// D1): the caller's own absences, through the self-service link. It is its
// own gatekeeper: staff:access, and a person the caller is linked to — no
// link, no items, never an error. Every row is the linked person's, so the
// Self audience's rule is the only one that applies, and it reads every one.
type calendarSource struct {
	q      *store.Queries
	access contracts.Access
}

var _ contracts.CalendarSource = (*calendarSource)(nil)

// newCalendarSource is Module's CalendarSource.
func newCalendarSource(d module.Deps) contracts.CalendarSource {
	return &calendarSource{q: store.New(d.Pool), access: d.Access}
}

func (s *calendarSource) CalendarKinds() []contracts.CalendarKind {
	return []contracts.CalendarKind{{Key: calendarKindAbsence, Module: "staff"}}
}

func (s *calendarSource) CalendarItems(ctx context.Context, q contracts.CalendarQuery) ([]contracts.CalendarItem, error) {
	if !contracts.HasPermission(ctx, s.access, "staff:access") {
		return nil, nil
	}
	person, err := personForUser(ctx, s.q, q.UserID)
	if err != nil {
		return nil, err
	}
	if person == nil {
		return nil, nil
	}
	rows, err := s.q.OwnCalendarAbsences(ctx, store.OwnCalendarAbsencesParams{
		PersonID: *person,
		FromDay:  pgtype.Date{Time: q.From, Valid: true},
		ToDay:    pgtype.Date{Time: q.To, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("staff: calendar absences: %w", err)
	}
	items := make([]contracts.CalendarItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, contracts.CalendarItem{
			Kind:      calendarKindAbsence,
			ID:        strconv.FormatInt(r.ID, 10),
			Title:     calendarAbsenceTitle(r),
			Subtitle:  calendarAbsenceSubtitle(r),
			AllDay:    true,
			StartDate: r.FirstDay.Time,
			EndDate:   r.LastDay.Time,
			Path:      "/staff/me?tab=absence",
		})
	}
	return items, nil
}

// calendarAbsenceTitle is the type's name in the person's language — en, else
// nb, the rule the module's own mail follows (notifications.go).
func calendarAbsenceTitle(r store.OwnCalendarAbsencesRow) string {
	if r.PreferredLanguage == "en" {
		return r.NameEn
	}
	return r.NameNb
}

// calendarAbsenceSubtitle is an hour type's hours ("3 h") and, for a request
// not yet decided, the status, in the language of the title, as the Staff
// app's own label words it; an approved or registered absence needs no word.
func calendarAbsenceSubtitle(r store.OwnCalendarAbsencesRow) string {
	var parts []string
	if r.Unit == unitHours && r.Hours.Valid {
		parts = append(parts, strconv.FormatFloat(numberOf(r.Hours), 'f', -1, 64)+" h")
	}
	if r.Status == absenceRequested {
		if r.PreferredLanguage == "en" {
			parts = append(parts, "Requested")
		} else {
			parts = append(parts, "Søkt om")
		}
	}
	return strings.Join(parts, " · ")
}
