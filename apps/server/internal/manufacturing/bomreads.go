package manufacturing

// The bill of materials reads (manufacturing design M10–M13): one BoM with
// its operations and lines, and the list. Each reads this module's rows in
// one snapshot, then the catalog after it, on the pool.

import (
	"context"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// The list's page (listManufacturingBoms).
const (
	defaultBomLimit = 50
	maxBomLimit     = 200
	// maxBomSearchVariants is how many of the catalog's matches for q the
	// list looks for BoMs of.
	maxBomSearchVariants = 200
)

// bomAnswer is the BoM as the contract answers it: its rows read in one
// snapshot, then the catalog's variants after it. An unknown id is the 404.
func (s *server) bomAnswer(ctx context.Context, id int64) (gen.ManufacturingBom, error) {
	var (
		b      store.ManufacturingBom
		ops    []store.OperationsOfBomsRow
		lines  []store.ManufacturingBomLine
		checks []store.ManufacturingBomOperationCheck
		edges  []store.ManufacturingBomOperationDependency
		outs   []store.ManufacturingBomOutput
	)
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if b, err = q.GetBom(ctx, id); noRows(err) {
			return notFound()
		} else if err != nil {
			return err
		}
		if ops, err = q.OperationsOfBoms(ctx, []int64{id}); err != nil {
			return err
		}
		opIDs := make([]int64, 0, len(ops))
		for _, o := range ops {
			opIDs = append(opIDs, o.ID)
		}
		if checks, err = q.ChecksOfOperations(ctx, opIDs); err != nil {
			return err
		}
		if edges, err = q.DependenciesOfBoms(ctx, []int64{id}); err != nil {
			return err
		}
		if outs, err = q.OutputsOfBoms(ctx, []int64{id}); err != nil {
			return err
		}
		lines, err = q.LinesOfBoms(ctx, []int64{id})
		return err
	})
	if err != nil {
		return gen.ManufacturingBom{}, err
	}
	entries, err := s.catalogVariants(ctx, lockedVariants(b.VariantID, append(storedComponents(lines), storedOutputs(outs)...)))
	if err != nil {
		return gen.ManufacturingBom{}, err
	}
	costs := s.has(ctx, permCostsView)

	out := gen.ManufacturingBom{
		Id: b.ID, Variant: refOf(entries, b.VariantID, costs), Code: b.Code,
		OutputQuantity: numericText(b.OutputQuantity, quantityPlaces), IsDefault: b.IsDefault, MakeToOrder: b.MakeToOrder,
		LeadTimeDays: b.LeadTimeDays, Note: b.Note, LotPattern: b.LotPattern, Status: gen.ManufacturingStatus(b.Status), Revision: b.Revision,
		Operations: make([]gen.ManufacturingBomOperation, 0, len(ops)), Lines: make([]gen.ManufacturingBomLine, 0, len(lines)),
		Outputs: make([]gen.ManufacturingBomOutput, 0, len(outs)), CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt,
	}
	for _, o := range outs {
		out.Outputs = append(out.Outputs, gen.ManufacturingBomOutput{Id: o.ID, Variant: refOf(entries, o.VariantID, costs),
			Quantity: numericText(o.Quantity, quantityPlaces), CostShare: numericText(o.CostShare, percentPlaces), SortOrder: o.SortOrder})
	}
	depends := map[int64][]string{}
	for _, e := range edges {
		depends[e.OperationID] = append(depends[e.OperationID], operationKey(e.DependsOnID))
	}
	for _, o := range ops {
		if out.SiteId == nil {
			site := o.WorkCenterSiteID
			out.SiteId = &site
		}
		var setup *string
		if r := ratOf(o.SetupMinutes); r != nil {
			t := text(r, minutePlaces)
			setup = &t
		}
		code, name := o.WorkCenterCode, o.WorkCenterName
		if o.WorkCenterGroupID != nil && o.GroupCode != nil && o.GroupName != nil {
			code, name = *o.GroupCode, *o.GroupName
		}
		var ahead *string
		if r := ratOf(o.SendAheadQuantity); r != nil {
			t := text(r, quantityPlaces)
			ahead = &t
		}
		dependsOn := depends[o.ID]
		if dependsOn == nil {
			dependsOn = []string{}
		}
		out.Operations = append(out.Operations, gen.ManufacturingBomOperation{
			Id: o.ID, Key: operationKey(o.ID), Sequence: o.Sequence, Name: o.Name, WorkCenterId: o.WorkCenterID,
			WorkCenterGroupId: o.WorkCenterGroupID, WorkCenterCode: code, WorkCenterName: name, SetupMinutes: setup,
			MinutesPerCycle: numericText(o.MinutesPerCycle, minutePlaces), Instructions: o.Instructions,
			Checks: checkTemplatesOf(checks, o.ID), DependsOn: dependsOn, SendAheadQuantity: ahead,
		})
	}
	for _, l := range lines {
		var key *string
		if l.OperationID != nil {
			k := operationKey(*l.OperationID)
			key = &k
		}
		out.Lines = append(out.Lines, gen.ManufacturingBomLine{
			Id: l.ID, Variant: refOf(entries, l.VariantID, costs), Quantity: numericText(l.Quantity, quantityPlaces),
			ScrapPercent: numericText(l.ScrapPercent, percentPlaces), OperationId: l.OperationID, OperationKey: key,
			SortOrder: l.SortOrder, Note: l.Note,
		})
	}
	return out, nil
}

// GetManufacturingBom is one BoM with its operations, by sequence, and its
// lines, by sort order.
func (s *server) GetManufacturingBom(ctx context.Context, req gen.GetManufacturingBomRequestObject) (gen.GetManufacturingBomResponseObject, error) {
	out, err := s.bomAnswer(ctx, req.Id)
	if err != nil {
		return answer(err, answers[gen.GetManufacturingBomResponseObject]{
			notFound: func() gen.GetManufacturingBomResponseObject { return gen.GetManufacturingBom404Response{} },
			unavailable: func(p gen.ManufacturingProblem) gen.GetManufacturingBomResponseObject {
				return gen.GetManufacturingBom503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	return gen.GetManufacturingBom200JSONResponse(out), nil
}

// ListManufacturingBoms is a page of BoMs in a status (active by default), of
// a variant, matching q on their code, their note or their variant's SKU or
// name in the catalog, newest change first, with the total.
func (s *server) ListManufacturingBoms(ctx context.Context, req gen.ListManufacturingBomsRequestObject) (gen.ListManufacturingBomsResponseObject, error) {
	unavailable := answers[gen.ListManufacturingBomsResponseObject]{
		unavailable: func(p gen.ManufacturingProblem) gen.ListManufacturingBomsResponseObject {
			return gen.ListManufacturingBoms503ApplicationProblemPlusJSONResponse(p)
		},
	}
	limit, offset := int32(defaultBomLimit), int32(0)
	if req.Params.Limit != nil {
		limit = min(max(*req.Params.Limit, 1), maxBomLimit)
	}
	if req.Params.Offset != nil {
		offset = max(*req.Params.Offset, 0)
	}
	params := store.ListBomsParams{Status: statusFilter(req.Params.Status), VariantID: req.Params.VariantId,
		QVariantIds: []int32{}, PageLimit: limit, PageOffset: offset}
	if req.Params.Q != nil && strings.TrimSpace(*req.Params.Q) != "" {
		q := strings.TrimSpace(*req.Params.Q)
		params.Q = &q
		matches, err := s.searchVariants(ctx, q, contracts.VariantSearch{Limit: maxBomSearchVariants, IncludeInactive: true})
		if err != nil {
			return answer(err, unavailable)
		}
		for _, v := range matches {
			params.QVariantIds = append(params.QVariantIds, v.ID)
		}
	}
	var (
		rows  []store.ListBomsRow
		total int32
	)
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if rows, err = q.ListBoms(ctx, params); err != nil {
			return err
		}
		total, err = q.CountBoms(ctx, store.CountBomsParams{Status: params.Status, VariantID: params.VariantID, Q: params.Q, QVariantIds: params.QVariantIds})
		return err
	})
	if err != nil {
		return nil, err
	}
	ids := make([]int32, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.VariantID)
	}
	entries, err := s.catalogVariants(ctx, dedupe(ids))
	if err != nil {
		return answer(err, unavailable)
	}
	costs := s.has(ctx, permCostsView)
	items := make([]gen.ManufacturingBomSummary, 0, len(rows))
	for _, r := range rows {
		items = append(items, gen.ManufacturingBomSummary{
			Id: r.ID, Variant: refOf(entries, r.VariantID, costs), Code: r.Code,
			OutputQuantity: numericText(r.OutputQuantity, quantityPlaces), IsDefault: r.IsDefault, MakeToOrder: r.MakeToOrder,
			Status: gen.ManufacturingStatus(r.Status), Revision: r.Revision, LineCount: r.LineCount,
			OperationCount: r.OperationCount, UpdatedAt: r.UpdatedAt,
		})
	}
	return gen.ListManufacturingBoms200JSONResponse{Items: items, Total: total}, nil
}
