package staff

import (
	"context"
	"fmt"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the account attention items (plan Task 4c, pre-flight I8):
// staff:manage's alone, read in the dashboard's snapshot from Staff's own
// rows — the people whose flag is on and the links as the accounts worker
// wrote them — never by asking identity. Each person's item is keyed
// <type>/<personId>, names the person and nothing more (no address, no
// reason beyond the type); the three installation items are one each,
// entity 'settings', counting the links blocked on them (reading 21).

// settingsEntity is the installation items' entityId: what the host links to
// the settings.
const settingsEntity = "settings"

// disableOverdueAfter is how long a disable Disable now asked for may wait
// unconfirmed before accountFailing is raised for it: longer than a claim's
// lease, so a disable still in hand raises nothing (Task 4c review M1).
const disableOverdueAfter = 10 * time.Minute

// erasedPersonTitle is an account item's title for an erased record (Task 4c
// review M2): the item says that an erased person's account still needs its
// disable, and names nobody.
const erasedPersonTitle = "An erased person"

// accountReasonItems is the item every reason the worker writes as a link's
// blocked_reason raises (accounts_decide.go, accounts_worker.go; the walk in
// accounts_attention_internal_test.go keeps it whole). no_employment and
// account_email_missing are judged on the person's rows instead
// (accountPersonReasons), so the item stands while the condition does, not
// while a link says it did; invalid_actor, a fault of Staff's own, is
// accountFailing.
var accountReasonItems = map[string]gen.StaffStatsAttentionItemType{
	reasonNoEmployment:         gen.AccountNoEmployment,
	reasonEmailMissing:         gen.AccountEmailMissing,
	reasonBlockedByOwner:       gen.AccountBlockedByOwner,
	reasonDisabledByOwner:      gen.AccountDisabledByOwner,
	reasonUserGone:             gen.AccountUserGone,
	reasonMismatch:             gen.AccountMismatch,
	reasonModeChanged:          gen.AccountModeChanged,
	reasonEmailDiffers:         gen.AccountEmailDiffers,
	reasonInvitationRevoked:    gen.InvitationRevoked,
	reasonInvitationUnanswered: gen.InvitationUnanswered,
	reasonNotManaged:           gen.AccountNotManaged,
	reasonPrivileged:           gen.AccountPrivileged,
	reasonNotFound:             gen.AccountNotFound,
	reasonEmailTaken:           gen.AccountEmailTaken,
	reasonNumberTaken:          gen.AccountNumberTaken,
	reasonInvalidActor:         gen.AccountFailing,
	reasonEmployeeRoleMissing:  gen.EmployeeRoleMissing,
	reasonEmployeeRoleUnsafe:   gen.EmployeeRoleUnsafe,
	reasonModeMismatch:         gen.AccountModeMismatch,
}

// accountDisableRefusals are the answers a disable asked for can meet —
// Deactivate's refusals, and a user carrying another employee number — which
// raise their item, not accountFailing, for a flag that is off or an erased
// record while the request stands: the user still enabled.
var accountDisableRefusals = map[string]bool{reasonNotManaged: true, reasonPrivileged: true, reasonMismatch: true}

// accountPersonReasons are the reasons judged on the person's rows, never
// read off a link.
var accountPersonReasons = map[string]bool{reasonNoEmployment: true, reasonEmailMissing: true}

// accountInstallationItems are the items about the installation, not a
// person, with their sentences (the host words its own from the type).
var accountInstallationItems = map[gen.StaffStatsAttentionItemType]string{
	gen.EmployeeRoleMissing: "No Owner has named the roles Staff may give, so no account is enabled",
	gen.EmployeeRoleUnsafe:  "A role named for Staff grants administration, so no account is given roles",
	gen.AccountModeMismatch: "A worker's authority mode differs from the API's, so only deactivations run",
}

// accountAttention is what the account items are judged on: the links the
// worker left a reason, an error or a disable requested on, and the people
// the person query found, at today and now.
type accountAttention struct {
	links  []store.AccountLinkAttentionRow
	people []store.AccountPersonAttentionRow
	today  time.Time
	now    time.Time
}

// readAccountAttention reads the account items' rows on q, a snapshot's.
func readAccountAttention(ctx context.Context, q *store.Queries, today, now time.Time) (accountAttention, error) {
	a := accountAttention{today: today, now: now}
	var err error
	if a.links, err = q.AccountLinkAttention(ctx); err != nil {
		return a, fmt.Errorf("staff: read the account links for attention: %w", err)
	}
	if a.people, err = q.AccountPersonAttention(ctx, dayParam(today)); err != nil {
		return a, fmt.Errorf("staff: read the people's accounts for attention: %w", err)
	}
	return a, nil
}

// accountLinkFailing is whether l raises accountFailing at now. For a
// person whose flag is on and who is not erased: three failed attempts in a
// row, or a fault of Staff's own (invalid_actor). Otherwise only from a
// disable Disable now asked for that identity has not confirmed — failed
// once, or waiting longer than disableOverdueAfter (review M1, L1); the
// worker retries it at every poll meanwhile.
func accountLinkFailing(l store.AccountLinkAttentionRow, now time.Time) bool {
	if l.Erased || !l.ProvisionAccount {
		// A refusal raises its own item instead (accountDisableRefusals).
		return l.DisableRequestedAt != nil && !accountDisableRefusals[deref(l.BlockedReason)] &&
			(l.LastError != nil || !now.Before(l.DisableRequestedAt.Add(disableOverdueAfter)))
	}
	return deref(l.BlockedReason) == reasonInvalidActor || (l.LastError != nil && l.Attempts >= accountsMaxAttempts)
}

// items is the account items.
func (a accountAttention) items() []gen.StaffStatsAttentionItem {
	var out []gen.StaffStatsAttentionItem
	for _, p := range a.people {
		kind := gen.AccountEmailMissing
		if !p.HasEmployment {
			kind = gen.AccountNoEmployment
		}
		out = append(out, personItem(kind, p.ID, p.ID, p.DisplayName, a.today))
	}
	installation := map[gen.StaffStatsAttentionItemType]*gen.StaffStatsAttentionItem{}
	for _, l := range a.links {
		if accountLinkFailing(l, a.now) {
			title := l.DisplayName
			if l.Erased {
				title = erasedPersonTitle
			}
			out = append(out, personItem(gen.AccountFailing, l.PersonID, l.PersonID, title, l.UpdatedAt))
		}
		reason := deref(l.BlockedReason)
		kind, ok := accountReasonItems[reason]
		if !ok || accountPersonReasons[reason] || kind == gen.AccountFailing {
			continue
		}
		// A flag that is off, or an erased record, raises only the refusal a
		// disable asked for was answered with, while it stands (re-check
		// MEDIUM-1 and LOW-1): the user is still enabled, and an Owner's, or
		// HR's number, to put right. A reason left from when the flag was on
		// raises nothing.
		if (l.Erased || !l.ProvisionAccount) && (l.DisableRequestedAt == nil || !accountDisableRefusals[reason]) {
			continue
		}
		title, isInstallation := accountInstallationItems[kind]
		if !isInstallation {
			name := l.DisplayName
			if l.Erased {
				name = erasedPersonTitle
			}
			out = append(out, personItem(kind, l.PersonID, l.PersonID, name, l.UpdatedAt))
			continue
		}
		item := installation[kind]
		if item == nil {
			count := int32(0)
			item = &gen.StaffStatsAttentionItem{Id: string(kind), Type: kind, Title: title, OccurredAt: l.UpdatedAt, EntityId: settingsEntity, Count: &count}
			installation[kind] = item
		}
		*item.Count++
		if l.UpdatedAt.Before(item.OccurredAt) {
			item.OccurredAt = l.UpdatedAt
		}
	}
	for _, item := range installation {
		out = append(out, *item)
	}
	return out
}
