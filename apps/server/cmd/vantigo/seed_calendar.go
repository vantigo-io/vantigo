package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const demoCalendarZone = "Europe/Oslo"

// demoAppointment is one row of calendar.appointments, timed or all-day, with
// the colleagues invited to it.
type demoAppointment struct {
	title        string
	location     string
	allDay       bool
	startDate    string // all-day: first day, 2006-01-02
	endDate      string // all-day: last day, inclusive
	startsAt     time.Time
	endsAt       time.Time
	visibility   string
	transparency string
	rrule        string
	customerID   *int32
	attendees    []demoAttendee
}

type demoAttendee struct {
	userID uuid.UUID
	status string
}

// seedCalendar gives every demo user a handful of appointments around now: a
// customer meeting, a meeting with colleagues, a course, a private doctor's
// visit, a free focus block and one weekly series with next week's occurrence
// cancelled. It skips when the calendar already has appointments.
func seedCalendar(ctx context.Context, tx pgx.Tx, now time.Time, userIDs []uuid.UUID, customerIDs []int32) error {
	var already bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM calendar.appointments)`).Scan(&already); err != nil {
		return fmt.Errorf("check existing calendar seed state: %w", err)
	}
	if already {
		return nil
	}

	zone, err := time.LoadLocation(demoCalendarZone)
	if err != nil {
		return fmt.Errorf("load %s: %w", demoCalendarZone, err)
	}
	local := now.In(zone)
	// The Monday of this week, midnight in Oslo; the demo weeks are offsets from it.
	weekStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone)
	weekStart = weekStart.AddDate(0, 0, -((int(weekStart.Weekday()) + 6) % 7))

	at := func(weeks, days, hour, minute int) time.Time {
		return time.Date(weekStart.Year(), weekStart.Month(), weekStart.Day()+7*weeks+days, hour, minute, 0, 0, zone)
	}
	day := func(weeks, days int) string {
		return at(weeks, days, 0, 0).Format("2006-01-02")
	}

	n := len(userIDs)
	for i, owner := range userIDs {
		colleague := func(offset int) uuid.UUID { return userIDs[(i+offset)%n] }
		customerID := customerIDs[i%len(customerIDs)]

		appointments := []demoAppointment{
			{
				title: "Kundemøte: statusgjennomgang", location: "Hos kunden", visibility: "shared", transparency: "busy",
				startsAt: at(0, i%5, 10, 0), endsAt: at(0, i%5, 11, 0), customerID: &customerID,
			},
			{
				title: "Oppsummering av forrige uke", location: "Møterom Fjord", visibility: "shared", transparency: "busy",
				startsAt: at(-1, (i+1)%5, 14, 0), endsAt: at(-1, (i+1)%5, 15, 0),
				attendees: []demoAttendee{{colleague(1), "accepted"}, {colleague(2), "accepted"}},
			},
			{
				title: "Sprintplanlegging", location: "Møterom Fjord", visibility: "shared", transparency: "busy",
				startsAt: at(1, i%5, 14, 0), endsAt: at(1, i%5, 15, 30),
				attendees: []demoAttendee{{colleague(1), "accepted"}, {colleague(2), "needs-action"}, {colleague(3), "tentative"}},
			},
			{
				title: "Kurs: skyarkitektur", location: "Oslo kurs- og konferansesenter", allDay: true, visibility: "shared", transparency: "busy",
				startDate: day(2, i%4), endDate: day(2, i%4+1),
			},
			{
				title: "Lege", visibility: "private", transparency: "busy",
				startsAt: at(0, (i+2)%5, 13, 0), endsAt: at(0, (i+2)%5, 14, 0),
			},
			{
				title: "Fokus", visibility: "shared", transparency: "free",
				startsAt: at(1, (i+2)%5, 9, 30), endsAt: at(1, (i+2)%5, 11, 30),
			},
			{
				// The series began two Mondays ago and has no end.
				title: "Ukesmøte", location: "Teams", visibility: "shared", transparency: "busy",
				startsAt: at(-2, 0, 9, 0), endsAt: at(-2, 0, 9, 30),
				rrule: "FREQ=WEEKLY;INTERVAL=1;BYDAY=MO",
			},
		}

		for _, a := range appointments {
			var startDate, endDate *string
			var startsAt, endsAt *time.Time
			if a.allDay {
				startDate, endDate = &a.startDate, &a.endDate
			} else {
				startsAt, endsAt = &a.startsAt, &a.endsAt
			}
			var location, rrule *string
			if a.location != "" {
				location = &a.location
			}
			if a.rrule != "" {
				rrule = &a.rrule
			}

			var appointmentID int64
			if err := tx.QueryRow(ctx, `
				INSERT INTO calendar.appointments (
					owner_user_id, title, location, all_day,
					start_date, end_date, starts_at, ends_at, time_zone,
					visibility, transparency, rrule, customer_id,
					ical_uid, sequence, revision, created_at, updated_at
				) VALUES (
					$1, $2, $3, $4,
					$5::date, $6::date, $7, $8, $9,
					$10, $11, $12, $13,
					$14, 0, 1, $15, $15
				)
				RETURNING id
			`,
				owner, a.title, location, a.allDay,
				startDate, endDate, startsAt, endsAt, demoCalendarZone,
				a.visibility, a.transparency, rrule, a.customerID,
				uuid.NewString()+"@vantigo", now,
			).Scan(&appointmentID); err != nil {
				return fmt.Errorf("insert appointment %q: %w", a.title, err)
			}

			for _, att := range a.attendees {
				var respondedAt *time.Time
				if att.status != "needs-action" {
					respondedAt = &now
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO calendar.appointment_attendees (appointment_id, user_id, status, responded_at, created_at)
					VALUES ($1, $2, $3, $4, $5)
				`, appointmentID, att.userID, att.status, respondedAt, now); err != nil {
					return fmt.Errorf("insert attendee of %q: %w", a.title, err)
				}
			}

			if a.rrule != "" {
				// Next week's Monday is cancelled; the exception is named by the
				// day the series put the occurrence on.
				if _, err := tx.Exec(ctx, `
					INSERT INTO calendar.appointment_exceptions (appointment_id, occurrence_date, cancelled, created_at, updated_at)
					VALUES ($1, $2::date, true, $3, $3)
				`, appointmentID, day(1, 0), now); err != nil {
					return fmt.Errorf("insert exception of %q: %w", a.title, err)
				}
			}
		}
	}
	return nil
}

// loadDemoCalendarIDs resolves the ids seedCalendar needs on an installation
// seeded before the calendar existed: the demo users in demoStaffMembers order
// and the demo customers in customer-number order.
func loadDemoCalendarIDs(ctx context.Context, tx pgx.Tx) ([]uuid.UUID, []int32, error) {
	userIDs := make([]uuid.UUID, len(demoStaffMembers))
	for i, m := range demoStaffMembers {
		if err := tx.QueryRow(ctx,
			`SELECT id FROM identity.users WHERE normalized_email = $1`,
			strings.ToUpper(m.email),
		).Scan(&userIDs[i]); err != nil {
			return nil, nil, fmt.Errorf("find demo user %s: %w", m.email, err)
		}
	}

	rows, err := tx.Query(ctx,
		`SELECT id FROM customers.customers WHERE customer_number >= $1 ORDER BY customer_number LIMIT $2`,
		demoFirstCustomerNumber, demoCustomerCount,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("list demo customers: %w", err)
	}
	defer rows.Close()
	var customerIDs []int32
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			return nil, nil, err
		}
		customerIDs = append(customerIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(customerIDs) == 0 {
		return nil, nil, fmt.Errorf("no demo customers to link calendar appointments to")
	}
	return userIDs, customerIDs, nil
}

// backfillDemoCalendar brings an installation seeded before the calendar
// existed up to date: the demo roles gain their calendar permissions and the
// demo users their appointments.
func backfillDemoCalendar(ctx context.Context, tx pgx.Tx, now time.Time) error {
	for _, r := range demoCustomRoles {
		for _, permKey := range r.permissions {
			if !strings.HasPrefix(permKey, "calendar:") {
				continue
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO identity.role_permissions (role_id, permission_key)
				SELECT id, $2 FROM identity.roles WHERE normalized_name = $1
				ON CONFLICT DO NOTHING
			`, strings.ToUpper(r.name), permKey); err != nil {
				return fmt.Errorf("grant %s to demo role %q: %w", permKey, r.name, err)
			}
		}
	}
	userIDs, customerIDs, err := loadDemoCalendarIDs(ctx, tx)
	if err != nil {
		return err
	}
	return seedCalendar(ctx, tx, now, userIDs, customerIDs)
}
