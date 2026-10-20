package staff

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the group sync in the accounts worker's cycle (Entra design
// D10, D14; delivery D, docs/superpowers/plans/2026-10-20-entra-groups.md
// task 3b): after the links, the cycle claims up to groupsBatch due group
// sections (staff.group_traits, one per group and provider HR turned on),
// one at a time under a lease as ClaimAccountLink claims a link, and brings
// the provider's group in step with Staff's:
//
//   - a provider not connected: nothing called, nothing counted, the section
//     claimed again at the next poll;
//   - a group archived, or a section HR turned off, retires: with Staff's
//     deleteExternalAfterDays a number, the external group is deleted that
//     many days after the archive or the switch-off (DeleteGroup, state
//     deleted); with it never, the group is renamed "<name> (archived)" once
//     through EnsureGroup and kept (state retired). Neither is claimed again;
//   - otherwise EnsureGroup when the provider holds no group for it yet or
//     the values' hash (the name, the description, the values and the
//     owners) moved, then SetMembers when the members' hash (the members
//     and the civil day) moved — at most two calls, each under the call
//     timeout on no transaction. A value the provider refuses for a group
//     it holds already is kept on the section, failed, and not sent again
//     until the values change, but the members are reconciled all the
//     same: a leaver never keeps the group's access while HR mends a trait;
//   - a group the provider found and adopted and nobody has confirmed there
//     (contracts.ErrGroupUnconfirmed) is no failure: the section waits,
//     pending with group_unconfirmed, claimed again at the next poll with
//     nothing counted, so the cycle after the confirmation proceeds.
//
// The members and owners are sent as the external ids of their enabled
// accounts under the provider (EnabledProviderLinksOfPeople): a person
// without one, or whose account is disabled or deleted, is no member there.
// The result is written on the claim's lease alone (WriteGroupTraitResult);
// a section HR changed meanwhile ended the claim, and its result is dropped.
// No event: groups are not audited beyond the person events and the
// provider's own trail (plan reading 10); the codes on the row are codes,
// never a directory's words nor a member's id.

const (
	// groupsBatch bounds one cycle at a hundred section claims.
	groupsBatch = 100
	// groupArchivedSuffix is what a retired group's name in the provider
	// ends with when Staff keeps it (deleteExternalAfterDays never).
	groupArchivedSuffix = " (archived)"
)

// The states of a group section (staff.group_traits.state).
const (
	groupTraitPending = "pending"
	groupTraitSynced  = "synced"
	groupTraitFailed  = "failed"
	groupTraitRetired = "retired"
	groupTraitDeleted = "deleted"
)

// The last_error codes the sync writes beside a GroupTraitError's own code.
const (
	groupErrorNotManageable = "group_not_manageable"
	groupErrorCallFailed    = "call_failed"
	groupErrorMembersFailed = "members_failed"
	// groupErrorUnconfirmed: the provider adopted a group it found and
	// waits for a holder of its permission to confirm it.
	groupErrorUnconfirmed = "group_unconfirmed"
)

// The bounds of what a provider's GroupTraitError writes to the section:
// last_error_field is varchar(40), last_error varchar(200).
const (
	groupErrorFieldMax = 40
	groupErrorCodeMax  = 200
)

// syncGroups works up to groupsBatch due group sections for run. A run by
// hand stops at its budget (run.until); a section claimed a second time in
// one cycle — its backoff shorter than the cycle — is handed back for the
// next poll and ends the batch.
func (s *server) syncGroups(ctx context.Context, run accountsRun) error {
	worked := map[string]bool{}
	for range groupsBatch {
		now := s.deps.Clock()
		if !run.until.IsZero() && !now.Before(run.until) {
			return nil
		}
		lease := uuid.New()
		claim, err := store.New(s.deps.Pool).ClaimGroupTrait(ctx, store.ClaimGroupTraitParams{
			LeaseID: lease, LeaseUntil: now.Add(accountsLeaseFor), Now: now,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("staff: claim a group section: %w", err)
		}
		key := strconv.FormatInt(claim.GroupID, 10) + "/" + claim.Provider
		if worked[key] {
			s.writeGroupTraitResult(ctx, newGroupTraitResult(claim), lease)
			return nil
		}
		worked[key] = true
		s.syncGroupTrait(ctx, run, claim, lease, now)
	}
	return nil
}

// groupFacts is what one claim reads of its group, in one snapshot: the
// group, its owners' and members' external ids under the claim's provider
// (sorted, each once), Staff's deleteExternalAfterDays (nil: never) and the
// civil day.
type groupFacts struct {
	group           store.GetGroupRow
	owners, members []string
	deleteAfterDays *int
	today           time.Time
	zone            *time.Location
}

// readGroupFacts reads claim's group's facts at now; errGroupGone when the
// group is gone (its sections went with it).
func (s *server) readGroupFacts(ctx context.Context, claim store.StaffGroupTrait, now time.Time) (groupFacts, error) {
	var f groupFacts
	zone, err := s.businessZone(ctx)
	if err != nil {
		return f, err
	}
	f.zone = zone
	f.today = civilToday(now, zone)
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if f.group, err = q.GetGroup(ctx, claim.GroupID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errGroupGone
			}
			return fmt.Errorf("staff: read group %d: %w", claim.GroupID, err)
		}
		set, err := settings(ctx, q)
		if err != nil {
			return err
		}
		if set.DeleteExternalAfterDays != nil {
			days := int(*set.DeleteExternalAfterDays)
			f.deleteAfterDays = &days
		}
		owners, err := q.ListGroupOwners(ctx, claim.GroupID)
		if err != nil {
			return fmt.Errorf("staff: read group %d's owners: %w", claim.GroupID, err)
		}
		ownerIDs := make([]int64, 0, len(owners))
		for _, o := range owners {
			ownerIDs = append(ownerIDs, o.PersonID)
		}
		if f.owners, err = externalIDsOf(ctx, q, ownerIDs, claim.Provider); err != nil {
			return fmt.Errorf("staff: read group %d's owners' accounts: %w", claim.GroupID, err)
		}
		memberIDs, err := q.GroupMemberIDs(ctx, claim.GroupID)
		if err != nil {
			return fmt.Errorf("staff: read group %d's members: %w", claim.GroupID, err)
		}
		if f.members, err = externalIDsOf(ctx, q, memberIDs, claim.Provider); err != nil {
			return fmt.Errorf("staff: read group %d's members' accounts: %w", claim.GroupID, err)
		}
		return nil
	})
	return f, err
}

// errGroupGone is readGroupFacts' answer for a group deleted since the claim.
var errGroupGone = errors.New("staff: the group is gone")

// externalIDsOf is the external ids of people's enabled accounts under
// provider on q, sorted, each once; nil for none.
func externalIDsOf(ctx context.Context, q *store.Queries, people []int64, provider string) ([]string, error) {
	if len(people) == 0 {
		return nil, nil
	}
	rows, err := q.EnabledProviderLinksOfPeople(ctx, store.EnabledProviderLinksOfPeopleParams{PersonIds: people, Provider: provider})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range rows {
		out = append(out, r.ExternalID)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// groupValuesHash is last_values_hash: SHA-256 over the group's name, its
// description and the section's values, as JSON with its keys sorted (Go's
// encoding of a map) — and the owners' external ids, sorted, which
// EnsureGroup reconciles too: an owner HR adds, or whose account is enabled
// since, reaches the provider by the same call.
func groupValuesHash(name string, description *string, values map[string]any, owners []string) []byte {
	if owners == nil {
		owners = []string{}
	}
	// The owners are in the hash under "owners" (beyond the plan's three keys; the coordinator's amendment).
	b, err := json.Marshal(map[string]any{"name": name, "description": description, "values": values, "owners": owners})
	if err != nil {
		// Values decoded from jsonb always encode again.
		panic(fmt.Sprintf("staff: hash a group's values: %v", err))
	}
	sum := sha256.Sum256(b)
	return sum[:]
}

// groupMembersHash is last_members_hash: SHA-256 over the civil day and the
// members' external ids, sorted and each once, joined by newlines. The day
// is in it so that every group's members are reconciled once a day whatever
// Staff saw: a member added in the directory by hand, or a self-join on a
// public group, is undone by the next day's SetMembers.
func groupMembersHash(ids []string, today time.Time) []byte {
	sum := sha256.Sum256([]byte(today.Format(time.DateOnly) + "\n" + strings.Join(ids, "\n")))
	return sum[:]
}

// cutRunes is s at most n characters long: what a provider answers is
// bounded before it is written.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// archivedName is name as a retired group keeps it in the provider: with
// groupArchivedSuffix, once.
func archivedName(name string) string {
	if strings.HasSuffix(name, groupArchivedSuffix) {
		return name
	}
	return name + groupArchivedSuffix
}

// groupTraitResult is what a claim writes to its section, built as the
// claim goes: it starts as the claimed row.
type groupTraitResult struct {
	claim                     store.StaffGroupTrait
	externalID                *string
	state                     string
	valuesHash, membersHash   []byte
	lastError, lastErrorField *string
	syncedAt, retiredAt       *time.Time
	attempts                  int32
	next                      *time.Time
}

func newGroupTraitResult(claim store.StaffGroupTrait) *groupTraitResult {
	return &groupTraitResult{
		claim: claim, externalID: claim.ExternalID, state: claim.State,
		valuesHash: claim.LastValuesHash, membersHash: claim.LastMembersHash,
		lastError: claim.LastError, lastErrorField: claim.LastErrorField,
		syncedAt: claim.SyncedAt, retiredAt: claim.RetiredAt, attempts: claim.Attempts, next: claim.NextAttemptAt,
	}
}

// answered clears the failure: the provider did what was asked.
func (r *groupTraitResult) answered() {
	r.lastError, r.lastErrorField, r.attempts = nil, nil, 0
}

// refused records an answer that will not change until HR changes the
// section: failed, the code and the field, never due again by itself.
func (r *groupTraitResult) refused(code, field string) {
	code, field = cutRunes(code, groupErrorCodeMax), cutRunes(field, groupErrorFieldMax)
	r.state = groupTraitFailed
	r.lastError = &code
	r.lastErrorField = nil
	if field != "" {
		r.lastErrorField = &field
	}
	r.attempts = 0
	r.next = nil
}

// failed records what a call answered: a refusal of a value or of the
// group, the provider unavailable (nothing counted, the next poll), or a
// failure to retry after the backoff — the section failed once it has
// failed accountsMaxAttempts times in a row, which raises its item.
func (r *groupTraitResult) failed(err error, now, poll time.Time) {
	var traitErr *contracts.GroupTraitError
	switch {
	case errors.As(err, &traitErr):
		r.refused(traitErr.Code, traitErr.Field)
	case errors.Is(err, contracts.ErrGroupNotManageable):
		r.refused(groupErrorNotManageable, "")
	case errors.Is(err, contracts.ErrGroupUnconfirmed):
		r.unconfirmed(poll)
	case errors.Is(err, contracts.ErrProviderUnavailable), errors.Is(err, errProviderUnavailable):
		r.next = &poll
	default:
		code := groupErrorCallFailed
		r.attempts = r.claim.Attempts + 1
		r.lastError, r.lastErrorField = &code, nil
		// Three failures in a row before failed (and groupSyncFailed); until then the state stands.
		if r.attempts >= accountsMaxAttempts {
			r.state = groupTraitFailed
		}
		next := now.Add(retryBackoff(r.attempts))
		r.next = &next
	}
}

// unconfirmed records a group the provider adopted and waits to have
// confirmed: pending with group_unconfirmed, no attempt counted, due at the
// next poll — as a provider not connected is waited for.
func (r *groupTraitResult) unconfirmed(poll time.Time) {
	code := groupErrorUnconfirmed
	r.state = groupTraitPending
	r.lastError, r.lastErrorField = &code, nil
	r.next = &poll
}

// keepsRefusal reports whether claim holds a value the provider refused for
// the values it holds now: failed on an EnsureGroup refusal whose values'
// hash was kept (a call that failed is call_failed, and leaves no hash).
func keepsRefusal(claim store.StaffGroupTrait) bool {
	return claim.State == groupTraitFailed && claim.LastError != nil && *claim.LastError != groupErrorCallFailed &&
		claim.LastValuesHash != nil
}

// syncGroupTrait works one claimed section and writes its result.
func (s *server) syncGroupTrait(ctx context.Context, run accountsRun, claim store.StaffGroupTrait, lease uuid.UUID, now time.Time) {
	log := s.logger().With("worker", accountsWorkerName, "group_id", claim.GroupID, "provider", claim.Provider)
	r := newGroupTraitResult(claim)
	poll := now.Add(s.workersPoll())
	p, _ := s.provisioner(claim.Provider)
	if info, ok := run.providers[claim.Provider]; p == nil || !ok || info.Status != contracts.ProviderConnected {
		r.next = &poll
		s.writeGroupTraitResult(ctx, r, lease)
		return
	}
	f, err := s.readGroupFacts(ctx, claim, now)
	if errors.Is(err, errGroupGone) {
		return
	}
	if err != nil {
		log.ErrorContext(ctx, "staff: read a group section's facts", "error", err.Error())
		r.next = &poll
		s.writeGroupTraitResult(ctx, r, lease)
		return
	}
	var values map[string]any
	if err := json.Unmarshal(claim.Values, &values); err != nil {
		log.ErrorContext(ctx, "staff: a group section's values are not an object", "error", err.Error())
		r.next = &poll
		s.writeGroupTraitResult(ctx, r, lease)
		return
	}
	g := f.group
	spec := contracts.GroupSpec{
		GroupID: strconv.FormatInt(g.ID, 10), Key: g.Key, Name: g.Name, Description: deref(g.Description),
		Values: values, OwnerExternalIDs: f.owners,
	}

	if since, retiring := retiringSince(g.ArchivedAt, claim, now); retiring {
		s.retireGroupTrait(ctx, log, run, p, r, f, spec, since, now, poll)
		s.writeGroupTraitResult(ctx, r, lease)
		return
	}

	called := false
	// refused: the values stand refused by the provider — this claim's
	// EnsureGroup, or an earlier one whose values have not changed since.
	// The refusal stays on the row, and the members are reconciled all the
	// same (the group exists in the provider).
	refused := false
	valuesHash := groupValuesHash(g.Name, g.Description, values, f.owners)
	if r.externalID == nil || !bytes.Equal(valuesHash, claim.LastValuesHash) {
		called = true
		got, err := s.ensureGroup(ctx, run, p, spec)
		var traitErr *contracts.GroupTraitError
		switch {
		case errors.As(err, &traitErr) && r.externalID != nil:
			// Not asked again until the values change: their hash is kept.
			r.refused(traitErr.Code, traitErr.Field)
			r.valuesHash = valuesHash
			refused = true
		case err != nil:
			logCallFailure(ctx, log, "EnsureGroup", err)
			r.failed(err, now, poll)
			s.writeGroupTraitResult(ctx, r, lease)
			return
		default:
			id := got.ExternalID
			r.externalID = &id
			if len(got.Pending) == 0 {
				// A trait the provider will act on later (reading 7) leaves
				// the hash as it was, so the next cycle asks again.
				r.valuesHash = valuesHash
			}
		}
	} else {
		refused = keepsRefusal(claim)
	}
	if !refused {
		r.answered()
	}
	if membersHash := groupMembersHash(f.members, f.today); !bytes.Equal(membersHash, claim.LastMembersHash) {
		called = true
		got, err := withCallTimeout(ctx, func(ctx context.Context) (contracts.MembersResult, error) {
			return s.providerSetMembers(ctx, p, run.actor, spec.GroupID, f.members)
		})
		switch {
		case err != nil && refused:
			// The refusal stays the row's word; the members are tried again
			// at the next poll, or after the backoff for a call that failed.
			logCallFailure(ctx, log, "SetMembers", err)
			next := poll
			if !errors.Is(err, contracts.ErrProviderUnavailable) && !errors.Is(err, errProviderUnavailable) {
				r.attempts = claim.Attempts + 1
				next = now.Add(retryBackoff(r.attempts))
			}
			r.next = &next
			s.writeGroupTraitResult(ctx, r, lease)
			return
		case err != nil:
			logCallFailure(ctx, log, "SetMembers", err)
			r.failed(err, now, poll)
			s.writeGroupTraitResult(ctx, r, lease)
			return
		}
		if n := len(got.Failed); n > 0 {
			// The members' hash stays as it was, so the next cycle tries
			// again; the ids are never stored nor logged.
			log.DebugContext(ctx, "staff: the provider could not change some of a group's members", "failed", n)
			if !refused {
				code := groupErrorMembersFailed + ":" + strconv.Itoa(n)
				r.lastError = &code
			}
		} else {
			r.membersHash = membersHash
		}
	}
	r.next = &poll
	if refused {
		// Failed on its values, due again at the next poll for its members.
		r.state = groupTraitFailed
		s.writeGroupTraitResult(ctx, r, lease)
		return
	}
	if called || r.state != groupTraitSynced || r.syncedAt == nil {
		r.syncedAt = &now
	}
	r.state = groupTraitSynced
	s.writeGroupTraitResult(ctx, r, lease)
}

// ensureGroup is EnsureGroup of spec under the call timeout.
func (s *server) ensureGroup(ctx context.Context, run accountsRun, p contracts.IdentityProvisioner, spec contracts.GroupSpec) (contracts.ExternalGroup, error) {
	return withCallTimeout(ctx, func(ctx context.Context) (contracts.ExternalGroup, error) {
		return s.providerEnsureGroup(ctx, p, run.actor, spec)
	})
}

// retiringSince is when claim's group began to retire under its provider —
// the group archived, or the section turned off, the earlier when both —
// and false for a section to keep in step. A section turned off with no
// date (none is written so) is taken as turned off at now.
func retiringSince(archivedAt *time.Time, claim store.StaffGroupTrait, now time.Time) (time.Time, bool) {
	var since *time.Time
	if archivedAt != nil {
		since = archivedAt
	}
	if !claim.Enabled {
		off := claim.DisabledAt
		if off == nil {
			off = &now
		}
		if since == nil || off.Before(*since) {
			since = off
		}
	}
	if since == nil {
		return time.Time{}, false
	}
	return *since, true
}

// retireGroupTrait retires r's section (D14, plan reading 5): with
// deleteExternalAfterDays a number, DeleteGroup once that many days have
// passed since since (until then nothing is called, and the section waits
// for the next poll); with it never, EnsureGroup once with the name
// suffixed, the group kept. A section whose group the provider never made
// is retired with no call. A refusal of the rename keeps the group as it
// stands, retired all the same, its code on the row: an archived group's
// values can no longer change, so nothing would make it due again.
func (s *server) retireGroupTrait(ctx context.Context, log *slog.Logger, run accountsRun, p contracts.IdentityProvisioner, r *groupTraitResult,
	f groupFacts, spec contracts.GroupSpec, since, now, poll time.Time,
) {
	if days := f.deleteAfterDays; days != nil {
		if f.today.Before(civilToday(since, f.zone).AddDate(0, 0, *days)) {
			r.next = &poll
			return
		}
		_, err := withCallTimeout(ctx, func(ctx context.Context) (struct{}, error) {
			return struct{}{}, s.providerDeleteGroup(ctx, p, run.actor, spec.GroupID)
		})
		if err != nil {
			logCallFailure(ctx, log, "DeleteGroup", err)
			r.failed(err, now, poll)
			return
		}
		r.answered()
		r.state = groupTraitDeleted
		r.next = nil
		return
	}
	if r.externalID != nil {
		spec.Name = archivedName(spec.Name)
		got, err := s.ensureGroup(ctx, run, p, spec)
		var traitErr *contracts.GroupTraitError
		// A refused rename still retires the section, its code kept on the row (no failed row nobody can clear).
		switch {
		case errors.As(err, &traitErr):
			r.refused(traitErr.Code, traitErr.Field)
		case errors.Is(err, contracts.ErrGroupNotManageable):
			r.refused(groupErrorNotManageable, "")
		case err != nil:
			logCallFailure(ctx, log, "EnsureGroup", err)
			r.failed(err, now, poll)
			return
		default:
			id := got.ExternalID
			r.externalID = &id
			r.answered()
			r.syncedAt = &now
		}
	} else {
		r.answered()
	}
	r.state = groupTraitRetired
	r.retiredAt = &now
	r.next = nil
}

// logCallFailure logs a provider call's error that is no answer of the
// contract's: a refusal is recorded on the row and needs no log line.
func logCallFailure(ctx context.Context, log *slog.Logger, method string, err error) {
	var traitErr *contracts.GroupTraitError
	if errors.As(err, &traitErr) || errors.Is(err, contracts.ErrGroupNotManageable) ||
		errors.Is(err, contracts.ErrGroupUnconfirmed) ||
		errors.Is(err, contracts.ErrProviderUnavailable) || errors.Is(err, errProviderUnavailable) {
		return
	}
	log.WarnContext(ctx, "staff: a group call failed", "method", method, "error", err.Error())
}

// writeGroupTraitResult writes r on the claim's lease; a section changed or
// claimed since, or gone, is not written, and logged.
func (s *server) writeGroupTraitResult(ctx context.Context, r *groupTraitResult, lease uuid.UUID) {
	n, err := store.New(s.deps.Pool).WriteGroupTraitResult(ctx, store.WriteGroupTraitResultParams{
		ExternalID: r.externalID, State: r.state, LastValuesHash: r.valuesHash, LastMembersHash: r.membersHash,
		LastError: r.lastError, LastErrorField: r.lastErrorField, SyncedAt: r.syncedAt, RetiredAt: r.retiredAt,
		Attempts: r.attempts, NextAttemptAt: r.next, GroupID: r.claim.GroupID, Provider: r.claim.Provider, LeaseID: lease,
	})
	log := s.logger().With("worker", accountsWorkerName, "group_id", r.claim.GroupID, "provider", r.claim.Provider)
	switch {
	case err != nil:
		log.ErrorContext(ctx, "staff: write a group section's result", "error", err.Error())
	case n == 0:
		log.InfoContext(ctx, "staff: a group section was changed or claimed meanwhile; its result is not written")
	}
}
