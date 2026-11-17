package purchasing

// Purchase orders (P15–P18, P23): the list, one order, a draft's create and
// delete, and the save of an order's header and lines whole — a draft
// freely, a sent order within P18's limits with every change in the change
// log.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// The states of an order (P18).
const (
	statusDraft     = "draft"
	statusSent      = "sent"
	statusPartial   = "partially_received"
	statusReceived  = "received"
	statusClosed    = "closed"
	statusCancelled = "cancelled"
)

// isOpen reports whether an order is sent and not yet received, closed or
// cancelled: what may still change, be confirmed, closed or received.
func isOpen(status string) bool { return status == statusSent || status == statusPartial }

// unconfirmedDays is how many business days a sent order waits for the
// supplier's confirmation before it is unconfirmed (P19).
const unconfirmedDays = 3

// days is what Late and Unconfirmed are judged at (P19, P23): today in the
// business time zone, and the instant an order sent before has waited more
// than three business days.
type days struct {
	today             time.Time
	unconfirmedBefore time.Time
}

// daysOf reads the business zone before any transaction and judges today and
// the unconfirmed cut-off: the start of the fourth business day back,
// counting today — Monday to Friday, and the Norwegian public holidays when
// the business calendar is composed.
func (s *server) daysOf(ctx context.Context) (days, error) {
	zone, err := s.businessZone(ctx)
	if err != nil {
		return days{}, err
	}
	today := dayOf(s.deps.Clock(), zone)
	businessDay := func(d time.Time) bool {
		if s.deps.Calendar != nil {
			return holidays.BusinessDay(d)
		}
		return d.Weekday() != time.Saturday && d.Weekday() != time.Sunday
	}
	d, n := today, 0
	for {
		if businessDay(d) {
			n++
			if n == unconfirmedDays+1 {
				break
			}
		}
		d = d.AddDate(0, 0, -1)
	}
	return days{today: today, unconfirmedBefore: time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, zone)}, nil
}

// unconfirmed reports whether a sent order has waited past the cut-off with
// neither the supplier's reference nor a confirmed day (P19).
func (d days) unconfirmed(o store.PurchasingOrder) bool {
	return isOpen(o.Status) && o.SupplierReference == nil && !o.ConfirmedOn.Valid && o.SentAt != nil && o.SentAt.Before(d.unconfirmedBefore)
}

// late reports whether an open line of a sent order is past its expected day
// — its own, else the order's (P23).
func (d days) late(o store.PurchasingOrder, lines []store.PurchasingOrderLine) bool {
	if !isOpen(o.Status) {
		return false
	}
	for _, l := range lines {
		expected := timeOfDate(l.ExpectedOn)
		if expected == nil {
			expected = timeOfDate(o.ExpectedOn)
		}
		if expected != nil && lineOpen(l).Sign() > 0 && expected.Before(d.today) {
			return true
		}
	}
	return false
}

// personOut is a user snapshot as the API answers it, nil when there is none.
func personOut(id *uuid.UUID, display *string) *gen.PurchasingPerson {
	if id == nil && display == nil {
		return nil
	}
	return &gen.PurchasingPerson{Id: id, DisplayName: deref(display)}
}

// actor is who did something, snapshotted on the row it did.
type actor struct {
	id      *uuid.UUID
	display *string
}

// actorFor is the caller as an actor, read before any transaction.
func (s *server) actorFor(ctx context.Context) (actor, error) {
	id := callerID(ctx)
	if id == uuid.Nil {
		return actor{}, nil
	}
	u, err := s.user(ctx, id)
	if err != nil {
		return actor{}, err
	}
	a := actor{id: &id}
	if u != nil {
		a.display = textPtr(u.DisplayName)
	}
	return a, nil
}

// address is a postal address an order snapshots.
type address struct {
	name, line1, line2, postalCode, city, country *string
}

func (a address) out() gen.PurchasingAddress {
	return gen.PurchasingAddress{Name: a.name, Line1: a.line1, Line2: a.line2, PostalCode: a.postalCode, City: a.city, Country: a.country}
}

// deliveryAddress is where an order is delivered (P9, decision 5): the
// settings' address for the site when it has one, else the company's with
// the site's name.
func deliveryAddress(st purchasingSettings, seller contracts.SellerProfile, siteID int64, siteName string) address {
	if a := st.addressFor(siteID); a != nil {
		name := a.Name
		if name == "" {
			name = strings.TrimSpace(strings.Join(nonEmpty(seller.LegalName, siteName), ", "))
		}
		return address{name: textPtr(name), line1: textPtr(a.Line1), line2: textPtr(a.Line2), postalCode: textPtr(a.PostalCode),
			city: textPtr(a.City), country: textPtr(a.Country)}
	}
	return address{
		name: textPtr(strings.Join(nonEmpty(seller.LegalName, siteName), ", ")), line1: textPtr(seller.AddressLine1),
		line2: textPtr(seller.AddressLine2), postalCode: textPtr(seller.PostalCode), city: textPtr(seller.City),
		country: textPtr(strings.ToUpper(seller.Country)),
	}
}

// storedDelivery is a sent order's delivery snapshot.
func storedDelivery(o store.PurchasingOrder) address {
	return address{name: o.DeliveryName, line1: o.DeliveryAddressLine1, line2: o.DeliveryAddressLine2,
		postalCode: o.DeliveryPostalCode, city: o.DeliveryCity, country: o.DeliveryCountry}
}

// nonEmpty is the non-empty strings of in, trimmed.
func nonEmpty(in ...string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// snapshot is a supplier as an order keeps it (P15).
type snapshot struct {
	number                                      *int32
	name                                        string
	organisationNumber, addressLine, postalCode *string
	city, country, email                        *string
}

func snapshotOf(e contracts.SupplierEntry) snapshot {
	out := snapshot{name: e.Name, organisationNumber: textPtr(e.OrganisationNumber), addressLine: textPtr(e.AddressLine),
		postalCode: textPtr(e.PostalCode), city: textPtr(e.City), country: textPtr(strings.ToUpper(e.Country)),
		email: textPtr(e.Email)}
	if e.Number != 0 {
		out.number = ptr(e.Number)
	}
	return out
}

func storedSnapshot(o store.PurchasingOrder) snapshot {
	return snapshot{number: o.SupplierNumber, name: o.SupplierName, organisationNumber: o.SupplierOrganisationNumber,
		addressLine: o.SupplierAddressLine, postalCode: o.SupplierPostalCode, city: o.SupplierCity,
		country: o.SupplierCountry, email: o.SupplierEmail}
}

// orderRead is an order with what its view reads of this module's own rows.
type orderRead struct {
	order       store.PurchasingOrder
	lines       []store.PurchasingOrderLine
	items       map[int32]store.PurchasingSupplierItem
	lastMailing string
}

// readOrder reads an order, its lines, its supplier's items of their
// variants and its last mailing's state on q.
func readOrder(ctx context.Context, q *store.Queries, id int64) (orderRead, error) {
	o, err := q.GetOrder(ctx, id)
	if noRows(err) {
		return orderRead{}, notFound("purchase order")
	}
	if err != nil {
		return orderRead{}, fmt.Errorf("purchasing: read order %d: %w", id, err)
	}
	lines, err := q.OrderLines(ctx, id)
	if err != nil {
		return orderRead{}, fmt.Errorf("purchasing: read order %d's lines: %w", id, err)
	}
	var variants []int32
	for _, l := range lines {
		if l.VariantID != nil {
			variants = append(variants, *l.VariantID)
		}
	}
	items, err := itemsOf(ctx, q, o.SupplierID, variants)
	if err != nil {
		return orderRead{}, err
	}
	out := orderRead{order: o, lines: lines, items: items}
	m, err := q.LatestMailing(ctx, store.LatestMailingParams{OrderID: id, Kind: docOrder})
	switch {
	case err == nil:
		out.lastMailing = m.Status
	case !noRows(err):
		return orderRead{}, fmt.Errorf("purchasing: read order %d's mailings: %w", id, err)
	}
	if c, err := q.LatestMailing(ctx, store.LatestMailingParams{OrderID: id, Kind: docCancellation}); err == nil {
		out.lastMailing = c.Status
	} else if !noRows(err) {
		return orderRead{}, fmt.Errorf("purchasing: read order %d's mailings: %w", id, err)
	}
	return out, nil
}

// itemsOf is a supplier's items of the variants, by variant.
func itemsOf(ctx context.Context, q *store.Queries, supplierID int64, variants []int32) (map[int32]store.PurchasingSupplierItem, error) {
	out := map[int32]store.PurchasingSupplierItem{}
	if len(variants) == 0 {
		return out, nil
	}
	rows, err := q.SupplierItemsOf(ctx, store.SupplierItemsOfParams{SupplierID: supplierID, VariantIds: distinctSorted(variants)})
	if err != nil {
		return nil, fmt.Errorf("purchasing: read supplier %d's items: %w", supplierID, err)
	}
	for _, r := range rows {
		out[r.VariantID] = r
	}
	return out, nil
}

// viewEnv is what an order's view reads from outside the module, before any
// transaction.
type viewEnv struct {
	days     days
	sites    map[int64]contracts.StockSite
	supplier *contracts.SupplierEntry
	settings purchasingSettings
	seller   contracts.SellerProfile
}

// envFor reads what the view of o needs from the other modules: today, the
// sites, the supplier now and — for a draft, whose delivery address is
// computed live — the settings and the seller.
func (s *server) envFor(ctx context.Context, o store.PurchasingOrder) (viewEnv, error) {
	var env viewEnv
	var err error
	if env.days, err = s.daysOf(ctx); err != nil {
		return env, err
	}
	if env.sites, _, err = s.sites(ctx); err != nil {
		return env, err
	}
	if env.supplier, err = s.supplier(ctx, o.SupplierID); err != nil {
		return env, err
	}
	if o.Status == statusDraft {
		if env.settings, err = readSettings(ctx, s.store()); err != nil {
			return env, err
		}
		if env.seller, err = s.seller(ctx); err != nil {
			return env, err
		}
	}
	return env, nil
}

// store is the module's queries on its pool.
func (s *server) store() *store.Queries { return store.New(s.deps.Pool) }

// orderView is an order as the API answers it. A draft shows the supplier
// as the register has it now and its delivery address computed; a sent
// order its snapshots (P15).
func orderView(r orderRead, env viewEnv) gen.PurchasingOrder {
	o := r.order
	snap := storedSnapshot(o)
	delivery := storedDelivery(o)
	if o.Status == statusDraft {
		if env.supplier != nil {
			snap = snapshotOf(*env.supplier)
		}
		siteName := ""
		if site, ok := env.sites[o.SiteID]; ok {
			siteName = site.Name
		}
		delivery = deliveryAddress(env.settings, env.seller, o.SiteID, siteName)
	}
	out := gen.PurchasingOrder{
		Id: o.ID, Number: o.Number, Status: gen.PurchasingOrderStatus(o.Status), Version: o.Version,
		SupplierId: o.SupplierID, SupplierNumber: snap.number, SupplierName: snap.name,
		SupplierAddress: gen.PurchasingAddress{Line1: snap.addressLine, PostalCode: snap.postalCode, City: snap.city, Country: snap.country},
		SupplierEmail:   snap.email, SupplierContact: o.SupplierContact, SupplierActive: env.supplier != nil && env.supplier.Active,
		SiteId: o.SiteID, DeliveryAddress: delivery.out(), Currency: o.Currency, Language: gen.PurchasingOrderLanguage(o.Language),
		OrderDate: openapi_types.Date{Time: o.OrderDate.Time}, ExpectedOn: dateOut(o.ExpectedOn),
		Buyer: personOut(o.BuyerID, o.BuyerDisplay), Note: o.Note, InternalNote: o.InternalNote,
		NetTotal: amountText(ratOrZero(o.NetTotal)), SupplierReference: o.SupplierReference, ConfirmedOn: dateOut(o.ConfirmedOn),
		ConfirmedAt: o.ConfirmedAt, ConfirmedBy: personOut(o.ConfirmedByUserID, o.ConfirmedByDisplay),
		SentAt: o.SentAt, SentBy: personOut(o.SentByUserID, o.SentByDisplay), ClosedAt: o.ClosedAt, CancelledAt: o.CancelledAt,
		CopiedFromId: o.CopiedFromID, CreatedAt: o.CreatedAt, CreatedBy: personOut(o.CreatedByUserID, o.CreatedByDisplay),
		UpdatedAt: o.UpdatedAt, Late: env.days.late(o, r.lines), Unconfirmed: env.days.unconfirmed(o),
		Lines: make([]gen.PurchasingOrderLine, 0, len(r.lines)),
	}
	if site, ok := env.sites[o.SiteID]; ok {
		out.SiteName = site.Name
	}
	if o.SendMethod != nil {
		out.SendMethod = ptr(gen.PurchasingOrderSendMethod(*o.SendMethod))
	}
	if r.lastMailing != "" {
		out.LastMailingStatus = ptr(gen.PurchasingOrderLastMailingStatus(r.lastMailing))
	}
	for _, l := range r.lines {
		var item *store.PurchasingSupplierItem
		if l.VariantID != nil {
			if it, ok := r.items[*l.VariantID]; ok {
				item = &it
			}
		}
		out.Lines = append(out.Lines, lineView(l, item))
	}
	return out
}

// getOrderView reads an order and answers its view.
func (s *server) getOrderView(ctx context.Context, id int64) (gen.PurchasingOrder, error) {
	var r orderRead
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		r, err = readOrder(ctx, q, id)
		return err
	})
	if err != nil {
		return gen.PurchasingOrder{}, err
	}
	env, err := s.envFor(ctx, r.order)
	if err != nil {
		return gen.PurchasingOrder{}, err
	}
	return orderView(r, env), nil
}

// GetPurchasingOrder Get a purchase order
// (GET /api/v1/purchasing/orders/{orderId})
func (s *server) GetPurchasingOrder(ctx context.Context, req gen.GetPurchasingOrderRequestObject) (gen.GetPurchasingOrderResponseObject, error) {
	view, err := s.getOrderView(ctx, req.OrderId)
	if err != nil {
		return answer(err, answers[gen.GetPurchasingOrderResponseObject]{
			notFound: func(p gen.PurchasingProblem) gen.GetPurchasingOrderResponseObject {
				return gen.GetPurchasingOrder404ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	return gen.GetPurchasingOrder200JSONResponse(view), nil
}

// ListPurchasingOrders List purchase orders
// (GET /api/v1/purchasing/orders)
func (s *server) ListPurchasingOrders(ctx context.Context, req gen.ListPurchasingOrdersRequestObject) (gen.ListPurchasingOrdersResponseObject, error) {
	p := req.Params
	page, size, f := paging(p.Page, p.PageSize)
	if f.failed() {
		return gen.ListPurchasingOrders400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
	}
	d, err := s.daysOf(ctx)
	if err != nil {
		return nil, err
	}
	params := store.ListOrdersParams{
		Statuses: []string{}, SupplierID: p.SupplierId, SiteID: p.SiteId, OpenOnly: p.Open != nil && *p.Open,
		LateOnly: p.Late != nil && *p.Late, UnconfirmedOnly: p.Unconfirmed != nil && *p.Unconfirmed,
		Today: dayCol(d.today), UnconfirmedBefore: d.unconfirmedBefore, PageSize: size, PageOffset: (page - 1) * size,
	}
	if p.Status != nil {
		for _, st := range *p.Status {
			params.Statuses = append(params.Statuses, string(st))
		}
	}
	if p.Mine != nil && *p.Mine {
		me := callerID(ctx)
		params.BuyerID = &me
	}
	if p.Q != nil {
		params.Q = strings.TrimSpace(*p.Q)
	}
	rows, err := s.store().ListOrders(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("purchasing: list the orders: %w", err)
	}
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	late := map[int64]bool{}
	if len(ids) > 0 {
		lateIDs, err := s.store().LateOrderIDs(ctx, store.LateOrderIDsParams{Ids: ids, Today: dayCol(d.today)})
		if err != nil {
			return nil, fmt.Errorf("purchasing: judge the orders' lateness: %w", err)
		}
		for _, id := range lateIDs {
			late[id] = true
		}
	}
	out := gen.PurchasingOrderPage{Items: []gen.PurchasingOrderSummary{}, Page: page, PageSize: size}
	for _, r := range rows {
		out.Total = r.Total
		o := orderOfRow(r)
		item := gen.PurchasingOrderSummary{
			Id: r.ID, Number: r.Number, Status: gen.PurchasingOrderSummaryStatus(r.Status), SupplierId: r.SupplierID,
			SupplierName: r.SupplierName, SiteId: r.SiteID, Currency: r.Currency, NetTotal: amountText(ratOrZero(r.NetTotal)),
			OrderDate: openapi_types.Date{Time: r.OrderDate.Time}, ExpectedOn: dateOut(r.ExpectedOn), SentAt: r.SentAt,
			SupplierReference: r.SupplierReference, ConfirmedOn: dateOut(r.ConfirmedOn), Buyer: personOut(r.BuyerID, r.BuyerDisplay),
			Late: late[r.ID], Unconfirmed: d.unconfirmed(o), GoodsLines: r.GoodsLines, ReceivedLines: r.ReceivedLines,
			InvoicedLines: r.InvoicedLines, LineCount: r.LineCount, UpdatedAt: r.UpdatedAt,
		}
		if r.LastMailingStatus != "" {
			item.LastMailingStatus = ptr(gen.PurchasingOrderSummaryLastMailingStatus(r.LastMailingStatus))
		}
		out.Items = append(out.Items, item)
	}
	if len(rows) == 0 && page > 1 {
		// The page is past the end: the total is still the filters'.
		all, err := s.store().ListOrders(ctx, store.ListOrdersParams{
			Statuses: params.Statuses, SupplierID: params.SupplierID, SiteID: params.SiteID, BuyerID: params.BuyerID,
			OpenOnly: params.OpenOnly, LateOnly: params.LateOnly, UnconfirmedOnly: params.UnconfirmedOnly, Today: params.Today,
			UnconfirmedBefore: params.UnconfirmedBefore, Q: params.Q, PageSize: 1,
		})
		if err != nil {
			return nil, fmt.Errorf("purchasing: count the orders: %w", err)
		}
		if len(all) > 0 {
			out.Total = all[0].Total
		}
	}
	return gen.ListPurchasingOrders200JSONResponse(out), nil
}

// orderOfRow is a list row's order.
func orderOfRow(r store.ListOrdersRow) store.PurchasingOrder {
	return store.PurchasingOrder{ID: r.ID, Status: r.Status, SupplierReference: r.SupplierReference, ConfirmedOn: r.ConfirmedOn, SentAt: r.SentAt}
}

// paging reads a list's page and page size: page from 1, 50 a page, at most
// 200.
func paging(page, pageSize *int32) (int32, int32, fieldErrors) {
	var f fieldErrors
	p, size := int32(1), int32(50)
	if page != nil {
		if *page < 1 {
			f.add("page", "A page is 1 or more")
		}
		p = *page
	}
	if pageSize != nil {
		if *pageSize < 1 || *pageSize > 200 {
			f.add("pageSize", "A page holds 1 to 200")
		}
		size = *pageSize
	}
	return p, size, f
}

// header is an order's header as a create or a save judged it.
type header struct {
	supplierID      int64
	siteID          int64
	currency        string
	language        string
	orderDate       time.Time
	expectedOn      *time.Time
	buyer           actor
	supplierContact string
	note            string
	internalNote    string
}

// judgeText records the free-text fields' lengths on f.
func judgeText(f *fieldErrors, contact, note, internal string) {
	if utf8.RuneCountInString(contact) > 200 {
		f.add("supplierContact", "The supplier's contact is at most 200 characters")
	}
	if utf8.RuneCountInString(note) > 4000 {
		f.add("note", "A note is at most 4000 characters")
	}
	if utf8.RuneCountInString(internal) > 4000 {
		f.add("internalNote", "An internal note is at most 4000 characters")
	}
}

// readCurrency is an ISO 4217 code, upper case, or false.
func readCurrency(c string) (string, bool) {
	c = strings.ToUpper(strings.TrimSpace(c))
	if len(c) != 3 {
		return c, false
	}
	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return c, false
		}
	}
	return c, true
}

// languageFor is an order's language by the supplier's country (decision 3).
func languageFor(e contracts.SupplierEntry) string {
	if c := strings.ToUpper(strings.TrimSpace(e.Country)); c == "" || c == "NO" {
		return "nb"
	}
	return "en"
}

// buyerFor is the buyer named, its display name read before any transaction;
// a user identity does not know is a field error.
func (s *server) buyerFor(ctx context.Context, f *fieldErrors, id *uuid.UUID) (actor, error) {
	if id == nil {
		return actor{}, nil
	}
	u, err := s.user(ctx, *id)
	if err != nil {
		return actor{}, err
	}
	if u == nil && s.deps.Users != nil {
		f.add("buyerId", "No user has this id")
		return actor{}, nil
	}
	a := actor{id: id}
	if u != nil {
		a.display = textPtr(u.DisplayName)
	}
	return a, nil
}

// defaultSite is an order's site when the request names none (decision 4):
// the settings' default site, else the only active site.
func defaultSite(st purchasingSettings, sites []contracts.StockSite) *int64 {
	if st.defaultSiteID != nil {
		return st.defaultSiteID
	}
	var active []contracts.StockSite
	for _, site := range sites {
		if site.Active {
			active = append(active, site)
		}
	}
	if len(active) == 1 {
		return &active[0].ID
	}
	return nil
}

// judgeSupplier refuses a supplier the register does not have (400) or that
// is inactive (409).
func judgeSupplier(e *contracts.SupplierEntry, id int64) error {
	if e == nil {
		return refuseField(invalidOrderTitle, "supplierId", codeSupplierNotFound, fmt.Sprintf("No supplier has the id %d", id))
	}
	if !e.Active {
		return conflict(codeSupplierInactive, "The supplier is inactive",
			fmt.Sprintf("%s is inactive in the register; a new order for it is refused.", e.Name))
	}
	return nil
}

// change is one action of the change log (P18).
type change struct {
	Action string  `json:"action"`
	Field  *string `json:"field,omitempty"`
	From   *string `json:"from,omitempty"`
	To     *string `json:"to,omitempty"`
	LineID *int64  `json:"lineId,omitempty"`
}

// logChange writes one change row of the order with its actions, by a at at.
func logChange(ctx context.Context, txq *store.Queries, orderID int64, a actor, at time.Time, actions ...change) error {
	body, err := json.Marshal(actions)
	if err != nil {
		return fmt.Errorf("purchasing: encode a change: %w", err)
	}
	if err := txq.InsertChange(ctx, store.InsertChangeParams{OrderID: orderID, ActorUserID: a.id, ActorDisplay: a.display, At: at, Actions: body}); err != nil {
		return fmt.Errorf("purchasing: log a change of order %d: %w", orderID, err)
	}
	return nil
}

// fieldChange is an update action of a header field.
func fieldChange(field, from, to string) change {
	return change{Action: "updated", Field: ptr(field), From: ptr(from), To: ptr(to)}
}

// writeLines stores the planned lines of an order in their order, removing
// the saved lines the plan leaves out, and answers the lines as stored.
func writeLines(ctx context.Context, txq *store.Queries, orderID int64, planned []plannedLine, saved []store.PurchasingOrderLine) ([]store.PurchasingOrderLine, error) {
	kept := map[int64]bool{}
	for _, p := range planned {
		if p.existing != nil {
			kept[p.existing.ID] = true
		}
	}
	for _, l := range saved {
		if !kept[l.ID] {
			if err := txq.DeleteLine(ctx, l.ID); err != nil {
				return nil, fmt.Errorf("purchasing: remove line %d: %w", l.ID, err)
			}
		}
	}
	if err := txq.ParkLinePositions(ctx, orderID); err != nil {
		return nil, fmt.Errorf("purchasing: renumber order %d's lines: %w", orderID, err)
	}
	out := make([]store.PurchasingOrderLine, 0, len(planned))
	for i, p := range planned {
		var row store.PurchasingOrderLine
		var err error
		if p.existing != nil {
			row, err = txq.UpdateLine(ctx, p.updateParams(int32(i+1)))
		} else {
			row, err = txq.InsertLine(ctx, p.insertParams(orderID, int32(i+1)))
		}
		if err != nil {
			return nil, fmt.Errorf("purchasing: write line %d of order %d: %w", i+1, orderID, err)
		}
		out = append(out, row)
	}
	return out, nil
}

// newDraft is a draft to create: its header, its lines as written and where
// it was copied from.
type newDraft struct {
	header     header
	lines      []lineInput
	copiedFrom *int64
	createdBy  actor
}

// createDraft writes a draft (P15) in one transaction: the variants locked,
// then the supplier held FOR SHARE (A8), then the order, its lines and its
// change log. It answers the order's id.
func (s *server) createDraft(ctx context.Context, d newDraft) (int64, error) {
	var id int64
	err := s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		variants, err := s.lockVariants(ctx, tx, variantIDsOf(d.lines, nil))
		if err != nil {
			return err
		}
		held, err := s.lockSuppliers(ctx, tx, d.header.supplierID)
		if err != nil {
			return err
		}
		entry, ok := held[d.header.supplierID]
		var e *contracts.SupplierEntry
		if ok {
			e = &entry
		}
		if err := judgeSupplier(e, d.header.supplierID); err != nil {
			return err
		}
		items, err := itemsOf(ctx, txq, d.header.supplierID, variantIDsOf(d.lines, nil))
		if err != nil {
			return err
		}
		planned, failures, err := planLines(lineContext{currency: d.header.currency, orderDate: d.header.orderDate, variants: variants, items: items}, d.lines)
		if err != nil {
			return err
		}
		if len(failures) > 0 {
			return refuseLines(invalidOrderTitle, failures)
		}
		snap := snapshotOf(entry)
		h := d.header
		now := s.deps.Clock()
		o, err := txq.InsertOrder(ctx, store.InsertOrderParams{
			SupplierID: h.supplierID, SupplierNumber: snap.number, SupplierName: snap.name, SupplierOrganisationNumber: snap.organisationNumber,
			SupplierAddressLine: snap.addressLine, SupplierPostalCode: snap.postalCode, SupplierCity: snap.city,
			SupplierCountry: snap.country, SupplierEmail: snap.email, SupplierContact: h.supplierContact, SiteID: h.siteID,
			Currency: h.currency, Language: h.language, OrderDate: dayCol(h.orderDate), ExpectedOn: dateOf(h.expectedOn),
			BuyerID: h.buyer.id, BuyerDisplay: h.buyer.display, Note: h.note, InternalNote: h.internalNote,
			NetTotal: numeric(netTotal(planned), amountPlaces), CopiedFromID: d.copiedFrom, CreatedByUserID: d.createdBy.id,
			CreatedByDisplay: d.createdBy.display, Now: now,
		})
		if err != nil {
			return fmt.Errorf("purchasing: create the order: %w", err)
		}
		if _, err := writeLines(ctx, txq, o.ID, planned, nil); err != nil {
			return err
		}
		id = o.ID
		return logChange(ctx, txq, o.ID, d.createdBy, now, change{Action: "created"})
	})
	return id, err
}

// CreatePurchasingOrder Create a purchase order
// (POST /api/v1/purchasing/orders)
func (s *server) CreatePurchasingOrder(ctx context.Context, req gen.CreatePurchasingOrderRequestObject) (gen.CreatePurchasingOrderResponseObject, error) {
	resp := answers[gen.CreatePurchasingOrderResponseObject]{
		badRequest: func(p gen.PurchasingProblem) gen.CreatePurchasingOrderResponseObject {
			return gen.CreatePurchasingOrder400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func(p gen.PurchasingProblem) gen.CreatePurchasingOrderResponseObject {
			return gen.CreatePurchasingOrder404ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.CreatePurchasingOrderResponseObject {
			return gen.CreatePurchasingOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.PurchasingProblem) gen.CreatePurchasingOrderResponseObject {
			return gen.CreatePurchasingOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
	b := *req.Body
	var f fieldErrors
	h := header{supplierID: b.SupplierId, supplierContact: strings.TrimSpace(deref(b.SupplierContact)), note: deref(b.Note),
		internalNote: deref(b.InternalNote), expectedOn: dateFromAPI(b.ExpectedOn)}
	judgeText(&f, h.supplierContact, h.note, h.internalNote)
	e, err := s.supplier(ctx, b.SupplierId)
	if err != nil {
		return answer(err, resp)
	}
	if err := judgeSupplier(e, b.SupplierId); err != nil {
		return answer(err, resp)
	}
	h.currency, h.language = strings.ToUpper(e.Currency), languageFor(*e)
	if h.currency == "" {
		h.currency = baseCurrency
	}
	if b.Currency != nil {
		c, ok := readCurrency(*b.Currency)
		if !ok {
			f.addCoded("currency", codeCurrencyInvalid, "A currency is three letters, ISO 4217")
		}
		h.currency = c
	}
	if b.Language != nil {
		h.language = string(*b.Language)
		if !b.Language.Valid() {
			f.add("language", "The language is nb or en")
		}
	}
	settings, err := readSettings(ctx, s.store())
	if err != nil {
		return nil, err
	}
	sites, list, err := s.sites(ctx)
	if err != nil {
		return answer(err, resp)
	}
	site := b.SiteId
	if site == nil {
		site = defaultSite(settings, list)
	}
	if site == nil {
		f.addCoded("siteId", codeSiteNotFound, "Name the site the order is received at")
	} else {
		h.siteID = *site
		judgeSite(&f, "siteId", sites, *site)
	}
	d, err := s.daysOf(ctx)
	if err != nil {
		return nil, err
	}
	h.orderDate = d.today
	if b.OrderDate != nil {
		h.orderDate = *dateFromAPI(b.OrderDate)
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	h.buyer = by
	if b.BuyerId != nil {
		if h.buyer, err = s.buyerFor(ctx, &f, b.BuyerId); err != nil {
			return nil, err
		}
	}
	if f.failed() {
		return gen.CreatePurchasingOrder400ApplicationProblemPlusJSONResponse(invalid(invalidOrderTitle, f)), nil
	}
	var lines []gen.PurchasingLineInput
	if b.Lines != nil {
		lines = *b.Lines
	}
	id, err := s.createDraft(ctx, newDraft{header: h, lines: linesIn(lines, linePresence(ctx)), createdBy: by})
	if err != nil {
		return answer(err, resp)
	}
	view, err := s.getOrderView(ctx, id)
	if err != nil {
		return nil, err
	}
	return gen.CreatePurchasingOrder201JSONResponse(view), nil
}

// UpdatePurchasingOrder Update a purchase order
// (PUT /api/v1/purchasing/orders/{orderId})
func (s *server) UpdatePurchasingOrder(ctx context.Context, req gen.UpdatePurchasingOrderRequestObject) (gen.UpdatePurchasingOrderResponseObject, error) {
	resp := answers[gen.UpdatePurchasingOrderResponseObject]{
		badRequest: func(p gen.PurchasingProblem) gen.UpdatePurchasingOrderResponseObject {
			return gen.UpdatePurchasingOrder400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func(p gen.PurchasingProblem) gen.UpdatePurchasingOrderResponseObject {
			return gen.UpdatePurchasingOrder404ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.UpdatePurchasingOrderResponseObject {
			return gen.UpdatePurchasingOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.PurchasingProblem) gen.UpdatePurchasingOrderResponseObject {
			return gen.UpdatePurchasingOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
	b := *req.Body
	cur, err := readOrder(ctx, s.store(), req.OrderId)
	if err != nil {
		return answer(err, resp)
	}
	o := cur.order
	if err := judgeEditable(o); err != nil {
		return answer(err, resp)
	}
	if err := judgeFresh(o.UpdatedAt, b.ExpectedUpdatedAt); err != nil {
		return answer(err, resp)
	}
	var f fieldErrors
	h := header{supplierID: b.SupplierId, siteID: b.SiteId, language: string(b.Language), orderDate: *dateFromAPI(&b.OrderDate),
		expectedOn: dateFromAPI(b.ExpectedOn), supplierContact: strings.TrimSpace(b.SupplierContact), note: b.Note,
		internalNote: b.InternalNote}
	judgeText(&f, h.supplierContact, h.note, h.internalNote)
	if !b.Language.Valid() {
		f.add("language", "The language is nb or en")
	}
	currency, ok := readCurrency(b.Currency)
	if !ok {
		f.addCoded("currency", codeCurrencyInvalid, "A currency is three letters, ISO 4217")
	}
	h.currency = currency
	draft := o.Status == statusDraft
	if !draft && b.SupplierId != o.SupplierID {
		return answer(conflict(codeSupplierChanged, "A sent order's supplier does not change",
			"The order was sent to its supplier; cancel it and make a new one instead."), resp)
	}
	if ok && currency != o.Currency {
		if !draft {
			return answer(conflict(codeCurrencyFixed, "The currency is fixed", "A sent order keeps its currency."), resp)
		}
		for _, l := range cur.lines {
			if ratOrZero(l.UnitPrice).Sign() > 0 {
				return answer(conflict(codeCurrencyFixed, "The currency is fixed",
					"A line has a price; the currency no longer changes. Take the prices off first."), resp)
			}
		}
	}
	var supplier *contracts.SupplierEntry
	if draft {
		if supplier, err = s.supplier(ctx, b.SupplierId); err != nil {
			return answer(err, resp)
		}
		if supplier == nil {
			f.addCoded("supplierId", codeSupplierNotFound, fmt.Sprintf("No supplier has the id %d", b.SupplierId))
		}
	}
	if b.SiteId != o.SiteID {
		sites, _, err := s.sites(ctx)
		if err != nil {
			return answer(err, resp)
		}
		judgeSite(&f, "siteId", sites, b.SiteId)
		if !draft {
			for _, l := range cur.lines {
				if ratOrZero(l.Received).Sign() > 0 {
					return answer(conflict(codeOrderHasReceipts, "Something is received",
						"The site changes only while nothing of the order is received."), resp)
				}
			}
		}
	}
	h.buyer = actor{id: o.BuyerID, display: o.BuyerDisplay}
	if !sameUser(b.BuyerId, o.BuyerID) {
		if h.buyer, err = s.buyerFor(ctx, &f, b.BuyerId); err != nil {
			return nil, err
		}
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	if f.failed() {
		return gen.UpdatePurchasingOrder400ApplicationProblemPlusJSONResponse(invalid(invalidOrderTitle, f)), nil
	}
	in := linesIn(b.Lines, linePresence(ctx))
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		saved := map[int64]store.PurchasingOrderLine{}
		for _, l := range cur.lines {
			saved[l.ID] = l
		}
		variantIDs := variantIDsOf(in, saved)
		variants, err := s.lockVariants(ctx, tx, variantIDs)
		if err != nil {
			return err
		}
		snap := storedSnapshot(o)
		if draft {
			held, err := s.lockSuppliers(ctx, tx, b.SupplierId)
			if err != nil {
				return err
			}
			if entry, ok := held[b.SupplierId]; ok {
				if b.SupplierId != o.SupplierID && !entry.Active {
					return judgeSupplier(&entry, b.SupplierId)
				}
				snap = snapshotOf(entry)
			} else {
				return judgeSupplier(nil, b.SupplierId)
			}
		}
		locked, err := txq.LockOrder(ctx, o.ID)
		if err != nil {
			return fmt.Errorf("purchasing: lock order %d: %w", o.ID, err)
		}
		if err := judgeEditable(locked); err != nil {
			return err
		}
		if err := judgeFresh(locked.UpdatedAt, b.ExpectedUpdatedAt); err != nil {
			return err
		}
		items, err := itemsOf(ctx, txq, b.SupplierId, variantIDs)
		if err != nil {
			return err
		}
		planned, failures, err := planLines(lineContext{currency: h.currency, orderDate: h.orderDate, sent: !draft,
			variants: variants, items: items, existing: saved}, in)
		if err != nil {
			return err
		}
		if len(failures) > 0 {
			return refuseLines(invalidOrderTitle, failures)
		}
		if !draft {
			for _, l := range cur.lines {
				if !slices.ContainsFunc(planned, func(p plannedLine) bool { return p.existing != nil && p.existing.ID == l.ID }) {
					return refuse(problem(http.StatusBadRequest, codeLineNotFound, invalidOrderTitle,
						fmt.Sprintf("Line %d is left out; a sent order's line is closed, not removed.", l.ID)))
				}
			}
		}
		now := s.deps.Clock()
		written, err := writeLines(ctx, txq, o.ID, planned, cur.lines)
		if err != nil {
			return err
		}
		if _, err := txq.UpdateOrderHeader(ctx, store.UpdateOrderHeaderParams{
			ID: o.ID, SupplierID: b.SupplierId, SupplierNumber: snap.number, SupplierName: snap.name,
			SupplierOrganisationNumber: snap.organisationNumber, SupplierAddressLine: snap.addressLine,
			SupplierPostalCode: snap.postalCode, SupplierCity: snap.city, SupplierCountry: snap.country,
			SupplierEmail: snap.email, SupplierContact: h.supplierContact, SiteID: h.siteID, Currency: h.currency,
			Language: h.language, OrderDate: dayCol(h.orderDate), ExpectedOn: dateOf(h.expectedOn), BuyerID: h.buyer.id,
			BuyerDisplay: h.buyer.display, Note: h.note, InternalNote: h.internalNote,
			NetTotal: numeric(netTotal(planned), amountPlaces), Now: now,
		}); err != nil {
			return fmt.Errorf("purchasing: save order %d: %w", o.ID, err)
		}
		if draft {
			return nil
		}
		actions := headerChanges(locked, h)
		for i, p := range planned {
			if p.existing == nil {
				actions = append(actions, change{Action: "line_added", LineID: &written[i].ID})
				continue
			}
			for _, c := range p.changes(*p.existing) {
				actions = append(actions, change{Action: "line_changed", LineID: &p.existing.ID, Field: ptr(c.field), From: ptr(c.from), To: ptr(c.to)})
			}
		}
		if len(actions) == 0 {
			return nil
		}
		return logChange(ctx, txq, o.ID, by, now, actions...)
	})
	if err != nil {
		return answer(err, resp)
	}
	view, err := s.getOrderView(ctx, o.ID)
	if err != nil {
		return nil, err
	}
	return gen.UpdatePurchasingOrder200JSONResponse(view), nil
}

// sameUser reports whether two optional user ids are the same.
func sameUser(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// headerChanges are a sent order's header fields a save changes (P18).
func headerChanges(o store.PurchasingOrder, h header) []change {
	var out []change
	add := func(field, from, to string) {
		if from != to {
			out = append(out, fieldChange(field, from, to))
		}
	}
	add("siteId", fmt.Sprint(o.SiteID), fmt.Sprint(h.siteID))
	add("language", o.Language, h.language)
	add("orderDate", o.OrderDate.Time.Format(time.DateOnly), h.orderDate.Format(time.DateOnly))
	add("expectedOn", dayText(timeOfDate(o.ExpectedOn)), dayText(h.expectedOn))
	add("buyer", deref(o.BuyerDisplay), deref(h.buyer.display))
	add("supplierContact", o.SupplierContact, h.supplierContact)
	add("note", o.Note, h.note)
	add("internalNote", o.InternalNote, h.internalNote)
	return out
}

// judgeEditable refuses a save of an order that is closed, received or
// cancelled (order_not_sent).
func judgeEditable(o store.PurchasingOrder) error {
	if o.Status == statusDraft || isOpen(o.Status) {
		return nil
	}
	return conflict(codeOrderNotSent, "The order does not change",
		fmt.Sprintf("The order is %s; it is no longer changed.", o.Status))
}

// DeletePurchasingOrder Delete a draft purchase order
// (DELETE /api/v1/purchasing/orders/{orderId})
func (s *server) DeletePurchasingOrder(ctx context.Context, req gen.DeletePurchasingOrderRequestObject) (gen.DeletePurchasingOrderResponseObject, error) {
	resp := answers[gen.DeletePurchasingOrderResponseObject]{
		notFound: func(p gen.PurchasingProblem) gen.DeletePurchasingOrderResponseObject {
			return gen.DeletePurchasingOrder404ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.DeletePurchasingOrderResponseObject {
			return gen.DeletePurchasingOrder409ApplicationProblemPlusJSONResponse(p)
		},
	}
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		o, err := txq.LockOrder(ctx, req.OrderId)
		if noRows(err) {
			return notFound("purchase order")
		}
		if err != nil {
			return fmt.Errorf("purchasing: lock order %d: %w", req.OrderId, err)
		}
		if o.Status != statusDraft {
			return conflict(codeOrderNotDraft, "The order is not a draft", "A sent order is cancelled, not deleted.")
		}
		if err := judgeFresh(o.UpdatedAt, req.Params.ExpectedUpdatedAt); err != nil {
			return err
		}
		return txq.DeleteOrder(ctx, o.ID)
	})
	if err != nil {
		return answer(err, resp)
	}
	return gen.DeletePurchasingOrder204Response{}, nil
}
