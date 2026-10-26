package planning

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// This file is the requests' writes (design S16, S23, §4.8): create, replace,
// withdraw and fill, and the reopen a deleted filling row causes
// (allocations_write.go's remove). Each reads everything it asks another
// module about before its transaction (rule 3) — the actor, the project, the
// catalogue, for a fill the person and their capacity — then takes S23's
// locks — the project's, then for a fill the person's — re-reads the request
// FOR UPDATE, judges it, writes it and its event. A filled or withdrawn
// request takes no fill, no change and no withdrawal (request_not_open; plan
// reading 2), so the filling row and the request cannot drift apart.

// CreatePlanningRequest Create a request
// (POST /api/v1/planning/requests)
func (s *server) CreatePlanningRequest(ctx context.Context, req gen.CreatePlanningRequestRequestObject) (gen.CreatePlanningRequestResponseObject, error) {
	var body gen.PlanningRequestInput
	if req.Body != nil {
		body = *req.Body
	}
	out, err := s.createRequest(ctx, body)
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.CreatePlanningRequest400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusConflict:
			return gen.CreatePlanningRequest409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.CreatePlanningRequest503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.CreatePlanningRequest201JSONResponse(out), nil
}

// parseRequestWrite is a request body's field rules, the bound's and the
// requirements' against Staff's catalogue as the caller may see it — read
// only when the body names a requirement — for a create or a replace. It
// answers the catalogue too, which names the requirements in the answer.
func (s *server) parseRequestWrite(ctx context.Context, c *callerContext, body gen.PlanningRequestInput) (requestFields, map[int64]contracts.CompetenceEntry, error) {
	f := s.unknownProperties(ctx, requestRequestSchema)
	fields, invalidFields := parseRequest(body)
	f.merge(invalidFields)
	if !f.failed() {
		f.merge(boundErrors(fields.from, fields.to, c.Today))
	}
	catalogue := map[int64]contracts.CompetenceEntry{}
	if len(fields.requirements) > 0 {
		entries, err := s.workforceCompetences(ctx)
		if err != nil {
			return requestFields{}, nil, err
		}
		catalogue = competencesByID(entries)
		f.merge(requirementErrors(body, catalogue))
	}
	if f.failed() {
		return requestFields{}, nil, refuseInvalid(invalidRequestTitle, f)
	}
	return fields, catalogue, nil
}

// requestProject is the project a request is written on, read before the
// transaction: a project the directory does not resolve is a field error on
// projectId, a closed one project_closed (S16).
func (s *server) requestProject(ctx context.Context, id int32) (*contracts.ProjectEntry, error) {
	project, err := s.projectEntry(ctx, id)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, refuseInvalid(invalidRequestTitle, fieldErrors{errs: fieldError("projectId", unknownProjectMessage)})
	}
	if projectClosed(project) {
		return nil, refuseRequestOnClosedProject(project)
	}
	return project, nil
}

// createRequest is POST /requests (S16): the request open, its requirements,
// and request_created, under the project's lock.
func (s *server) createRequest(ctx context.Context, body gen.PlanningRequestInput) (gen.PlanningRequestWritten, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return gen.PlanningRequestWritten{}, err
	}
	fields, catalogue, err := s.parseRequestWrite(ctx, c, body)
	if err != nil {
		return gen.PlanningRequestWritten{}, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return gen.PlanningRequestWritten{}, err
	}
	project, err := s.requestProject(ctx, fields.projectID)
	if err != nil {
		return gen.PlanningRequestWritten{}, err
	}

	var (
		row          store.PlanningRequest
		requirements []store.PlanningRequestRequirement
	)
	err = s.withProjectLock(ctx, fields.projectID, func(ctx context.Context, txq *store.Queries) error {
		if row, err = txq.InsertRequest(ctx, store.InsertRequestParams{
			ProjectID: fields.projectID, Title: fields.title,
			FromDate: dayParam(fields.from), ToDate: dayParam(fields.to), HoursPerWeek: numericOfHundredths(fields.hours),
			Note: fields.note, Now: s.deps.Clock(), UserID: &c.UserID,
		}); err != nil {
			return fmt.Errorf("planning: create a request: %w", err)
		}
		requirements = requirementRows(row.ID, fields.requirements)
		if err := insertRequirements(ctx, txq, requirements); err != nil {
			return err
		}
		return s.recordChanges(ctx, txq, who, requestEvent(row, eventRequestCreated),
			requestChangesOf(nil, requestRecorded(&row, requirements)))
	})
	if err != nil {
		return gen.PlanningRequestWritten{}, err
	}
	return gen.PlanningRequestWritten{
		Request:  requestResponse(row, requirements, catalogue, project, nil, c.Manage),
		Warnings: requestWarnings(project, fields.from, fields.to),
	}, nil
}

// PutPlanningRequest Change a request
// (PUT /api/v1/planning/requests/{id})
func (s *server) PutPlanningRequest(ctx context.Context, req gen.PutPlanningRequestRequestObject) (gen.PutPlanningRequestResponseObject, error) {
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutPlanningRequest400ApplicationProblemPlusJSONResponse(invalid(invalidRequestTitle, badIfMatch())), nil
	}
	var body gen.PlanningRequestInput
	if req.Body != nil {
		body = *req.Body
	}
	out, err := s.replaceRequest(ctx, req.Id, revision, body)
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.PutPlanningRequest400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusNotFound:
			return gen.PutPlanningRequest404Response{}, nil
		case http.StatusConflict:
			return gen.PutPlanningRequest409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.PutPlanningRequest503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.PutPlanningRequest200JSONResponse(out), nil
}

// replaceRequest is PUT /requests/{id} (S16, §4.8): an open request replaced
// whole, its requirements with it, under the old and the new project's
// locks; project_closed judged on both projects (as amendment 19 judges an
// allocation's), request_changed with the fields that changed. A replace
// that changes nothing writes nothing.
func (s *server) replaceRequest(ctx context.Context, id int64, revision int32, body gen.PlanningRequestInput) (gen.PlanningRequestWritten, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return gen.PlanningRequestWritten{}, err
	}
	fields, catalogue, err := s.parseRequestWrite(ctx, c, body)
	if err != nil {
		return gen.PlanningRequestWritten{}, err
	}
	current, found, err := s.request(ctx, id)
	if err != nil {
		return gen.PlanningRequestWritten{}, err
	}
	if !found {
		return gen.PlanningRequestWritten{}, notFound()
	}
	if err := checkRevision("request", current.Revision, revision); err != nil {
		return gen.PlanningRequestWritten{}, err
	}
	if current.Status != requestOpen {
		return gen.PlanningRequestWritten{}, refuseRequestNotOpen(current)
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return gen.PlanningRequestWritten{}, err
	}
	project, err := s.requestProject(ctx, fields.projectID)
	if err != nil {
		return gen.PlanningRequestWritten{}, err
	}
	if current.ProjectID != fields.projectID {
		previous, err := s.projectEntry(ctx, current.ProjectID)
		if err != nil {
			return gen.PlanningRequestWritten{}, err
		}
		if projectClosed(previous) {
			return gen.PlanningRequestWritten{}, refuseRequestOnClosedProject(previous)
		}
	}

	var (
		row          store.PlanningRequest
		requirements []store.PlanningRequestRequirement
	)
	err = s.withLocks(ctx, lockSet{projects: []int32{current.ProjectID, fields.projectID}}, func(ctx context.Context, txq *store.Queries) error {
		before, err := lockedRequest(ctx, txq, id)
		if err != nil {
			return err
		}
		// The status moves the revision on too, so a request filled or
		// withdrawn since the read is a revision the caller has not seen.
		if before.Revision != current.Revision {
			return checkRevision("request", before.Revision, revision)
		}
		held, err := requirementsByRequest(ctx, txq, []int64{id})
		if err != nil {
			return err
		}
		after := before
		after.ProjectID, after.Title, after.FromDate, after.ToDate = fields.projectID, fields.title, dayParam(fields.from), dayParam(fields.to)
		after.HoursPerWeek, after.Note = numericOfHundredths(fields.hours), fields.note
		wanted := requirementRows(id, fields.requirements)
		changed := requestChangesOf(requestRecorded(&before, held[id]), requestRecorded(&after, wanted))
		if len(changed) == 0 {
			row, requirements = before, held[id]
			return nil
		}
		if row, err = txq.UpdateRequest(ctx, store.UpdateRequestParams{
			ID: id, ProjectID: after.ProjectID, Title: after.Title, FromDate: after.FromDate, ToDate: after.ToDate,
			HoursPerWeek: after.HoursPerWeek, Note: after.Note, Now: s.deps.Clock(), UserID: &c.UserID,
		}); err != nil {
			return fmt.Errorf("planning: change request %d: %w", id, err)
		}
		requirements = held[id]
		if _, ok := changed["requirements"]; ok {
			if err := txq.DeleteRequirements(ctx, id); err != nil {
				return fmt.Errorf("planning: replace request %d's requirements: %w", id, err)
			}
			if err := insertRequirements(ctx, txq, wanted); err != nil {
				return err
			}
			requirements = wanted
		}
		return s.recordChanges(ctx, txq, who, requestEvent(row, eventRequestChanged), changed)
	})
	if err != nil {
		return gen.PlanningRequestWritten{}, err
	}
	return gen.PlanningRequestWritten{
		Request:  requestResponse(row, requirements, catalogue, project, nil, c.Manage),
		Warnings: requestWarnings(project, fields.from, fields.to),
	}, nil
}

// WithdrawPlanningRequest Withdraw a request
// (POST /api/v1/planning/requests/{id}/withdraw)
func (s *server) WithdrawPlanningRequest(ctx context.Context, req gen.WithdrawPlanningRequestRequestObject) (gen.WithdrawPlanningRequestResponseObject, error) {
	var out gen.PlanningRequest
	err := s.retryMoved(func() error {
		var err error
		out, err = s.withdraw(ctx, req.Id)
		return err
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.WithdrawPlanningRequest404Response{}, nil
		case http.StatusConflict:
			return gen.WithdrawPlanningRequest409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.WithdrawPlanningRequest503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.WithdrawPlanningRequest200JSONResponse(out), nil
}

// withdraw is POST /requests/{id}/withdraw (S16, §4.8): an open request
// closed for good under its project's lock, recorded as request_withdrawn.
// No If-Match: the request is re-read under the lock, and one changed in
// between starts the withdrawal again (retryMoved), so a request filled
// meanwhile is request_not_open. A closed project's request may be
// withdrawn: withdrawing changes no demand but to remove it. The answer is
// built from what was read before the transaction — the project, the
// requirements and the catalogue naming them, which the revision checked
// under the lock vouches for — and the row the update returns, so a
// contract that fails after the commit cannot answer 503 for a request that
// is withdrawn. A withdrawn request is filled by no row.
func (s *server) withdraw(ctx context.Context, id int64) (gen.PlanningRequest, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return gen.PlanningRequest{}, err
	}
	current, found, err := s.request(ctx, id)
	if err != nil {
		return gen.PlanningRequest{}, err
	}
	if !found {
		return gen.PlanningRequest{}, notFound()
	}
	if current.Status != requestOpen {
		return gen.PlanningRequest{}, refuseRequestNotOpen(current)
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return gen.PlanningRequest{}, err
	}
	project, err := s.projectEntry(ctx, current.ProjectID)
	if err != nil {
		return gen.PlanningRequest{}, err
	}
	requirements, err := requirementsByRequest(ctx, store.New(s.deps.Pool), []int64{id})
	if err != nil {
		return gen.PlanningRequest{}, err
	}
	catalogue, err := s.catalogueFor(ctx, requirements)
	if err != nil {
		return gen.PlanningRequest{}, err
	}
	var row store.PlanningRequest
	err = s.withProjectLock(ctx, current.ProjectID, func(ctx context.Context, txq *store.Queries) error {
		before, err := lockedRequest(ctx, txq, id)
		if err != nil {
			return err
		}
		if before.Revision != current.Revision {
			return errRowMoved
		}
		if row, err = txq.SetRequestStatus(ctx, store.SetRequestStatusParams{
			ID: id, Status: requestWithdrawn, Now: s.deps.Clock(), UserID: &c.UserID,
		}); err != nil {
			return fmt.Errorf("planning: withdraw request %d: %w", id, err)
		}
		return s.recordChanges(ctx, txq, who, requestEvent(row, eventRequestWithdrawn),
			changes{"status": {From: before.Status, To: row.Status}})
	})
	if err != nil {
		return gen.PlanningRequest{}, err
	}
	return requestResponse(row, requirements[id], catalogue, project, nil, c.Manage), nil
}

// FillPlanningRequest Fill a request
// (POST /api/v1/planning/requests/{id}/fill)
func (s *server) FillPlanningRequest(ctx context.Context, req gen.FillPlanningRequestRequestObject) (gen.FillPlanningRequestResponseObject, error) {
	var body gen.PlanningFillInput
	if req.Body != nil {
		body = *req.Body
	}
	var out gen.PlanningRequestFilled
	err := s.retryMoved(func() error {
		var err error
		out, err = s.fill(ctx, req.Id, body)
		return err
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.FillPlanningRequest400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusNotFound:
			return gen.FillPlanningRequest404Response{}, nil
		case http.StatusConflict:
			return gen.FillPlanningRequest409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.FillPlanningRequest503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.FillPlanningRequest201JSONResponse(out), nil
}

// fill is POST /requests/{id}/fill (S16, S23): the allocation written from
// the request's project, period, hours a week and title exactly as
// POST /allocations writes one (readCreate, insertAllocation) — the status
// as given or by the project's (plan reading 7), the person employed over
// the whole period, S7's overbooking under the person's lock — naming the
// request, and the request set filled with request_filled, in one
// transaction under the project's lock, then the person's. The person is
// mailed after the commit when the row is confirmed. No If-Match: the
// request is re-read under the lock, and one changed in between starts the
// fill again (retryMoved).
func (s *server) fill(ctx context.Context, id int64, body gen.PlanningFillInput) (gen.PlanningRequestFilled, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return gen.PlanningRequestFilled{}, err
	}
	f := s.unknownProperties(ctx, fillRequestSchema)
	if body.PersonId <= 0 {
		f.add("personId", "personId is required and must be a person's id")
	}
	status := enumField(&f, "status", (*string)(body.Status), allocationStatuses)
	if f.failed() {
		return gen.PlanningRequestFilled{}, refuseInvalid(invalidAllocationTitle, f)
	}
	current, found, err := s.request(ctx, id)
	if err != nil {
		return gen.PlanningRequestFilled{}, err
	}
	if !found {
		return gen.PlanningRequestFilled{}, notFound()
	}
	if current.Status != requestOpen {
		return gen.PlanningRequestFilled{}, refuseRequestNotOpen(current)
	}
	title := current.Title
	fields := allocationFields{
		personID: body.PersonId, projectID: current.ProjectID,
		from: civil(current.FromDate), to: civil(current.ToDate),
		loadUnit: loadUnitHours, load: hundredthsOfNumeric(current.HoursPerWeek),
		status: status, title: &title,
	}
	if b := boundErrors(fields.from, fields.to, c.Today); b.failed() {
		return gen.PlanningRequestFilled{}, refuseInvalid(invalidAllocationTitle, b)
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return gen.PlanningRequestFilled{}, err
	}
	// The fill's body has no projectId: the field names the request's
	// project, which the directory no longer resolves, so the 400 is the
	// request's (invalidRequestTitle), not the allocation's the fill's own
	// fields answer under.
	w, err := s.readCreate(ctx, c, fields, invalidRequestTitle)
	if err != nil {
		return gen.PlanningRequestFilled{}, err
	}
	held, err := requirementsByRequest(ctx, store.New(s.deps.Pool), []int64{id})
	if err != nil {
		return gen.PlanningRequestFilled{}, err
	}
	catalogue := map[int64]contracts.CompetenceEntry{}
	if len(held[id]) > 0 {
		entries, err := s.workforceCompetences(ctx)
		if err != nil {
			return gen.PlanningRequestFilled{}, err
		}
		catalogue = competencesByID(entries)
	}

	var (
		row     store.PlanningAllocation
		request store.PlanningRequest
		o       overbooking
	)
	locks := lockSet{projects: []int32{current.ProjectID}, persons: []int64{body.PersonId}}
	err = s.withLocks(ctx, locks, func(ctx context.Context, txq *store.Queries) error {
		before, err := lockedRequest(ctx, txq, id)
		if err != nil {
			return err
		}
		// What was read for the request — its project, period and hours,
		// its requirements — no longer matches one changed since.
		if before.Revision != current.Revision {
			return errRowMoved
		}
		if row, o, err = s.insertAllocation(ctx, txq, c, who, w, &id); err != nil {
			return err
		}
		if request, err = txq.SetRequestStatus(ctx, store.SetRequestStatusParams{
			ID: id, Status: requestFilled, Now: s.deps.Clock(), UserID: &c.UserID,
		}); err != nil {
			return fmt.Errorf("planning: fill request %d: %w", id, err)
		}
		e := requestEvent(request, eventRequestFilled)
		e.PersonID = &row.PersonID
		e.Payload = map[string]any{"allocationId": row.ID, "personId": row.PersonID}
		return s.recordEvent(ctx, txq, who, e)
	})
	if err != nil {
		return gen.PlanningRequestFilled{}, err
	}
	written := s.created(ctx, c, w, row, o)
	return gen.PlanningRequestFilled{
		Request:    requestResponse(request, held[id], catalogue, w.project, filledByOf(row, w.person), c.Manage),
		Allocation: written.Allocation,
		Warnings:   written.Warnings,
	}, nil
}

// reopenRequest is S16's rule on a deleted row that filled a request, inside
// the delete's transaction under the request's project's lock: the request
// open again, recorded as request_reopened naming the deleted row, filed
// under the request's project and the row's person (plan reading 8).
func (s *server) reopenRequest(ctx context.Context, txq *store.Queries, c *callerContext, who actor, deleted store.PlanningAllocation) error {
	request, err := txq.SetRequestStatus(ctx, store.SetRequestStatusParams{
		ID: *deleted.RequestID, Status: requestOpen, Now: s.deps.Clock(), UserID: &c.UserID,
	})
	if err != nil {
		return fmt.Errorf("planning: reopen request %d: %w", *deleted.RequestID, err)
	}
	e := requestEvent(request, eventRequestReopened)
	e.PersonID = &deleted.PersonID
	e.Payload = map[string]any{"allocationId": deleted.ID}
	return s.recordEvent(ctx, txq, who, e)
}

// requirementRows is a body's requirements as the rows of request id.
func requirementRows(id int64, fields []requirementField) []store.PlanningRequestRequirement {
	out := make([]store.PlanningRequestRequirement, len(fields))
	for i, r := range fields {
		out[i] = store.PlanningRequestRequirement{RequestID: id, CompetenceID: r.competenceID, MinLevel: r.minLevel}
	}
	return out
}

// insertRequirements writes rows, in their order.
func insertRequirements(ctx context.Context, txq *store.Queries, rows []store.PlanningRequestRequirement) error {
	for _, r := range rows {
		if err := txq.InsertRequirement(ctx, store.InsertRequirementParams{
			RequestID: r.RequestID, CompetenceID: r.CompetenceID, MinLevel: r.MinLevel,
		}); err != nil {
			return fmt.Errorf("planning: write request %d's requirement: %w", r.RequestID, err)
		}
	}
	return nil
}

// requestEvent is an event of kind about r, touching its project.
func requestEvent(r store.PlanningRequest, kind string) event {
	projectID := r.ProjectID
	return event{EntityKind: entityRequest, EntityID: r.ID, ProjectID: &projectID, Kind: kind}
}

// requestRecorded is a request's fields as an event records them, by the
// contract's names, each present only when set: the requirements as
// [{competenceId, minLevel}] ordered by competence, so a body that only
// reorders them changes nothing. The status
// is its own events' (fill, reopen, withdraw), never a replace's.
func requestRecorded(r *store.PlanningRequest, requirements []store.PlanningRequestRequirement) map[string]any {
	if r == nil {
		return map[string]any{}
	}
	v := map[string]any{
		"projectId":    r.ProjectID,
		"title":        r.Title,
		"fromDate":     civil(r.FromDate).Format(time.DateOnly),
		"toDate":       civil(r.ToDate).Format(time.DateOnly),
		"hoursPerWeek": loadOf(r.HoursPerWeek),
	}
	if r.Note != nil {
		v["note"] = *r.Note
	}
	if len(requirements) == 0 {
		return v
	}
	sorted := slices.SortedFunc(slices.Values(requirements), func(a, b store.PlanningRequestRequirement) int {
		return cmp.Compare(a.CompetenceID, b.CompetenceID)
	})
	recorded := make([]map[string]any, len(sorted))
	for i, req := range sorted {
		recorded[i] = map[string]any{"competenceId": req.CompetenceID}
		if req.MinLevel != nil {
			recorded[i]["minLevel"] = *req.MinLevel
		}
	}
	v["requirements"] = recorded
	return v
}

// requestChangesOf is the fields that differ between two recorded requests
// (before empty for a create), their values before and after; a changed
// note is recorded by its name alone, with neither text (amendment 18: an
// event is planning:view's, the note planning:manage's).
func requestChangesOf(before, after map[string]any) changes {
	names := make([]string, 0, len(before)+len(after))
	for name := range before {
		names = append(names, name)
	}
	for name := range after {
		names = append(names, name)
	}
	out := changes{}
	for _, name := range slices.Compact(slices.Sorted(slices.Values(names))) {
		if reflect.DeepEqual(before[name], after[name]) {
			continue
		}
		if name == "note" {
			out[name] = fieldChange{}
			continue
		}
		out[name] = fieldChange{From: before[name], To: after[name]}
	}
	return out
}
