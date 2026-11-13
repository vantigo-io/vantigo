package manufacturing

// Work center groups (manufacturing phase 2 M117): interchangeable centers of
// one site an operation may target instead of one center (M118). A write
// takes the BoM graph's lock first, as a work center's site change does — a
// BoM write reads the groups its operations target under it — then the
// group's row FOR UPDATE, then its members FOR SHARE in id order.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

const (
	groupTitle       = "The work center group was not saved"
	groupWhat        = "work center group"
	groupCodeIndex   = "ux_work_center_groups_code"
	maxGroupMembers  = 50
	groupMembersPath = "memberIds"
)

// groupValues is a group's input judged.
type groupValues struct {
	code, name string
	siteID     int64
	members    []int64
}

// judgeGroup is the input's rules that need no row: the code, the name and
// 1–50 distinct members.
func (s *server) judgeGroup(ctx context.Context, code, name string, siteID int64, members []int64, schema string) (groupValues, error) {
	f := s.unknownProperties(ctx, schema)
	v := groupValues{siteID: siteID}
	v.code = strings.ToUpper(strings.TrimSpace(code))
	if !workCenterCode.MatchString(v.code) {
		f.add("code", "1–16 characters A–Z and 0–9")
	}
	v.name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(v.name); n < 1 || n > maxNameLength {
		f.add("name", "1–200 characters")
	}
	v.members = slices.Compact(slices.Sorted(slices.Values(members)))
	if len(v.members) < 1 || len(v.members) > maxGroupMembers {
		f.add(groupMembersPath, "1–50 work centers")
	}
	if f.failed() {
		return v, refuseInvalid(groupTitle, f)
	}
	return v, nil
}

// judgeMembers locks the members FOR SHARE in id order and judges them: each
// a work center at the group's site (work_center_other_site), active unless
// it was a member already (work_center_unknown).
func judgeMembers(ctx context.Context, txq *store.Queries, v groupValues, kept []int64) error {
	centers, err := txq.LockWorkCentersForShareByIDs(ctx, v.members)
	if err != nil {
		return err
	}
	byID := map[int64]store.ManufacturingWorkCenter{}
	for _, c := range centers {
		byID[c.ID] = c
	}
	for _, id := range v.members {
		c, ok := byID[id]
		switch {
		case !ok || (c.Status != statusActive && !slices.Contains(kept, id)):
			return refuseField(groupTitle, groupMembersPath, codeWorkCenterUnknown, fmt.Sprintf("Work center %d is not an active work center", id))
		case c.SiteID != v.siteID:
			return refuseField(groupTitle, groupMembersPath, codeWorkCenterOtherSite,
				fmt.Sprintf("%s stands at another site; a group holds work centers of its own site", c.Code))
		}
	}
	return nil
}

// writeMembers replaces the group's members.
func writeMembers(ctx context.Context, txq *store.Queries, groupID int64, members []int64) error {
	if err := txq.DeleteWorkCenterGroupMembers(ctx, groupID); err != nil {
		return err
	}
	for _, id := range members {
		if err := txq.InsertWorkCenterGroupMember(ctx, store.InsertWorkCenterGroupMemberParams{GroupID: groupID, WorkCenterID: id}); err != nil {
			return err
		}
	}
	return nil
}

// groupCodeTaken is the 409 work_center_code_taken of a group's code.
func groupCodeTaken(code string) error {
	return conflict(codeWorkCenterCodeTaken, "The code is taken", fmt.Sprintf("Another work center group has the code %s.", code))
}

// lockGroup is the group's row FOR UPDATE under the revision the client read.
func lockGroup(ctx context.Context, txq *store.Queries, id int64, revision int32) (store.ManufacturingWorkCenterGroup, error) {
	g, err := txq.GetWorkCenterGroupForUpdate(ctx, id)
	if noRows(err) {
		return g, notFound()
	}
	if err != nil {
		return g, err
	}
	if g.Revision != revision {
		return g, revisionConflict(groupWhat, g.Revision, revision)
	}
	return g, nil
}

// groupsAnswer is groups as the contract answers them, each with its members
// read on q, its site named from sites.
func groupsAnswer(ctx context.Context, q *store.Queries, groups []store.ManufacturingWorkCenterGroup, sites []contracts.StockSite) ([]gen.ManufacturingWorkCenterGroup, error) {
	ids := make([]int64, 0, len(groups))
	for _, g := range groups {
		ids = append(ids, g.ID)
	}
	members, err := groupMembers(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	out := make([]gen.ManufacturingWorkCenterGroup, 0, len(groups))
	for _, g := range groups {
		site, _ := siteOf(sites, g.SiteID)
		refs := make([]gen.ManufacturingWorkCenterRef, 0, len(members[g.ID]))
		for _, c := range members[g.ID] {
			refs = append(refs, centerRefOf(c, c.ID))
		}
		out = append(out, gen.ManufacturingWorkCenterGroup{
			Id: g.ID, Code: g.Code, Name: g.Name, SiteId: g.SiteID, SiteCode: site.Code, SiteName: site.Name, Members: refs,
			Status: gen.ManufacturingStatus(g.Status), Revision: g.Revision, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt,
		})
	}
	return out, nil
}

// groupAnswer is one group read back after its write.
func (s *server) groupAnswer(ctx context.Context, id int64, sites []contracts.StockSite) (gen.ManufacturingWorkCenterGroup, error) {
	q := store.New(s.deps.Pool)
	g, err := q.GetWorkCenterGroup(ctx, id)
	if noRows(err) {
		return gen.ManufacturingWorkCenterGroup{}, notFound()
	}
	if err != nil {
		return gen.ManufacturingWorkCenterGroup{}, err
	}
	out, err := groupsAnswer(ctx, q, []store.ManufacturingWorkCenterGroup{g}, sites)
	if err != nil {
		return gen.ManufacturingWorkCenterGroup{}, err
	}
	return out[0], nil
}

// ListManufacturingWorkCenterGroups is the groups in a status (active by
// default), at a site, by code.
func (s *server) ListManufacturingWorkCenterGroups(ctx context.Context, req gen.ListManufacturingWorkCenterGroupsRequestObject) (gen.ListManufacturingWorkCenterGroupsResponseObject, error) {
	sites, err := s.activeSites(ctx)
	if err != nil {
		return answer(err, answers[gen.ListManufacturingWorkCenterGroupsResponseObject]{
			unavailable: func(p gen.ManufacturingProblem) gen.ListManufacturingWorkCenterGroupsResponseObject {
				return gen.ListManufacturingWorkCenterGroups503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	var items []gen.ManufacturingWorkCenterGroup
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		rows, err := q.ListWorkCenterGroups(ctx, store.ListWorkCenterGroupsParams{Status: statusFilter(req.Params.Status), SiteID: req.Params.SiteId})
		if err != nil {
			return err
		}
		items, err = groupsAnswer(ctx, q, rows, sites)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.ListManufacturingWorkCenterGroups200JSONResponse{Items: items}, nil
}

// GetManufacturingWorkCenterGroup is one group with its members.
func (s *server) GetManufacturingWorkCenterGroup(ctx context.Context, req gen.GetManufacturingWorkCenterGroupRequestObject) (gen.GetManufacturingWorkCenterGroupResponseObject, error) {
	reply := answers[gen.GetManufacturingWorkCenterGroupResponseObject]{
		notFound: func() gen.GetManufacturingWorkCenterGroupResponseObject {
			return gen.GetManufacturingWorkCenterGroup404Response{}
		},
		unavailable: func(p gen.ManufacturingProblem) gen.GetManufacturingWorkCenterGroupResponseObject {
			return gen.GetManufacturingWorkCenterGroup503ApplicationProblemPlusJSONResponse(p)
		},
	}
	sites, err := s.activeSites(ctx)
	if err != nil {
		return answer(err, reply)
	}
	out, err := s.groupAnswer(ctx, req.Id, sites)
	if err != nil {
		return answer(err, reply)
	}
	return gen.GetManufacturingWorkCenterGroup200JSONResponse(out), nil
}

// CreateManufacturingWorkCenterGroup creates a group of active work centers of
// one site (M117).
func (s *server) CreateManufacturingWorkCenterGroup(ctx context.Context, req gen.CreateManufacturingWorkCenterGroupRequestObject) (gen.CreateManufacturingWorkCenterGroupResponseObject, error) {
	reply := answers[gen.CreateManufacturingWorkCenterGroupResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.CreateManufacturingWorkCenterGroupResponseObject {
			return gen.CreateManufacturingWorkCenterGroup400ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.ManufacturingProblem) gen.CreateManufacturingWorkCenterGroupResponseObject {
			return gen.CreateManufacturingWorkCenterGroup409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.CreateManufacturingWorkCenterGroupResponseObject {
			return gen.CreateManufacturingWorkCenterGroup503ApplicationProblemPlusJSONResponse(p)
		},
	}
	in := req.Body
	v, err := s.judgeGroup(ctx, in.Code, in.Name, in.SiteId, in.MemberIds, groupInputSchema)
	if err != nil {
		return answer(err, reply)
	}
	sites, err := s.siteOrRefuse(ctx, v.siteID)
	if err != nil {
		return answer(err, reply)
	}
	var id int64
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := txq.LockBomGraph(ctx); err != nil {
			return err
		}
		taken, err := txq.WorkCenterGroupCodeTaken(ctx, store.WorkCenterGroupCodeTakenParams{Code: v.code})
		if err != nil {
			return err
		}
		if taken {
			return groupCodeTaken(v.code)
		}
		g, err := txq.InsertWorkCenterGroup(ctx, store.InsertWorkCenterGroupParams{Code: v.code, Name: v.name, SiteID: v.siteID, Now: s.deps.Clock()})
		if db.IsUniqueViolation(err, groupCodeIndex) {
			return groupCodeTaken(v.code)
		}
		if err != nil {
			return err
		}
		id = g.ID
		if err := judgeMembers(ctx, txq, v, nil); err != nil {
			return err
		}
		return writeMembers(ctx, txq, g.ID, v.members)
	})
	if err != nil {
		return answer(err, reply)
	}
	out, err := s.groupAnswer(ctx, id, sites)
	if err != nil {
		return answer(err, reply)
	}
	return gen.CreateManufacturingWorkCenterGroup201JSONResponse(out), nil
}

// UpdateManufacturingWorkCenterGroup replaces a group's fields and members
// under its revision. Its site does not change while an active BoM targets
// it (work_center_other_site, M12a).
func (s *server) UpdateManufacturingWorkCenterGroup(ctx context.Context, req gen.UpdateManufacturingWorkCenterGroupRequestObject) (gen.UpdateManufacturingWorkCenterGroupResponseObject, error) {
	reply := answers[gen.UpdateManufacturingWorkCenterGroupResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.UpdateManufacturingWorkCenterGroupResponseObject {
			return gen.UpdateManufacturingWorkCenterGroup400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.UpdateManufacturingWorkCenterGroupResponseObject {
			return gen.UpdateManufacturingWorkCenterGroup404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.UpdateManufacturingWorkCenterGroupResponseObject {
			return gen.UpdateManufacturingWorkCenterGroup409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.UpdateManufacturingWorkCenterGroupResponseObject {
			return gen.UpdateManufacturingWorkCenterGroup503ApplicationProblemPlusJSONResponse(p)
		},
	}
	in := req.Body
	v, err := s.judgeGroup(ctx, in.Code, in.Name, in.SiteId, in.MemberIds, groupUpdateSchema)
	if err != nil {
		return answer(err, reply)
	}
	sites, err := s.activeSites(ctx)
	if err != nil {
		return answer(err, reply)
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := txq.LockBomGraph(ctx); err != nil {
			return err
		}
		g, err := lockGroup(ctx, txq, req.Id, in.Revision)
		if err != nil {
			return err
		}
		if v.siteID != g.SiteID {
			if _, ok := siteOf(sites, v.siteID); !ok {
				return refuseField(groupTitle, "siteId", codeSiteUnknown, "Not an active Inventory site")
			}
			used, err := txq.CountActiveBomsAtGroup(ctx, &g.ID)
			if err != nil {
				return err
			}
			if used > 0 {
				return refuseField(groupTitle, "siteId", codeWorkCenterOtherSite,
					fmt.Sprintf("%d active bill(s) of materials have operations at %s; its site does not change while they do", used, g.Code))
			}
		}
		taken, err := txq.WorkCenterGroupCodeTaken(ctx, store.WorkCenterGroupCodeTakenParams{Code: v.code, ID: g.ID})
		if err != nil {
			return err
		}
		if taken {
			return groupCodeTaken(v.code)
		}
		kept, err := txq.MemberIDsOfGroups(ctx, []int64{g.ID})
		if err != nil {
			return err
		}
		keptIDs := make([]int64, 0, len(kept))
		for _, m := range kept {
			keptIDs = append(keptIDs, m.WorkCenterID)
		}
		if err := judgeMembers(ctx, txq, v, keptIDs); err != nil {
			return err
		}
		if _, err := txq.UpdateWorkCenterGroup(ctx, store.UpdateWorkCenterGroupParams{
			ID: g.ID, Code: v.code, Name: v.name, SiteID: v.siteID, Now: s.deps.Clock(),
		}); db.IsUniqueViolation(err, groupCodeIndex) {
			return groupCodeTaken(v.code)
		} else if err != nil {
			return err
		}
		return writeMembers(ctx, txq, g.ID, v.members)
	})
	if err != nil {
		return answer(err, reply)
	}
	out, err := s.groupAnswer(ctx, req.Id, sites)
	if err != nil {
		return answer(err, reply)
	}
	return gen.UpdateManufacturingWorkCenterGroup200JSONResponse(out), nil
}

// ArchiveManufacturingWorkCenterGroup archives a group no active BoM targets
// (work_center_in_use). Work orders that target it keep their member and are
// planned at it.
func (s *server) ArchiveManufacturingWorkCenterGroup(ctx context.Context, req gen.ArchiveManufacturingWorkCenterGroupRequestObject) (gen.ArchiveManufacturingWorkCenterGroupResponseObject, error) {
	reply := answers[gen.ArchiveManufacturingWorkCenterGroupResponseObject]{
		notFound: func() gen.ArchiveManufacturingWorkCenterGroupResponseObject {
			return gen.ArchiveManufacturingWorkCenterGroup404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.ArchiveManufacturingWorkCenterGroupResponseObject {
			return gen.ArchiveManufacturingWorkCenterGroup409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.ArchiveManufacturingWorkCenterGroupResponseObject {
			return gen.ArchiveManufacturingWorkCenterGroup503ApplicationProblemPlusJSONResponse(p)
		},
	}
	sites, err := s.activeSites(ctx)
	if err != nil {
		return answer(err, reply)
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := txq.LockBomGraph(ctx); err != nil {
			return err
		}
		g, err := lockGroup(ctx, txq, req.Id, req.Body.Revision)
		if err != nil || g.Status == statusArchived {
			return err
		}
		used, err := txq.CountActiveBomsAtGroup(ctx, &g.ID)
		if err != nil {
			return err
		}
		if used > 0 {
			return conflict(codeWorkCenterInUse, "The work center group is in use",
				fmt.Sprintf("%d active bill(s) of materials have operations at %s. Move them to another work center first.", used, g.Code))
		}
		_, err = txq.SetWorkCenterGroupStatus(ctx, store.SetWorkCenterGroupStatusParams{ID: g.ID, Status: statusArchived, Now: s.deps.Clock()})
		return err
	})
	if err != nil {
		return answer(err, reply)
	}
	out, err := s.groupAnswer(ctx, req.Id, sites)
	if err != nil {
		return answer(err, reply)
	}
	return gen.ArchiveManufacturingWorkCenterGroup200JSONResponse(out), nil
}

// ReactivateManufacturingWorkCenterGroup reactivates an archived group,
// refused while its site is not an active Inventory site (site_archived).
func (s *server) ReactivateManufacturingWorkCenterGroup(ctx context.Context, req gen.ReactivateManufacturingWorkCenterGroupRequestObject) (gen.ReactivateManufacturingWorkCenterGroupResponseObject, error) {
	reply := answers[gen.ReactivateManufacturingWorkCenterGroupResponseObject]{
		notFound: func() gen.ReactivateManufacturingWorkCenterGroupResponseObject {
			return gen.ReactivateManufacturingWorkCenterGroup404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.ReactivateManufacturingWorkCenterGroupResponseObject {
			return gen.ReactivateManufacturingWorkCenterGroup409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.ReactivateManufacturingWorkCenterGroupResponseObject {
			return gen.ReactivateManufacturingWorkCenterGroup503ApplicationProblemPlusJSONResponse(p)
		},
	}
	sites, err := s.activeSites(ctx)
	if err != nil {
		return answer(err, reply)
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		g, err := lockGroup(ctx, txq, req.Id, req.Body.Revision)
		if err != nil || g.Status == statusActive {
			return err
		}
		if _, ok := siteOf(sites, g.SiteID); !ok {
			return conflict(codeSiteArchived, "The group's site is archived",
				"Its Inventory site is no longer active. Move the group to an active site first.")
		}
		_, err = txq.SetWorkCenterGroupStatus(ctx, store.SetWorkCenterGroupStatusParams{ID: g.ID, Status: statusActive, Now: s.deps.Clock()})
		return err
	})
	if err != nil {
		return answer(err, reply)
	}
	out, err := s.groupAnswer(ctx, req.Id, sites)
	if err != nil {
		return answer(err, reply)
	}
	return gen.ReactivateManufacturingWorkCenterGroup200JSONResponse(out), nil
}
