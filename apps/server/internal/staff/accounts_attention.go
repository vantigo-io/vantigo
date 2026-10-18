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
// worker left a reason or an error on, and the people the person query
// found, at today.
type accountAttention struct {
	links  []store.AccountLinkAttentionRow
	people []store.AccountPersonAttentionRow
	today  time.Time
}

// readAccountAttention reads the account items' rows on q, a snapshot's.
func readAccountAttention(ctx context.Context, q *store.Queries, today time.Time) (accountAttention, error) {
	a := accountAttention{today: today}
	var err error
	if a.links, err = q.AccountLinkAttention(ctx); err != nil {
		return a, fmt.Errorf("staff: read the account links for attention: %w", err)
	}
	if a.people, err = q.AccountPersonAttention(ctx, dayParam(today)); err != nil {
		return a, fmt.Errorf("staff: read the people's accounts for attention: %w", err)
	}
	return a, nil
}

// failing is whether l raises accountFailing: three failed attempts in a
// row, a fault of Staff's own, or a Disable now identity did not answer —
// the flag off, the link naming a user not disabled, the error set — which
// no cycle retries, the flag being off.
func accountLinkFailing(l store.AccountLinkAttentionRow) bool {
	switch {
	case deref(l.BlockedReason) == reasonInvalidActor:
		return true
	case l.LastError == nil:
		return false
	case l.Attempts >= accountsMaxAttempts:
		return true
	default:
		return !l.ProvisionAccount && l.HasUser && l.State != linkStateDisabled
	}
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
		if accountLinkFailing(l) {
			out = append(out, personItem(gen.AccountFailing, l.PersonID, l.PersonID, l.DisplayName, l.UpdatedAt))
		}
		reason := deref(l.BlockedReason)
		kind, ok := accountReasonItems[reason]
		if !l.ProvisionAccount || !ok || accountPersonReasons[reason] || kind == gen.AccountFailing {
			continue
		}
		title, isInstallation := accountInstallationItems[kind]
		if !isInstallation {
			out = append(out, personItem(kind, l.PersonID, l.PersonID, l.DisplayName, l.UpdatedAt))
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
