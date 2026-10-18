package staff

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// decideCase is one row of the table test: the input built from the base by
// edit, and the step wanted in each mode.
type decideCase struct {
	name      string
	edit      func(*accountDecision)
	local     accountStep
	federated accountStep
}

var (
	stepNoneWant       = accountStep{Kind: accountStepNone}
	stepPrepareWant    = accountStep{Kind: accountStepPrepare}
	stepActivateWant   = accountStep{Kind: accountStepActivate}
	stepDeactivateWant = accountStep{Kind: accountStepDeactivate}
	stepUpdateWant     = accountStep{Kind: accountStepUpdate}
)

func reportWant(reason, state string) accountStep {
	return accountStep{Kind: accountStepReport, Reason: reason, State: state}
}

// TestDecide is the spec's §4 Accounts table, row by row in both modes, with
// the edge cases of §9 and the plan's Task 4a. The base is a person with the
// flag on, a work address, employee number A-1 and an open employment from
// 1 October 2026; today is 18 October 2026 and createBeforeDays 14.
func TestDecide(t *testing.T) {
	userID := uuid.MustParse("6f1c1f39-0a49-4c0e-9f1d-7d0d6a3c2b11")
	external := userID.String()
	ref := func(state, disabledBy string, credential bool, inv string) *contracts.AccountRef {
		return &contracts.AccountRef{
			UserID: userID, State: state, DisabledBy: disabledBy, Managed: true, EmployeeNumber: "A-1",
			HasCredential: credential, Invitation: contracts.Invitation{State: inv},
		}
	}
	active := func() *contracts.AccountRef {
		return ref(contracts.AccountStateActive, "", true, contracts.InvitationNone)
	}
	prepared := func() *contracts.AccountRef {
		return ref(contracts.AccountStatePending, contracts.DisabledByStaff, false, contracts.InvitationNone)
	}
	invited := func(inv string) *contracts.AccountRef { return ref(contracts.AccountStatePending, "", false, inv) }
	link := func(state string) *accountLinkFacts {
		return &accountLinkFacts{State: state, ExternalID: &external, Managed: true}
	}
	withUser := func(state string, r *contracts.AccountRef) func(*accountDecision) {
		return func(in *accountDecision) { in.Link, in.Ref = link(state), r }
	}
	employed := func(spans ...decideEmployment) func(*accountDecision) {
		return func(in *accountDecision) { in.Person.Employments = spans }
	}
	span := func(start, end string) decideEmployment {
		e := decideEmployment{Start: day(t, start)}
		if end != "" {
			d := day(t, end)
			e.End = &d
		}
		return e
	}
	all := func(fns ...func(*accountDecision)) func(*accountDecision) {
		return func(in *accountDecision) {
			for _, fn := range fns {
				fn(in)
			}
		}
	}
	both := func(name string, edit func(*accountDecision), want accountStep) decideCase {
		return decideCase{name: name, edit: edit, local: want, federated: want}
	}
	blocked := linkStateBlocked
	ended := employed(span("2025-01-01", "2026-10-17"))
	gap := employed(span("2025-01-01", "2026-09-30"), span("2026-12-01", ""))

	cases := []decideCase{
		// erased: nothing, a hold or not — the hold stops the erase only.
		both("erased, an active user after the end", all(func(in *accountDecision) { in.Person.Erased = true }, ended, withUser(linkStateActive, active())), stepNoneWant),
		both("erased, no link, due", func(in *accountDecision) { in.Person.Erased = true }, stepNoneWant),

		// flag off: nothing, with or without a link.
		both("flag off, no link", func(in *accountDecision) { in.Person.Flag = false }, stepNoneWant),
		both("flag off, an active link after the end", all(func(in *accountDecision) { in.Person.Flag = false }, ended, withUser(linkStateActive, active())), stepNoneWant),

		// flag on, no employment at all.
		both("no employment", employed(), reportWant(reasonNoEmployment, "")),
		both("no employment, a linked user", all(employed(), withUser(linkStateActive, active())), reportWant(reasonNoEmployment, "")),

		// T < F − createBeforeDays.
		both("before the window", employed(span("2026-11-10", "")), stepNoneWant),
		both("before the window, a stored late provisionFrom", func(in *accountDecision) {
			f := day(t, "2026-11-15")
			in.Person.ProvisionFrom = &f
		}, stepNoneWant),

		// F − createBeforeDays ≤ T < F.
		both("in the window, no link", employed(span("2026-10-25", "")), stepPrepareWant),
		both("in the window, on its first day", employed(span("2026-11-01", "")), stepPrepareWant),
		both("in the window, a preparing link", all(employed(span("2026-10-25", "")), func(in *accountDecision) {
			in.Link = &accountLinkFacts{State: linkStatePreparing}
		}), stepPrepareWant),
		{name: "in the window, a prepared user", edit: all(employed(span("2026-10-25", "")), withUser(linkStatePending, prepared())),
			local: stepNoneWant, federated: reportWant(reasonModeChanged, linkStatePending)},
		both("createBeforeDays 0, starting tomorrow", all(employed(span("2026-10-19", "")), func(in *accountDecision) { in.CreateBeforeDays = 0 }), stepNoneWant),
		both("createBeforeDays 0, starting today", all(employed(span("2026-10-18", "")), func(in *accountDecision) { in.CreateBeforeDays = 0 }), stepPrepareWant),

		// T ≥ F, T ≤ E or no E.
		both("due, no link", nil, stepPrepareWant),
		{name: "due, a prepared user", edit: withUser(linkStatePending, prepared()),
			local: stepActivateWant, federated: reportWant(reasonModeChanged, linkStatePending)},
		both("due, an active user", withUser(linkStateActive, active()), stepNoneWant),
		both("due, a future end", all(employed(span("2026-10-01", "2026-12-31")), withUser(linkStateActive, active())), stepNoneWant),
		both("due, ending today (disabled tomorrow)", all(employed(span("2026-10-01", "2026-10-18")), withUser(linkStateActive, active())), stepNoneWant),
		both("two concurrent employments, one ended yesterday", all(employed(span("2025-01-01", "2026-10-17"), span("2026-06-01", "")), withUser(linkStateActive, active())), stepNoneWant),
		both("an early stored provisionFrom, before the start", func(in *accountDecision) {
			f := day(t, "2026-10-10")
			in.Person.ProvisionFrom = &f
			in.Person.Employments = []decideEmployment{span("2026-11-01", "")}
		}, stepPrepareWant),
		{name: "an early stored provisionFrom, a prepared user", edit: all(func(in *accountDecision) {
			f := day(t, "2026-10-10")
			in.Person.ProvisionFrom = &f
			in.Person.Employments = []decideEmployment{span("2026-11-01", "")}
		}, withUser(linkStatePending, prepared())), local: stepActivateWant, federated: reportWant(reasonModeChanged, linkStatePending)},
		{name: "due, a pending user blocked by an Owner, already reported", edit: withUser(linkStateBlocked, ref(contracts.AccountStatePending, contracts.DisabledByOwner, false, contracts.InvitationNone)),
			local: reportWant(reasonBlockedByOwner, blocked), federated: reportWant(reasonModeChanged, linkStatePending)},
		{name: "due, a pending user blocked by an Owner", edit: withUser(linkStatePending, ref(contracts.AccountStatePending, contracts.DisabledByOwner, false, contracts.InvitationNone)),
			local: reportWant(reasonBlockedByOwner, blocked), federated: reportWant(reasonModeChanged, linkStatePending)},

		// The invitation of a pending, enabled user (local; federated reads the
		// mode change).
		{name: "invitation active", edit: withUser(linkStatePending, invited(contracts.InvitationActive)), local: stepNoneWant, federated: reportWant(reasonModeChanged, linkStatePending)},
		{name: "invitation none", edit: withUser(linkStatePending, invited(contracts.InvitationNone)), local: stepActivateWant, federated: reportWant(reasonModeChanged, linkStatePending)},
		{name: "invitation expired", edit: withUser(linkStatePending, invited(contracts.InvitationExpired)), local: stepActivateWant, federated: reportWant(reasonModeChanged, linkStatePending)},
		{name: "invitation revoked by an Owner", edit: withUser(linkStatePending, invited(contracts.InvitationRevokedByOwner)),
			local: reportWant(reasonInvitationRevoked, linkStatePending), federated: reportWant(reasonModeChanged, linkStatePending)},
		{name: "invitation revoked by an Owner, a fresh run", edit: all(withUser(linkStatePending, invited(contracts.InvitationRevokedByOwner)), func(in *accountDecision) { in.Fresh = true }),
			local: accountStep{Kind: accountStepActivate, Fresh: true}, federated: reportWant(reasonModeChanged, linkStatePending)},
		{name: "invitation exhausted", edit: withUser(linkStatePending, invited(contracts.InvitationExhausted)),
			local: reportWant(reasonInvitationUnanswered, linkStatePending), federated: reportWant(reasonModeChanged, linkStatePending)},
		{name: "invitation exhausted, a fresh run", edit: all(withUser(linkStatePending, invited(contracts.InvitationExhausted)), func(in *accountDecision) { in.Fresh = true }),
			local: accountStep{Kind: accountStepActivate, Fresh: true}, federated: reportWant(reasonModeChanged, linkStatePending)},
		{name: "invitation active, a fresh run (never re-sent)", edit: all(withUser(linkStatePending, invited(contracts.InvitationActive)), func(in *accountDecision) { in.Fresh = true }),
			local: stepNoneWant, federated: reportWant(reasonModeChanged, linkStatePending)},
		{name: "a prepared user, a fresh run", edit: all(withUser(linkStatePending, prepared()), func(in *accountDecision) { in.Fresh = true }),
			local: accountStep{Kind: accountStepActivate, Fresh: true}, federated: reportWant(reasonModeChanged, linkStatePending)},
		{name: "a prepared user with a credential", edit: withUser(linkStatePending, ref(contracts.AccountStatePending, contracts.DisabledByStaff, true, contracts.InvitationNone)),
			local: stepActivateWant, federated: stepNoneWant},
		both("a pending user with a credential", withUser(linkStatePending, ref(contracts.AccountStatePending, "", true, contracts.InvitationNone)), stepNoneWant),

		// T > E, no later employment.
		both("after the end, an active user", all(ended, withUser(linkStateActive, active())), stepDeactivateWant),
		both("after the end, a pending enabled user", all(ended, withUser(linkStatePending, invited(contracts.InvitationActive))), stepDeactivateWant),
		both("after the end, a prepared user (already disabled)", all(ended, withUser(linkStatePending, prepared())), stepNoneWant),
		both("after the end, disabled by Staff", all(ended, withUser(linkStateDisabled, ref(contracts.AccountStateDisabled, contracts.DisabledByStaff, true, contracts.InvitationNone))), stepNoneWant),
		both("after the end, disabled by an Owner", all(ended, withUser(linkStateActive, ref(contracts.AccountStateDisabled, contracts.DisabledByOwner, true, contracts.InvitationNone))), stepNoneWant),
		both("after the end, no link", ended, stepNoneWant),
		both("after the end, a preparing link", all(ended, func(in *accountDecision) { in.Link = &accountLinkFacts{State: linkStatePreparing} }), stepNoneWant),
		both("after the end, the address gone", all(ended, withUser(linkStateActive, active()), func(in *accountDecision) { in.Person.AddressPresent = false }), stepDeactivateWant),
		both("after the end, a stale stored provisionFrom", all(ended, withUser(linkStateActive, active()), func(in *accountDecision) {
			f := day(t, "2025-01-01")
			in.Person.ProvisionFrom = &f
		}), stepDeactivateWant),
		both("an Owner re-enabled a leaver (reading 32)", all(ended, withUser(linkStateDisabled, active())), stepDeactivateWant),

		// T > E, a later employment from S: the row for F = S.
		both("between employments, before S's window, an active user", all(gap, withUser(linkStateActive, active())), stepDeactivateWant),
		both("between employments, a stale stored provisionFrom", all(gap, withUser(linkStateActive, active()), func(in *accountDecision) {
			f := day(t, "2025-01-01")
			in.Person.ProvisionFrom = &f
		}), stepDeactivateWant),
		both("between employments, disabled by Staff", all(gap, withUser(linkStateDisabled, ref(contracts.AccountStateDisabled, contracts.DisabledByStaff, true, contracts.InvitationNone))), stepNoneWant),
		both("between employments, no link", gap, stepNoneWant),
		both("between employments, before S's window, disabled by an Owner", all(gap, withUser(linkStateDisabled, ref(contracts.AccountStateDisabled, contracts.DisabledByOwner, true, contracts.InvitationNone))), stepNoneWant),
		both("between employments, before S's window, the address missing, disabled by Staff", all(gap, func(in *accountDecision) { in.Person.AddressPresent = false },
			withUser(linkStateDisabled, ref(contracts.AccountStateDisabled, contracts.DisabledByStaff, true, contracts.InvitationNone))), stepNoneWant),
		both("between employments, before S's window, the email differs", all(gap, withUser(linkStateDisabled, func() *contracts.AccountRef {
			r := ref(contracts.AccountStateDisabled, contracts.DisabledByStaff, true, contracts.InvitationNone)
			r.EmailDiffers = true
			return r
		}())), stepNoneWant),
		both("between employments, in S's window, disabled by an Owner", all(gap, func(in *accountDecision) { in.Today = day(t, "2026-11-20") },
			withUser(linkStateDisabled, ref(contracts.AccountStateDisabled, contracts.DisabledByOwner, true, contracts.InvitationNone))), reportWant(reasonDisabledByOwner, blocked)),
		both("between employments, in S's window, no link", all(gap, func(in *accountDecision) { in.Today = day(t, "2026-11-20") }), stepPrepareWant),
		both("between employments, in S's window, disabled by Staff", all(gap, func(in *accountDecision) { in.Today = day(t, "2026-11-20") },
			withUser(linkStateDisabled, ref(contracts.AccountStateDisabled, contracts.DisabledByStaff, true, contracts.InvitationNone))), stepNoneWant),
		both("between employments, a stored provisionFrom after the end, reached", all(gap, withUser(linkStateDisabled, ref(contracts.AccountStateDisabled, contracts.DisabledByStaff, true, contracts.InvitationNone)), func(in *accountDecision) {
			f := day(t, "2026-10-15")
			in.Person.ProvisionFrom = &f
		}), stepActivateWant),

		// Rehire: L disabled, flag on, T ≥ F.
		both("rehire, disabled by Staff", all(gap, func(in *accountDecision) { in.Today = day(t, "2026-12-01") },
			withUser(linkStateDisabled, ref(contracts.AccountStateDisabled, contracts.DisabledByStaff, true, contracts.InvitationNone))), stepActivateWant),
		both("rehire, disabled by an Owner", all(gap, func(in *accountDecision) { in.Today = day(t, "2026-12-01") },
			withUser(linkStateDisabled, ref(contracts.AccountStateDisabled, contracts.DisabledByOwner, true, contracts.InvitationNone))), reportWant(reasonDisabledByOwner, blocked)),
		both("disabled by an Owner during the employment", withUser(linkStateActive, ref(contracts.AccountStateDisabled, contracts.DisabledByOwner, true, contracts.InvitationNone)), reportWant(reasonDisabledByOwner, blocked)),
		both("disabled with no one recorded (read as an Owner's)", withUser(linkStateActive, ref(contracts.AccountStateDisabled, "", true, contracts.InvitationNone)), reportWant(reasonDisabledByOwner, blocked)),
		both("disabled by an Owner, already reported (the reason kept)", withUser(linkStateBlocked, ref(contracts.AccountStateDisabled, contracts.DisabledByOwner, true, contracts.InvitationNone)), reportWant(reasonDisabledByOwner, blocked)),

		// The directory deactivated the user (SCIM).
		both("the directory, during the employment", withUser(linkStateActive, ref(contracts.AccountStateDisabled, contracts.DisabledByDirectory, true, contracts.InvitationNone)), stepNoneWant),
		both("the directory, after the end", all(ended, withUser(linkStateActive, ref(contracts.AccountStateDisabled, contracts.DisabledByDirectory, true, contracts.InvitationNone))), stepDeactivateWant),
		both("the directory, after the end, the link recorded disabled (every run)", all(ended, withUser(linkStateDisabled, ref(contracts.AccountStateDisabled, contracts.DisabledByDirectory, true, contracts.InvitationNone))), stepDeactivateWant),
		both("the directory, between employments, the link recorded disabled", all(gap, withUser(linkStateDisabled, ref(contracts.AccountStateDisabled, contracts.DisabledByDirectory, true, contracts.InvitationNone))), stepDeactivateWant),

		// The user as identity reports them.
		both("unmanaged: linked, not managed by Staff", withUser(linkStateLinked, &contracts.AccountRef{UserID: userID, State: contracts.AccountStateActive, EmployeeNumber: "A-1"}),
			accountStep{Kind: accountStepNone, State: linkStateLinked}),
		both("worker link, user unmanaged by an Owner", withUser(linkStateActive, &contracts.AccountRef{UserID: userID, State: contracts.AccountStateActive, EmployeeNumber: "A-1"}),
			reportWant(reasonNotManaged, "")),
		both("worker link, user promoted to Owner (privileged, unmanaged)", withUser(linkStateActive, &contracts.AccountRef{UserID: userID, State: contracts.AccountStateActive, Privileged: true}),
			reportWant(reasonPrivileged, blocked)),
		both("worker link, a managed SystemAdmin", withUser(linkStateActive, func() *contracts.AccountRef { r := active(); r.Privileged = true; return r }()),
			reportWant(reasonPrivileged, blocked)),
		both("self-service link, an Owner employee (privileged, unmanaged)", withUser(linkStateLinked, &contracts.AccountRef{UserID: userID, State: contracts.AccountStateActive, Privileged: true}),
			accountStep{Kind: accountStepNone, State: linkStateLinked}),
		both("self-service link, the user since adopted, a leaver", all(ended, withUser(linkStateLinked, active())), stepDeactivateWant),
		both("worker link, user unmanaged, after the end", all(ended, withUser(linkStateDisabled, &contracts.AccountRef{UserID: userID, State: contracts.AccountStateActive, EmployeeNumber: "A-1"})),
			reportWant(reasonNotManaged, "")),
		both("unmanaged, after the end", all(ended, withUser(linkStateLinked, &contracts.AccountRef{UserID: userID, State: contracts.AccountStateActive, EmployeeNumber: "A-1"})),
			accountStep{Kind: accountStepNone, State: linkStateLinked}),
		both("the user gone on a worker's link (never re-created)", withUser(linkStateActive, nil), reportWant(reasonUserGone, linkStateFailed)),
		both("the user gone on a pending link", withUser(linkStatePending, nil), reportWant(reasonUserGone, linkStateFailed)),
		both("the user gone on HR's self-service link", withUser(linkStateLinked, nil), stepNoneWant),
		both("another employee number", withUser(linkStateActive, func() *contracts.AccountRef { r := active(); r.EmployeeNumber = "B-2"; return r }()), reportWant(reasonMismatch, blocked)),
		both("no employee number in identity", withUser(linkStateActive, func() *contracts.AccountRef { r := active(); r.EmployeeNumber = ""; return r }()), reportWant(reasonMismatch, blocked)),
		both("another employee number, after the end (nothing)", all(ended, withUser(linkStateActive, func() *contracts.AccountRef { r := active(); r.EmployeeNumber = "B-2"; return r }())), reportWant(reasonMismatch, blocked)),
		both("renumbered (identity holds the synced number)", all(withUser(linkStateActive, func() *contracts.AccountRef { r := active(); r.EmployeeNumber = "OLD-7"; return r }()), func(in *accountDecision) { in.Link.SyncedEmployeeNumber = "old-7" }), stepUpdateWant),
		both("renumbered, the spec changed", all(withUser(linkStateActive, func() *contracts.AccountRef { r := active(); r.EmployeeNumber = "OLD-7"; return r }()), func(in *accountDecision) { in.Link.SyncedEmployeeNumber = "old-7" }, func(in *accountDecision) { in.Link.SpecChanged = true }), stepUpdateWant),
		both("renumbered, a leaver", all(ended, withUser(linkStateActive, func() *contracts.AccountRef { r := active(); r.EmployeeNumber = "OLD-7"; return r }()), func(in *accountDecision) { in.Link.SyncedEmployeeNumber = "old-7" }), stepDeactivateWant),
		both("renumbered, the number matching neither", all(withUser(linkStateActive, func() *contracts.AccountRef { r := active(); r.EmployeeNumber = "OLD-7"; return r }()), func(in *accountDecision) { in.Link.SyncedEmployeeNumber = "C-3" }), reportWant(reasonMismatch, blocked)),
		both("the synced number is the person's, identity another", all(withUser(linkStateActive, func() *contracts.AccountRef { r := active(); r.EmployeeNumber = "B-2"; return r }()), func(in *accountDecision) { in.Link.SyncedEmployeeNumber = "A-1" }), reportWant(reasonMismatch, blocked)),
		both("the same number in another case", withUser(linkStateActive, func() *contracts.AccountRef { r := active(); r.EmployeeNumber = " a-1 "; return r }()), stepNoneWant),

		// The spec changed, the address differs.
		both("the spec changed", all(withUser(linkStateActive, active()), func(in *accountDecision) { in.Link.SpecChanged = true }), stepUpdateWant),
		both("the spec changed, in the window", all(employed(span("2026-10-25", "")), withUser(linkStateDisabled, ref(contracts.AccountStateDisabled, contracts.DisabledByStaff, true, contracts.InvitationNone)), func(in *accountDecision) { in.Link.SpecChanged = true }), stepUpdateWant),
		{name: "the email differs, active", edit: withUser(linkStateActive, func() *contracts.AccountRef { r := active(); r.EmailDiffers = true; return r }()),
			local: reportWant(reasonEmailDiffers, ""), federated: stepNoneWant},
		{name: "the email differs, active, the spec changed", edit: all(withUser(linkStateActive, func() *contracts.AccountRef { r := active(); r.EmailDiffers = true; return r }()), func(in *accountDecision) { in.Link.SpecChanged = true }),
			local: accountStep{Kind: accountStepUpdate, Reason: reasonEmailDiffers}, federated: stepUpdateWant},
		{name: "the email differs, pending", edit: withUser(linkStatePending, func() *contracts.AccountRef {
			r := invited(contracts.InvitationActive)
			r.EmailDiffers = true
			return r
		}()),
			local: stepUpdateWant, federated: reportWant(reasonModeChanged, linkStatePending)},

		// The address.
		both("due, the address missing, no link", func(in *accountDecision) { in.Person.AddressPresent = false }, reportWant(reasonEmailMissing, "")),
		{name: "due, the address missing, a prepared user", edit: all(withUser(linkStatePending, prepared()), func(in *accountDecision) { in.Person.AddressPresent = false }),
			local: reportWant(reasonEmailMissing, ""), federated: reportWant(reasonModeChanged, linkStatePending)},
		both("before the window, a current employee, the spec changed", all(withUser(linkStateActive, active()), func(in *accountDecision) {
			f := day(t, "2026-11-15")
			in.Person.ProvisionFrom = &f
			in.Link.SpecChanged = true
		}), stepUpdateWant),
		both("before the window, disabled by an Owner", all(employed(span("2026-11-10", "")), withUser(linkStateActive, ref(contracts.AccountStateDisabled, contracts.DisabledByOwner, true, contracts.InvitationNone))), reportWant(reasonDisabledByOwner, blocked)),
		both("before the window, disabled by Staff (never activated early)", all(employed(span("2026-11-10", "")), withUser(linkStateDisabled, ref(contracts.AccountStateDisabled, contracts.DisabledByStaff, true, contracts.InvitationNone))), stepNoneWant),
		both("before the window, the address missing", all(employed(span("2026-11-10", "")), func(in *accountDecision) { in.Person.AddressPresent = false }), stepNoneWant),
	}

	for _, c := range cases {
		for _, mode := range []string{contracts.AccountModeLocal, contracts.AccountModeFederated} {
			in := accountDecision{
				Person: accountPersonFacts{
					Flag: true, AddressPresent: true, EmployeeNumber: "A-1",
					Employments: []decideEmployment{span("2026-10-01", "")},
				},
				Mode: mode, CreateBeforeDays: 14, Today: day(t, "2026-10-18"),
			}
			if c.edit != nil {
				c.edit(&in)
			}
			want := c.local
			if mode == contracts.AccountModeFederated {
				want = c.federated
			}
			if got := decide(in); got != want {
				t.Errorf("%s (%s): decide = %+v, want %+v", c.name, mode, got, want)
			}
		}
	}
}

// TestDecide_TheOctoberDSTDay pins that decide judges civil days: on 25
// October 2026 Europe/Oslo leaves summer time, so the day is 25 hours long.
// decide is handed the day (the caller's civilToday), and a day given as a
// local midnight or any hour of it is the same civil date — never a UTC
// instant a day early or late.
func TestDecide_TheOctoberDSTDay(t *testing.T) {
	oslo, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatal(err)
	}
	base := func(start string, before int, today time.Time) accountDecision {
		return accountDecision{
			Person: accountPersonFacts{Flag: true, AddressPresent: true, EmployeeNumber: "A-1",
				Employments: []decideEmployment{{Start: day(t, start)}}},
			Mode: contracts.AccountModeLocal, CreateBeforeDays: before, Today: today,
		}
	}
	for _, c := range []struct {
		name string
		in   accountDecision
		want accountStepKind
	}{
		{"starting on the day, at Oslo's midnight", base("2026-10-25", 0, time.Date(2026, 10, 25, 0, 0, 0, 0, oslo)), accountStepPrepare},
		{"starting on the day, at its last hour", base("2026-10-25", 0, time.Date(2026, 10, 25, 23, 30, 0, 0, oslo)), accountStepPrepare},
		{"starting the next day, at its last hour", base("2026-10-26", 0, time.Date(2026, 10, 25, 23, 30, 0, 0, oslo)), accountStepNone},
		{"a window of one day across the change", base("2026-10-26", 1, civilToday(time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC), oslo)), accountStepPrepare},
		{"the day before the window", base("2026-10-26", 1, civilToday(time.Date(2026, 10, 24, 21, 59, 0, 0, time.UTC), oslo)), accountStepNone},
	} {
		if got := decide(c.in).Kind; got != c.want {
			t.Errorf("%s: decide = %s, want %s", c.name, got, c.want)
		}
	}
}

// TestDecide_Purity pins that the decision table is pure: accounts_decide.go
// imports the time package and the contracts only — no context, no store, no
// driver — never touches the clock, called or not, declares no method (a
// receiver would reach the server's Deps) and names no provider. Every fact is
// an input, so the worker's snapshot, not decide, is what reads.
func TestDecide_Purity(t *testing.T) {
	const name = "accounts_decide.go"
	allowed := map[string]bool{"time": true, "github.com/vantigo-io/vantigo/server/internal/contracts": true}
	clock := map[string]bool{"Now": true, "Since": true, "Until": true, "After": true, "AfterFunc": true, "Tick": true, "NewTicker": true, "NewTimer": true, "Sleep": true, "Local": true}
	f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	timeName, contractsName := "", ""
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if !allowed[path] {
			t.Errorf("%s imports %q; the decision table imports only %v", name, path, allowed)
		}
		local := lastElem(path)
		if imp.Name != nil {
			local = imp.Name.Name
		}
		switch path {
		case "time":
			timeName = local
		case "github.com/vantigo-io/vantigo/server/internal/contracts":
			contractsName = local
		}
	}
	decls := 0
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			decls++
			if fn.Recv != nil {
				t.Errorf("%s declares the method %s; the decision table is functions of their inputs", name, fn.Name.Name)
			}
		}
	}
	if decls == 0 {
		t.Errorf("%s declares no function; want decide", name)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if timeName != "" && pkg.Name == timeName && clock[sel.Sel.Name] {
			t.Errorf("%s uses time.%s; today is an input", name, sel.Sel.Name)
		}
		if contractsName != "" && pkg.Name == contractsName && (sel.Sel.Name == "AccountProvisioning" || sel.Sel.Name == "UserDirectory") {
			t.Errorf("%s names contracts.%s; decide calls no provider", name, sel.Sel.Name)
		}
		return true
	})
}

// lastElem is the last element of an import path.
func lastElem(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

// TestDecide_AccountFrom is the effective provisionFrom the worker acts on
// (and the Account tab will show): the stored date, else the start of the
// earliest employment not ended by today; between employments the later
// start, unless the stored date falls after the earlier end.
func TestDecide_AccountFrom(t *testing.T) {
	today := day(t, "2026-10-18")
	at := func(s string) *time.Time { d := day(t, s); return &d }
	span := func(start string, end *time.Time) decideEmployment {
		return decideEmployment{Start: day(t, start), End: end}
	}
	for _, c := range []struct {
		name   string
		stored *time.Time
		emps   []decideEmployment
		want   string
	}{
		{"no employment, none stored", nil, nil, ""},
		{"no employment, a stored date", at("2026-11-01"), nil, "2026-11-01"},
		{"one open employment", nil, []decideEmployment{span("2026-10-01", nil)}, "2026-10-01"},
		{"a future employment", nil, []decideEmployment{span("2026-12-01", nil)}, "2026-12-01"},
		{"the earliest of two open", nil, []decideEmployment{span("2026-06-01", nil), span("2025-01-01", at("2026-12-31"))}, "2025-01-01"},
		{"an ended one ignored", nil, []decideEmployment{span("2024-01-01", at("2026-10-17")), span("2026-06-01", nil)}, "2026-06-01"},
		{"ending today still counts", nil, []decideEmployment{span("2024-01-01", at("2026-10-18"))}, "2024-01-01"},
		{"every employment ended, none stored", nil, []decideEmployment{span("2024-01-01", at("2026-10-17"))}, ""},
		{"every employment ended, a stored date", at("2024-01-01"), []decideEmployment{span("2024-01-01", at("2026-10-17"))}, "2024-01-01"},
		{"a stored early date", at("2026-10-10"), []decideEmployment{span("2026-11-01", nil)}, "2026-10-10"},
		{"a stored late date", at("2026-11-15"), []decideEmployment{span("2026-10-01", nil)}, "2026-11-15"},
		{"between employments, none stored", nil, []decideEmployment{span("2025-01-01", at("2026-09-30")), span("2026-12-01", nil)}, "2026-12-01"},
		{"between employments, a stale stored date", at("2025-01-01"), []decideEmployment{span("2025-01-01", at("2026-09-30")), span("2026-12-01", nil)}, "2026-12-01"},
		{"between employments, a stored date after the end", at("2026-10-15"), []decideEmployment{span("2025-01-01", at("2026-09-30")), span("2026-12-01", nil)}, "2026-10-15"},
	} {
		got, ok := accountFrom(c.stored, c.emps, today)
		gotS := ""
		if ok {
			gotS = got.Format(time.DateOnly)
		}
		if gotS != c.want {
			t.Errorf("%s: accountFrom = %q, want %q", c.name, gotS, c.want)
		}
	}
}
