package customers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
)

// This file is the typed contact roles (typed contact roles design D2, D3):
// the vocabulary, the validation of a request's `roles` array, and the
// one-primary-per-role invariant every association write keeps.
//
// The invariant is the ADDRESSES' invariant (invoice-ready customer design D3;
// addresses.go), deliberately unaltered, with (customer_id, type) swapped for
// (customer_id, role) and "the oldest address" for "the longest-standing
// holder". That is not laziness: the two are the same problem, the addresses'
// version has a partial unique index, a concurrency test and a paragraph of
// documentation behind it, and a second, subtly different version of the same
// rule in one module is how the two drift. Anything below that reads like
// addresses.go is meant to.
//
// Every function here assumes the caller already holds the customer row's
// FOR NO KEY UPDATE lock (queries/addresses.sql's LockCustomer) and is inside
// that same transaction. Nothing here takes the lock itself, because the
// handlers need it for their own 404 first.

// The role vocabulary (design D2). contactRoleOrder is also the order every
// response and every payload lists roles in, so a client never sees them
// shuffle, and it is the order the promotion bookkeeping walks in, so two
// roles lost in one write promote deterministically.
const (
	contactRoleBilling       = "billing"
	contactRoleProject       = "project"
	contactRoleDecisionMaker = "decision_maker"
)

var contactRoleOrder = []string{contactRoleBilling, contactRoleProject, contactRoleDecisionMaker}

// contactRoleRank is contactRoleOrder as a sort key, with an unknown code
// last. There is no unknown code today — validateAssociationRole is the only
// way one enters — but a widened vocabulary is a value change, and a sort that
// silently drops what it does not recognise is worse than one that puts it at
// the end.
func contactRoleRank(role string) int {
	if i := slices.Index(contactRoleOrder, role); i >= 0 {
		return i
	}
	return len(contactRoleOrder)
}

// contactRole is one role an association holds, exactly the pair the contract
// answers (gen.CustomerContactRole) and exactly the pair the timeline payload
// carries — the json tags let it be both, the convention addressSnapshot
// follows for the same reason.
type contactRole struct {
	Role    string `json:"role"`
	Primary bool   `json:"primary"`
}

// requestedRole is one validated element of a request's `roles` array: the
// role, and what the request asked its primary flag to be. Primary is a
// POINTER because the field is three-valued, and the three values mean
// different things (design D2, D3):
//
//   - nil (omitted) on a role the association already holds: leave the flag
//     exactly as it is. This is the case that makes a set replace safe to
//     write: a client changing which roles a contact holds should not have to
//     echo every primary flag back, and reading an omitted flag as false would
//     turn "also give this contact billing" into "and stop being the primary
//     project contact" — which is not even applied, it is refused (see phase 1
//     of applyRoles), so the request fails for something it never said.
//   - nil on a role the association does NOT hold yet: the first-holder rule
//     decides — primary if nobody holds the role, otherwise not.
//   - non-nil: what the request said. true demotes the incumbent; an explicit
//     false on the only or primary holder is the refusal.
//
// "Asked" is still the operative word for the non-nil case: the invariant may
// override it, because the first contact given a role is its primary whatever
// the request says.
type requestedRole struct {
	Role    string
	Primary *bool
}

// wantsPrimary and clearsPrimary read requestedRole.Primary's three states
// without every call site repeating the nil check — and, more to the point,
// without any of them collapsing nil into false by accident, which is the one
// mistake this pointer exists to prevent.
func (r requestedRole) wantsPrimary() bool  { return r.Primary != nil && *r.Primary }
func (r requestedRole) clearsPrimary() bool { return r.Primary != nil && !*r.Primary }

// heldPrimary indexes a role set by role, answering each one's primary flag —
// the shape applyRoles' phase 1, requestedAsHeld and the update handler's early
// refusal all need, written once.
func heldPrimary(roles []contactRole) map[string]bool {
	out := make(map[string]bool, len(roles))
	for _, r := range roles {
		out[r.Role] = r.Primary
	}
	return out
}

// validatedAssociation is the validated, normalized values of an
// AttachCustomerContactRequest or a CustomerContactRequest
// (Endpoints/Customers/Contacts/Dtos/CustomerContactRequest.cs, plus design
// D1 and D3). RolesGiven distinguishes the two things a nil Roles can mean on
// an update: `roles: []` (hold none) from an omitted `roles` (leave them
// alone). Title is nil when the association has none.
type validatedAssociation struct {
	Title        *string
	Roles        []requestedRole
	RolesGiven   bool
	Phone, Email *string
}

// validateCustomerContactRequest is CustomerContactRequest.TryApplyTo, widened
// by design D1 and D3. Every field is validated regardless of an earlier one's
// failure and every error is reported together, keyed by the JSON field name —
// the module's all-errors-at-once shape.
//
// title and role are the same value under two names (D1): role is the
// deprecated alias the recorded corpus sends, title wins when both are given,
// and each is validated under its own noun so the message names the field the
// caller wrote. A blank string in either is an error, not an absence.
//
// rolesWhenOmitted is how many roles the association already holds, and it
// exists only for the title-or-role rule: an attach passes 0 (a new
// association holds none), an update passes the count it just read, because
// `roles` omitted on an update means "leave them alone" and an association
// that keeps three roles is not saying nothing about the person just because
// this request did not mention them.
func validateCustomerContactRequest(title, role *string, roles *[]gen.CustomerContactRoleRequest, phone, email *string, rolesWhenOmitted int) (validatedAssociation, map[string][]string) {
	errs := map[string][]string{}

	var parsedTitle *string
	switch {
	case title != nil:
		t, err := validateContactTitle(*title)
		if err != "" {
			errs["title"] = []string{err}
		} else {
			parsedTitle = &t
		}
	case role != nil:
		t, err := validateContactRole(*role)
		if err != "" {
			errs["role"] = []string{err}
		} else {
			parsedTitle = &t
		}
	}

	var parsedRoles []requestedRole
	rolesGiven := roles != nil
	if rolesGiven {
		var roleErrs []string
		seen := map[string]bool{}
		for _, r := range *roles {
			name, err := validateAssociationRole(r.Role)
			if err != "" {
				roleErrs = append(roleErrs, err)
				continue
			}
			if seen[name] {
				// Not the same as sending it once: a request naming a role
				// twice with two different primary flags has asked for two
				// contradictory things, and picking one of them silently is
				// how a client learns the wrong lesson about what it sent.
				roleErrs = append(roleErrs, fmt.Sprintf("A contact role can only be given once, but '%s' was given more than once", name))
				continue
			}
			seen[name] = true
			// r.Primary travels through as the pointer it arrived as: absent,
			// true and false are three different instructions here (see
			// requestedRole), so this is the one place that must NOT normalise
			// it into a bool.
			parsedRoles = append(parsedRoles, requestedRole{Role: name, Primary: r.Primary})
		}
		if len(roleErrs) > 0 {
			errs["roles"] = roleErrs
		}
		// Answered in the design's fixed order rather than the request's, so
		// the write's bookkeeping — and therefore which contact a lost primary
		// promotes when two roles move at once — does not depend on how a
		// client happened to order its array.
		slices.SortFunc(parsedRoles, func(a, b requestedRole) int { return contactRoleRank(a.Role) - contactRoleRank(b.Role) })
	}

	// The title-or-role rule (design D1): an association that says nothing
	// about the person is not worth having. Checked only once the two halves
	// are known to be individually valid, so a request with a too-long title
	// hears about the title rather than about a rule it did not break.
	if len(errs) == 0 {
		effectiveRoles := rolesWhenOmitted
		if rolesGiven {
			effectiveRoles = len(parsedRoles)
		}
		if parsedTitle == nil && effectiveRoles == 0 {
			errs["title"] = []string{"A contact needs a title or at least one role"}
		}
	}

	p := validateOptionalPhone("phone", phone, errs)
	e := validateOptionalEmail("email", email, errs)

	if len(errs) > 0 {
		return validatedAssociation{}, errs
	}
	return validatedAssociation{Title: parsedTitle, Roles: parsedRoles, RolesGiven: rolesGiven, Phone: p, Email: e}, nil
}

// errRolePrimaryTransitionRefused is the one role refusal that is only knowable
// under the customer's lock, so it travels out of the transaction as an error
// the way addresses.go's errPrimaryTransitionRefused does — and it is the same
// refusal, worded for contacts. It is a TYPE rather than a sentinel because the
// refusal has to say which role it is about, and a role carried in a field is a
// role the handler reads with errors.As; the alternative — wrapping a sentinel
// with the name and trimming it back out of the message — makes the message's
// wording load-bearing for control flow, which is the kind of coupling that
// breaks silently the first time somebody reworded it.
type errRolePrimaryTransitionRefused struct{ Role string }

func (e errRolePrimaryTransitionRefused) Error() string {
	return fmt.Sprintf("customers: primary contact role transition refused: %s", e.Role)
}

// rolePrimaryTransitionMessage is errRolePrimaryTransitionRefused's field
// error, keyed `roles` (design D2). It names the role, because a request
// carrying three of them should not have to guess which one was refused.
func rolePrimaryTransitionMessage(role string) string {
	return fmt.Sprintf("A contact that is the only or primary holder of the '%s' role stays primary; make another contact primary instead", role)
}

// rolePromotion is one contact that became primary for one role as a SIDE
// EFFECT of somebody else's write — a request that dropped a role it was
// primary for, a detach, or a deleted contact. The handlers record one
// timeline event per promotion, on the promoted contact, with the acting user
// who caused it (design D4), which is why this has to travel back out of the
// bookkeeping instead of being invisible inside it.
type rolePromotion struct {
	ContactID int32
	Role      string
}

// demoteRoleHolder is the demote half of demote-before-promote (design D2;
// addresses.go's demoteCurrentPrimary): the role's current primary, whoever it
// is, stops being it. A role with no holder at all is not an error — there is
// simply nothing to demote.
func demoteRoleHolder(ctx context.Context, txq *store.Queries, customerID int32, role string) error {
	current, err := txq.PrimaryContactRoleHolder(ctx, store.PrimaryContactRoleHolderParams{CustomerID: customerID, Role: role})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return txq.SetContactRolePrimary(ctx, store.SetContactRolePrimaryParams{
		CustomerID: customerID, ContactID: current, Role: role, IsPrimary: false,
	})
}

// promoteLongestStandingHolder is the promote half (design D2; addresses.go's
// promoteOldestOfType): the longest-standing remaining holder of role, other
// than excludeContactID — the contact that just gave it up — becomes its
// primary. Nobody remaining is not an error: the role is unheld now, and
// "always a primary while anyone holds the role" is vacuous when nobody does.
// It answers the promoted contact, or 0 when there was none, so the caller can
// record the event design D4 requires.
func promoteLongestStandingHolder(ctx context.Context, txq *store.Queries, customerID, excludeContactID int32, role string) (int32, error) {
	next, err := txq.OldestContactRoleHolder(ctx, store.OldestContactRoleHolderParams{
		CustomerID: customerID, Role: role, ExcludeContactID: excludeContactID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if err := txq.SetContactRolePrimary(ctx, store.SetContactRolePrimaryParams{
		CustomerID: customerID, ContactID: next, Role: role, IsPrimary: true,
	}); err != nil {
		return 0, err
	}
	return next, nil
}

// applyRoles brings one association's roles from existing to want, inside the
// caller's transaction and under the customer row's lock it already holds, and
// answers the set the association holds afterwards plus every OTHER contact
// promoted along the way (design D2).
//
// The order of the four phases is the whole correctness argument, and it is
// addresses.go's order:
//
//  1. Refuse first, write nothing. An EXPLICIT `primary: false` on a role this
//     association currently holds AS primary is the refusal — the only or the
//     primary holder stays primary — and it has to be decided before any
//     statement runs, so a request that is going to be refused leaves the
//     database untouched rather than half-applied and rolled back. An OMITTED
//     flag is not that refusal: it means "leave this one alone" (see
//     requestedRole), which is why phase 1 asks clearsPrimary() and not
//     !wantsPrimary().
//  2. Delete the dropped roles, then promote in each of them. Deleting first
//     is what makes the promotion safe: while a row with is_primary = true
//     still exists, promoting another holder of the same role would put two
//     primaries in ux_customer_contact_roles_primary at once, even if only
//     until the transaction commits.
//  3. For each kept role, demote the incumbent before this contact takes over.
//     Same index, same reason, opposite direction.
//  4. For each new role, count the holders FIRST: none means this contact is
//     the first and is primary whatever it asked for (or did not ask); some
//     means the request's own flag decides — true demotes the incumbent, false
//     or omitted joins as a plain member.
func applyRoles(ctx context.Context, txq *store.Queries, customerID, contactID int32, existing []contactRole, want []requestedRole, now time.Time) ([]contactRole, []rolePromotion, error) {
	held := heldPrimary(existing)
	wanted := make(map[string]bool, len(want))
	for _, r := range want {
		wanted[r.Role] = true
	}

	// Phase 1: refuse, before anything is written. Only an EXPLICIT false
	// refuses; an omitted flag is "leave it alone" and can never be the
	// refusal, which is what keeps a set replace from failing for something it
	// did not say.
	for _, r := range want {
		if wasPrimary, ok := held[r.Role]; ok && wasPrimary && r.clearsPrimary() {
			return nil, nil, errRolePrimaryTransitionRefused{Role: r.Role}
		}
	}

	// Phase 2: the dropped roles leave, then each of them promotes.
	keep := make([]string, 0, len(want))
	for _, r := range want {
		keep = append(keep, r.Role)
	}
	if err := txq.DeleteContactRolesNotIn(ctx, store.DeleteContactRolesNotInParams{
		CustomerID: customerID, ContactID: contactID, Keep: keep,
	}); err != nil {
		return nil, nil, err
	}
	var promotions []rolePromotion
	for _, r := range existing { // existing is already in contactRoleOrder
		if _, stillWanted := wanted[r.Role]; stillWanted || !r.Primary {
			continue
		}
		promoted, err := promoteLongestStandingHolder(ctx, txq, customerID, contactID, r.Role)
		if err != nil {
			return nil, nil, err
		}
		if promoted != 0 {
			promotions = append(promotions, rolePromotion{ContactID: promoted, Role: r.Role})
		}
	}

	// Phases 3 and 4: the roles the association is to hold, in the fixed order.
	after := make([]contactRole, 0, len(want))
	for _, r := range want {
		wasPrimary, alreadyHeld := held[r.Role]
		switch {
		case alreadyHeld:
			// Omitted keeps what it was; true promotes; explicit false on a
			// non-primary holder keeps it non-primary (false on a PRIMARY
			// holder never reaches here — phase 1 refused it).
			primary := wasPrimary || r.wantsPrimary()
			if primary && !wasPrimary {
				if err := demoteRoleHolder(ctx, txq, customerID, r.Role); err != nil {
					return nil, nil, err
				}
				if err := txq.SetContactRolePrimary(ctx, store.SetContactRolePrimaryParams{
					CustomerID: customerID, ContactID: contactID, Role: r.Role, IsPrimary: true,
				}); err != nil {
					return nil, nil, err
				}
			}
			after = append(after, contactRole{Role: r.Role, Primary: primary})
		default:
			holders, err := txq.CountContactRoleHolders(ctx, store.CountContactRoleHoldersParams{
				CustomerID: customerID, Role: r.Role, ExcludeContactID: contactID,
			})
			if err != nil {
				return nil, nil, err
			}
			// A role the association does not hold yet: the flag it asked for,
			// with omitted reading as false here and only here — the
			// first-holder rule below is what an omitted flag on a new role
			// actually means, and it is the next line.
			primary := r.wantsPrimary()
			if holders == 0 {
				primary = true
			} else if primary {
				if err := demoteRoleHolder(ctx, txq, customerID, r.Role); err != nil {
					return nil, nil, err
				}
			}
			if err := txq.InsertContactRole(ctx, store.InsertContactRoleParams{
				CustomerID: customerID, ContactID: contactID, Role: r.Role, IsPrimary: primary, Now: now,
			}); err != nil {
				return nil, nil, err
			}
			after = append(after, contactRole{Role: r.Role, Primary: primary})
		}
	}
	return after, promotions, nil
}

// releaseRoles is applyRoles' detach case (design D2): the association is gone
// — DELETE .../contacts/{contactId} removed its row, or DELETE
// /customers/contacts/{id} removed the contact and the composite foreign key's
// ON DELETE CASCADE took the role rows with it — so there is nothing to
// refuse, nothing to keep and nothing to insert, only a promotion in each role
// this association was primary for. The caller must already have deleted the
// row: promoting while a primary row still exists is the double-primary the
// partial unique index forbids, which is why this takes `held` as an argument
// rather than reading it itself.
func releaseRoles(ctx context.Context, txq *store.Queries, customerID, contactID int32, held []contactRole) ([]rolePromotion, error) {
	var promotions []rolePromotion
	for _, r := range held { // already in contactRoleOrder
		if !r.Primary {
			continue
		}
		promoted, err := promoteLongestStandingHolder(ctx, txq, customerID, contactID, r.Role)
		if err != nil {
			return nil, err
		}
		if promoted != 0 {
			promotions = append(promotions, rolePromotion{ContactID: promoted, Role: r.Role})
		}
	}
	return promotions, nil
}

// contactRolesOf reads one association's roles as the type the rest of this
// file speaks, already in the design's fixed order (the query's own ORDER BY).
func contactRolesOf(ctx context.Context, q *store.Queries, customerID, contactID int32) ([]contactRole, error) {
	rows, err := q.ContactRolesForAssociation(ctx, store.ContactRolesForAssociationParams{CustomerID: customerID, ContactID: contactID})
	if err != nil {
		return nil, err
	}
	out := make([]contactRole, 0, len(rows))
	for _, r := range rows {
		out = append(out, contactRole{Role: r.Role, Primary: r.IsPrimary})
	}
	return out, nil
}

// genContactRoles is contactRole's contract projection. It always answers a
// non-nil pointer to a non-nil slice, so `roles` is always on the wire and is
// `[]` rather than `null` for a contact that holds none — the contract keeps
// the property optional only because the recorded exchange corpus predates it
// (the same treatment SafeCustomerResponse.tags gets).
func genContactRoles(roles []contactRole) *[]gen.CustomerContactRole {
	out := make([]gen.CustomerContactRole, 0, len(roles))
	for _, r := range roles {
		out = append(out, gen.CustomerContactRole{Role: r.Role, Primary: r.Primary})
	}
	return &out
}

// rolesChanged reports whether two role sets differ at all — membership or a
// primary flag. Both sides are in contactRoleOrder, so this is an element-wise
// comparison and not a set operation.
func rolesChanged(before, after []contactRole) bool {
	return !slices.Equal(before, after)
}

// associationProblem is the 400 body every association write answers, keyed by
// field. One helper because the two writing handlers each have their own
// generated response type and would otherwise repeat the title string — and
// the title is what a UI shows above the field errors, so a typo in one of two
// copies is a visible inconsistency.
func associationProblem(errs map[string][]string) apicommon.HttpValidationProblemDetails {
	return apicommon.ValidationProblem("Invalid contact association", errs)
}

// roleErrorsFor turns errRolePrimaryTransitionRefused into the field error the
// caller sees. It takes the refusal itself rather than a plain error, so the
// handlers' errors.As is the only place that has to succeed for the role name to
// be right.
func roleErrorsFor(refused errRolePrimaryTransitionRefused) map[string][]string {
	return map[string][]string{"roles": {rolePrimaryTransitionMessage(refused.Role)}}
}
