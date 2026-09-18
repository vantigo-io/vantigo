package timetracking

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/time/gen"
	"github.com/vantigo-io/vantigo/server/internal/time/store"
)

// This file turns stored entry rows into the contract's TimeEntryResponse,
// including D8's shaping: the money on an entry is not forbidden to a caller
// who may not see it, it is simply not in their copy of the entry.

// entryNames is everything an entry is rendered with that lives in another
// module: its owner's and approver's names (identity, through
// contracts.UserDirectory) and its project's and billing line's codes and
// names (projects, through contracts.ProjectDirectory). None of it can be a
// join, so it is resolved in bulk for a set of rows first.
type entryNames struct {
	users    map[uuid.UUID]contracts.UserEntry
	projects map[int32]contracts.ProjectEntry
	lines    map[int32]contracts.BillingLineEntry
}

// namesFor resolves rows' names: one user-directory call for every owner and
// approver, one project-directory call for every project, and one
// billing-lines call per project that any row has a line on.
func (s *server) namesFor(ctx context.Context, rows []store.TimeEntry) (entryNames, error) {
	var userIDs []uuid.UUID
	var projectIDs []int32
	seenUsers := map[uuid.UUID]bool{}
	seenProjects := map[int32]bool{}
	lineProjects := map[int32]bool{}
	for _, row := range rows {
		for _, id := range []*uuid.UUID{&row.UserID, row.ApprovedByUserID} {
			if id != nil && !seenUsers[*id] {
				seenUsers[*id] = true
				userIDs = append(userIDs, *id)
			}
		}
		if !seenProjects[row.ProjectID] {
			seenProjects[row.ProjectID] = true
			projectIDs = append(projectIDs, row.ProjectID)
		}
		if row.BillingLineID != nil {
			lineProjects[row.ProjectID] = true
		}
	}

	users, err := s.userEntries(ctx, userIDs)
	if err != nil {
		return entryNames{}, err
	}
	names := entryNames{
		users:    users,
		projects: make(map[int32]contracts.ProjectEntry, len(projectIDs)),
		lines:    map[int32]contracts.BillingLineEntry{},
	}
	if len(projectIDs) > 0 {
		found, err := s.deps.Projects.Projects(ctx, projectIDs)
		if err != nil {
			return entryNames{}, fmt.Errorf("time: resolve the entries' projects: %w", err)
		}
		for _, p := range found {
			names.projects[p.ID] = p
		}
	}
	for _, projectID := range projectIDs {
		if !lineProjects[projectID] {
			continue
		}
		lines, err := s.deps.Projects.BillingLines(ctx, projectID)
		if err != nil {
			return entryNames{}, fmt.Errorf("time: resolve the entries' billing lines: %w", err)
		}
		for _, l := range lines {
			names.lines[l.ID] = l
		}
	}
	return names, nil
}

// entryResponse projects one row for one caller. billing is set exactly when
// the caller may see the entry's bill rate and cost exactly when they may see
// its cost rate — and then always set, even with nothing in it, so a client
// can tell "may see, no rate resolved" from "may not see" (D8).
func entryResponse(row store.TimeEntry, a entryAccess, names entryNames) (gen.TimeEntryResponse, error) {
	hours, err := floatPtrFromNumeric(row.Hours)
	if err != nil {
		return gen.TimeEntryResponse{}, err
	}
	resp := gen.TimeEntryResponse{
		Id:              row.ID,
		UserId:          row.UserID,
		UserDisplayName: names.users[row.UserID].DisplayName,
		ProjectId:       row.ProjectID,
		ProjectCode:     "",
		ProjectName:     unknownProject,
		BillingLineId:   row.BillingLineID,
		TaskId:          row.TaskID,
		TaskTitle:       row.TaskTitle,
		EntryDate:       openapi_types.Date{Time: row.EntryDate.Time},
		StartTime:       clockFromTime(row.StartTime),
		EndTime:         clockFromTime(row.EndTime),
		Note:            row.Note,
		Billable:        row.Billable,
		RateSource:      row.RateSource,
		Status:          row.Status,
		RejectionReason: row.RejectionReason,
		SubmittedAt:     row.SubmittedAt,
		ApprovedAt:      row.ApprovedAt,
		InvoicedAt:      row.InvoicedAt,
		Revision:        row.Revision,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
		Capabilities: gen.TimeEntryCapabilities{
			CanEdit:      a.CanEdit,
			CanSubmit:    a.CanSubmit,
			CanApprove:   a.CanApprove,
			CanUnapprove: a.CanUnapprove,
		},
	}
	if hours != nil {
		resp.Hours = *hours
	}
	if p, ok := names.projects[row.ProjectID]; ok {
		resp.ProjectCode, resp.ProjectName = p.Code, p.Name
	}
	// A line the directory no longer lists keeps its id and loses its codes:
	// the entry still renders, and says which line it was on. The trackable
	// code needs both halves, so a project the directory cannot resolve
	// quotes none rather than a code like "-PM" nobody ever wrote.
	if row.BillingLineID != nil {
		if l, ok := names.lines[*row.BillingLineID]; ok {
			code := l.Code
			resp.BillingLineCode = &code
			if resp.ProjectCode != "" {
				trackable := trackableCode(resp.ProjectCode, l.Code)
				resp.TrackableCode = &trackable
			}
		}
	}
	if row.ApprovedByUserID != nil {
		resp.ApprovedBy = &gen.TimeEntryApprover{
			UserId:      *row.ApprovedByUserID,
			DisplayName: names.users[*row.ApprovedByUserID].DisplayName,
		}
	}
	if a.CanSeeBilling {
		rate, err := floatPtrFromNumeric(row.BillRate)
		if err != nil {
			return gen.TimeEntryResponse{}, err
		}
		resp.Billing = &gen.TimeEntryBilling{BillRate: rate, Currency: row.BillCurrency}
	}
	if a.CanSeeCost {
		rate, err := floatPtrFromNumeric(row.CostRate)
		if err != nil {
			return gen.TimeEntryResponse{}, err
		}
		resp.Cost = &gen.TimeEntryCost{CostRate: rate, Currency: row.CostCurrency}
	}
	return resp, nil
}

// entryResponseFor renders the single entry a create or a read answers with,
// through exactly the code that will render a list of them.
func (s *server) entryResponseFor(ctx context.Context, row store.TimeEntry, a entryAccess) (gen.TimeEntryResponse, error) {
	names, err := s.namesFor(ctx, []store.TimeEntry{row})
	if err != nil {
		return gen.TimeEntryResponse{}, err
	}
	return entryResponse(row, a, names)
}

// entryResponses renders a set of rows for one caller: the names resolved
// once for all of them (namesFor), the access resolved per row through the
// caller's cached roles, so a page of entries on one project asks the
// directory about it once.
func (s *server) entryResponses(ctx context.Context, c *caller, rows []store.TimeEntry) ([]gen.TimeEntryResponse, error) {
	names, err := s.namesFor(ctx, rows)
	if err != nil {
		return nil, err
	}
	out := make([]gen.TimeEntryResponse, 0, len(rows))
	for _, row := range rows {
		a, err := s.entryAccess(ctx, c, row)
		if err != nil {
			return nil, err
		}
		resp, err := entryResponse(row, a, names)
		if err != nil {
			return nil, err
		}
		out = append(out, resp)
	}
	return out, nil
}

// trackableCode is the pair an entry quotes its line by — the project's code
// and the line's, joined with a hyphen ('KVEM1000-PM') — computed the way
// projects computes it (D2/D9 of the projects design), from the codes as they
// stand now.
func trackableCode(projectCode, lineCode string) string {
	return projectCode + "-" + lineCode
}
