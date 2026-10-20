package staff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is a group's members and owners (Entra design D9, plan task 3a):
// the materialisation of every derived group by the accounts worker's cycle,
// and HR's writes of a listed group's members and of any group's owners.
// A member write is the difference between the rows and the set wanted —
// the people not wanted removed, the people missing added from today — each
// change a group_joined or group_left event on the person, under the
// group's advisory lock (groupLock) and then its row, so the worker's
// materialisation and HR's PUT of the same group queue rather than race. The
// owners are replaced whole and record no event.

// groupLock is the advisory lock a group's members are written under.
func groupLock(id int64) string { return fmt.Sprintf("staff:group:%d", id) }

// groupEvent is a group_joined or group_left event's payload.
type groupEvent struct {
	GroupID   int64  `json:"groupId"`
	GroupName string `json:"groupName"`
	GroupKey  string `json:"groupKey"`
	Source    string `json:"source"`
}

// groupEventClasses is the retention class every group event touches.
var groupEventClasses = []string{"C"}

// writeGroupMembers makes g's members exactly wanted (ascending, each once)
// inside the caller's transaction, which holds groupLock and g's row: the
// others removed, the missing added from today as source, each change an
// event on the person by who. When anything changed, every provider's
// section of g is made due (MakeGroupTraitsDue), a failing one included, so
// the worker's next cycle carries the members whatever the section waits
// for. It answers how many joined and left.
func (s *server) writeGroupMembers(ctx context.Context, txq *store.Queries, g store.StaffGroup, wanted []int64, source string, who actor, today time.Time) (joined, left int, err error) {
	current, err := txq.GroupMemberIDs(ctx, g.ID)
	if err != nil {
		return 0, 0, fmt.Errorf("staff: read group %d's members: %w", g.ID, err)
	}
	if wanted == nil {
		wanted = []int64{}
	}
	removed, err := txq.DeleteGroupMembersNotIn(ctx, store.DeleteGroupMembersNotInParams{GroupID: g.ID, PersonIds: wanted})
	if err != nil {
		return 0, 0, fmt.Errorf("staff: remove group %d's members: %w", g.ID, err)
	}
	payload := groupEvent{GroupID: g.ID, GroupName: g.Name, GroupKey: g.Key, Source: source}
	for _, id := range removed {
		if err := s.recordEvent(ctx, txq, &id, eventGroupLeft, who, payload, groupEventClasses); err != nil {
			return 0, 0, err
		}
	}
	for _, id := range wanted {
		if _, found := slices.BinarySearch(current, id); found {
			continue
		}
		if err := txq.InsertGroupMember(ctx, store.InsertGroupMemberParams{
			GroupID: g.ID, PersonID: id, Source: source, Since: dayParam(today),
		}); err != nil {
			return 0, 0, fmt.Errorf("staff: add person %d to group %d: %w", id, g.ID, err)
		}
		if err := s.recordEvent(ctx, txq, &id, eventGroupJoined, who, payload, groupEventClasses); err != nil {
			return 0, 0, err
		}
		joined++
	}
	if joined > 0 || len(removed) > 0 {
		if err := txq.MakeGroupTraitsDue(ctx, store.MakeGroupTraitsDueParams{GroupID: g.ID, Now: s.deps.Clock()}); err != nil {
			return 0, 0, fmt.Errorf("staff: make group %d's sections due: %w", g.ID, err)
		}
	}
	return joined, len(removed), nil
}

// materialiseGroups writes every derived group's members as its rule yields
// them today (Entra design D9), by who — the accounts worker's cycle calls it
// before it claims the links. Each group is its own transaction under its
// lock; one group's failure is logged and the rest go on, so one bad rule
// never holds the others. Only reading the groups fails the call.
func (s *server) materialiseGroups(ctx context.Context, who actor, today time.Time) error {
	groups, err := store.New(s.deps.Pool).ListDerivedGroups(ctx)
	if err != nil {
		return fmt.Errorf("staff: list the derived groups: %w", err)
	}
	for _, g := range groups {
		if err := ctx.Err(); err != nil {
			return err
		}
		joined, left, err := s.materialiseGroup(ctx, g.ID, who, today)
		if err != nil {
			s.logger().WarnContext(ctx, "staff: could not materialise a derived group's members; the next cycle tries again",
				"worker", accountsWorkerName, "group", g.ID, "error", err)
			continue
		}
		if joined > 0 || left > 0 {
			s.logger().InfoContext(ctx, "staff: materialised a derived group's members",
				"worker", accountsWorkerName, "group", g.ID, "joined", joined, "left", left)
		}
	}
	return nil
}

// materialiseGroup writes one derived group's members under its lock. A
// group deleted, archived or made listed since the list was read is left.
func (s *server) materialiseGroup(ctx context.Context, id int64, who actor, today time.Time) (joined, left int, err error) {
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := advisoryLock(ctx, txq, groupLock(id)); err != nil {
			return err
		}
		g, err := txq.LockGroup(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		} else if err != nil {
			return fmt.Errorf("staff: lock group %d: %w", id, err)
		}
		if g.Kind != string(gen.Derived) || g.ArchivedAt != nil {
			return nil
		}
		var rule gen.StaffGroupRule
		if err := json.Unmarshal(g.Rule, &rule); err != nil {
			return fmt.Errorf("staff: read group %d's rule: %w", id, err)
		}
		wanted, err := evaluateRule(ctx, txq, rule, today)
		if err != nil {
			return err
		}
		joined, left, err = s.writeGroupMembers(ctx, txq, g, wanted, string(gen.Derived), who, today)
		return err
	})
	return joined, left, err
}

// groupPeople is a members' or owners' request's people, each once and
// ascending, or the field error naming those that are no person or an
// erased one, checked inside the caller's transaction.
func groupPeople(ctx context.Context, txq *store.Queries, ids []int64) ([]int64, error) {
	wanted := slices.Clone(ids)
	slices.Sort(wanted)
	wanted = slices.Compact(wanted)
	if wanted == nil {
		wanted = []int64{}
	}
	if len(wanted) == 0 {
		return wanted, nil
	}
	found, err := txq.PeopleNotErased(ctx, wanted)
	if err != nil {
		return nil, fmt.Errorf("staff: read the people of a group: %w", err)
	}
	var f fieldErrors
	for _, id := range wanted {
		if _, ok := slices.BinarySearch(found, id); !ok {
			f.add("personIds", fmt.Sprintf("No person has id %d, or the person is erased", id))
		}
	}
	if f.failed() {
		return nil, refuseInvalid(invalidGroupTitle, f)
	}
	return wanted, nil
}

// lockWritableGroup takes group id's row for a write that an archived group
// refuses: notFound for no such group, group_archived for an archived one.
func lockWritableGroup(ctx context.Context, txq *store.Queries, id int64) (store.StaffGroup, error) {
	g, err := txq.LockGroup(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return g, notFound()
	} else if err != nil {
		return g, fmt.Errorf("staff: lock group %d: %w", id, err)
	}
	if g.ArchivedAt != nil {
		return g, refuseConflict(groupArchived())
	}
	return g, nil
}

// PutStaffGroupsByIdMembers Set a listed group's members
// (PUT /api/v1/staff/groups/{id}/members)
func (s *server) PutStaffGroupsByIdMembers(ctx context.Context, req gen.PutStaffGroupsByIdMembersRequestObject) (gen.PutStaffGroupsByIdMembersResponseObject, error) {
	var body gen.StaffGroupMembersRequest
	if req.Body != nil {
		body = *req.Body
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	zone, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	today := civilToday(s.deps.Clock(), zone)
	var out gen.StaffGroup
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := advisoryLock(ctx, txq, groupLock(req.Id)); err != nil {
			return err
		}
		g, err := lockWritableGroup(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		if g.Kind == string(gen.Derived) {
			return refuseConflict(conflict(codeGroupDerived, "Derived group",
				"The group's members are its rule's, materialised daily; change the rule, or make the group listed."))
		}
		wanted, err := groupPeople(ctx, txq, body.PersonIds)
		if err != nil {
			return err
		}
		if _, _, err := s.writeGroupMembers(ctx, txq, g, wanted, string(gen.Listed), who, today); err != nil {
			return err
		}
		out, err = readGroup(ctx, txq, g.ID)
		return err
	})
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PutStaffGroupsByIdMembers404Response{}, nil
		case http.StatusConflict:
			return gen.PutStaffGroupsByIdMembers409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PutStaffGroupsByIdMembers400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("staff: set group %d's members: %w", req.Id, err)
	}
	return gen.PutStaffGroupsByIdMembers200JSONResponse(out), nil
}

// PutStaffGroupsByIdOwners Set a group's owners
// (PUT /api/v1/staff/groups/{id}/owners)
//
// The owners are replaced whole, no event recorded; every provider's section
// is made due, so the worker carries them to the directory.
func (s *server) PutStaffGroupsByIdOwners(ctx context.Context, req gen.PutStaffGroupsByIdOwnersRequestObject) (gen.PutStaffGroupsByIdOwnersResponseObject, error) {
	var body gen.StaffGroupOwnersRequest
	if req.Body != nil {
		body = *req.Body
	}
	var out gen.StaffGroup
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		g, err := lockWritableGroup(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		wanted, err := groupPeople(ctx, txq, body.PersonIds)
		if err != nil {
			return err
		}
		if err := txq.ReplaceGroupOwners(ctx, store.ReplaceGroupOwnersParams{GroupID: g.ID, PersonIds: wanted}); err != nil {
			return fmt.Errorf("staff: replace group %d's owners: %w", g.ID, err)
		}
		if err := txq.MakeGroupTraitsDue(ctx, store.MakeGroupTraitsDueParams{GroupID: g.ID, Now: s.deps.Clock()}); err != nil {
			return fmt.Errorf("staff: make group %d's sections due: %w", g.ID, err)
		}
		out, err = readGroup(ctx, txq, g.ID)
		return err
	})
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PutStaffGroupsByIdOwners404Response{}, nil
		case http.StatusConflict:
			return gen.PutStaffGroupsByIdOwners409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PutStaffGroupsByIdOwners400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("staff: set group %d's owners: %w", req.Id, err)
	}
	return gen.PutStaffGroupsByIdOwners200JSONResponse(out), nil
}
