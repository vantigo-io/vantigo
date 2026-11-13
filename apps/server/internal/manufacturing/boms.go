package manufacturing

// The bill of materials writes (manufacturing design M10–M14, M10a, M12a,
// M14a): create, update, archive, reactivate and make default. Every one runs
// in the same order: the request's shape is judged before any transaction;
// then, inside one, ProductCatalog.LockVariants on the product and every
// component (M10a) — whose answer alone says whether each exists and is
// goods — then the BoM graph's advisory lock (M14a), then the BoM's own row,
// the rules, the cycle and depth walk (M14) and the write.

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// A BoM's limits (M10–M12).
const (
	maxLines           = 200
	maxOutputs         = 20
	maxOperations      = 50
	maxBomCode         = 32
	maxBomNote         = 2000
	maxLineNote        = 2000
	maxOperationKey    = 40
	maxInstructions    = 20000
	bomWhat            = "bill of materials"
	bomTitle           = "The bill of materials was not saved"
	bomDefaultIndex    = "ux_boms_default"
	bomCodeUniqueIndex = "ux_boms_code"
)

// bomFields is a BoM's input, whichever operation sent it.
type bomFields struct {
	Code           *string
	OutputQuantity string
	MakeToOrder    *bool
	LeadTimeDays   *int32
	Note           *string
	LotPattern     *string
	Operations     []gen.ManufacturingBomOperationInput
	Lines          []gen.ManufacturingBomLineInput
	Outputs        *[]gen.ManufacturingBomOutputInput
}

// bomValues is a BoM's input judged: what is stored.
type bomValues struct {
	code        *string
	output      *big.Rat
	makeToOrder bool
	leadTime    int32
	note        string
	lotPattern  *string // nil: not sent — the default on a create, kept on an update
	ops         []opValues
	deps        [][]string // each operation's predecessors, by key, in ops' order (M112)
	lines       []lineValues
	outputs     []outputValues // the by-products (manufacturing phase 2 M120)
}

// outputValues is a by-product as stored (M120).
type outputValues struct {
	variantID int32
	quantity  *big.Rat
	share     *big.Rat
	sortOrder int32
}

type opValues struct {
	key          string
	id           *int64
	sequence     int32
	name         string
	workCenterID *int64
	groupID      *int64 // the work center group it targets instead (M118)
	sendAhead    *big.Rat
	setup        *big.Rat
	perCycle     *big.Rat
	instructions string
	checks       []checkValues
}

type lineValues struct {
	variantID int32
	quantity  *big.Rat
	scrap     *big.Rat
	opKey     *string
	sortOrder int32
	note      string
}

// judgement is a BoM write's failures so far: the field errors and every
// failing line, answered together in one 400.
type judgement struct {
	f     fieldErrors
	lines []lineFailure
}

func (j *judgement) line(index int, code, detail string) {
	j.lines = append(j.lines, lineFailure{index: index, code: code, detail: detail})
}

func (j *judgement) failed() bool { return j.f.failed() || len(j.lines) > 0 }

// err is the judgement as a 400 — the first field's code, else the first
// failing line's — or nil when nothing failed.
func (j *judgement) err() error {
	if !j.failed() {
		return nil
	}
	slices.SortStableFunc(j.lines, func(a, b lineFailure) int { return a.index - b.index })
	var p gen.ManufacturingProblem
	if len(j.lines) > 0 {
		p = linesProblem(bomTitle, j.lines)
	} else {
		p = problem(http.StatusBadRequest, "", bomTitle, "")
	}
	if j.f.failed() {
		p.Errors = &j.f.errs
		if j.f.code != "" {
			p.Code = &j.f.code
		}
	}
	return refuse(p)
}

// judgeBom is the request's shape: every rule that needs no catalog and no
// row (M10–M12).
func (s *server) judgeBom(ctx context.Context, in bomFields, schema string) (bomValues, judgement) {
	j := judgement{f: s.unknownProperties(ctx, schema)}
	var v bomValues
	if in.Code != nil {
		if code := strings.TrimSpace(*in.Code); code != "" {
			if utf8.RuneCountInString(code) > maxBomCode {
				j.f.add("code", "At most 32 characters")
			}
			v.code = &code
		}
	}
	output, msg := parsePositive(in.OutputQuantity, quantityPlaces, maxQuantity)
	j.f.addCoded("outputQuantity", codeQuantityInvalid, msg)
	v.output = output
	if in.MakeToOrder != nil {
		v.makeToOrder = *in.MakeToOrder
	}
	if in.LeadTimeDays != nil {
		v.leadTime = *in.LeadTimeDays
		if v.leadTime < 0 {
			j.f.add("leadTimeDays", "Must be 0 or more")
		}
	}
	if in.Note != nil {
		v.note = strings.TrimSpace(*in.Note)
		if utf8.RuneCountInString(v.note) > maxBomNote {
			j.f.add("note", "At most 2000 characters")
		}
	}
	if in.LotPattern != nil {
		pattern := strings.TrimSpace(*in.LotPattern)
		if n := utf8.RuneCountInString(pattern); n < 1 || n > maxLotPattern {
			j.f.addCoded("lotPattern", codeLotPatternInvalid, "1–40 characters")
		}
		v.lotPattern = &pattern
	}
	if len(in.Operations) > maxOperations {
		j.f.addCoded("operations", codeTooManyOperations, "At most 50 operations")
	}
	if len(in.Lines) > maxLines {
		j.f.addCoded("lines", codeTooManyLines, "At most 200 lines")
	}
	if j.f.code == codeTooManyLines || j.f.code == codeTooManyOperations {
		return v, j
	}

	keys := map[string]bool{}
	ids := map[int64]bool{}
	sequences := map[int32]bool{}
	dependsOn := make([][]string, len(in.Operations))
	sent := make([]bool, len(in.Operations))
	for i, op := range in.Operations {
		at := fmt.Sprintf("operations[%d].", i)
		o := opValues{id: op.Id, sequence: op.Sequence, workCenterID: op.WorkCenterId, groupID: op.WorkCenterGroupId}
		if (o.workCenterID == nil) == (o.groupID == nil) {
			j.f.add(at+"workCenterId", "Exactly one of workCenterId and workCenterGroupId")
		}
		if op.DependsOn != nil {
			dependsOn[i], sent[i] = *op.DependsOn, true
		}
		if op.SendAheadQuantity != nil {
			q, msg := parsePositive(*op.SendAheadQuantity, quantityPlaces, maxQuantity)
			j.f.addCoded(at+"sendAheadQuantity", codeQuantityInvalid, msg)
			o.sendAhead = q
		}
		o.key = strings.TrimSpace(op.Key)
		switch n := utf8.RuneCountInString(o.key); {
		case n < 1 || n > maxOperationKey:
			j.f.add(at+"key", "1–40 characters")
		case keys[o.key]:
			j.f.add(at+"key", "Two operations have this key")
		}
		keys[o.key] = true
		if o.id != nil {
			if ids[*o.id] {
				j.f.add(at+"id", "Two operations have this id")
			}
			ids[*o.id] = true
		}
		if sequences[o.sequence] {
			j.f.add(at+"sequence", "Two operations have this sequence")
		}
		sequences[o.sequence] = true
		o.name = strings.TrimSpace(op.Name)
		if n := utf8.RuneCountInString(o.name); n < 1 || n > maxNameLength {
			j.f.add(at+"name", "1–200 characters")
		}
		if op.SetupMinutes != nil {
			setup, msg := parseDecimal(*op.SetupMinutes, minutePlaces, maxMinutes)
			j.f.add(at+"setupMinutes", msg)
			o.setup = setup
		}
		perCycle, msg := parsePositive(op.MinutesPerCycle, minutePlaces, maxMinutes)
		j.f.add(at+"minutesPerCycle", msg)
		o.perCycle = perCycle
		if op.Instructions != nil {
			o.instructions = *op.Instructions
			if utf8.RuneCountInString(o.instructions) > maxInstructions {
				j.f.add(at+"instructions", "At most 20000 characters")
			}
		}
		if op.Checks != nil {
			o.checks = judgeChecks(&j.f, at, *op.Checks)
		}
		v.ops = append(v.ops, o)
	}
	if !j.f.failed() {
		v.deps = judgeDependencies(&j.f, v.ops, dependsOn, sent)
	}

	for i, line := range in.Lines {
		at := fmt.Sprintf("lines[%d].", i)
		l := lineValues{variantID: line.VariantId, sortOrder: int32(i), scrap: new(big.Rat)}
		quantity, msg := parsePositive(line.Quantity, quantityPlaces, maxQuantity)
		if msg != "" {
			j.line(i, codeQuantityInvalid, "The quantity: "+strings.ToLower(msg[:1])+msg[1:])
		}
		l.quantity = quantity
		if line.ScrapPercent != nil {
			scrap, msg := parseDecimal(*line.ScrapPercent, percentPlaces, maxScrap)
			j.f.add(at+"scrapPercent", msg)
			l.scrap = scrap
		}
		if line.OperationKey != nil {
			if key := strings.TrimSpace(*line.OperationKey); key != "" {
				if !keys[key] {
					j.line(i, codeOperationKeyUnknown, fmt.Sprintf("No operation of this BoM has the key %q", key))
				}
				l.opKey = &key
			}
		}
		if line.SortOrder != nil {
			l.sortOrder = *line.SortOrder
		}
		if line.Note != nil {
			l.note = strings.TrimSpace(*line.Note)
			if utf8.RuneCountInString(l.note) > maxLineNote {
				j.f.add(at+"note", "At most 2000 characters")
			}
		}
		v.lines = append(v.lines, l)
	}
	v.outputs = judgeOutputs(&j, in.Outputs)
	return v, j
}

// judgeOutputs is the by-products' shape (M120): at most 20, each variant
// once, a quantity more than 0 and a share of 0–100 with at most two
// decimals, the shares summing to at most 100 (cost_share_over).
func judgeOutputs(j *judgement, in *[]gen.ManufacturingBomOutputInput) []outputValues {
	if in == nil {
		return nil
	}
	if len(*in) > maxOutputs {
		j.f.add("outputs", "At most 20 by-products")
		return nil
	}
	out := make([]outputValues, 0, len(*in))
	seen := map[int32]bool{}
	sum := new(big.Rat)
	for i, o := range *in {
		at := fmt.Sprintf("outputs[%d].", i)
		v := outputValues{variantID: o.VariantId, share: new(big.Rat), sortOrder: int32(i)}
		if seen[o.VariantId] {
			j.f.add(at+"variantId", "Two by-products name this variant")
		}
		seen[o.VariantId] = true
		q, msg := parsePositive(o.Quantity, quantityPlaces, maxQuantity)
		j.f.addCoded(at+"quantity", codeQuantityInvalid, msg)
		v.quantity = q
		if o.CostShare != nil {
			share, msg := parseDecimal(*o.CostShare, percentPlaces, hundred)
			j.f.add(at+"costShare", msg)
			if share != nil {
				v.share = share
			}
		}
		sum.Add(sum, v.share)
		if o.SortOrder != nil {
			v.sortOrder = *o.SortOrder
		}
		out = append(out, v)
	}
	if sum.Cmp(hundred) > 0 {
		j.f.addCoded("outputs", codeCostShareOver, fmt.Sprintf("The by-products' shares sum to %s %%; at most 100", text(sum, percentPlaces)))
	}
	return out
}

// outputVariants is the by-products' variants, in their order.
func outputVariants(outputs []outputValues) []int32 {
	ids := make([]int32, 0, len(outputs))
	for _, o := range outputs {
		ids = append(ids, o.variantID)
	}
	return ids
}

// storedOutputs is stored by-products' variants, in their order.
func storedOutputs(outputs []store.ManufacturingBomOutput) []int32 {
	ids := make([]int32, 0, len(outputs))
	for _, o := range outputs {
		ids = append(ids, o.VariantID)
	}
	return ids
}

// judgeOutputGoods judges each by-product from what LockVariants answered
// (M120): absent is variant_not_found, not goods variant_not_goods, the BoM's
// own variant output_is_product, one of its components output_is_component.
func judgeOutputGoods(j *judgement, product int32, components, outputs []int32, locked map[int32]contracts.VariantEntry) {
	for i, id := range outputs {
		field := fmt.Sprintf("outputs[%d].variantId", i)
		v, ok := locked[id]
		switch {
		case !ok:
			j.f.addCoded(field, codeVariantNotFound, "No such variant in the catalog")
		case v.ProductType != goodsType:
			j.f.addCoded(field, codeVariantNotGoods, fmt.Sprintf("%s is not goods; only goods are made", v.SKU))
		case id == product:
			j.f.addCoded(field, codeOutputIsProduct, "The BoM's own product is not one of its by-products")
		case slices.Contains(components, id):
			j.f.addCoded(field, codeOutputIsComponent, fmt.Sprintf("%s is a component of this BoM; a by-product is never consumed by the BoM that makes it", v.SKU))
		}
	}
}

// lockedVariants is the product and the components a write locks (M10a).
func lockedVariants(product int32, components []int32) []int32 {
	ids := append([]int32{product}, components...)
	slices.Sort(ids)
	return slices.Compact(ids)
}

// judgeGoods judges the product and each component from what LockVariants
// answered (M10a): absent is variant_not_found, not goods variant_not_goods
// — the product's on variantId, a component's on its line.
func judgeGoods(j *judgement, product int32, components []int32, locked map[int32]contracts.VariantEntry) {
	if v, ok := locked[product]; !ok {
		j.f.addCoded("variantId", codeVariantNotFound, "No such variant in the catalog")
	} else if v.ProductType != goodsType {
		j.f.addCoded("variantId", codeVariantNotGoods, fmt.Sprintf("%s is not goods; only goods are made", v.SKU))
	}
	for i, id := range components {
		v, ok := locked[id]
		switch {
		case !ok:
			j.line(i, codeVariantNotFound, fmt.Sprintf("No variant %d in the catalog", id))
		case v.ProductType != goodsType:
			j.line(i, codeVariantNotGoods, fmt.Sprintf("%s is not goods; only goods are consumed", v.SKU))
		}
	}
}

// componentsOf is the lines' variants, in the lines' order.
func componentsOf(lines []lineValues) []int32 {
	out := make([]int32, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.variantID)
	}
	return out
}

// storedComponents is stored lines' variants, in their order.
func storedComponents(lines []store.ManufacturingBomLine) []int32 {
	out := make([]int32, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.VariantID)
	}
	return out
}

// judgeOperations is the rules on the operations that read this module's
// rows: each names an active work center, or an active work center group with
// an active member (work_center_unknown, manufacturing phase 2 M118), all of
// them at one site (work_center_other_site, M12a), and an operation kept by
// id is one of this BoM's.
func judgeOperations(ctx context.Context, txq *store.Queries, ops []opValues, storedOps map[int64]bool) error {
	if len(ops) == 0 {
		return nil
	}
	var ids, groupIDs []int64
	for _, o := range ops {
		if o.workCenterID != nil {
			ids = append(ids, *o.workCenterID)
		}
		if o.groupID != nil {
			groupIDs = append(groupIDs, *o.groupID)
		}
	}
	centers, err := txq.WorkCentersByIDs(ctx, orEmpty(ids))
	if err != nil {
		return err
	}
	byID := map[int64]store.ManufacturingWorkCenter{}
	for _, c := range centers {
		byID[c.ID] = c
	}
	groups, err := txq.WorkCenterGroupsByIDs(ctx, orEmpty(groupIDs))
	if err != nil {
		return err
	}
	groupByID := map[int64]store.ManufacturingWorkCenterGroup{}
	for _, g := range groups {
		groupByID[g.ID] = g
	}
	members, err := groupMembers(ctx, txq, groupIDs)
	if err != nil {
		return err
	}
	var f fieldErrors
	sites := map[int64]bool{}
	for i, o := range ops {
		if o.groupID != nil {
			g, ok := groupByID[*o.groupID]
			if !ok || g.Status != statusActive || !slices.ContainsFunc(members[g.ID], func(c store.ManufacturingWorkCenter) bool { return c.Status == statusActive }) {
				f.addCoded(fmt.Sprintf("operations[%d].workCenterGroupId", i), codeWorkCenterUnknown, "Not an active work center group with an active member")
				continue
			}
			sites[g.SiteID] = true
		} else {
			c, ok := byID[*o.workCenterID]
			if !ok || c.Status != statusActive {
				f.addCoded(fmt.Sprintf("operations[%d].workCenterId", i), codeWorkCenterUnknown, "Not an active work center")
				continue
			}
			sites[c.SiteID] = true
		}
		if o.id != nil && !storedOps[*o.id] {
			f.add(fmt.Sprintf("operations[%d].id", i), "Not an operation of this bill of materials")
		}
	}
	if f.failed() {
		return refuseInvalid(bomTitle, f)
	}
	if len(sites) > 1 {
		p := problem(http.StatusBadRequest, codeWorkCenterOtherSite, bomTitle,
			"Every operation of a bill of materials is done at work centers of one site.")
		p.Errors = &map[string][]string{"operations": {"The work centers stand at more than one site"}}
		return refuse(p)
	}
	return nil
}

// judgeCode is the 409 bom_code_taken when another BoM has the code.
func judgeCode(ctx context.Context, txq *store.Queries, code *string, id int64) error {
	if code == nil {
		return nil
	}
	taken, err := txq.BomCodeTaken(ctx, store.BomCodeTakenParams{Code: code, ID: id})
	if err != nil {
		return err
	}
	if taken {
		return bomCodeTaken(*code)
	}
	return nil
}

func bomCodeTaken(code string) error {
	return conflict(codeBomCodeTaken, "The code is taken", fmt.Sprintf("Another bill of materials has the code %s.", code))
}

// mapBomUnique is a write's unique violation as the refusal it is: two
// defaults of one variant is a race on the default (revision_conflict, M14a),
// a code another BoM took meanwhile bom_code_taken.
func mapBomUnique(err error) error {
	switch {
	case db.IsUniqueViolation(err, bomDefaultIndex):
		return conflict(codeRevisionConflict, "Revision conflict",
			"Another change made a different bill of materials this variant's default meanwhile. Reload and try again.")
	case db.IsUniqueViolation(err, bomCodeUniqueIndex):
		return conflict(codeBomCodeTaken, "The code is taken", "Another bill of materials took the code meanwhile.")
	}
	return err
}

// writeStructure replaces the BoM's operations and lines (M13): an operation
// sent with its id is kept and rewritten, one without is inserted, the rest
// are deleted — a line naming one has its operation cleared by the key, and
// every line is replaced anyway, naming its operation by the request's key.
func writeStructure(ctx context.Context, txq *store.Queries, bomID int64, v bomValues) error {
	if err := txq.DeleteBomLines(ctx, bomID); err != nil {
		return err
	}
	keep := []int64{}
	for _, o := range v.ops {
		if o.id != nil {
			keep = append(keep, *o.id)
		}
	}
	if err := txq.DeleteBomOperationsExcept(ctx, store.DeleteBomOperationsExceptParams{BomID: bomID, KeepIds: keep}); err != nil {
		return err
	}
	if err := txq.DeleteChecksOfBom(ctx, bomID); err != nil {
		return err
	}
	if err := txq.DeleteDependenciesOfBom(ctx, bomID); err != nil {
		return err
	}
	opIDs := map[string]int64{}
	for _, o := range v.ops {
		setup := numeric(o.setup, minutePlaces)
		if o.id != nil {
			row, err := txq.UpdateBomOperation(ctx, store.UpdateBomOperationParams{
				ID: *o.id, BomID: bomID, Sequence: o.sequence, Name: o.name, WorkCenterID: o.workCenterID, WorkCenterGroupID: o.groupID,
				SetupMinutes: setup, MinutesPerCycle: numeric(o.perCycle, minutePlaces), SendAheadQuantity: numeric(o.sendAhead, quantityPlaces),
				Instructions: o.instructions,
			})
			if err != nil {
				return err
			}
			opIDs[o.key] = row.ID
			if err := writeChecks(ctx, txq, row.ID, o.checks); err != nil {
				return err
			}
			continue
		}
		row, err := txq.InsertBomOperation(ctx, store.InsertBomOperationParams{
			BomID: bomID, Sequence: o.sequence, Name: o.name, WorkCenterID: o.workCenterID, WorkCenterGroupID: o.groupID,
			SetupMinutes: setup, MinutesPerCycle: numeric(o.perCycle, minutePlaces), SendAheadQuantity: numeric(o.sendAhead, quantityPlaces),
			Instructions: o.instructions,
		})
		if err != nil {
			return err
		}
		opIDs[o.key] = row.ID
		if err := writeChecks(ctx, txq, row.ID, o.checks); err != nil {
			return err
		}
	}
	// The graph, by the request's keys (M112).
	for i, o := range v.ops {
		if i >= len(v.deps) {
			break
		}
		for _, key := range v.deps[i] {
			if err := txq.InsertBomOperationDependency(ctx, store.InsertBomOperationDependencyParams{
				OperationID: opIDs[o.key], DependsOnID: opIDs[key],
			}); err != nil {
				return err
			}
		}
	}
	if err := txq.DeleteBomOutputs(ctx, bomID); err != nil {
		return err
	}
	for _, o := range v.outputs {
		if err := txq.InsertBomOutput(ctx, store.InsertBomOutputParams{
			BomID: bomID, VariantID: o.variantID, Quantity: numeric(o.quantity, quantityPlaces), CostShare: numeric(o.share, percentPlaces),
			SortOrder: o.sortOrder,
		}); err != nil {
			return err
		}
	}
	for _, l := range v.lines {
		var op *int64
		if l.opKey != nil {
			id := opIDs[*l.opKey]
			op = &id
		}
		if _, err := txq.InsertBomLine(ctx, store.InsertBomLineParams{
			BomID: bomID, VariantID: l.variantID, Quantity: numeric(l.quantity, quantityPlaces),
			ScrapPercent: numeric(l.scrap, percentPlaces), OperationID: op, SortOrder: l.sortOrder, Note: l.note,
		}); err != nil {
			return err
		}
	}
	return nil
}

// lockBom is the BoM's row FOR UPDATE, after LockBomGraph, under the
// revision the client read.
func lockBom(ctx context.Context, txq *store.Queries, id int64, revision int32) (store.ManufacturingBom, error) {
	b, err := txq.GetBomForUpdate(ctx, id)
	if noRows(err) {
		return b, notFound()
	}
	if err != nil {
		return b, err
	}
	if b.Revision != revision {
		return b, revisionConflict(bomWhat, b.Revision, revision)
	}
	return b, nil
}

// notActive is the 409 bom_not_active.
func notActive() error {
	return conflict(codeBomNotActive, "The bill of materials is archived",
		"An archived bill of materials is neither edited nor made the default. Reactivate it first.")
}

// createBomAnswers are the create's typed refusals.
func createBomAnswers() answers[gen.CreateManufacturingBomResponseObject] {
	return answers[gen.CreateManufacturingBomResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.CreateManufacturingBomResponseObject {
			return gen.CreateManufacturingBom400ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.ManufacturingProblem) gen.CreateManufacturingBomResponseObject {
			return gen.CreateManufacturingBom409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.CreateManufacturingBomResponseObject {
			return gen.CreateManufacturingBom503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// CreateManufacturingBom creates a BoM (M10). The variant's first active BoM
// becomes its default; isDefault makes it the default, unmarking the old one
// in the same transaction.
func (s *server) CreateManufacturingBom(ctx context.Context, req gen.CreateManufacturingBomRequestObject) (gen.CreateManufacturingBomResponseObject, error) {
	in := req.Body
	v, j := s.judgeBom(ctx, bomFields{
		Code: in.Code, OutputQuantity: in.OutputQuantity, MakeToOrder: in.MakeToOrder, LeadTimeDays: in.LeadTimeDays,
		Note: in.Note, LotPattern: in.LotPattern, Operations: in.Operations, Lines: in.Lines, Outputs: in.Outputs,
	}, bomInputSchema)
	if j.f.failed() {
		return answer(j.err(), createBomAnswers())
	}
	components := componentsOf(v.lines)
	outputs := outputVariants(v.outputs)
	var id int64
	err := s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		return mapBomUnique(func() error {
			locked, err := s.lockVariants(ctx, tx, lockedVariants(in.VariantId, append(slices.Clone(components), outputs...)))
			if err != nil {
				return err
			}
			judgeGoods(&j, in.VariantId, components, locked)
			judgeOutputGoods(&j, in.VariantId, components, outputs, locked)
			pattern := defaultLotPattern
			if v.lotPattern != nil {
				pattern = *v.lotPattern
			}
			judgeLotPattern(&j, pattern, locked[in.VariantId].SKU)
			if err := j.err(); err != nil {
				return err
			}
			if err := txq.LockBomGraph(ctx); err != nil {
				return err
			}
			if err := judgeOperations(ctx, txq, v.ops, nil); err != nil {
				return err
			}
			if err := judgeCode(ctx, txq, v.code, 0); err != nil {
				return err
			}
			hasDefault, err := txq.VariantHasActiveDefault(ctx, store.VariantHasActiveDefaultParams{VariantID: in.VariantId})
			if err != nil {
				return err
			}
			isDefault := !hasDefault || (in.IsDefault != nil && *in.IsDefault)
			if err := walkBom(ctx, txq, in.VariantId, components, outputs, isDefault); err != nil {
				return err
			}
			now := s.deps.Clock()
			if isDefault && hasDefault {
				if _, err := txq.UnsetVariantDefault(ctx, store.UnsetVariantDefaultParams{VariantID: in.VariantId, Now: now}); err != nil {
					return err
				}
			}
			b, err := txq.InsertBom(ctx, store.InsertBomParams{
				VariantID: in.VariantId, Code: v.code, OutputQuantity: numeric(v.output, quantityPlaces), IsDefault: isDefault,
				MakeToOrder: v.makeToOrder, LeadTimeDays: v.leadTime, Note: v.note, LotPattern: pattern, Now: now,
			})
			if err != nil {
				return err
			}
			id = b.ID
			return writeStructure(ctx, txq, b.ID, v)
		}())
	})
	if err = s.cycleRefusal(ctx, err); err != nil {
		return answer(err, createBomAnswers())
	}
	out, err := s.bomAnswer(ctx, id)
	if err != nil {
		return answer(err, createBomAnswers())
	}
	return gen.CreateManufacturingBom201JSONResponse(out), nil
}

// updateBomAnswers are the update's typed refusals.
func updateBomAnswers() answers[gen.UpdateManufacturingBomResponseObject] {
	return answers[gen.UpdateManufacturingBomResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.UpdateManufacturingBomResponseObject {
			return gen.UpdateManufacturingBom400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.UpdateManufacturingBomResponseObject { return gen.UpdateManufacturingBom404Response{} },
		conflict: func(p gen.ManufacturingProblem) gen.UpdateManufacturingBomResponseObject {
			return gen.UpdateManufacturingBom409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.UpdateManufacturingBomResponseObject {
			return gen.UpdateManufacturingBom503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// UpdateManufacturingBom replaces a BoM's fields, operations and lines under
// its revision (M13). Its variant and its default are not an edit's.
func (s *server) UpdateManufacturingBom(ctx context.Context, req gen.UpdateManufacturingBomRequestObject) (gen.UpdateManufacturingBomResponseObject, error) {
	in := req.Body
	// The variant never changes, so it is read before the transaction, to be
	// locked first (M10a).
	pre, err := store.New(s.deps.Pool).GetBom(ctx, req.Id)
	if noRows(err) {
		return gen.UpdateManufacturingBom404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	v, j := s.judgeBom(ctx, bomFields{
		Code: in.Code, OutputQuantity: in.OutputQuantity, MakeToOrder: in.MakeToOrder, LeadTimeDays: in.LeadTimeDays,
		Note: in.Note, LotPattern: in.LotPattern, Operations: in.Operations, Lines: in.Lines, Outputs: in.Outputs,
	}, bomUpdateSchema)
	if j.f.failed() {
		return answer(j.err(), updateBomAnswers())
	}
	components := componentsOf(v.lines)
	outputs := outputVariants(v.outputs)
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		return mapBomUnique(func() error {
			locked, err := s.lockVariants(ctx, tx, lockedVariants(pre.VariantID, append(slices.Clone(components), outputs...)))
			if err != nil {
				return err
			}
			if err := txq.LockBomGraph(ctx); err != nil {
				return err
			}
			b, err := lockBom(ctx, txq, req.Id, in.Revision)
			if err != nil {
				return err
			}
			if b.Status != statusActive {
				return notActive()
			}
			judgeGoods(&j, b.VariantID, components, locked)
			judgeOutputGoods(&j, b.VariantID, components, outputs, locked)
			pattern := b.LotPattern
			if v.lotPattern != nil {
				pattern = *v.lotPattern
			}
			judgeLotPattern(&j, pattern, locked[b.VariantID].SKU)
			if err := j.err(); err != nil {
				return err
			}
			stored, err := txq.OperationsOfBoms(ctx, []int64{b.ID})
			if err != nil {
				return err
			}
			storedOps := map[int64]bool{}
			for _, o := range stored {
				storedOps[o.ID] = true
			}
			if err := judgeOperations(ctx, txq, v.ops, storedOps); err != nil {
				return err
			}
			if err := judgeCode(ctx, txq, v.code, b.ID); err != nil {
				return err
			}
			if err := walkBom(ctx, txq, b.VariantID, components, outputs, b.IsDefault); err != nil {
				return err
			}
			if _, err := txq.UpdateBom(ctx, store.UpdateBomParams{
				ID: b.ID, Code: v.code, OutputQuantity: numeric(v.output, quantityPlaces), MakeToOrder: v.makeToOrder,
				LeadTimeDays: v.leadTime, Note: v.note, LotPattern: pattern, Now: s.deps.Clock(),
			}); err != nil {
				return err
			}
			return writeStructure(ctx, txq, b.ID, v)
		}())
	})
	if err = s.cycleRefusal(ctx, err); err != nil {
		return answer(err, updateBomAnswers())
	}
	out, err := s.bomAnswer(ctx, req.Id)
	if err != nil {
		return answer(err, updateBomAnswers())
	}
	return gen.UpdateManufacturingBom200JSONResponse(out), nil
}

// lifecycle is one of the BoM's status and default writes — archive,
// reactivate, make default — given the BoM's row under lock and its stored
// lines' variants.
type lifecycle func(ctx context.Context, txq *store.Queries, b store.ManufacturingBom, components []int32, locked map[int32]contracts.VariantEntry) error

// bomOutputsOf is the BoM's stored by-products' variants.
func bomOutputsOf(ctx context.Context, q *store.Queries, id int64) ([]int32, error) {
	rows, err := q.OutputsOfBoms(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	return storedOutputs(rows), nil
}

// runLifecycle runs a lifecycle write in the structural order (M10a, M14a):
// the product and its stored components locked through LockVariants, the
// graph lock, the BoM's row under its revision, then do.
func (s *server) runLifecycle(ctx context.Context, id int64, revision int32, do lifecycle) error {
	q := store.New(s.deps.Pool)
	pre, err := q.GetBom(ctx, id)
	if noRows(err) {
		return notFound()
	}
	if err != nil {
		return err
	}
	// The lines are read before the transaction, to be locked first; a change
	// to them since moves the revision on, which the lock below refuses.
	preLines, err := q.LinesOfBoms(ctx, []int64{id})
	if err != nil {
		return err
	}
	preOutputs, err := bomOutputsOf(ctx, q, id)
	if err != nil {
		return err
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		return mapBomUnique(func() error {
			locked, err := s.lockVariants(ctx, tx, lockedVariants(pre.VariantID, append(storedComponents(preLines), preOutputs...)))
			if err != nil {
				return err
			}
			if err := txq.LockBomGraph(ctx); err != nil {
				return err
			}
			b, err := lockBom(ctx, txq, id, revision)
			if err != nil {
				return err
			}
			lines, err := txq.LinesOfBoms(ctx, []int64{id})
			if err != nil {
				return err
			}
			return do(ctx, txq, b, storedComponents(lines), locked)
		}())
	})
	return s.cycleRefusal(ctx, err)
}

// judgeStoredGoods is judgeGoods of a stored BoM, for a write that makes it
// count again (reactivate, make default).
func judgeStoredGoods(b store.ManufacturingBom, components []int32, locked map[int32]contracts.VariantEntry) error {
	var j judgement
	judgeGoods(&j, b.VariantID, components, locked)
	return j.err()
}

// ArchiveManufacturingBom archives a BoM. Archiving the default promotes the
// variant's oldest other active BoM, or leaves none (M14a).
func (s *server) ArchiveManufacturingBom(ctx context.Context, req gen.ArchiveManufacturingBomRequestObject) (gen.ArchiveManufacturingBomResponseObject, error) {
	reply := answers[gen.ArchiveManufacturingBomResponseObject]{
		notFound: func() gen.ArchiveManufacturingBomResponseObject { return gen.ArchiveManufacturingBom404Response{} },
		conflict: func(p gen.ManufacturingProblem) gen.ArchiveManufacturingBomResponseObject {
			return gen.ArchiveManufacturingBom409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.ArchiveManufacturingBomResponseObject {
			return gen.ArchiveManufacturingBom503ApplicationProblemPlusJSONResponse(p)
		},
	}
	err := s.runLifecycle(ctx, req.Id, req.Body.Revision, func(ctx context.Context, txq *store.Queries, b store.ManufacturingBom, _ []int32, _ map[int32]contracts.VariantEntry) error {
		if b.Status == statusArchived {
			return nil
		}
		now := s.deps.Clock()
		if _, err := txq.SetBomStatus(ctx, store.SetBomStatusParams{ID: b.ID, Status: statusArchived, IsDefault: false, Now: now}); err != nil {
			return err
		}
		if !b.IsDefault {
			return nil
		}
		return promoteOldest(ctx, txq, b, now)
	})
	if err != nil {
		return answer(err, reply)
	}
	out, err := s.bomAnswer(ctx, req.Id)
	if err != nil {
		return answer(err, reply)
	}
	return gen.ArchiveManufacturingBom200JSONResponse(out), nil
}

// promoteOldest makes the variant's oldest other active BoM its default when
// its default was archived (M14a). One whose components would now make a
// cycle or go too deep is not promoted: the variant is left with no default,
// as it is when it has no other active BoM.
func promoteOldest(ctx context.Context, txq *store.Queries, archived store.ManufacturingBom, now time.Time) error {
	next, err := txq.OldestOtherActiveBom(ctx, store.OldestOtherActiveBomParams{VariantID: archived.VariantID, ExceptID: archived.ID})
	if noRows(err) {
		return nil
	}
	if err != nil {
		return err
	}
	lines, err := txq.LinesOfBoms(ctx, []int64{next.ID})
	if err != nil {
		return err
	}
	outputs, err := bomOutputsOf(ctx, txq, next.ID)
	if err != nil {
		return err
	}
	if err := walkBom(ctx, txq, next.VariantID, storedComponents(lines), outputs, true); err != nil {
		var cycle *cycleError
		var refused *refusal
		if errors.As(err, &cycle) || errors.As(err, &refused) {
			return nil
		}
		return err
	}
	_, err = txq.SetBomDefault(ctx, store.SetBomDefaultParams{ID: next.ID, Now: now})
	return err
}

// ReactivateManufacturingBom reactivates an archived BoM: its product and
// components judged again, its operations' work centers judged again
// (work_center_unknown, work_center_other_site), the cycle and depth walk run
// again, and it becomes
// the variant's default when the variant has no active default.
func (s *server) ReactivateManufacturingBom(ctx context.Context, req gen.ReactivateManufacturingBomRequestObject) (gen.ReactivateManufacturingBomResponseObject, error) {
	reply := answers[gen.ReactivateManufacturingBomResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.ReactivateManufacturingBomResponseObject {
			return gen.ReactivateManufacturingBom400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.ReactivateManufacturingBomResponseObject {
			return gen.ReactivateManufacturingBom404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.ReactivateManufacturingBomResponseObject {
			return gen.ReactivateManufacturingBom409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.ReactivateManufacturingBomResponseObject {
			return gen.ReactivateManufacturingBom503ApplicationProblemPlusJSONResponse(p)
		},
	}
	err := s.runLifecycle(ctx, req.Id, req.Body.Revision, func(ctx context.Context, txq *store.Queries, b store.ManufacturingBom, components []int32, locked map[int32]contracts.VariantEntry) error {
		if b.Status == statusActive {
			return nil
		}
		if err := judgeStoredGoods(b, components, locked); err != nil {
			return err
		}
		// Its work centers are judged again too: one may have been archived,
		// or moved to another site, while it was archived.
		stored, err := txq.OperationsOfBoms(ctx, []int64{b.ID})
		if err != nil {
			return err
		}
		ops := make([]opValues, 0, len(stored))
		for _, o := range stored {
			ops = append(ops, opValues{workCenterID: o.WorkCenterID, groupID: o.WorkCenterGroupID})
		}
		if err := judgeOperations(ctx, txq, ops, nil); err != nil {
			return err
		}
		hasDefault, err := txq.VariantHasActiveDefault(ctx, store.VariantHasActiveDefaultParams{VariantID: b.VariantID, ExceptID: b.ID})
		if err != nil {
			return err
		}
		outputs, err := bomOutputsOf(ctx, txq, b.ID)
		if err != nil {
			return err
		}
		if err := walkBom(ctx, txq, b.VariantID, components, outputs, !hasDefault); err != nil {
			return err
		}
		_, err = txq.SetBomStatus(ctx, store.SetBomStatusParams{ID: b.ID, Status: statusActive, IsDefault: !hasDefault, Now: s.deps.Clock()})
		return err
	})
	if err != nil {
		return answer(err, reply)
	}
	out, err := s.bomAnswer(ctx, req.Id)
	if err != nil {
		return answer(err, reply)
	}
	return gen.ReactivateManufacturingBom200JSONResponse(out), nil
}

// MakeDefaultManufacturingBom makes an active BoM its variant's default,
// unmarking the old one in the same transaction, after the cycle and depth
// walk (M14a).
func (s *server) MakeDefaultManufacturingBom(ctx context.Context, req gen.MakeDefaultManufacturingBomRequestObject) (gen.MakeDefaultManufacturingBomResponseObject, error) {
	reply := answers[gen.MakeDefaultManufacturingBomResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.MakeDefaultManufacturingBomResponseObject {
			return gen.MakeDefaultManufacturingBom400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.MakeDefaultManufacturingBomResponseObject {
			return gen.MakeDefaultManufacturingBom404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.MakeDefaultManufacturingBomResponseObject {
			return gen.MakeDefaultManufacturingBom409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.MakeDefaultManufacturingBomResponseObject {
			return gen.MakeDefaultManufacturingBom503ApplicationProblemPlusJSONResponse(p)
		},
	}
	err := s.runLifecycle(ctx, req.Id, req.Body.Revision, func(ctx context.Context, txq *store.Queries, b store.ManufacturingBom, components []int32, locked map[int32]contracts.VariantEntry) error {
		if b.Status != statusActive {
			return notActive()
		}
		if b.IsDefault {
			return nil
		}
		if err := judgeStoredGoods(b, components, locked); err != nil {
			return err
		}
		outputs, err := bomOutputsOf(ctx, txq, b.ID)
		if err != nil {
			return err
		}
		if err := walkBom(ctx, txq, b.VariantID, components, outputs, true); err != nil {
			return err
		}
		now := s.deps.Clock()
		if _, err := txq.UnsetVariantDefault(ctx, store.UnsetVariantDefaultParams{VariantID: b.VariantID, ExceptID: b.ID, Now: now}); err != nil {
			return err
		}
		_, err = txq.SetBomDefault(ctx, store.SetBomDefaultParams{ID: b.ID, Now: now})
		return err
	})
	if err != nil {
		return answer(err, reply)
	}
	out, err := s.bomAnswer(ctx, req.Id)
	if err != nil {
		return answer(err, reply)
	}
	return gen.MakeDefaultManufacturingBom200JSONResponse(out), nil
}

// operationKey is an operation's key as the editor reads it back: its id.
func operationKey(id int64) string { return strconv.FormatInt(id, 10) }
