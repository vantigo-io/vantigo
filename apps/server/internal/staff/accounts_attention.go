package staff

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
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
//
// A provider link (Entra design D7) raises its own: its reasons
// (accountReasonItems), the facts the provider reported on it
// (providerCodeItems — licence_unavailable once per person, its SKU in no
// title), a pass not shown within passUnrevealedAfter, and an announcement
// nothing fulfilled within accountNotArrivedAfter. The provider's own state
// is the installation's: the status the worker last recorded on
// settings.provider_status (providerStatusItem), counted over the people of
// that kind whose flag is on and raised only while someone has the kind.
// The item types are the design's, named for Entra, though the codes and
// the slot are generic (plan reading 10). A group section the worker left
// failed raises groupSyncFailed, one per section, naming the group
// (delivery D).

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
	reasonErasureIncomplete:    gen.ErasureIncomplete,
	// The provider link's reasons (accounts_provider.go). Its
	// provider_unavailable is judged on settings.provider_status instead
	// (accountStatusReasons); external_enabled — a delete the provider
	// refused for an account found enabled, which a deactivate settles
	// next — is a fault to look at, accountFailing.
	reasonProviderUnavailable:      gen.EntraNotConnected,
	reasonExternalAmbiguous:        gen.AccountAmbiguousInEntra,
	reasonExternalAddressTaken:     gen.AccountAddressTakenInEntra,
	reasonExternalSyncedOnPremises: gen.AccountSyncedOnPremises,
	reasonExternalPrivileged:       gen.AccountPrivilegedInEntra,
	reasonExternalUnconfirmed:      gen.AccountUnconfirmedInEntra,
	reasonExternalEnabled:          gen.AccountFailing,
}

// accountStatusReasons are the reasons judged on settings.provider_status,
// never read off a link: the provider's state is the installation's.
var accountStatusReasons = map[string]bool{reasonProviderUnavailable: true}

// providerCodeItems is the item each code a provider link's reported_codes
// may carry raises: the provider's (contracts.Reported*, a licence's with
// ":" and the SKU after it) and Staff's own manager_not_in_entra.
// pass_not_allowed raises none: the Account tab says it beside the pass,
// and nothing on the dashboard would let HR act on it.
var providerCodeItems = map[string]gen.StaffStatsAttentionItemType{
	contracts.ReportedHireDateNotWritten: gen.HireDateNotWritten,
	contracts.ReportedPrivileged:         gen.AccountPrivilegedInEntra,
	contracts.ReportedLicenceUnavailable: gen.LicenceUnavailable,
	contracts.ReportedManagerNotFound:    gen.ManagerNotInEntra,
	codeManagerNotInEntra:                gen.ManagerNotInEntra,
}

// providerCodeItem is the item code raises, false for none: a licence's
// code is judged on its part before the SKU.
func providerCodeItem(code string) (gen.StaffStatsAttentionItemType, bool) {
	base, _, _ := strings.Cut(code, ":")
	kind, ok := providerCodeItems[base]
	return kind, ok
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
	gen.EntraNotConnected:   "The identity provider is not connected, so no external account is made or changed",
	gen.EntraFailing:        "The identity provider's connection is failing, so no external account is made or changed",
	gen.SecretExpiring:      "The identity provider's credential expires soon",
	gen.SecretExpired:       "The identity provider's credential has expired, so no external account is made or changed",
}

// providerStatusItem is the installation item a provider's recorded status
// raises, false for none: unconfigured → entraNotConnected; failing →
// secretExpired when its credential expired, else entraFailing; connected
// with a credential about to expire → secretExpiring.
func providerStatusItem(status, reason string) (gen.StaffStatsAttentionItemType, bool) {
	switch {
	case status == contracts.ProviderUnconfigured:
		return gen.EntraNotConnected, true
	case status == contracts.ProviderFailing && reason == "secret_expired":
		return gen.SecretExpired, true
	case status == contracts.ProviderFailing:
		return gen.EntraFailing, true
	case reason == "secret_expiring":
		return gen.SecretExpiring, true
	}
	return "", false
}

// accountAttention is what the account items are judged on: the links the
// worker left a reason, an error or a disable requested on, and the people
// the person query found, at today and now.
type accountAttention struct {
	links  []store.AccountLinkAttentionRow
	people []store.AccountPersonAttentionRow
	// providers is settings.provider_status, and kinds the people whose
	// flag is on of each provider's kind.
	providers map[string]providerStatusSeen
	kinds     map[string]int64
	// groups is the group sections the worker left failed (groupSyncFailed).
	groups []store.ListFailedGroupTraitsRow
	today  time.Time
	now    time.Time
}

// readAccountAttention reads the account items' rows on q, a snapshot's.
func readAccountAttention(ctx context.Context, q *store.Queries, today, now time.Time) (accountAttention, error) {
	a := accountAttention{today: today, now: now}
	var err error
	if a.links, err = q.AccountLinkAttention(ctx, dayParam(today)); err != nil {
		return a, fmt.Errorf("staff: read the account links for attention: %w", err)
	}
	if a.people, err = q.AccountPersonAttention(ctx, dayParam(today)); err != nil {
		return a, fmt.Errorf("staff: read the people's accounts for attention: %w", err)
	}
	set, err := q.GetSettings(ctx)
	if err != nil {
		return a, fmt.Errorf("staff: read the settings for attention: %w", err)
	}
	if len(set.ProviderStatus) > 0 {
		if err := json.Unmarshal(set.ProviderStatus, &a.providers); err != nil {
			return a, fmt.Errorf("staff: read the providers' status for attention: %w", err)
		}
	}
	kinds, err := q.FlaggedAccountKinds(ctx)
	if err != nil {
		return a, fmt.Errorf("staff: read the people's account kinds for attention: %w", err)
	}
	a.kinds = make(map[string]int64, len(kinds))
	for _, k := range kinds {
		a.kinds[k.AccountKind] = k.People
	}
	if a.groups, err = q.ListFailedGroupTraits(ctx); err != nil {
		return a, fmt.Errorf("staff: read the failed group sections for attention: %w", err)
	}
	return a, nil
}

// accountLinkFailing is whether l raises accountFailing at now. For a
// person whose flag is on and who is not erased: three failed attempts in a
// row, or a fault of Staff's own (invalid_actor). Otherwise only from a
// disable Disable now asked for that identity has not confirmed — failed
// once, or waiting longer than disableOverdueAfter (review M1, L1); the
// worker retries it at every poll meanwhile. A flag-off leaver's link (not
// erased; Leaver) is the exception: the worker deactivates it whatever the
// flag (D2), so three failed attempts raise accountFailing as with the flag
// on — a leaver keeping access is HR's to know.
func accountLinkFailing(l store.AccountLinkAttentionRow, now time.Time) bool {
	if !l.Erased && !l.ProvisionAccount && l.Leaver && l.LastError != nil && l.Attempts >= accountsMaxAttempts {
		return true
	}
	if l.Erased || !l.ProvisionAccount {
		// A refusal raises its own item instead (accountDisableRefusals).
		return l.DisableRequestedAt != nil && !accountDisableRefusals[deref(l.BlockedReason)] &&
			(l.LastError != nil || !now.Before(l.DisableRequestedAt.Add(disableOverdueAfter)))
	}
	reason := deref(l.BlockedReason)
	return reason == reasonInvalidActor || reason == reasonExternalEnabled || (l.LastError != nil && l.Attempts >= accountsMaxAttempts)
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
		if l.Provider != linkProviderIdentity && !l.Erased && l.ProvisionAccount {
			out = append(out, a.providerLinkItems(l)...)
		}
		reason := deref(l.BlockedReason)
		kind, ok := accountReasonItems[reason]
		if !ok || accountPersonReasons[reason] || accountStatusReasons[reason] || kind == gen.AccountFailing {
			continue
		}
		// A flag that is off, or an erased record, raises only the refusal a
		// disable asked for was answered with, while it stands (re-check
		// MEDIUM-1 and LOW-1) — the user is still enabled, and an Owner's, or
		// HR's number, to put right — or an identity erase the retention run
		// could not finish (erasure_incomplete). A reason left from when the
		// flag was on raises nothing.
		if (l.Erased || !l.ProvisionAccount) && reason != reasonErasureIncomplete && (l.DisableRequestedAt == nil || !accountDisableRefusals[reason]) {
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
	out = append(out, a.providerItems()...)
	out = append(out, a.groupItems()...)
	// One item per type and person: a person's two links may raise the same
	// (accountFailing on both, a privileged account reported and refused).
	seen := make(map[string]bool, len(out))
	return slices.DeleteFunc(out, func(item gen.StaffStatsAttentionItem) bool {
		if seen[item.Id] {
			return true
		}
		seen[item.Id] = true
		return false
	})
}

// providerLinkItems is what one provider link raises beside its reason: the
// codes the provider reported (one item per type, licence_unavailable once
// whatever its SKUs), a pass issued and not shown within
// passUnrevealedAfter, and an announcement nothing fulfilled within
// accountNotArrivedAfter. Each names the person only.
func (a accountAttention) providerLinkItems(l store.AccountLinkAttentionRow) []gen.StaffStatsAttentionItem {
	var out []gen.StaffStatsAttentionItem
	for _, code := range l.ReportedCodes {
		if kind, ok := providerCodeItem(code); ok {
			out = append(out, personItem(kind, l.PersonID, l.PersonID, l.DisplayName, l.UpdatedAt))
		}
	}
	if l.PassIssuedAt != nil && !a.now.Before(l.PassIssuedAt.Add(passUnrevealedAfter)) {
		out = append(out, personItem(gen.PassUnrevealed, l.PersonID, l.PersonID, l.DisplayName, *l.PassIssuedAt))
	}
	if l.AnnouncedAt != nil && !l.IdentityLinked && !a.now.Before(l.AnnouncedAt.Add(accountNotArrivedAfter)) {
		out = append(out, personItem(gen.AccountNotArrived, l.PersonID, l.PersonID, l.DisplayName, *l.AnnouncedAt))
	}
	return out
}

// providerItems is the installation's items from each provider's recorded
// status, raised only while people whose flag is on have its kind, and
// counting them.
func (a accountAttention) providerItems() []gen.StaffStatsAttentionItem {
	var out []gen.StaffStatsAttentionItem
	keys := make([]string, 0, len(a.providers))
	for key := range a.providers {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		seen := a.providers[key]
		people := a.kinds[key]
		kind, ok := providerStatusItem(seen.Status, seen.Reason)
		if !ok || people == 0 {
			continue
		}
		count := int32(people)
		out = append(out, gen.StaffStatsAttentionItem{
			Id: string(kind), Type: kind, Title: accountInstallationItems[kind], OccurredAt: seen.CheckedAt,
			EntityId: settingsEntity, Count: &count,
		})
	}
	return out
}

// groupItems is one groupSyncFailed per group section the worker left
// failed (groups_sync.go): keyed by the group and the provider, naming the
// group, its entity the group's id — the host links it to the group's page
// — and dated today, since the row keeps no date of its failure. The code
// and the field stay on the group's page; the item says no more.
func (a accountAttention) groupItems() []gen.StaffStatsAttentionItem {
	out := make([]gen.StaffStatsAttentionItem, 0, len(a.groups))
	for _, g := range a.groups {
		id := strconv.FormatInt(g.GroupID, 10)
		out = append(out, gen.StaffStatsAttentionItem{
			Id: string(gen.GroupSyncFailed) + "/" + id + "/" + g.Provider, Type: gen.GroupSyncFailed,
			Title: g.Name, OccurredAt: a.today, EntityId: id,
		})
	}
	return out
}
