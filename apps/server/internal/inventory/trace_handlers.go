package inventory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/line"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/fontrepository"
	"github.com/johnfercher/maroto/v2/pkg/props"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// The trace's answers (manufacturing phase 2 M89–M92): the tree as JSON, CSV
// and PDF, and the lot page's block with its downstream list. Who received
// a lot (M90) is Orders' to say: an issue posted for an Orders delivery
// carries the delivery, its order and its customer through
// contracts.DeliveryTrace, which refuses a caller without orders:access —
// the trace then shows the delivery as its movement alone. The customer's
// name comes from the customer directory, shown only to a caller who also
// holds customers:view, which Inventory checks: the directory has no gate of
// its own (rule 12).

// permCustomersView is Customers' read, which gates the customers' names on
// a trace (M90).
const permCustomersView = "customers:view"

// invalidTraceTitle is the title of a trace's field errors.
const invalidTraceTitle = "Invalid trace"

// traceRead is a trace read and resolved: the walked graph, the deliveries
// its issues were for and their customers' names.
type traceRead struct {
	opts          traceOptions
	direction     gen.InventoryTraceDirection
	components    gen.InventoryTraceComponents
	g             traceGraph
	deliveries    map[int64]contracts.TracedDelivery
	deliveryState gen.InventoryTraceDeliveries
	customers     map[int32]string
	names         bool
	generatedAt   time.Time
}

// traceParams judges a trace's parameters, the defaults filled in.
func traceParams(lotID int64, direction *gen.InventoryTraceDirection, components *gen.InventoryTraceComponents, maxDepth *int32) (traceRead, fieldErrors) {
	var f fieldErrors
	r := traceRead{direction: gen.Usage, components: gen.Tracked, opts: traceOptions{root: lotID, maxDepth: maxTraceDepth}}
	if direction != nil {
		if !direction.Valid() {
			f.add("direction", "The direction is usage or origin")
		}
		r.direction = *direction
	}
	if components != nil {
		if !components.Valid() {
			f.add("components", "The components are tracked or all")
		}
		r.components = *components
	}
	if maxDepth != nil {
		if *maxDepth < 1 || *maxDepth > maxTraceDepth {
			f.add("maxDepth", fmt.Sprintf("The depth is 1 to %d", maxTraceDepth))
		}
		r.opts.maxDepth = int(*maxDepth)
	}
	r.opts.usage = r.direction == gen.Usage
	r.opts.components = r.components == gen.All
	return r, f
}

// deliveryOf is the delivery id an issue's movement names, 0 when it is not
// an Orders delivery's.
func deliveryOf(m *traceMovement) int64 {
	if m == nil || m.kind != movementKindIssue || m.source == nil || m.source.module != "orders" || m.source.kind != "delivery" {
		return 0
	}
	id, err := strconv.ParseInt(m.source.id, 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

// readTrace walks the graph in one snapshot on the pool, then reads the
// deliveries and the customers outside it (M90).
func (s *server) readTrace(ctx context.Context, r traceRead) (traceRead, error) {
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		r.g, err = walkTrace(ctx, q, r.opts)
		return err
	})
	if err != nil {
		return r, err
	}
	r.generatedAt = s.deps.Clock()
	var ids []int64
	for _, n := range r.g.nodes {
		if id := deliveryOf(n.movement); id != 0 {
			ids = append(ids, id)
		}
	}
	ids = slices.Compact(slices.Sorted(slices.Values(ids)))
	r.deliveries = map[int64]contracts.TracedDelivery{}
	r.deliveryState = gen.Shown
	delivered, err := s.tracedDeliveries(ctx, ids)
	switch {
	case errors.Is(err, errNoDeliveryTrace):
		r.deliveryState = gen.Unavailable
	case errors.Is(err, contracts.ErrForbidden):
		r.deliveryState = gen.Hidden
	case err != nil:
		return r, fmt.Errorf("inventory: read the traced deliveries: %w", err)
	}
	customerIDs := make([]int32, 0, len(delivered))
	for _, d := range delivered {
		r.deliveries[d.DeliveryID] = d
		customerIDs = append(customerIDs, d.CustomerID)
	}
	r.customers = map[int32]string{}
	if r.deliveryState == gen.Shown && s.deps.Directory != nil && s.has(ctx, permCustomersView) {
		r.names = true
		entries, err := s.directoryCustomers(ctx, slices.Compact(slices.Sorted(slices.Values(customerIDs))))
		if err != nil {
			return r, fmt.Errorf("inventory: read the traced customers: %w", err)
		}
		for _, c := range entries {
			r.customers[c.ID] = c.Name
		}
	}
	return r, nil
}

// wireSource is a movement source as the contract answers it.
func wireSource(src *movementSource) *gen.InventorySourceRef {
	if src == nil {
		return nil
	}
	return &gen.InventorySourceRef{Module: src.module, Kind: src.kind, Id: src.id, LineId: src.lineID}
}

// wire is the trace as the contract answers it.
func (r traceRead) wire() gen.InventoryTrace {
	out := gen.InventoryTrace{
		LotId: r.opts.root, Direction: r.direction, Components: r.components, MaxDepth: int32(r.opts.maxDepth),
		GeneratedAt: r.generatedAt, Deliveries: r.deliveryState, CustomerNames: r.names,
		Nodes: make([]gen.InventoryTraceNode, 0, len(r.g.nodes)),
	}
	for _, n := range r.g.nodes {
		w := gen.InventoryTraceNode{
			Key: n.key, Depth: int32(n.depth), Kind: gen.InventoryTraceNodeKind(n.kind), DepthLimit: n.depthLimit,
			Variant: gen.InventoryVariantRef{Id: n.variant.id, Sku: n.variant.sku, ProductName: n.variant.name, Unit: n.variant.unit},
		}
		if n.parent != "" {
			w.ParentKey = ptr(n.parent)
		}
		if n.seenAt != "" {
			w.SeenAt = ptr(n.seenAt)
		}
		if n.quantity != nil {
			w.Quantity = ptr(wireQuantity(n.quantity))
		}
		if n.kind == traceLot {
			l := r.g.lots[n.lotID]
			w.Lot = &gen.InventoryTraceLot{
				Id: l.ID, Number: l.Number, Serial: l.Serial, Status: gen.InventoryLotStatus(l.Status),
				Expiry: wireDate(dateOrNil(l.Expiry)), BestBefore: wireDate(dateOrNil(l.BestBefore)),
				ProductionDate: wireDate(dateOrNil(l.ProductionDate)), SupplierLot: l.SupplierLot,
				QuantityIn: wireQuantity(ratOr0(r.g.quantityIn[n.lotID])), OnHand: wireQuantity(ratOr0(r.g.onHand[n.lotID])),
			}
		}
		if e := n.edge; e != nil {
			w.Edge = &gen.InventoryTraceEdge{MovementId: e.movementID, PostedAt: e.postedAt, Consumed: wireQuantity(e.consumed),
				Produced: wireQuantity(e.produced), Source: wireSource(e.source)}
		}
		if m := n.movement; m != nil {
			w.Movement = &gen.InventoryTraceMovement{Id: m.id, Kind: gen.InventoryMovementKind(m.kind), PostedAt: m.postedAt,
				Reason: m.reason, SupplierName: m.supplier, SupplierReference: m.supplierRef, ToSiteName: m.toSite, Source: wireSource(m.source)}
			if d, ok := r.deliveries[deliveryOf(m)]; ok {
				w.Delivery = &gen.InventoryTraceDelivery{DeliveryId: d.DeliveryID, DeliveryNumber: d.DeliveryNumber, OrderId: d.OrderID,
					OrderNumber: d.OrderNumber, CustomerId: d.CustomerID, PostedAt: d.PostedAt}
				if name, ok := r.customers[d.CustomerID]; ok {
					w.Delivery.CustomerName = ptr(name)
				}
			}
		}
		out.Nodes = append(out.Nodes, w)
	}
	return out
}

// ratOr0 is v, 0 when nil.
func ratOr0(v *big.Rat) *big.Rat {
	if v == nil {
		return new(big.Rat)
	}
	return v
}

// GetInventoryTrace Trace a lot
// (GET /api/v1/inventory/trace)
func (s *server) GetInventoryTrace(ctx context.Context, req gen.GetInventoryTraceRequestObject) (gen.GetInventoryTraceResponseObject, error) {
	p := req.Params
	r, f := traceParams(p.LotId, p.Direction, p.Components, p.MaxDepth)
	if f.failed() {
		return gen.GetInventoryTrace400ApplicationProblemPlusJSONResponse(invalid(invalidTraceTitle, f)), nil
	}
	r, err := s.readTrace(ctx, r)
	if status, pr, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.GetInventoryTrace404Response{}, nil
		case http.StatusBadRequest:
			return gen.GetInventoryTrace400ApplicationProblemPlusJSONResponse(pr), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.GetInventoryTrace200JSONResponse(r.wire()), nil
}

// traceColumns are the CSV's header (M91): one row per node.
var traceColumns = []string{"key", "depth", "kind", "lot", "sku", "product", "quantity", "unit", "date", "document", "party", "note"}

// traceTextColumns are the CSV's columns of text, the ones the formula guard
// judges.
var traceTextColumns = map[int]bool{2: true, 3: true, 4: true, 5: true, 7: true, 9: true, 10: true, 11: true}

// traceRow is one node as the CSV and the PDF print it.
type traceRow struct {
	key, kind, lot, sku, product, quantity, unit, date, document, party, note string
	depth                                                                     int
}

// rows is the trace's nodes as the exports print them; zone is the
// business zone their dates are in.
func (r traceRead) rows(zone *time.Location) []traceRow {
	out := make([]traceRow, 0, len(r.g.nodes))
	day := func(t time.Time) string { return t.In(zone).Format(time.DateOnly) }
	for _, n := range r.g.nodes {
		row := traceRow{key: n.key, depth: n.depth, kind: n.kind, sku: n.variant.sku, product: n.variant.name, unit: n.variant.unit}
		if n.lotID != 0 {
			row.lot = r.g.lots[n.lotID].Number
		}
		switch {
		case n.kind == traceLot && n.edge == nil:
			row.quantity = decimalText(ratOr0(r.g.quantityIn[n.lotID]))
			row.date = day(r.g.lots[n.lotID].ReceivedAt)
		case n.kind == traceLot && r.opts.usage:
			row.quantity = decimalText(n.edge.produced)
		case n.kind == traceLot:
			row.quantity = decimalText(n.edge.consumed)
		case n.quantity != nil:
			row.quantity = decimalText(n.quantity)
		}
		if n.edge != nil {
			row.date = day(n.edge.postedAt)
			row.document = productionText(n.edge.source, n.edge.movementID)
		}
		if m := n.movement; m != nil {
			row.date = day(m.postedAt)
			row.document, row.party = r.endText(m)
		}
		switch {
		case n.seenAt != "":
			row.note = "Already shown at " + n.seenAt
		case n.depthLimit:
			row.note = "Not traced further: depth limit"
		}
		out = append(out, row)
	}
	return out
}

// productionText names a production by its document: the production and
// the manufacturing order it was for, else the movement.
func productionText(src *movementSource, movementID int64) string {
	if src != nil && src.module == "manufacturing" && src.kind == "production" {
		if src.lineID != nil {
			return fmt.Sprintf("Production %s, manufacturing order %s", src.id, *src.lineID)
		}
		return "Production " + src.id
	}
	return fmt.Sprintf("Production, movement %d", movementID)
}

// endText is an end's document and party as the exports print them: the
// delivery and its customer — by name only under customers:view, else by
// id — the receipt and its supplier, or the transfer out and the site it
// was sent to.
func (r traceRead) endText(m *traceMovement) (document, party string) {
	switch m.kind {
	case movementKindIssue:
		if d, ok := r.deliveries[deliveryOf(m)]; ok {
			party = fmt.Sprintf("Customer #%d", d.CustomerID)
			if name, ok := r.customers[d.CustomerID]; ok {
				party = name
			}
			return fmt.Sprintf("Delivery %d, order %d", d.DeliveryNumber, d.OrderNumber), party
		}
		if deliveryOf(m) != 0 {
			return fmt.Sprintf("Delivery, movement %d", m.id), ""
		}
		return fmt.Sprintf("Issue, movement %d", m.id), ""
	case string(gen.Receipt):
		document = fmt.Sprintf("Receipt, movement %d", m.id)
		if m.supplierRef != nil {
			document += ", ref " + *m.supplierRef
		}
		return document, deref(m.supplier)
	case movementKindProduction:
		return productionText(m.source, m.id), ""
	case movementKindUnbuild:
		if m.source != nil {
			return fmt.Sprintf("Unbuild %s, movement %d", m.source.id, m.id), ""
		}
		return fmt.Sprintf("Unbuild, movement %d", m.id), ""
	case string(gen.Adjustment):
		if m.reason != nil {
			return fmt.Sprintf("Adjustment (%s), movement %d", *m.reason, m.id), ""
		}
		return fmt.Sprintf("Adjustment, movement %d", m.id), ""
	case string(gen.Count):
		return fmt.Sprintf("Count, movement %d", m.id), ""
	case string(gen.TransferOut):
		if m.toSite != nil {
			party = "Sent to " + *m.toSite
		}
		if m.source != nil {
			return fmt.Sprintf("Transfer %s, movement %d", m.source.id, m.id), party
		}
		return fmt.Sprintf("Transfer out, movement %d", m.id), party
	}
	return fmt.Sprintf("Movement %d", m.id), ""
}

// traceFileName is an export's file name: trace-<lot>-<direction>.<ext>.
func (r traceRead) traceFileName(ext string) string {
	number := strconv.FormatInt(r.opts.root, 10)
	if l, ok := r.g.lots[r.opts.root]; ok {
		number = strings.NewReplacer("/", "-", ".", "-").Replace(l.Number)
	}
	return fmt.Sprintf("trace-%s-%s.%s", number, r.direction, ext)
}

// traceCSV is the trace as an attachment.
type traceCSV csvDownload

func (d traceCSV) VisitExportInventoryTraceResponse(w http.ResponseWriter) error {
	return csvDownload(d).VisitExportInventoryLowStockResponse(w)
}

// exportTrace reads a trace for an export, with the zone its dates are
// printed in; a refusal is answered as its problem with ok false.
func (s *server) exportTrace(ctx context.Context, lotID int64, direction *gen.InventoryTraceDirection, components *gen.InventoryTraceComponents,
	maxDepth *int32) (traceRead, *time.Location, error) {
	r, f := traceParams(lotID, direction, components, maxDepth)
	if f.failed() {
		return r, nil, refuseInvalid(invalidTraceTitle, f)
	}
	zone, err := s.businessZone(ctx)
	if err != nil {
		return r, nil, err
	}
	r, err = s.readTrace(ctx, r)
	return r, zone, err
}

// ExportInventoryTrace Export a lot's trace as CSV
// (GET /api/v1/inventory/trace.csv)
func (s *server) ExportInventoryTrace(ctx context.Context, req gen.ExportInventoryTraceRequestObject) (gen.ExportInventoryTraceResponseObject, error) {
	p := req.Params
	r, zone, err := s.exportTrace(ctx, p.LotId, p.Direction, p.Components, p.MaxDepth)
	if status, pr, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.ExportInventoryTrace404Response{}, nil
		case http.StatusBadRequest:
			return gen.ExportInventoryTrace400ApplicationProblemPlusJSONResponse(pr), nil
		}
	}
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	writeCSVRow(&b, traceColumns, nil)
	for _, row := range r.rows(zone) {
		writeCSVRow(&b, []string{row.key, strconv.Itoa(row.depth), row.kind, row.lot, row.sku, row.product,
			strings.Replace(row.quantity, ".", ",", 1), row.unit, row.date, row.document, row.party, row.note}, traceTextColumns)
	}
	return traceCSV{body: b.Bytes(), fileName: r.traceFileName("csv")}, nil
}

// traceRowsBuilt is the tests' window on what a trace PDF prints: every
// row's cells, in order (SetTracePDFHook).
var traceRowsBuilt atomic.Pointer[func(header []string, rows [][]string)]

// traceKindText is a node's kind as the PDF names it, in Norwegian and
// English.
var traceKindText = map[string]string{
	traceLot: "Parti / Lot", traceComponent: "Komponent / Component", string(gen.Receipt): "Mottak / Receipt",
	movementKindIssue: "Utlevering / Delivery", string(gen.Adjustment): "Justering / Adjustment", string(gen.Count): "Telling / Count",
	string(gen.TransferOut): "Overføring / Transfer", movementKindProduction: "Produksjon / Production", movementKindUnbuild: "Demontering / Unbuild",
}

// renderTracePDF is the trace as a PDF in the house style — the labels'
// fonts, maroto's layout — stating when it was made, by whom and in which
// direction (M91).
func renderTracePDF(r traceRead, rows []traceRow, zone *time.Location, by string) ([]byte, error) {
	fonts, err := fontrepository.New().
		AddUTF8FontFromBytes(labelFontFamily, fontstyle.Normal, notoSansRegular).
		AddUTF8FontFromBytes(labelFontFamily, fontstyle.Bold, notoSansBold).
		Load()
	if err != nil {
		return nil, fmt.Errorf("inventory: load the trace's font: %w", err)
	}
	root := r.g.lots[r.opts.root]
	cfg := config.NewBuilder().
		WithCustomFonts(fonts).
		WithDefaultFont(&props.Font{Family: labelFontFamily, Size: 8}).
		WithSequentialMode().
		WithLeftMargin(12).WithRightMargin(12).WithTopMargin(12).
		WithCreationDate(labelDate).
		WithTitle("Sporing / Trace "+root.Number, true).
		Build()
	doc := maroto.New(cfg)
	bold := props.Text{Style: fontstyle.Bold}
	direction := "Hvor gikk det / Where it went (usage)"
	if !r.opts.usage {
		direction = "Hvor kom det fra / Where it came from (origin)"
	}
	doc.AddRows(text.NewRow(10, "Sporing / Trace", props.Text{Style: fontstyle.Bold, Size: 16}))
	meta := [][2]string{
		{"Parti / Lot", root.Number + " · " + root.Sku + " · " + root.ProductName},
		{"Retning / Direction", direction},
		{"Laget / Made", r.generatedAt.In(zone).Format("2006-01-02 15:04") + ", " + by},
	}
	switch r.deliveryState {
	case gen.Hidden:
		meta = append(meta, [2]string{"Leveranser / Deliveries", "Uten tilgang til ordre / Without access to orders: movements only"})
	case gen.Unavailable:
		meta = append(meta, [2]string{"Leveranser / Deliveries", "Ordre er ikke i bruk / Orders is not in use: movements only"})
	}
	for _, kv := range meta {
		doc.AddAutoRow(text.NewCol(3, kv[0], bold), text.NewCol(9, kv[1]))
	}
	doc.AddRows(line.NewRow(4))
	sizes := []int{3, 3, 2, 1, 3}
	header := []string{"Parti / Lot", "Vare / Item", "Antall / Quantity", "Dato / Date", "Dokument / Document"}
	cellsOf := make([][]string, 0, len(rows))
	for _, row := range rows {
		first := strings.Repeat("   ", row.depth)
		if row.kind == traceLot {
			first += row.lot
		} else {
			first += traceKindText[row.kind]
		}
		if row.note != "" {
			first += " (" + row.note + ")"
		}
		document := row.document
		if row.party != "" {
			document += " · " + row.party
		}
		cellsOf = append(cellsOf, []string{first, strings.TrimSpace(row.sku + " " + row.product), strings.TrimSpace(row.quantity + " " + row.unit), row.date, document})
	}
	if hook := traceRowsBuilt.Load(); hook != nil {
		(*hook)(header, cellsOf)
	}
	add := func(cells []string, style props.Text) {
		cols := make([]core.Col, 0, len(cells))
		for i, c := range cells {
			s := style
			if i == 2 {
				s.Align = align.Right
			}
			cols = append(cols, text.NewCol(sizes[i], c, s))
		}
		doc.AddAutoRow(cols...)
	}
	add(header, bold)
	for _, cells := range cellsOf {
		add(cells, props.Text{})
	}
	out, err := doc.Generate()
	if err != nil {
		return nil, fmt.Errorf("inventory: render the trace of lot %d: %w", r.opts.root, err)
	}
	return out.GetBytes(), nil
}

// tracePDF is the PDF answer with its download headers.
type tracePDF struct {
	body        gen.PrintInventoryTrace200ApplicationpdfResponse
	disposition string
}

func (t tracePDF) VisitPrintInventoryTraceResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", t.disposition)
	return t.body.VisitPrintInventoryTraceResponse(w)
}

// PrintInventoryTrace Print a lot's trace
// (GET /api/v1/inventory/trace.pdf)
func (s *server) PrintInventoryTrace(ctx context.Context, req gen.PrintInventoryTraceRequestObject) (gen.PrintInventoryTraceResponseObject, error) {
	p := req.Params
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	r, zone, err := s.exportTrace(ctx, p.LotId, p.Direction, p.Components, p.MaxDepth)
	if status, pr, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.PrintInventoryTrace404Response{}, nil
		case http.StatusBadRequest:
			return gen.PrintInventoryTrace400ApplicationProblemPlusJSONResponse(pr), nil
		}
	}
	if err != nil {
		return nil, err
	}
	body, err := renderTracePDF(r, r.rows(zone), zone, who.Display)
	if err != nil {
		return nil, err
	}
	return tracePDF{
		body:        gen.PrintInventoryTrace200ApplicationpdfResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))},
		disposition: fmt.Sprintf("attachment; filename=%q", r.traceFileName("pdf")),
	}, nil
}

// The causes a block of the lot page takes (M92).
var blockCauses = []string{"recall", "complaint", "other"}

// The block's own refusal and bounds (M92).
const (
	codeTraceChanged = "trace_changed"
	// blockAttempts is how many times a block runs when it loses a race to a
	// deadlock: once more.
	blockAttempts = 2
)

// blockRelocks is how many times a block locks the lots its walk found
// again, when lots were made from them while it locked them; past it the
// block is 409 trace_changed. A variable for the tests.
var blockRelocks atomic.Int32

func init() { blockRelocks.Store(3) }

// blockWalked is the tests' window on a block between its walks: called
// with the round after each walk — 0 for the first, read before any lock.
var blockWalked atomic.Pointer[func(round int)]

// errTraceChanged is the 409 of a block whose lots kept being made from
// while it locked them.
func errTraceChanged() error {
	return refuse(problem(http.StatusConflict, codeTraceChanged, "The trace changed",
		"Lots were being made from this lot while it was blocked, so nothing was blocked. Try again."))
}

// BlockInventoryLot Block a lot and the lots made from it
// (POST /api/v1/inventory/lots/{id}/block)
//
// Without downstream, the lot FOR NO KEY UPDATE. With it (M92, M1/M3 of the
// review): the usage walk, with no depth limit, read before any lock — the
// dialog's summary was read on the pool and may be stale — then every lot
// it found, the root among them, FOR NO KEY UPDATE in id order, the lock
// every production that consumes or makes one of them takes; then the walk
// again under those locks. A lot made since — by a production that held its
// locks first — is locked too and the walk read once more, a bounded number
// of times (blockRelocks); past it 409 trace_changed. One block hold of the
// lot page on the lot and on every lot made from it that still holds stock,
// each with its own event naming the lot blocked. A walk beyond the bound
// blocks nothing; a deadlock runs the block once more.
func (s *server) BlockInventoryLot(ctx context.Context, req gen.BlockInventoryLotRequestObject) (gen.BlockInventoryLotResponseObject, error) {
	if req.Body == nil {
		return gen.BlockInventoryLot400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidLotTitle, "The request has no body.")), nil
	}
	in := *req.Body
	f := s.unknownProperties(ctx, lotBlockInputSchema)
	if !slices.Contains(blockCauses, string(in.Cause)) {
		f.add("cause", "The cause is recall, complaint or other")
	}
	note := optionalText(&f, "note", in.Note, 1000)
	if f.failed() {
		return gen.BlockInventoryLot400ApplicationProblemPlusJSONResponse(invalid(invalidLotTitle, f)), nil
	}
	downstream := in.Downstream == nil || *in.Downstream
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var blocked []gen.InventoryLotBlocked
	err = db.RetrySerializable(ctx, blockAttempts, func() error {
		return s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
			var ids []int64
			blocked = nil
			if !downstream {
				root, err := lockLotForChange(ctx, txq, req.Id)
				if err != nil {
					return err
				}
				ids = []int64{root.ID}
				blocked = []gen.InventoryLotBlocked{{Id: root.ID, Number: root.Number, Sku: root.Sku, ProductName: root.ProductName, Root: true}}
			} else {
				lots, below, err := s.lockBlockWalk(ctx, txq, req.Id)
				if err != nil {
					return err
				}
				ids = append([]int64{req.Id}, below...)
				for _, id := range ids {
					l := lots[id]
					blocked = append(blocked, gen.InventoryLotBlocked{Id: l.ID, Number: l.Number, Sku: l.Sku, ProductName: l.ProductName, Root: id == req.Id})
				}
			}
			holds := make([]contracts.LotHold, 0, len(ids))
			for _, id := range ids {
				holds = append(holds, contracts.LotHold{LotID: id, Kind: "block", Cause: string(in.Cause), Note: deref(note),
					By: contracts.LotActor{UserID: who.UserID, Display: who.Display}})
			}
			return s.holdLots(ctx, tx, txq, lotPageSource(req.Id), holds, ids)
		})
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.BlockInventoryLot404Response{}, nil
		case http.StatusBadRequest:
			return gen.BlockInventoryLot400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusConflict:
			return gen.BlockInventoryLot409ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	out, err := s.lotPage(ctx, req.Id)
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.BlockInventoryLot503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.BlockInventoryLot200JSONResponse(gen.InventoryLotBlock{Lot: out, Blocked: blocked}), nil
}

// lockBlockWalk is the block's walk and its locks (see BlockInventoryLot):
// the lots locked, by id, and those below root that still hold stock. An
// unknown root is the 404 refusal.
func (s *server) lockBlockWalk(ctx context.Context, txq *store.Queries, root int64) (map[int64]store.InventoryLot, []int64, error) {
	found, below, err := blockWalk(ctx, txq, root)
	if err != nil {
		return nil, nil, err
	}
	walked := func(round int) {
		if hook := blockWalked.Load(); hook != nil {
			(*hook)(round)
		}
	}
	walked(0)
	all := func(ids []int64) map[int64]bool {
		m := make(map[int64]bool, len(ids))
		for _, id := range ids {
			m[id] = true
		}
		return m
	}
	lots, err := lockLots(ctx, txq, found, all(found))
	if err != nil {
		return nil, nil, err
	}
	if _, ok := lots[root]; !ok {
		return nil, nil, notFound()
	}
	for round := 1; ; round++ {
		found, below, err = blockWalk(ctx, txq, root)
		if err != nil {
			return nil, nil, err
		}
		walked(round)
		var fresh []int64
		for _, id := range found {
			if _, ok := lots[id]; !ok {
				fresh = append(fresh, id)
			}
		}
		if len(fresh) == 0 {
			return lots, below, nil
		}
		if round > int(blockRelocks.Load()) {
			return nil, nil, errTraceChanged()
		}
		more, err := lockLots(ctx, txq, fresh, all(fresh))
		if err != nil {
			return nil, nil, err
		}
		for id, l := range more {
			lots[id] = l
		}
	}
}
