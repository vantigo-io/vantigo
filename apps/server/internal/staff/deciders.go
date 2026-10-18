package staff

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is who decides a person's absence (design S12, plan Task 4,
// readings 1, 2 and 4). The deciders of person P today are
//
//   - (a) the people managersOf(P, today) names — the one manager rule
//     (terms_derived.go), so a manager who has left decides nothing and the
//     department climb takes over — whose self-service link names an active
//     user: the manager-deciders, the only deciders Staff can address by
//     mail, since it holds their persons' addresses (reading 4);
//   - (b) every holder of staff:approve, and (c) every holder of
//     staff:manage — by right: the inbox and an attention item, and a mail
//     only when the person has no manager-decider (holders.go, the owner's
//     answer 3).
//
// Nobody decides their own request: a manager's own goes to their manager by
// the same rule, and anyone else's waits in another holder's inbox
// (own_request). The one exception (provisional, open question 2,
// pre-flight I7): a holder of staff:approve or staff:manage who heads the
// structure today (headsTopOfStructure) and for whom (a) is empty approves
// their own, recorded absence_self_approved and listed to staff:manage.
//
// A request of an HR-only type (review H4, interim option b, reading 33) is
// decided by staff:personal-data alone — §5 gives HR-only types to that
// permission — and never by its own holder, but by the sole active holder of
// staff:personal-data (the owner's decision on B's open question 9, delivery
// C): when identity names exactly one holder and it is the caller, they
// approve their own, recorded absence_self_approved and raised to
// staff:personal-data as hrOnlySelfApproved. A directory that cannot answer
// makes nobody sole (holders.go), so the request waits.
//
// The rule reads the manager graph in a read snapshot and the directory
// after it — never inside a transaction (the lock rule) — so a decision
// judges its decider before it takes the person's lock, and judges the row
// under it.

// decider is what the caller is to one person's requests.
type decider struct {
	decides      bool // the caller decides the person's requests
	own          bool // the person is the caller themselves
	selfApproves bool // own, and the caller approves their own (I7)
}

// judgeDecider is what the caller is to personID's requests today — of an
// HR-only type when hrOnly.
func (s *server) judgeDecider(ctx context.Context, caller *callerContext, personID int64, hrOnly bool) (decider, error) {
	own := caller.Person != nil && *caller.Person == personID
	if hrOnly {
		if own && s.solePersonalData(ctx, caller) {
			return decider{decides: true, own: true, selfApproves: true}, nil
		}
		return decider{decides: caller.PersonalData && !own, own: own}, nil
	}
	byRight := caller.Approve || caller.Manage
	if !own && byRight {
		return decider{decides: true}, nil
	}
	if !own && caller.Person == nil {
		return decider{}, nil
	}
	if own && !byRight {
		return decider{own: true}, nil
	}
	var (
		managers []int64
		heads    bool
		links    []store.PeopleLinksRow
	)
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		g, err := loadManagerGraph(ctx, q, []int64{personID})
		if err != nil {
			return err
		}
		managers = g.managersOf(personID, caller.Today)
		if !own {
			return nil
		}
		for _, e := range g.employments[personID] {
			heads = heads || g.headsTopOfStructure(e, caller.Today)
		}
		if !heads || len(managers) == 0 {
			return nil
		}
		if links, err = q.PeopleLinks(ctx, managers); err != nil {
			return fmt.Errorf("staff: read the links of person %d's managers: %w", personID, err)
		}
		return nil
	})
	if err != nil {
		return decider{}, err
	}
	if !own {
		return decider{decides: slices.Contains(managers, *caller.Person)}, nil
	}
	if !heads {
		return decider{own: true}, nil
	}
	active, err := s.activeLinked(ctx, links)
	if err != nil {
		return decider{}, err
	}
	if len(active) > 0 {
		return decider{own: true}, nil
	}
	return decider{decides: true, own: true, selfApproves: true}, nil
}

// activeLinked is the people among links whose linked user the directory
// answers active — called outside every transaction. A link naming no user
// (a preparing link, delivery C) or no user id, or a user the directory does
// not know, is no active user.
func (s *server) activeLinked(ctx context.Context, links []store.PeopleLinksRow) ([]int64, error) {
	byUser := map[uuid.UUID]int64{}
	ids := make([]uuid.UUID, 0, len(links))
	for _, l := range links {
		if l.ExternalID == nil {
			continue
		}
		id, err := uuid.Parse(*l.ExternalID)
		if err != nil {
			continue
		}
		byUser[id] = l.PersonID
		ids = append(ids, id)
	}
	users, err := s.usersUsers(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("staff: read the managers' users: %w", err)
	}
	var out []int64
	for _, u := range users {
		if p, ok := byUser[u.ID]; ok && u.Active && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out, nil
}

// managerDeciders is (a) for personID today: the managers by the one manager
// rule whose link names an active user — the people an absence mail about
// personID goes to. The graph and the links are read in a snapshot, the
// directory after it.
func (s *server) managerDeciders(ctx context.Context, personID int64, today time.Time) ([]int64, error) {
	var links []store.PeopleLinksRow
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		g, err := loadManagerGraph(ctx, q, []int64{personID})
		if err != nil {
			return err
		}
		managers := g.managersOf(personID, today)
		if len(managers) == 0 {
			return nil
		}
		if links, err = q.PeopleLinks(ctx, managers); err != nil {
			return fmt.Errorf("staff: read the links of person %d's managers: %w", personID, err)
		}
		return nil
	})
	if err != nil || len(links) == 0 {
		return nil, err
	}
	return s.activeLinked(ctx, links)
}
