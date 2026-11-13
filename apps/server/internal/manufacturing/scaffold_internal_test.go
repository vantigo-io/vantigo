package manufacturing

// The scaffold's own rules, held before any operation is built: the closed
// request schemas and the strict-body judgement over them, the refusal
// helpers and the contract's list of codes, the transaction marks the
// accessors refuse a call out of the module under, and the variant holder.

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

func loadContract(t *testing.T) *server {
	t.Helper()
	doc, err := openapi.Load(context.Background(), moduleName)
	if err != nil {
		t.Fatal(err)
	}
	return &server{deps: module.Deps{Doc: doc}}
}

func TestCheckClosedSchemas_TheContractClosesEveryRequestSchema(t *testing.T) {
	s := loadContract(t)
	if err := checkClosedSchemas(s.deps.Doc); err != nil {
		t.Fatalf("checkClosedSchemas(the contract) = %v, want nil", err)
	}
	opened := true
	s.deps.Doc.Components.Schemas[bomLineInputSchema].Value.AdditionalProperties.Has = &opened
	if err := checkClosedSchemas(s.deps.Doc); err == nil || !strings.Contains(err.Error(), bomLineInputSchema) {
		t.Errorf("checkClosedSchemas(an opened %s) = %v, want it named", bomLineInputSchema, err)
	}
	if err := checkClosedSchemas(nil); err == nil {
		t.Error("checkClosedSchemas(nil) = nil, want a refusal")
	}
}

func TestUnknownProperties_NamesEveryKeyTheContractDoesNot(t *testing.T) {
	s := loadContract(t)
	for _, tc := range []struct {
		schema, body string
		want         []string
	}{
		{bomInputSchema, `{"variantId":1,"outputQuantity":"1","operations":[{"key":"a","bogus":1}],"lines":[{"variantId":2,"quantity":"1"},{"variantId":3,"quantity":"1","scrap":"2"}],"extra":true}`,
			[]string{"extra", "lines[1].scrap", "operations[0].bogus"}},
		{bomUpdateSchema, `{"revision":1,"variantId":4,"outputQuantity":"1","operations":[],"lines":[]}`, []string{"variantId"}},
		{workCenterInputSchema, `{"code":"A","pattern":{"monday":[{"start":"07:00","end":"15:00","break":"x"}],"funday":[]}}`,
			[]string{"pattern.funday", "pattern.monday[0].break"}},
		{workCenterUpdateSchema, `{"code":"A","name":"B","siteId":1,"revision":2}`, nil},
		{closureInputSchema, `[1,2]`, nil},
	} {
		ctx := context.WithValue(context.Background(), rawBodyKey{}, []byte(tc.body))
		f := s.unknownProperties(ctx, tc.schema)
		var got []string
		for field := range f.errs {
			got = append(got, field)
		}
		slices.Sort(got)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s %s: unknown = %v, want %v", tc.schema, tc.body, got, tc.want)
		}
	}
}

func TestHasProperty_TellsAbsentFromNull(t *testing.T) {
	ctx := context.WithValue(context.Background(), rawBodyKey{}, []byte(`{"machineCostPerHour":null}`))
	if !hasProperty(ctx, "machineCostPerHour") || hasProperty(ctx, "labourCostPerHour") {
		t.Error("hasProperty: want a null property present and a missing one absent")
	}
	if hasProperty(context.Background(), "machineCostPerHour") {
		t.Error("hasProperty without a body: want false")
	}
}

func TestProblemDescription_NamesEveryCode(t *testing.T) {
	s := loadContract(t)
	desc := s.deps.Doc.Components.Schemas["ManufacturingProblem"].Value.Description
	for _, code := range refusalCodes {
		if !strings.Contains(desc, code) {
			t.Errorf("ManufacturingProblem's description does not name %s", code)
		}
	}
}

// answered is what answer turns a refusal into in these tests: the status
// and the code.
type answered struct {
	status int
	code   string
}

func codeOf(p gen.ManufacturingProblem) string {
	if p.Code == nil {
		return ""
	}
	return *p.Code
}

var every = answers[answered]{
	badRequest:  func(p gen.ManufacturingProblem) answered { return answered{http.StatusBadRequest, codeOf(p)} },
	forbidden:   func(p gen.ManufacturingProblem) answered { return answered{http.StatusForbidden, codeOf(p)} },
	notFound:    func() answered { return answered{status: http.StatusNotFound} },
	conflict:    func(p gen.ManufacturingProblem) answered { return answered{http.StatusConflict, codeOf(p)} },
	unavailable: func(p gen.ManufacturingProblem) answered { return answered{http.StatusServiceUnavailable, codeOf(p)} },
}

func TestAnswer_TurnsEveryRefusalIntoItsResponse(t *testing.T) {
	var f fieldErrors
	f.add("name", "")
	if f.failed() {
		t.Error("an empty message recorded a failure")
	}
	f.add("name", "Required.")
	f.addCoded("siteId", codeSiteUnknown, "Not an active site.")
	f.addCoded("code", codeWorkCenterCodeTaken, "Taken.")
	if !f.failed() || f.code != codeSiteUnknown || len(f.errs) != 3 {
		t.Errorf("fieldErrors = %+v, want three fields under the first code", f)
	}

	for _, tc := range []struct {
		err  error
		want answered
	}{
		{notFound(), answered{status: http.StatusNotFound}},
		{refuseInvalid("Invalid work center", f), answered{http.StatusBadRequest, codeSiteUnknown}},
		{refuseField("Invalid closure", "toDate", "", "Before fromDate."), answered{http.StatusBadRequest, ""}},
		{refuseField("Invalid closure", "closures", codeClosuresTooMany, "At most 100."), answered{http.StatusBadRequest, codeClosuresTooMany}},
		{refuseLines("Invalid BoM", []lineFailure{{1, codeVariantNotGoods, "SKU-1 is a service"}, {3, codeQuantityInvalid, "0"}}),
			answered{http.StatusBadRequest, codeVariantNotGoods}},
		{refuse(cycleProblem([]gen.ManufacturingProblemPathStep{{VariantId: 1, Sku: "A"}})), answered{http.StatusBadRequest, codeBomCycle}},
		{conflict(codeWorkCenterInUse, "In use", ""), answered{http.StatusConflict, codeWorkCenterInUse}},
		{revisionConflict("BoM", 3, 2), answered{http.StatusConflict, codeRevisionConflict}},
		{costsViewRequired(), answered{http.StatusForbidden, codeCostsViewRequired}},
		{errDependencyUnavailable, answered{http.StatusServiceUnavailable, codeDependencyUnavailable}},
	} {
		got, err := answer(tc.err, every)
		if err != nil || got != tc.want {
			t.Errorf("answer(%v) = %+v, %v; want %+v", tc.err, got, err, tc.want)
		}
	}

	p := linesProblem("Invalid BoM", []lineFailure{{0, codeVariantNotFound, "no such variant"}})
	if p.Lines == nil || len(*p.Lines) != 1 || (*p.Lines)[0].Code != codeVariantNotFound || p.Detail == nil {
		t.Errorf("linesProblem = %+v, want its one line and a detail", p)
	}

	// A status the operation does not declare is a 500, never a silent
	// mismatch; anything but a refusal is handed back as it is.
	if _, err := answer(notFound(), answers[answered]{}); err == nil {
		t.Error("answer of an undeclared 404: want an error")
	}
	plain := errors.New("boom")
	if _, err := answer(plain, every); !errors.Is(err, plain) {
		t.Errorf("answer(plain) = %v, want it handed back", err)
	}
	if !noRows(pgx.ErrNoRows) || noRows(plain) {
		t.Error("noRows: want pgx.ErrNoRows and nothing else")
	}
}

func TestCaller_OutsideARequestHoldsNothing(t *testing.T) {
	s := &server{}
	if s.has(context.Background(), permAccess) {
		t.Error("has outside a request: want false")
	}
	if id := callerID(context.Background()); id != uuid.Nil {
		t.Errorf("callerID outside a request = %s, want the zero id", id)
	}
}

// recordingStock is a contracts.Stock that records the batches Available is
// asked for and answers each variant at 1.
type recordingStock struct{ batches [][]int32 }

func (r *recordingStock) OnHand(context.Context, []int32, *int64) ([]contracts.StockQuantity, error) {
	return nil, nil
}

func (r *recordingStock) Available(_ context.Context, ids []int32, _ *int64) ([]contracts.StockQuantity, error) {
	if err := contracts.ValidateStockVariants(ids); err != nil {
		return nil, err
	}
	r.batches = append(r.batches, slices.Clone(ids))
	out := make([]contracts.StockQuantity, 0, len(ids))
	for _, id := range ids {
		out = append(out, contracts.StockQuantity{VariantID: id, Quantity: "1"})
	}
	return out, nil
}

func (r *recordingStock) Sites(context.Context) ([]contracts.StockSite, error) {
	return []contracts.StockSite{{ID: 1, Code: "MAIN", Active: true}}, nil
}

func (r *recordingStock) Locations(context.Context, []int32, int64) ([]contracts.StockLocation, error) {
	return nil, nil
}

func (r *recordingStock) SiteLocations(context.Context, int64) ([]contracts.StockSiteLocation, error) {
	return []contracts.StockSiteLocation{{ID: 1, Root: true, Active: true}}, nil
}

func (r *recordingStock) Lots(context.Context, []int32, int64) ([]contracts.StockLot, error) {
	return nil, nil
}

func (r *recordingStock) LotRefs(context.Context, []int64) ([]contracts.LotRef, error) {
	return nil, nil
}

func (r *recordingStock) Placements(context.Context, int64) ([]contracts.StockPlacement, error) {
	return nil, nil
}

func TestStockAvailable_DeduplicatesAndBatches(t *testing.T) {
	stock := &recordingStock{}
	s := &server{deps: module.Deps{Stock: stock}}
	ids := make([]int32, 0, 2600)
	for i := range int32(2500) {
		ids = append(ids, i+1)
	}
	ids = append(ids, ids[:100]...)
	got, err := s.stockAvailable(context.Background(), ids, nil)
	if err != nil {
		t.Fatal(err)
	}
	sizes := make([]int, 0, len(stock.batches))
	for _, b := range stock.batches {
		sizes = append(sizes, len(b))
	}
	if len(got) != 2500 || !slices.Equal(sizes, []int{1000, 1000, 500}) {
		t.Errorf("stockAvailable: %d variants in batches %v, want 2500 in 1000, 1000, 500", len(got), sizes)
	}
	if sites, err := s.stockSites(context.Background()); err != nil || len(sites) != 1 {
		t.Errorf("stockSites = %v, %v", sites, err)
	}
	if locations, err := s.siteLocations(context.Background(), 1); err != nil || len(locations) != 1 {
		t.Errorf("siteLocations = %v, %v", locations, err)
	}
}

// TestAccessors_RefuseInsideATransaction holds the lock rule: every call out
// of the module is refused inside one of its transactions but LockVariants,
// which runs on it — and each is heard by the hook. It is not parallel: it
// installs the package's hook.
func TestAccessors_RefuseInsideATransaction(t *testing.T) {
	var heard []string
	prev := contractCallHook
	contractCallHook = func(_ context.Context, method string) { heard = append(heard, method) }
	t.Cleanup(func() { contractCallHook = prev })

	s := &server{}
	calls := func(ctx context.Context) []error {
		_, catalog := s.catalogVariants(ctx, []int32{1})
		_, search := s.searchVariants(ctx, "a", contracts.VariantSearch{Types: []string{"Goods"}, Limit: 20})
		_, sites := s.stockSites(ctx)
		_, available := s.stockAvailable(ctx, []int32{1}, nil)
		_, locations := s.siteLocations(ctx, 1)
		return []error{catalog, search, sites, available, locations}
	}
	for i, err := range calls(context.Background()) {
		if !errors.Is(err, errDependencyUnavailable) {
			t.Errorf("call %d outside a transaction without its contract = %v, want errDependencyUnavailable", i, err)
		}
	}
	for _, ctx := range []context.Context{
		context.WithValue(context.Background(), lockedTxKey{}, true),
		context.WithValue(context.Background(), openTxKey{}, true),
	} {
		for i, err := range calls(ctx) {
			if !errors.Is(err, errCallInsideTransaction) {
				t.Errorf("call %d inside a transaction = %v, want errCallInsideTransaction", i, err)
			}
		}
		if _, err := s.lockVariants(ctx, nil, []int32{1}); !errors.Is(err, errDependencyUnavailable) {
			t.Errorf("lockVariants inside a transaction = %v, want it let through", err)
		}
	}
	if !slices.Contains(heard, lockVariantsCall) || !slices.Contains(heard, "Stock.Available") {
		t.Errorf("the hook heard %v, want every call", heard)
	}

	// Nothing asked is nothing read, inside or out.
	if got, err := s.catalogVariants(context.Background(), nil); err != nil || len(got) != 0 {
		t.Errorf("catalogVariants(none) = %v, %v", got, err)
	}
	if got, err := s.stockAvailable(context.Background(), nil, nil); err != nil || len(got) != 0 {
		t.Errorf("stockAvailable(none) = %v, %v", got, err)
	}
}

func numericOf(t *testing.T, s string) pgtype.Numeric {
	t.Helper()
	var n pgtype.Numeric
	if err := n.Scan(s); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestTransactions_MarkTheirContextAndTakeTheGraphLock runs the two
// transaction helpers on a migrated database: the locked one takes the BoM
// graph's advisory lock, the read one sees a snapshot
// — and the holder counts the active BoMs naming a variant on a caller's
// transaction.
func TestTransactions_MarkTheirContextAndTakeTheGraphLock(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	s := &server{deps: module.Deps{Pool: pool, Stock: &recordingStock{}}}
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)

	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if !inLockedTx(ctx) || !inOpenTx(ctx) {
			t.Error("withLockedTx: the context is not marked")
		}
		// The refusal of a call out from inside is TestAccessors_RefuseInsideATransaction's:
		// this test runs in parallel with the harness tests, whose suite-wide
		// hook would record a deliberate one here as theirs.
		if err := txq.LockBomGraph(ctx); err != nil {
			return err
		}
		if _, err := txq.InsertBom(ctx, store.InsertBomParams{VariantID: 7, OutputQuantity: numericOf(t, "1"), IsDefault: true, Now: now}); err != nil {
			return err
		}
		other, err := txq.InsertBom(ctx, store.InsertBomParams{VariantID: 8, OutputQuantity: numericOf(t, "2.5"), Now: now})
		if err != nil {
			return err
		}
		if _, err := txq.InsertBomLine(ctx, store.InsertBomLineParams{BomID: other.ID, VariantID: 9, Quantity: numericOf(t, "3"), ScrapPercent: numericOf(t, "0")}); err != nil {
			return err
		}
		archived, err := txq.InsertBom(ctx, store.InsertBomParams{VariantID: 11, OutputQuantity: numericOf(t, "1"), Now: now})
		if err != nil {
			return err
		}
		if _, err := txq.SetBomStatus(ctx, store.SetBomStatusParams{ID: archived.ID, Status: "archived", Now: now}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("withLockedTx: %v", err)
	}

	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if !inOpenTx(ctx) || inLockedTx(ctx) {
			t.Error("withReadTx: the context is not marked as a read")
		}
		stats, err := q.Stats(ctx)
		if err != nil {
			return err
		}
		if stats.ActiveBoms != 2 || stats.ActiveWorkCenters != 0 {
			t.Errorf("Stats = %+v, want 2 active BoMs and no work center", stats)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("withReadTx: %v", err)
	}

	holder := newVariantHolder(s.deps)
	for _, tc := range []struct {
		variant int32
		purpose contracts.VariantHoldPurpose
		held    bool
	}{
		{7, contracts.VariantHoldDelete, true},       // the product of an active BoM
		{9, contracts.VariantHoldTypeChange, true},   // a component of one
		{11, contracts.VariantHoldDelete, false},     // only an archived BoM's
		{12, contracts.VariantHoldTypeChange, false}, // no BoM's
		{7, contracts.VariantHoldPurpose("rename"), false},
	} {
		var hold *contracts.VariantHold
		err := db.WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			var err error
			hold, err = holder.VariantHeld(ctx, tx, tc.variant, tc.purpose)
			return err
		})
		if err != nil {
			t.Fatalf("VariantHeld(%d, %s): %v", tc.variant, tc.purpose, err)
		}
		if (hold != nil) != tc.held {
			t.Errorf("VariantHeld(%d, %s) = %+v, want held %v", tc.variant, tc.purpose, hold, tc.held)
		}
		if hold != nil && (hold.Kind != contracts.VariantHoldKindOpenDocuments || hold.Documents != 1) {
			t.Errorf("VariantHeld(%d) = %+v, want one open document", tc.variant, hold)
		}
	}
}
