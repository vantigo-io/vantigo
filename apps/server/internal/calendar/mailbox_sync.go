package calendar

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the person's Outlook sync setting (calendar Outlook sync
// design O5): GET, PUT and Sync now on calendar.mailbox_sync, and the nudge
// every appointment write makes. What is pushed is mailbox_push.go's, the
// worker that pushes it mailbox_worker.go's.
//
// Whether the sync can run is the availability, asked of the neighbours
// outside any transaction: no_provider when no enabled module fills the
// MailboxCalendar slot; the provider's Status — available, switch_off
// whenever the provider's reason is switch_off, whatever its state (a
// switch off with no working credential is unavailable to the provider, but
// off is what the person needs to hear), unavailable otherwise;
// and no_account when Staff links the caller to no enabled account of the
// provider's (Workforce.DirectoryAccount, design O3) — a disabled account,
// a leaver's, is none. Nothing of a person's mailbox is ever guessed. The
// availability permission_missing is the contract's still but never
// answered: the provider's token may not show a permission granted with
// Exchange RBAC for Applications, so it never refuses for it (design O2).

// The values of calendar.mailbox_sync's columns (design O5), held by the
// module's validation rather than a CHECK.
const (
	mailboxTargetDefault = "default"
	mailboxTargetOwn     = "own"
	mailboxDetailFull    = "full"
	mailboxDetailBusy    = "busy"

	mailboxStatePending     = "pending"
	mailboxStateActive      = "active"
	mailboxStateStopping    = "stopping"
	mailboxStateOff         = "off"
	mailboxStateUnavailable = "unavailable"
	mailboxStateFailed      = "failed"

	// codeMailboxSyncUnavailable is a PUT turning the sync on, or a Sync now,
	// while it cannot run.
	codeMailboxSyncUnavailable = "mailbox_sync_unavailable"
	// codeMailboxSyncOff is a Sync now of a sync that is turned off.
	codeMailboxSyncOff = "mailbox_sync_off"

	invalidMailboxSyncTitle = "Invalid Outlook sync"
)

// mailboxDefaultKinds are the kinds a person who never turned the sync on
// is offered: their appointments and their absences.
var mailboxDefaultKinds = []string{appointmentKind, "staff.absence"}

// GetCalendarMailboxSync The caller's Outlook sync
// (GET /api/v1/calendar/mailbox-sync)
func (s *server) GetCalendarMailboxSync(ctx context.Context, _ gen.GetCalendarMailboxSyncRequestObject) (gen.GetCalendarMailboxSyncResponseObject, error) {
	me := callerID(ctx)
	availability, kinds, err := s.mailboxAvailability(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.mailboxSyncRow(ctx, s.queries(), me, false)
	if err != nil {
		return nil, err
	}
	return gen.GetCalendarMailboxSync200JSONResponse(mailboxSyncOut(row, availability, kinds)), nil
}

// PutCalendarMailboxSync Change the caller's Outlook sync
// (PUT /api/v1/calendar/mailbox-sync)
//
// The setting is replaced whole under the row's lock, If-Match the revision
// (0 for a caller who never turned it on). Turning it on, or keeping it on,
// while it cannot run is 409 mailbox_sync_unavailable; turning it off is
// always taken. A clean-up is owed — state stopping — when a sync that was on
// is turned off or moved to the other target, or one was owed already; the
// worker removes the copies (O8), then sets off, or pending to sync anew.
// Otherwise on is pending and off is off. Every write is due at once, and
// turning it on, or owing a clean-up, makes the next sync read the mailbox's
// own list first.
func (s *server) PutCalendarMailboxSync(ctx context.Context, req gen.PutCalendarMailboxSyncRequestObject) (gen.PutCalendarMailboxSyncResponseObject, error) {
	revision, ok := parseMailboxRevision(req.Params.IfMatch)
	if !ok {
		var f fieldErrors
		f.add("If-Match", "If-Match is the revision the setting was read at, such as 3, or 0 for none")
		return gen.PutCalendarMailboxSync400ApplicationProblemPlusJSONResponse(invalid(invalidMailboxSyncTitle, f)), nil
	}
	var body gen.MailboxSyncWrite
	if req.Body != nil {
		body = *req.Body
	}
	me := callerID(ctx)
	availability, available, err := s.mailboxAvailability(ctx)
	if err != nil {
		return nil, err
	}
	kinds, f := readMailboxSync(body, available)
	if f.failed() {
		return gen.PutCalendarMailboxSync400ApplicationProblemPlusJSONResponse(invalid(invalidMailboxSyncTitle, f)), nil
	}
	if body.Enabled && availability != gen.MailboxSyncAvailabilityAvailable {
		return gen.PutCalendarMailboxSync409ApplicationProblemPlusJSONResponse(mailboxSyncUnavailable(string(availability))), nil
	}
	var row store.CalendarMailboxSync
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		cur, err := s.mailboxSyncRow(ctx, txq, me, true)
		if err != nil {
			return err
		}
		var current int32
		if cur != nil {
			current = cur.Revision
		}
		if current != revision {
			return refuse(http.StatusConflict, conflict(codeRevisionMismatch, "Revision mismatch",
				fmt.Sprintf("The Outlook sync setting has revision %d; the supplied revision was %d.", current, revision)))
		}
		wasOn := cur != nil && cur.Enabled
		owed := (cur != nil && cur.State == mailboxStateStopping) ||
			(wasOn && (!body.Enabled || cur.Target != string(body.Target)))
		state := mailboxStateOff
		switch {
		case owed:
			state = mailboxStateStopping
		case body.Enabled:
			state = mailboxStatePending
		}
		reconcile := owed || (body.Enabled && !wasOn) || (cur != nil && cur.Reconcile)
		row, err = txq.UpsertMailboxSync(ctx, store.UpsertMailboxSyncParams{
			UserID: me, Enabled: body.Enabled, Target: string(body.Target), Detail: string(body.Detail),
			Kinds: kinds, State: state, Reconcile: reconcile, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("calendar: write the Outlook sync setting: %w", err)
		}
		return nil
	})
	if r, ok := asRefusal(err); ok {
		return gen.PutCalendarMailboxSync409ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PutCalendarMailboxSync200JSONResponse(mailboxSyncOut(&row, availability, available)), nil
}

// PostCalendarMailboxSyncRun Sync the caller's calendar now
// (POST /api/v1/calendar/mailbox-sync/run)
//
// The sync is made due at once; the revision stays. A sync that cannot run
// is 409 mailbox_sync_unavailable, its detail the availability; one that is
// off 409 mailbox_sync_off.
func (s *server) PostCalendarMailboxSyncRun(ctx context.Context, _ gen.PostCalendarMailboxSyncRunRequestObject) (gen.PostCalendarMailboxSyncRunResponseObject, error) {
	me := callerID(ctx)
	availability, kinds, err := s.mailboxAvailability(ctx)
	if err != nil {
		return nil, err
	}
	q := s.queries()
	row, err := s.mailboxSyncRow(ctx, q, me, false)
	if err != nil {
		return nil, err
	}
	switch {
	case availability != gen.MailboxSyncAvailabilityAvailable:
		return gen.PostCalendarMailboxSyncRun409ApplicationProblemPlusJSONResponse(mailboxSyncUnavailable(string(availability))), nil
	case row == nil || !row.Enabled:
		return gen.PostCalendarMailboxSyncRun409ApplicationProblemPlusJSONResponse(
			conflict(codeMailboxSyncOff, "Outlook sync off", "Turn the Outlook sync on before syncing now.")), nil
	}
	if err := q.NudgeMailboxSync(ctx, store.NudgeMailboxSyncParams{Now: s.deps.Clock(), UserIds: []uuid.UUID{me}}); err != nil {
		return nil, fmt.Errorf("calendar: make the Outlook sync due: %w", err)
	}
	if row, err = s.mailboxSyncRow(ctx, q, me, false); err != nil {
		return nil, err
	}
	return gen.PostCalendarMailboxSyncRun200JSONResponse(mailboxSyncOut(row, availability, kinds)), nil
}

// mailboxAvailability is whether the caller's sync can run (see the top of
// the file) and the kinds they may choose: the appointment kind, then every
// kind the sources declare, in module order — never calendar.subscription,
// whose events came from elsewhere. It calls out of the module, so never
// inside a transaction.
func (s *server) mailboxAvailability(ctx context.Context) (gen.MailboxSyncAvailability, []gen.MailboxSyncKind, error) {
	kinds := []gen.MailboxSyncKind{{Key: appointmentKind, Module: "calendar"}}
	sources, err := s.sourceKinds(ctx)
	if err != nil {
		return "", nil, err
	}
	for _, k := range sources {
		if k.Kind != subscriptionKind && k.Kind != appointmentKind {
			kinds = append(kinds, gen.MailboxSyncKind{Key: k.Kind, Module: k.Module})
		}
	}
	provider := s.mailboxCalendar()
	if provider == nil {
		return gen.MailboxSyncAvailabilityNoProvider, kinds, nil
	}
	st, err := s.mailboxStatus(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("calendar: read the mailbox provider's status: %w", err)
	}
	if a := availabilityOf(st); a != gen.MailboxSyncAvailabilityAvailable {
		return a, kinds, nil
	}
	acct, err := s.workforceDirectoryAccount(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("calendar: read the caller's directory account: %w", err)
	}
	if !usableAccount(acct, provider.Key()) {
		return gen.MailboxSyncAvailabilityNoAccount, kinds, nil
	}
	return gen.MailboxSyncAvailabilityAvailable, kinds, nil
}

// availabilityOf is the provider's status as the person's availability:
// available, switch_off whenever the reason is switch_off — cleanup or
// unavailable alike — and unavailable otherwise.
func availabilityOf(st contracts.MailboxStatus) gen.MailboxSyncAvailability {
	switch {
	case st.State == contracts.MailboxAvailable:
		return gen.MailboxSyncAvailabilityAvailable
	case st.Reason == "switch_off":
		return gen.MailboxSyncAvailabilitySwitchOff
	}
	return gen.MailboxSyncAvailabilityUnavailable
}

// usableAccount reports whether acct is a mailbox the sync may write: the
// provider's (key), with an external id, and enabled — a disabled link, a
// leaver's, is no account (design O3).
func usableAccount(acct *contracts.DirectoryAccount, key string) bool {
	return acct != nil && acct.Enabled && acct.Provider == key && acct.ExternalID != ""
}

// mailboxSyncRow is the person's row, nil when they never turned the sync
// on; lock takes its lock for the PUT.
func (s *server) mailboxSyncRow(ctx context.Context, q *store.Queries, userID uuid.UUID, lock bool) (*store.CalendarMailboxSync, error) {
	read := q.GetMailboxSync
	if lock {
		read = q.LockMailboxSync
	}
	row, err := read(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("calendar: read the Outlook sync setting: %w", err)
	}
	return &row, nil
}

// readMailboxSync holds a PUT's body to its rules — target and detail one of
// their values, each kind one the caller may choose and none twice — and
// answers the kinds in the order availableKinds lists them.
func readMailboxSync(body gen.MailboxSyncWrite, available []gen.MailboxSyncKind) ([]string, fieldErrors) {
	var f fieldErrors
	if !body.Target.Valid() {
		f.add("target", "target is default or own")
	}
	if !body.Detail.Valid() {
		f.add("detail", "detail is full or busy")
	}
	chosen := map[string]bool{}
	for i, k := range body.Kinds {
		field := fmt.Sprintf("kinds[%d]", i)
		switch {
		case !slices.ContainsFunc(available, func(a gen.MailboxSyncKind) bool { return a.Key == k }):
			f.add(field, fmt.Sprintf("'%s' is not a kind that can be synced", k))
		case chosen[k]:
			f.add(field, fmt.Sprintf("'%s' is listed twice", k))
		}
		chosen[k] = true
	}
	kinds := []string{}
	for _, a := range available {
		if chosen[a.Key] {
			kinds = append(kinds, a.Key)
		}
	}
	return kinds, f
}

// mailboxSyncOut is the setting on the wire: the row, or the defaults at
// revision 0 — off, the main calendar, full detail, and the default kinds
// the caller may choose.
func mailboxSyncOut(row *store.CalendarMailboxSync, availability gen.MailboxSyncAvailability, available []gen.MailboxSyncKind) gen.MailboxSync {
	out := gen.MailboxSync{
		Availability: availability, AvailableKinds: available,
		Target: gen.Default, Detail: gen.MailboxSyncDetailFull, State: gen.MailboxSyncStateOff,
		Kinds: []string{},
	}
	if row == nil {
		for _, k := range mailboxDefaultKinds {
			if slices.ContainsFunc(available, func(a gen.MailboxSyncKind) bool { return a.Key == k }) {
				out.Kinds = append(out.Kinds, k)
			}
		}
		return out
	}
	out.Enabled, out.Revision = row.Enabled, row.Revision
	out.Target, out.Detail = gen.MailboxSyncTarget(row.Target), gen.MailboxSyncDetail(row.Detail)
	out.State = gen.MailboxSyncState(row.State)
	out.Kinds = append(out.Kinds, row.Kinds...)
	if row.Error != nil {
		e := gen.MailboxSyncError(*row.Error)
		out.Error = &e
	}
	out.LastSyncedAt = utcPtr(row.LastSyncedAt)
	out.BusySyncedAt = utcPtr(row.BusySyncedAt)
	return out
}

// mailboxSyncUnavailable is the 409 of a sync that cannot run, its detail
// the reason's code.
func mailboxSyncUnavailable(reason string) gen.CalendarProblem {
	return conflict(codeMailboxSyncUnavailable, "Outlook sync unavailable", reason)
}

// parseMailboxRevision is the If-Match of the setting's PUT: parseIfMatch's
// revision, or 0 — bare, quoted or weakly tagged — for a caller who never
// turned the sync on.
func parseMailboxRevision(raw string) (int32, bool) {
	v := strings.TrimPrefix(strings.TrimSpace(raw), "W/")
	if v == "0" || v == `"0"` {
		return 0, true
	}
	return parseIfMatch(raw)
}

// nudgeMailboxes makes the Outlook syncs of an appointment's people due at
// once (design O7's nudge, plan reading 9), from inside the write's own
// transaction: its owner and every colleague attendee in each list — the
// attendees before the write and after it, so one removed is cleaned and one
// added is written. Rows that are off are left alone by the query.
func (s *server) nudgeMailboxes(ctx context.Context, txq *store.Queries, owner uuid.UUID, lists ...[]store.CalendarAppointmentAttendee) error {
	ids := []uuid.UUID{owner}
	for _, atts := range lists {
		for _, id := range userAttendees(atts) {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	if err := txq.NudgeMailboxSync(ctx, store.NudgeMailboxSyncParams{Now: s.deps.Clock(), UserIds: ids}); err != nil {
		return fmt.Errorf("calendar: make the Outlook syncs due: %w", err)
	}
	return nil
}
