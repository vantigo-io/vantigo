package orders

// Shipments (orders design O50): what of a posted delivery goes with a
// carrier. A draft proposes one package weighing the delivered variants'
// weights, to the order's delivery address and contact, on the site's default
// account and its default service; the user adjusts the packages and the
// service, sees the carrier's prices and books. Booking stores the
// shipment's idempotency key's attempt before the carrier is called — the key
// itself was stored when the draft was made — so a retried request books
// once. The attempt is a lease (bookingLease): while it is recent no second
// call is made, and after an unknown outcome a retry asks the carrier for the
// booking under the key before it books — refusing while the carrier may not
// show it yet (bookingLookupLag). Booking records the carrier's answer, publishes orders.shipment.booked.v1,
// counts the order's shipping lines delivered (O53) and queues the shipping
// notice (O52) on one transaction, then fetches and stores the label and the
// waybill. No carrier call is ever made inside a transaction.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
	"github.com/vantigo-io/vantigo/server/internal/shipping"
)

// The shipment states (O50).
const (
	shipmentDraft     = "draft"
	shipmentBooked    = "booked"
	shipmentInTransit = "in_transit"
	shipmentDelivered = "delivered"
	shipmentCancelled = "cancelled"
)

// ownerShipment is a shipment as orders.mailings names its owner.
const ownerShipment = "shipment"

// defaultParcelGrams is a proposed package's weight when no delivered variant
// has one: a kilogram, for the user to correct.
const defaultParcelGrams = 1000

// The booking's lease and its lookup (O50). bookingLease is how long an
// attempt holds the shipment: longer than the carrier call's own deadline,
// so no second call starts while the first may still book. bookingLookupLag
// is how long after an attempt whose outcome is unknown the carrier may not
// yet show its booking by reference (Bring indexes it for tracking with a
// lag): until then a retry that finds none books nothing. documentTimeout
// bounds each document fetched right after a booking.
const (
	bookingLease     = 2 * time.Minute
	bookingLookupLag = 15 * time.Minute
	documentTimeout  = 10 * time.Second
)

// bookingInProgress is the 409 of a shipment whose booking attempt holds it.
func bookingInProgress(sh store.OrdersShipment) error {
	return conflict(codeBookingInProgress, "The shipment is being booked",
		fmt.Sprintf("Shipment %d is being booked right now. Wait a couple of minutes and look again.", sh.ID))
}

// leased is whether an attempt at attemptedAt still holds the shipment at now.
func leased(attemptedAt *time.Time, now time.Time) bool {
	return attemptedAt != nil && attemptedAt.After(now.Add(-bookingLease))
}

// actionShippingDelivered is a booking counting the order's shipping lines
// delivered (O53): to is the consignment number.
const actionShippingDelivered = "shipping_delivered"

// shipmentAnswers are the typed refusals most shipment writes share.
func shipmentAnswers[R any](bad func(gen.OrdersProblem) R, nf func() R, cf func(gen.OrdersProblem) R, un func(gen.OrdersProblem) R,
	unproc func(gen.OrdersProblem) R) answers[R] {
	return answers[R]{badRequest: bad, notFound: nf, conflict: cf, unavailable: un, unprocessable: unproc}
}

// judgeShipment refuses a write of a shipment that is not a draft, or made
// from a version of it that is not its current one.
func judgeShipment(sh store.OrdersShipment, version int32, expected time.Time) error {
	if sh.Version != version {
		return versionConflict("shipment's version", sh.Version, version)
	}
	if err := judgeFresh("shipment", sh.UpdatedAt, expected); err != nil {
		return err
	}
	if sh.Status != shipmentDraft {
		return conflict(codeShipmentNotDraft, "The shipment is not a draft",
			fmt.Sprintf("Shipment %d is %s; only a draft is changed or booked.", sh.ID, sh.Status))
	}
	return nil
}

// documentRefs are a booking's document references as stored.
func documentRefs(raw []byte) []shipping.DocumentRef {
	var out []struct {
		Kind string `json:"kind"`
		Ref  string `json:"ref"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	refs := make([]shipping.DocumentRef, 0, len(out))
	for _, r := range out {
		refs = append(refs, shipping.DocumentRef{Kind: shipping.DocumentKind(r.Kind), Ref: r.Ref})
	}
	return refs
}

func encodeRefs(refs []shipping.DocumentRef) []byte {
	out := make([]map[string]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, map[string]string{"kind": string(r.Kind), "ref": r.Ref})
	}
	// Strings always encode.
	raw, _ := json.Marshal(out)
	return raw
}

// bookingOf is a booked shipment as the port names it.
func bookingOf(sh store.OrdersShipment, pkgs []store.OrdersShipmentPackage) shipping.Booking {
	b := shipping.Booking{
		ConsignmentNumber: deref(sh.ConsignmentNumber), Service: cmpOr(deref(sh.BookedService), sh.Service),
		TrackingURL: deref(sh.TrackingUrl), Documents: documentRefs(sh.DocumentRefs),
	}
	for _, p := range pkgs {
		b.PackageNumbers = append(b.PackageNumbers, deref(p.PackageNumber))
	}
	return b
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// shipmentViews answers rows with their packages, documents, events, accounts,
// deliveries and orders, read on q in one snapshot.
func shipmentViews(ctx context.Context, q *store.Queries, rows []store.OrdersShipment) ([]gen.OrdersShipment, error) {
	out := make([]gen.OrdersShipment, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	pkgs, err := q.PackagesOf(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("orders: read the shipments' packages: %w", err)
	}
	docs, err := q.DocumentsOfShipments(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("orders: read the shipments' documents: %w", err)
	}
	evs, err := q.EventsOfShipments(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("orders: read the shipments' events: %w", err)
	}
	accounts, err := q.ListCarrierAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("orders: list the carrier accounts: %w", err)
	}
	byAccount := map[int64]store.OrdersCarrierAccount{}
	for _, a := range accounts {
		byAccount[a.ID] = a
	}
	deliveries := map[int64]store.OrdersDelivery{}
	orderRows := map[int64]store.OrdersOrder{}
	for _, r := range rows {
		if _, ok := deliveries[r.DeliveryID]; ok {
			continue
		}
		d, err := q.GetDelivery(ctx, r.DeliveryID)
		if err != nil {
			return nil, fmt.Errorf("orders: read delivery %d: %w", r.DeliveryID, err)
		}
		deliveries[d.ID] = d
		if _, ok := orderRows[d.OrderID]; !ok {
			o, err := q.OrderByID(ctx, d.OrderID)
			if err != nil {
				return nil, fmt.Errorf("orders: read order %d: %w", d.OrderID, err)
			}
			orderRows[o.ID] = o
		}
	}
	for _, r := range rows {
		a := byAccount[r.CarrierAccountID]
		d := deliveries[r.DeliveryID]
		o := orderRows[d.OrderID]
		sv, _ := serviceOf(a.Carrier, r.Service)
		v := gen.OrdersShipment{
			Id: r.ID, DeliveryId: d.ID, DeliveryNumber: d.Number, OrderId: o.ID, OrderNumber: o.Number,
			CarrierAccountId: a.ID, CarrierAccountName: a.Name, Carrier: a.Carrier, Test: a.Test, Service: r.Service,
			ServiceName: serviceName(a.Carrier, r.Service, o.Language), Vas: append([]string{}, r.Vas...),
			Status: gen.OrdersShipmentStatus(r.Status), Exception: r.Exception, Voided: r.Voided,
			ConsignmentNumber: r.ConsignmentNumber, TrackingUrl: r.TrackingUrl, BookedService: r.BookedService,
			BookedCost: wirePtr(ratOf(r.BookedCost), amountPlaces), BookedCostCurrency: r.BookedCostCurrency,
			BookedCostEstimate: r.BookedCostEstimate, PlannedWindowStart: r.PlannedWindowStart, PlannedWindowEnd: r.PlannedWindowEnd,
			Recipient: gen.OrdersShipmentRecipient{
				Name: r.RecipientName, Line1: r.RecipientLine1, Line2: r.RecipientLine2, PostalCode: r.RecipientPostalCode,
				City: r.RecipientCity, Country: r.RecipientCountry, Contact: r.RecipientContact, Phone: r.RecipientPhone,
				Email: r.RecipientEmail,
			},
			PickupPointId: r.PickupPointID, BookedAt: r.BookedAt, BookedBy: personOut(r.BookedByUserID, r.BookedByDisplay),
			CancelledAt: r.CancelledAt, Packages: []gen.OrdersShipmentPackage{}, Events: []gen.OrdersShipmentEvent{},
			WaybillNeeded: sv.Waybill, BookAttempted: r.BookAttemptedAt != nil && r.Status == shipmentDraft,
			LabelInMybringOnly: r.LabelInMybringOnly, Version: r.Version, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		}
		for _, p := range pkgs {
			if p.ShipmentID == r.ID {
				v.Packages = append(v.Packages, gen.OrdersShipmentPackage{
					Position: p.Position, WeightGrams: p.WeightG, LengthMm: p.LengthMm, WidthMm: p.WidthMm, HeightMm: p.HeightMm,
					Content: p.Content, PackageNumber: p.PackageNumber,
				})
			}
		}
		for _, d := range docs {
			if d.ShipmentID == r.ID {
				switch d.Kind {
				case string(shipping.DocumentLabel):
					v.Label = true
				case string(shipping.DocumentWaybill):
					v.Waybill = true
				}
			}
		}
		for _, e := range evs {
			if e.ShipmentID == r.ID {
				v.Events = append(v.Events, gen.OrdersShipmentEvent{
					Code: e.Code, Text: e.Text, Place: e.Place, OccurredAt: e.OccurredAt, State: gen.OrdersShipmentEventState(e.FoldedState),
				})
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// shipmentOut reads one shipment and answers it.
func (s *server) shipmentOut(ctx context.Context, id int64) (gen.OrdersShipment, error) {
	var out gen.OrdersShipment
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		sh, err := q.GetShipment(ctx, id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return fmt.Errorf("orders: read shipment %d: %w", id, err)
		}
		views, err := shipmentViews(ctx, q, []store.OrdersShipment{sh})
		if err != nil {
			return err
		}
		out = views[0]
		return nil
	})
	return out, err
}

// GetShipment Get a shipment
// (GET /api/v1/orders/shipments/{id})
func (s *server) GetShipment(ctx context.Context, req gen.GetShipmentRequestObject) (gen.GetShipmentResponseObject, error) {
	out, err := s.shipmentOut(ctx, req.Id)
	if err != nil {
		return answer(err, answers[gen.GetShipmentResponseObject]{notFound: func() gen.GetShipmentResponseObject { return gen.GetShipment404Response{} }})
	}
	return gen.GetShipment200JSONResponse(out), nil
}

// ListDeliveryShipments List a delivery's shipments
// (GET /api/v1/orders/deliveries/{id}/shipments)
func (s *server) ListDeliveryShipments(ctx context.Context, req gen.ListDeliveryShipmentsRequestObject) (gen.ListDeliveryShipmentsResponseObject, error) {
	out := gen.OrdersShipmentList{Items: []gen.OrdersShipment{}}
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := q.GetDelivery(ctx, req.Id); noRows(err) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("orders: read delivery %d: %w", req.Id, err)
		}
		rows, err := q.ShipmentsOfDelivery(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("orders: read delivery %d's shipments: %w", req.Id, err)
		}
		out.Items, err = shipmentViews(ctx, q, rows)
		return err
	})
	if err != nil {
		return answer(err, answers[gen.ListDeliveryShipmentsResponseObject]{
			notFound: func() gen.ListDeliveryShipmentsResponseObject { return gen.ListDeliveryShipments404Response{} },
		})
	}
	return gen.ListDeliveryShipments200JSONResponse(out), nil
}

// ListOrderShipments List an order's shipments
// (GET /api/v1/orders/orders/{id}/shipments)
func (s *server) ListOrderShipments(ctx context.Context, req gen.ListOrderShipmentsRequestObject) (gen.ListOrderShipmentsResponseObject, error) {
	out := gen.OrdersShipmentList{Items: []gen.OrdersShipment{}}
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := q.OrderByID(ctx, req.Id); noRows(err) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("orders: read order %d: %w", req.Id, err)
		}
		rows, err := q.ShipmentsOfOrder(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("orders: read order %d's shipments: %w", req.Id, err)
		}
		shipments := make([]store.OrdersShipment, 0, len(rows))
		for _, r := range rows {
			shipments = append(shipments, r.OrdersShipment)
		}
		out.Items, err = shipmentViews(ctx, q, shipments)
		return err
	})
	if err != nil {
		return answer(err, answers[gen.ListOrderShipmentsResponseObject]{
			notFound: func() gen.ListOrderShipmentsResponseObject { return gen.ListOrderShipments404Response{} },
		})
	}
	return gen.ListOrderShipments200JSONResponse(out), nil
}

// proposedWeight is a delivery's goods' weight in grams: each line's quantity
// times its variant's weight, rounded up; defaultParcelGrams when no variant
// has a weight.
func proposedWeight(quantities map[int32]*big.Rat, variants map[int32]contracts.VariantEntry) int64 {
	total := new(big.Rat)
	for id, qty := range quantities {
		v, ok := variants[id]
		if !ok || v.WeightGrams == nil {
			continue
		}
		total.Add(total, new(big.Rat).Mul(qty, new(big.Rat).SetInt64(*v.WeightGrams)))
	}
	if total.Sign() <= 0 {
		return defaultParcelGrams
	}
	whole := new(big.Int).Quo(total.Num(), total.Denom())
	if new(big.Rat).SetInt(whole).Cmp(total) < 0 {
		whole.Add(whole, big.NewInt(1))
	}
	return whole.Int64()
}

// CreateShipment Create a shipment
// (POST /api/v1/orders/deliveries/{id}/shipments)
func (s *server) CreateShipment(ctx context.Context, req gen.CreateShipmentRequestObject) (gen.CreateShipmentResponseObject, error) {
	resp := shipmentAnswers(
		func(p gen.OrdersProblem) gen.CreateShipmentResponseObject {
			return gen.CreateShipment400ApplicationProblemPlusJSONResponse(p)
		},
		func() gen.CreateShipmentResponseObject { return gen.CreateShipment404Response{} },
		func(p gen.OrdersProblem) gen.CreateShipmentResponseObject {
			return gen.CreateShipment409ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.OrdersProblem) gen.CreateShipmentResponseObject {
			return gen.CreateShipment503ApplicationProblemPlusJSONResponse(p)
		}, nil)
	if f := s.unknownProperties(ctx, shipmentInputSchema); f.failed() {
		return gen.CreateShipment400ApplicationProblemPlusJSONResponse(invalid(invalidShipmentTitle, f)), nil
	}
	var r deliveryRead
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		r, err = readDelivery(ctx, q, req.Id)
		return err
	})
	if err != nil {
		return answer(err, resp)
	}
	if r.d.Status != deliveryPosted {
		return answer(conflict(codeDeliveryNotPosted, "The delivery is not posted",
			fmt.Sprintf("Delivery %d is %s; only a posted delivery is shipped.", r.d.Number, r.d.Status)), resp)
	}
	quantities := map[int32]*big.Rat{}
	var ids []int32
	for _, dl := range r.dlines {
		if quantities[dl.VariantID] == nil {
			quantities[dl.VariantID] = new(big.Rat)
			ids = append(ids, dl.VariantID)
		}
		quantities[dl.VariantID].Add(quantities[dl.VariantID], ratOrZero(dl.Quantity))
	}
	slices.Sort(ids)
	variants, err := s.catalogVariants(ctx, ids)
	if err != nil {
		return nil, err
	}
	weight := proposedWeight(quantities, variants)

	q := store.New(s.deps.Pool)
	var account *store.OrdersCarrierAccount
	if req.Body != nil && req.Body.CarrierAccountId != nil {
		a, err := q.GetCarrierAccount(ctx, *req.Body.CarrierAccountId)
		if noRows(err) {
			return answer(refuseField(invalidShipmentTitle, "carrierAccountId", "", "There is no such carrier account"), resp)
		}
		if err != nil {
			return nil, fmt.Errorf("orders: read carrier account %d: %w", *req.Body.CarrierAccountId, err)
		}
		account = &a
	} else if account, err = defaultAccount(ctx, q, &r.d.SiteID); err != nil {
		return nil, err
	}
	if account == nil {
		return answer(noCarrierAccount(), resp)
	}
	if account.Status != accountActive {
		return answer(conflict(codeCarrierAccountArchived, "The carrier account is archived",
			fmt.Sprintf("Carrier account %q is archived; choose another.", account.Name)), resp)
	}
	if !s.carrierAllowed(account.Carrier) {
		return answer(carrierUnavailable(errCarrierWithheld), resp)
	}
	service := deref(account.DefaultService)
	if req.Body != nil && req.Body.Service != nil && strings.TrimSpace(*req.Body.Service) != "" {
		service = strings.TrimSpace(*req.Body.Service)
	}
	if service == "" {
		if all := carrierServices(account.Carrier); len(all) > 0 {
			service = all[0].Code
		}
	}
	if _, ok := serviceOf(account.Carrier, service); !ok {
		return answer(refuseField(invalidShipmentTitle, "service", codeServiceUnknown, fmt.Sprintf("%q is not one of the carrier's services", service)), resp)
	}
	h := headerOfOrder(r.o)
	to := shipTo(h)
	var created store.OrdersShipment
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		var err error
		created, err = txq.InsertShipment(ctx, store.InsertShipmentParams{
			DeliveryID: r.d.ID, CarrierAccountID: account.ID, Service: service, Vas: []string{}, IdempotencyKey: uuid.New(),
			RecipientName: truncate(to.Name, 255), RecipientLine1: truncate(to.Line1, 255), RecipientLine2: truncate(to.Line2, 255),
			RecipientPostalCode: truncate(to.PostalCode, 20), RecipientCity: truncate(to.City, 100), RecipientCountry: to.Country,
			RecipientContact: truncate(deref(h.ContactName), 200), RecipientPhone: truncate(deref(h.ContactPhone), 50),
			RecipientEmail: truncate(deref(defaultRecipient(h)), 254), Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("orders: insert delivery %d's shipment: %w", r.d.ID, err)
		}
		if _, err := txq.InsertShipmentPackage(ctx, store.InsertShipmentPackageParams{
			ShipmentID: created.ID, Position: 1, WeightG: int32(min(weight, 1000000)), Content: "",
		}); err != nil {
			return fmt.Errorf("orders: insert shipment %d's package: %w", created.ID, err)
		}
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	out, err := s.shipmentOut(ctx, created.ID)
	if err != nil {
		return nil, err
	}
	return gen.CreateShipment201JSONResponse(out), nil
}

// UpdateShipment Save a shipment
// (PUT /api/v1/orders/shipments/{id})
func (s *server) UpdateShipment(ctx context.Context, req gen.UpdateShipmentRequestObject) (gen.UpdateShipmentResponseObject, error) {
	resp := shipmentAnswers(
		func(p gen.OrdersProblem) gen.UpdateShipmentResponseObject {
			return gen.UpdateShipment400ApplicationProblemPlusJSONResponse(p)
		},
		func() gen.UpdateShipmentResponseObject { return gen.UpdateShipment404Response{} },
		func(p gen.OrdersProblem) gen.UpdateShipmentResponseObject {
			return gen.UpdateShipment409ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.OrdersProblem) gen.UpdateShipmentResponseObject {
			return gen.UpdateShipment503ApplicationProblemPlusJSONResponse(p)
		}, nil)
	f := s.unknownProperties(ctx, shipmentUpdateSchema)
	b := *req.Body
	if len(b.Packages) < 1 || len(b.Packages) > 20 {
		f.add("packages", "One to twenty packages")
	}
	for i, p := range b.Packages {
		field := fmt.Sprintf("packages[%d]", i)
		if p.WeightGrams < 1 || p.WeightGrams > 1000000 {
			f.add(field+".weightGrams", "A weight is 1 gram to 1 tonne")
		}
		for name, v := range map[string]*int32{"lengthMm": p.LengthMm, "widthMm": p.WidthMm, "heightMm": p.HeightMm} {
			if v != nil && (*v < 0 || *v > 10000) {
				f.add(field+"."+name, "A dimension is 0 to 10 000 mm")
			}
		}
		if p.Content != nil && len([]rune(*p.Content)) > 200 {
			f.add(field+".content", "At most 200 characters")
		}
	}
	var vasIn []string
	if b.Vas != nil {
		vasIn = *b.Vas
	}
	if len(vasIn) > 10 {
		f.add("vas", "At most ten value-added services")
	}
	contact, phone, email := strings.TrimSpace(deref(b.RecipientContact)), strings.TrimSpace(deref(b.RecipientPhone)), strings.TrimSpace(deref(b.RecipientEmail))
	if len([]rune(contact)) > 200 || len([]rune(phone)) > 50 || len([]rune(email)) > 254 {
		f.add("recipientContact", "The contact is at most 200 characters, the phone 50 and the e-mail 254")
	}
	if f.failed() {
		return gen.UpdateShipment400ApplicationProblemPlusJSONResponse(invalid(invalidShipmentTitle, f)), nil
	}
	q := store.New(s.deps.Pool)
	account, err := q.GetCarrierAccount(ctx, b.CarrierAccountId)
	if noRows(err) {
		return answer(refuseField(invalidShipmentTitle, "carrierAccountId", "", "There is no such carrier account"), resp)
	}
	if err != nil {
		return nil, fmt.Errorf("orders: read carrier account %d: %w", b.CarrierAccountId, err)
	}
	service := strings.TrimSpace(b.Service)
	if _, ok := serviceOf(account.Carrier, service); !ok {
		return answer(refuseField(invalidShipmentTitle, "service", codeServiceUnknown, fmt.Sprintf("%q is not one of the carrier's services", service)), resp)
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		sh, err := txq.LockShipment(ctx, req.Id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return fmt.Errorf("orders: lock shipment %d: %w", req.Id, err)
		}
		if err := judgeShipment(sh, b.Version, b.ExpectedUpdatedAt); err != nil {
			return err
		}
		if sh.BookAttemptedAt != nil {
			// A retry books what was attempted, so it stays as it is.
			return conflict(codeBookingUnresolved, "The booking is unresolved",
				fmt.Sprintf("A booking of shipment %d was attempted and its outcome is unknown. Book it again to resolve that before changing it.", sh.ID))
		}
		if account.Status != accountActive && account.ID != sh.CarrierAccountID {
			return conflict(codeCarrierAccountArchived, "The carrier account is archived",
				fmt.Sprintf("Carrier account %q is archived; choose another.", account.Name))
		}
		vas := []string{}
		for _, v := range vasIn {
			if v = strings.TrimSpace(v); v != "" && !slices.Contains(vas, v) {
				vas = append(vas, v)
			}
		}
		var pickup *string
		if b.PickupPointId != nil && strings.TrimSpace(*b.PickupPointId) != "" {
			pickup = ptr(strings.TrimSpace(*b.PickupPointId))
		}
		if _, err := txq.UpdateShipmentDraft(ctx, store.UpdateShipmentDraftParams{
			ID: sh.ID, CarrierAccountID: account.ID, Service: service, Vas: vas, PickupPointID: pickup,
			RecipientContact: contact, RecipientPhone: phone, RecipientEmail: email, Now: s.deps.Clock(),
		}); err != nil {
			return fmt.Errorf("orders: save shipment %d: %w", sh.ID, err)
		}
		if err := txq.DeleteShipmentPackages(ctx, sh.ID); err != nil {
			return fmt.Errorf("orders: clear shipment %d's packages: %w", sh.ID, err)
		}
		for i, p := range b.Packages {
			if _, err := txq.InsertShipmentPackage(ctx, store.InsertShipmentPackageParams{
				ShipmentID: sh.ID, Position: int32(i + 1), WeightG: p.WeightGrams, LengthMm: derefInt32(p.LengthMm),
				WidthMm: derefInt32(p.WidthMm), HeightMm: derefInt32(p.HeightMm), Content: strings.TrimSpace(deref(p.Content)),
			}); err != nil {
				return fmt.Errorf("orders: write shipment %d's package: %w", sh.ID, err)
			}
		}
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	out, err := s.shipmentOut(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.UpdateShipment200JSONResponse(out), nil
}

// shipmentRead is a shipment with everything a carrier call needs, read on
// the pool before any transaction.
type shipmentRead struct {
	sh      store.OrdersShipment
	d       store.OrdersDelivery
	o       store.OrdersOrder
	account store.OrdersCarrierAccount
	pkgs    []store.OrdersShipmentPackage
}

func readShipment(ctx context.Context, q *store.Queries, id int64) (shipmentRead, error) {
	var r shipmentRead
	var err error
	if r.sh, err = q.GetShipment(ctx, id); noRows(err) {
		return r, notFound()
	} else if err != nil {
		return r, fmt.Errorf("orders: read shipment %d: %w", id, err)
	}
	if r.d, err = q.GetDelivery(ctx, r.sh.DeliveryID); err != nil {
		return r, fmt.Errorf("orders: read delivery %d: %w", r.sh.DeliveryID, err)
	}
	if r.o, err = q.OrderByID(ctx, r.d.OrderID); err != nil {
		return r, fmt.Errorf("orders: read order %d: %w", r.d.OrderID, err)
	}
	if r.account, err = q.GetCarrierAccount(ctx, r.sh.CarrierAccountID); err != nil {
		return r, fmt.Errorf("orders: read carrier account %d: %w", r.sh.CarrierAccountID, err)
	}
	if r.pkgs, err = q.PackagesOf(ctx, []int64{r.sh.ID}); err != nil {
		return r, fmt.Errorf("orders: read shipment %d's packages: %w", r.sh.ID, err)
	}
	return r, nil
}

// senderOf is who a shipment is sent from: the account's sender, each field
// falling back to the seller's.
func senderOf(a store.OrdersCarrierAccount, seller contracts.SellerProfile) shipping.Address {
	or := func(v *string, fallback string) string {
		if v != nil && *v != "" {
			return *v
		}
		return fallback
	}
	return shipping.Address{
		Name: or(a.SenderName, seller.LegalName), Line1: or(a.SenderAddressLine1, seller.AddressLine1),
		Line2: or(a.SenderAddressLine2, seller.AddressLine2), PostalCode: or(a.SenderPostalCode, seller.PostalCode),
		City: or(a.SenderCity, seller.City), Country: strings.ToUpper(or(a.SenderCountry, seller.Country)),
		ContactName: or(a.SenderName, seller.LegalName), Phone: or(a.SenderPhone, seller.Phone), Email: or(a.SenderEmail, seller.Email),
	}
}

// shipTo is where a document's goods go: its delivery address, else — a
// customer with no delivery address — its invoice address, named for the
// delivery address's name or the customer.
func shipTo(h header) shipping.Address {
	a := shipping.Address{
		Name: cmpOr(deref(h.DeliveryName), h.CustomerName), Line1: deref(h.DeliveryAddressLine1), Line2: deref(h.DeliveryAddressLine2),
		PostalCode: deref(h.DeliveryPostalCode), City: deref(h.DeliveryCity), Country: deref(h.DeliveryCountry),
	}
	if strings.TrimSpace(a.PostalCode) == "" {
		a.Line1, a.Line2, a.PostalCode = deref(h.InvoiceAddressLine1), deref(h.InvoiceAddressLine2), deref(h.InvoicePostalCode)
		a.City, a.Country = deref(h.InvoiceCity), cmpOr(deref(h.InvoiceCountry), deref(h.CustomerCountry))
	}
	a.Country = cmpOr(strings.ToUpper(a.Country), "NO")
	return a
}

// recipientOf is a shipment's recipient as the port names it.
func recipientOf(sh store.OrdersShipment) shipping.Address {
	return shipping.Address{
		Name: sh.RecipientName, Line1: sh.RecipientLine1, Line2: sh.RecipientLine2, PostalCode: sh.RecipientPostalCode,
		City: sh.RecipientCity, Country: sh.RecipientCountry, ContactName: sh.RecipientContact, Phone: sh.RecipientPhone,
		Email: sh.RecipientEmail, PickupPointID: deref(sh.PickupPointID),
	}
}

func packagesOf(pkgs []store.OrdersShipmentPackage) []shipping.Package {
	out := make([]shipping.Package, 0, len(pkgs))
	for _, p := range pkgs {
		out = append(out, shipping.Package{
			WeightGrams: int64(p.WeightG), LengthMM: int64(p.LengthMm), WidthMM: int64(p.WidthMm), HeightMM: int64(p.HeightMm),
			Content: p.Content, CorrelationID: fmt.Sprint(p.Position),
		})
	}
	return out
}

// moneyOf is minor units as an exact decimal.
func moneyOf(m shipping.Money) *big.Rat { return new(big.Rat).SetFrac64(m.Minor, 100) }

// GetShipmentRates Get a shipment's prices
// (GET /api/v1/orders/shipments/{id}/rates)
func (s *server) GetShipmentRates(ctx context.Context, req gen.GetShipmentRatesRequestObject) (gen.GetShipmentRatesResponseObject, error) {
	resp := shipmentAnswers(nil,
		func() gen.GetShipmentRatesResponseObject { return gen.GetShipmentRates404Response{} },
		func(p gen.OrdersProblem) gen.GetShipmentRatesResponseObject {
			return gen.GetShipmentRates409ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.OrdersProblem) gen.GetShipmentRatesResponseObject {
			return gen.GetShipmentRates503ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.OrdersProblem) gen.GetShipmentRatesResponseObject {
			return gen.GetShipmentRates422ApplicationProblemPlusJSONResponse(p)
		})
	r, err := readShipment(ctx, store.New(s.deps.Pool), req.Id)
	if err != nil {
		return answer(err, resp)
	}
	if r.sh.Status != shipmentDraft {
		return answer(conflict(codeShipmentNotDraft, "The shipment is not a draft", "A booked shipment's price is its booked cost."), resp)
	}
	c, err := s.carrierOf(r.account)
	if err != nil {
		return answer(carrierFailed(err, ""), resp)
	}
	out := gen.OrdersShippingOptionList{Items: []gen.OrdersShippingOption{}}
	if !c.Capabilities().Rates {
		return gen.GetShipmentRates200JSONResponse(out), nil
	}
	seller, err := s.seller(ctx)
	if err != nil {
		return nil, err
	}
	opts, err := c.Rates(ctx, shipping.RateRequest{
		From: senderOf(r.account, seller), To: recipientOf(r.sh), Packages: packagesOf(r.pkgs), ShippingDate: s.deps.Clock(),
	})
	if err != nil {
		return answer(carrierFailed(err, "The carrier did not answer with its prices. Try again later."), resp)
	}
	slices.SortStableFunc(opts, func(a, b shipping.Option) int { return int(a.Price.Minor - b.Price.Minor) })
	for _, o := range opts {
		item := gen.OrdersShippingOption{
			Service: o.Service, Name: cmpOr(serviceName(r.account.Carrier, o.Service, r.o.Language), o.Name),
			Price: wire(moneyOf(o.Price), amountPlaces), Vat: wire(moneyOf(o.VAT), amountPlaces), Currency: o.Price.Currency,
			Estimate: o.Estimate, ExpiresAt: o.ExpiresAt,
		}
		if _, ok := serviceOf(r.account.Carrier, o.Service); !ok {
			item.Name = o.Name
		}
		if o.ExpectedDate != nil {
			d := wireDate(*o.ExpectedDate)
			item.ExpectedDate = &d
		}
		if o.Window != nil {
			item.WindowStart, item.WindowEnd = &o.Window.Start, &o.Window.End
		}
		out.Items = append(out.Items, item)
	}
	return gen.GetShipmentRates200JSONResponse(out), nil
}

// BookShipment Book a shipment
// (POST /api/v1/orders/shipments/{id}/book)
//
// The attempt is recorded under the shipment's lock and committed before the
// carrier is called, moving the version: it is the booking's lease, and while
// it is under bookingLease old another book is booking_in_progress. The call
// is made under the key stored on the shipment, with Retry when an earlier
// attempt's outcome is unknown, so the carrier books once; while that attempt
// is under bookingLookupLag old the retry only looks (FindOnly), and a
// booking the carrier does not show yet is booking_outcome_unknown. A
// definitive refusal of a call that could book gives the draft a fresh key
// and takes the attempt back. The answer is recorded under the order's and the shipment's
// locks: a booking another request recorded first is answered as it is.
func (s *server) BookShipment(ctx context.Context, req gen.BookShipmentRequestObject) (gen.BookShipmentResponseObject, error) {
	resp := shipmentAnswers(nil,
		func() gen.BookShipmentResponseObject { return gen.BookShipment404Response{} },
		func(p gen.OrdersProblem) gen.BookShipmentResponseObject {
			return gen.BookShipment409ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.OrdersProblem) gen.BookShipmentResponseObject {
			return gen.BookShipment503ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.OrdersProblem) gen.BookShipmentResponseObject {
			return gen.BookShipment422ApplicationProblemPlusJSONResponse(p)
		})
	if f := s.unknownProperties(ctx, versionInputSchema); f.failed() {
		return answer(refuseInvalid(invalidShipmentTitle, f), resp)
	}
	r, err := readShipment(ctx, store.New(s.deps.Pool), req.Id)
	if err != nil {
		return answer(err, resp)
	}
	if err := judgeShipment(r.sh, req.Body.Version, req.Body.ExpectedUpdatedAt); err != nil {
		return answer(err, resp)
	}
	if r.account.Status != accountActive {
		return answer(conflict(codeCarrierAccountArchived, "The carrier account is archived",
			fmt.Sprintf("Carrier account %q is archived; choose another.", r.account.Name)), resp)
	}
	c, err := s.carrierOf(r.account)
	if err != nil {
		return answer(carrierFailed(err, ""), resp)
	}
	seller, err := s.seller(ctx)
	if err != nil {
		return nil, err
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	settings, err := readSettings(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	_, orderLines, err := readOrder(ctx, store.New(s.deps.Pool), r.o.ID)
	if err != nil {
		return nil, err
	}
	var freight []int32
	for _, l := range orderLines {
		if l.Kind == kindShipping && l.VariantID != nil && !slices.Contains(freight, *l.VariantID) {
			freight = append(freight, *l.VariantID)
		}
	}
	slices.Sort(freight)

	// The attempt, committed before the call: the lease.
	var attempted store.OrdersShipment
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		sh, err := txq.LockShipment(ctx, r.sh.ID)
		if err != nil {
			return fmt.Errorf("orders: lock shipment %d: %w", r.sh.ID, err)
		}
		if err := judgeShipment(sh, req.Body.Version, req.Body.ExpectedUpdatedAt); err != nil {
			return err
		}
		now := s.deps.Clock()
		if leased(sh.BookAttemptedAt, now) {
			return bookingInProgress(sh)
		}
		r.sh = sh
		attempted, err = txq.MarkShipmentBookAttempt(ctx, store.MarkShipmentBookAttemptParams{
			ID: sh.ID, Now: &now, LeaseBefore: ptr(now.Add(-bookingLease)),
		})
		if noRows(err) {
			return bookingInProgress(sh)
		}
		if err != nil {
			return fmt.Errorf("orders: record shipment %d's booking attempt: %w", sh.ID, err)
		}
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	earlier := r.sh.BookAttemptedAt
	findOnly := earlier != nil && earlier.After(s.deps.Clock().Add(-bookingLookupLag))
	booking, err := c.Book(ctx, shipping.BookRequest{
		Sender: senderOf(r.account, seller), Recipient: recipientOf(attempted), Service: attempted.Service,
		ValueAddedServices: attempted.Vas, Packages: packagesOf(r.pkgs), IdempotencyKey: attempted.IdempotencyKey.String(),
		Retry: earlier != nil, FindOnly: findOnly, SenderReference: fmt.Sprintf("%d", r.o.Number), RecipientReference: r.o.YourReference,
		ShippingDate: s.deps.Clock(),
	})
	switch {
	case err == nil:
	case findOnly && errors.Is(err, shipping.ErrNotFound):
		return answer(conflict(codeBookingOutcomeUnknown, "Whether the carrier booked is not known yet",
			fmt.Sprintf("An earlier booking of shipment %d got no answer, and the carrier does not show a booking under it yet. "+
				"Check Mybring, or wait: from %s Vantigo books again if the carrier still shows none.", attempted.ID,
				earlier.Add(bookingLookupLag).UTC().Format(time.RFC3339))), resp)
	case shipping.IsDefinitiveRefusal(err) && !findOnly:
		// Nothing was booked under the key, and nothing ever will be: a fresh
		// key, and the draft is the user's to change again.
		if _, rerr := store.New(s.deps.Pool).ResetShipmentBookAttempt(ctx, store.ResetShipmentBookAttemptParams{
			ID: attempted.ID, IdempotencyKey: uuid.New(), AttemptedAt: attempted.BookAttemptedAt, Now: s.deps.Clock(),
		}); rerr != nil && !noRows(rerr) {
			return nil, fmt.Errorf("orders: take back shipment %d's refused attempt: %w", attempted.ID, rerr)
		}
		return answer(carrierFailed(err, ""), resp)
	default:
		if !shipping.IsDefinitiveRefusal(err) {
			s.deps.Logger.WarnContext(ctx, "orders: a booking's outcome is unknown", "shipment_id", attempted.ID, "error", err.Error())
		}
		return answer(carrierFailed(err,
			"The carrier did not answer, and whether it booked is unknown. Book again in a couple of minutes: Vantigo asks the carrier first and books once."), resp)
	}
	err = s.recordBooking(ctx, r, attempted, booking, settings, freight, by)
	if err != nil {
		return answer(err, resp)
	}
	s.storeShipmentDocuments(context.WithoutCancel(ctx), c, attempted.ID)
	out, err := s.shipmentOut(ctx, attempted.ID)
	if err != nil {
		return nil, err
	}
	return gen.BookShipment200JSONResponse(out), nil
}

// recordBooking records a carrier's booking on one transaction: the
// shipment's row and its package numbers, orders.shipment.booked.v1, the
// order's shipping lines counted delivered (O53) and, when the settings say
// so, the shipping notice queued (O52). Locks: the shipping lines' variants,
// the order, the shipment.
func (s *server) recordBooking(ctx context.Context, r shipmentRead, sh store.OrdersShipment, b shipping.Booking, st ordersSettings,
	freight []int32, by actor) error {
	return s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if _, err := s.lockVariants(ctx, tx, freight); err != nil {
			return err
		}
		o, err := lockOrder(ctx, txq, r.o.ID)
		if err != nil {
			return err
		}
		cur, err := txq.LockShipment(ctx, sh.ID)
		if err != nil {
			return fmt.Errorf("orders: lock shipment %d: %w", sh.ID, err)
		}
		if cur.Status != shipmentDraft {
			// Another request recorded it first.
			return nil
		}
		now := s.deps.Clock()
		// A booking found again by its reference has no label link: Bring
		// gives none by consignment number, so the label is Mybring's.
		hasLabel := slices.ContainsFunc(b.Documents, func(d shipping.DocumentRef) bool { return d.Kind == shipping.DocumentLabel })
		params := store.BookShipmentRowParams{
			ID: sh.ID, ConsignmentNumber: &b.ConsignmentNumber, TrackingUrl: textPtr(b.TrackingURL), DocumentRefs: encodeRefs(b.Documents),
			LabelInMybringOnly: b.Recovered && !hasLabel,
			BookedService:      ptr(cmpOr(b.Service, sh.Service)), BookedCostEstimate: true, Now: &now, ByUserID: by.id, ByDisplay: by.display,
		}
		if b.Cost != nil {
			params.BookedCost = numeric(moneyOf(*b.Cost), amountPlaces)
			params.BookedCostCurrency = &b.Cost.Currency
			params.BookedCostEstimate = b.CostEstimate
		}
		booked, err := txq.BookShipmentRow(ctx, params)
		if err != nil {
			return fmt.Errorf("orders: record shipment %d's booking: %w", sh.ID, err)
		}
		for i, n := range b.PackageNumbers {
			if n == "" {
				continue
			}
			if err := txq.SetPackageNumber(ctx, store.SetPackageNumberParams{ShipmentID: sh.ID, Position: int32(i + 1), PackageNumber: &n}); err != nil {
				return fmt.Errorf("orders: record shipment %d's package number: %w", sh.ID, err)
			}
		}
		if err := events.Publish(ctx, tx, moduleName, contracts.ShipmentBooked{
			ShipmentID: booked.ID, DeliveryID: r.d.ID, OrderID: o.ID, Carrier: r.account.Carrier, Service: *params.BookedService,
			ConsignmentNumber: b.ConsignmentNumber, BookedAt: now,
		}, now); err != nil {
			return err
		}
		if err := s.deliverShippingLines(ctx, tx, txq, o, b.ConsignmentNumber, by, now); err != nil {
			return err
		}
		if st.shippingNoticeOnBook {
			recipient := strings.TrimSpace(booked.RecipientEmail)
			if recipient == "" {
				recipient = deref(defaultRecipient(headerOfOrder(o)))
			}
			if recipient != "" {
				if _, err := txq.QueueShippingNotice(ctx, store.QueueShippingNoticeParams{
					ShipmentID: booked.ID, Recipient: truncate(recipient, 254), TriggerEventID: &booked.IdempotencyKey, CreatedAt: &now,
				}); err != nil {
					return fmt.Errorf("orders: queue shipment %d's notice: %w", booked.ID, err)
				}
			}
		}
		return nil
	})
}

// deliverShippingLines counts every shipping line of an order delivered —
// what is ordered less what is cancelled — when a shipment of it is booked
// (O53): a shipping line has no delivered quantity of its own. The order's
// progress is recomputed and the change logged; its version stays.
func (s *server) deliverShippingLines(ctx context.Context, tx pgx.Tx, txq *store.Queries, o store.OrdersOrder, consignment string, by actor, at time.Time) error {
	lines, err := linesOf(ctx, txq, ownerOrder, o.ID)
	if err != nil {
		return err
	}
	var actions []changeAction
	for i, l := range lines {
		if l.Kind != kindShipping || !l.Quantity.Valid {
			continue
		}
		full := new(big.Rat).Sub(ratOf(l.Quantity), ratOrZero(l.QtyCancelled))
		before := ratOrZero(l.QtyDelivered)
		if before.Cmp(full) >= 0 {
			continue
		}
		lines[i].QtyDelivered = numeric(full, quantityPlaces)
		if err := txq.SetLineBuckets(ctx, store.SetLineBucketsParams{
			ID: l.ID, QtyCancelled: l.QtyCancelled, QtyDelivered: lines[i].QtyDelivered, QtyInvoiced: l.QtyInvoiced,
		}); err != nil {
			return fmt.Errorf("orders: deliver shipping line %d: %w", l.ID, err)
		}
		id := l.ID
		actions = append(actions, changeAction{Kind: actionShippingDelivered, LineID: &id,
			From: ptr(before.FloatString(quantityPlaces)), To: ptr(consignment)})
	}
	if len(actions) == 0 {
		return nil
	}
	row, err := s.bumpProgress(ctx, tx, txq, o, lines, o.Version)
	if err != nil {
		return err
	}
	return logChange(ctx, txq, row.ID, row.Version, by, at, actions)
}

// storeShipmentDocuments fetches a booked shipment's label and waybill from
// the carrier and stores them, outside any transaction, each within
// documentTimeout; a failure is logged, and the first download fetches it.
func (s *server) storeShipmentDocuments(ctx context.Context, c shipping.Carrier, id int64) {
	q := store.New(s.deps.Pool)
	sh, err := q.GetShipment(ctx, id)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "orders: a booked shipment could not be read to store its documents", "shipment_id", id, "error", err.Error())
		return
	}
	pkgs, err := q.PackagesOf(ctx, []int64{id})
	if err != nil {
		return
	}
	for _, ref := range documentRefs(sh.DocumentRefs) {
		fctx, cancel := context.WithTimeout(ctx, documentTimeout)
		_, err := s.fetchShipmentDocument(fctx, c, sh, pkgs, ref.Kind)
		cancel()
		if err != nil {
			s.deps.Logger.WarnContext(ctx, "orders: a shipment's document could not be stored", "shipment_id", id, "kind", ref.Kind, "error", err.Error())
		}
	}
}

// fetchShipmentDocument fetches one document from the carrier, stores it and
// records it; a second store of the same kind keeps the first row.
func (s *server) fetchShipmentDocument(ctx context.Context, c shipping.Carrier, sh store.OrdersShipment, pkgs []store.OrdersShipmentPackage,
	kind shipping.DocumentKind) ([]byte, error) {
	doc, err := c.Label(ctx, bookingOf(sh, pkgs), kind, shipping.FormatPDF)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("shipment/%d/%s-%s.pdf", sh.ID, kind, deref(sh.ConsignmentNumber))
	if err := s.objectPut(ctx, key, bytes.NewReader(doc.Body), "application/pdf"); err != nil {
		return nil, fmt.Errorf("orders: store shipment %d's %s: %w", sh.ID, kind, err)
	}
	if _, err := store.New(s.deps.Pool).InsertShipmentDocument(ctx, store.InsertShipmentDocumentParams{
		ShipmentID: sh.ID, Kind: string(kind), StorageKey: key, Format: string(shipping.FormatPDF), Now: s.deps.Clock(),
	}); err != nil {
		return nil, fmt.Errorf("orders: record shipment %d's %s: %w", sh.ID, kind, err)
	}
	return doc.Body, nil
}

// CancelShipment Cancel a shipment
// (POST /api/v1/orders/shipments/{id}/cancel)
func (s *server) CancelShipment(ctx context.Context, req gen.CancelShipmentRequestObject) (gen.CancelShipmentResponseObject, error) {
	resp := shipmentAnswers(nil,
		func() gen.CancelShipmentResponseObject { return gen.CancelShipment404Response{} },
		func(p gen.OrdersProblem) gen.CancelShipmentResponseObject {
			return gen.CancelShipment409ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.OrdersProblem) gen.CancelShipmentResponseObject {
			return gen.CancelShipment503ApplicationProblemPlusJSONResponse(p)
		}, nil)
	r, err := readShipment(ctx, store.New(s.deps.Pool), req.Id)
	if err != nil {
		return answer(err, resp)
	}
	if r.sh.Version != req.Body.Version {
		return answer(versionConflict("shipment's version", r.sh.Version, req.Body.Version), resp)
	}
	if err := judgeFresh("shipment", r.sh.UpdatedAt, req.Body.ExpectedUpdatedAt); err != nil {
		return answer(err, resp)
	}
	voided := false
	switch r.sh.Status {
	case shipmentDraft:
		if leased(r.sh.BookAttemptedAt, s.deps.Clock()) {
			return answer(bookingInProgress(r.sh), resp)
		}
	case shipmentBooked:
		if caps, ok := carrierCapabilities(r.account.Carrier); ok && !caps.Cancel {
			// No cancel call to make: void it here, keys or no keys.
			voided = true
			break
		}
		c, err := s.carrierOf(r.account)
		if err != nil {
			return answer(carrierFailed(err, ""), resp)
		}
		outcome, err := c.Cancel(ctx, bookingOf(r.sh, r.pkgs), uuid.NewSHA1(r.sh.IdempotencyKey, []byte("cancel")).String())
		switch {
		case errors.Is(err, shipping.ErrTooLate) || (err == nil && outcome == shipping.CancelTooLate):
			return answer(pastCancel(r.sh), resp)
		case err != nil:
			return answer(carrierFailed(err, "The carrier did not answer the cancel. Try again later."), resp)
		}
		voided = outcome == shipping.CancelNotSupported
	case shipmentInTransit, shipmentDelivered:
		return answer(pastCancel(r.sh), resp)
	default:
		return answer(conflict(codeShipmentNotCancellable, "The shipment is cancelled", fmt.Sprintf("Shipment %d is already cancelled.", r.sh.ID)), resp)
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		cur, err := txq.LockShipment(ctx, r.sh.ID)
		if err != nil {
			return fmt.Errorf("orders: lock shipment %d: %w", r.sh.ID, err)
		}
		if cur.Version != r.sh.Version {
			return versionConflict("shipment's version", cur.Version, r.sh.Version)
		}
		if cur.Status == shipmentDraft && leased(cur.BookAttemptedAt, s.deps.Clock()) {
			return bookingInProgress(cur)
		}
		if _, err := txq.CancelShipmentRow(ctx, store.CancelShipmentRowParams{ID: cur.ID, Voided: voided, Now: ptr(s.deps.Clock())}); err != nil {
			return fmt.Errorf("orders: cancel shipment %d: %w", cur.ID, err)
		}
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	out, err := s.shipmentOut(ctx, r.sh.ID)
	if err != nil {
		return nil, err
	}
	return gen.CancelShipment200JSONResponse(out), nil
}

// pastCancel is the 409 of a shipment the carrier already holds.
func pastCancel(sh store.OrdersShipment) error {
	return conflict(codeShipmentPastCancel, "The carrier has the parcel",
		fmt.Sprintf("Shipment %s is with the carrier and cannot be cancelled; arrange a return instead.", deref(sh.ConsignmentNumber)))
}

// shipmentPDF answers a shipment's document with the headers every PDF carries.
type shipmentPDF struct {
	label       *gen.GetShipmentLabel200ApplicationpdfResponse
	waybill     *gen.GetShipmentWaybill200ApplicationpdfResponse
	disposition string
}

func (r shipmentPDF) VisitGetShipmentLabelResponse(w http.ResponseWriter) error {
	pdfHeaders(w, r.disposition)
	return r.label.VisitGetShipmentLabelResponse(w)
}

func (r shipmentPDF) VisitGetShipmentWaybillResponse(w http.ResponseWriter) error {
	pdfHeaders(w, r.disposition)
	return r.waybill.VisitGetShipmentWaybillResponse(w)
}

// shipmentDocument is a booked shipment's stored document of kind, fetched
// from the carrier and stored now when its booking could not; nil when there
// is none to have. An anonymised order's shipment has none: its documents
// print the recipient, and they went with the erase (O29).
func (s *server) shipmentDocument(ctx context.Context, id int64, kind shipping.DocumentKind) ([]byte, store.OrdersShipment, error) {
	q := store.New(s.deps.Pool)
	r, err := readShipment(ctx, q, id)
	if err != nil {
		return nil, r.sh, err
	}
	if r.o.AnonymisedAt != nil {
		return nil, r.sh, notFound()
	}
	docs, err := q.DocumentsOfShipments(ctx, []int64{id})
	if err != nil {
		return nil, r.sh, fmt.Errorf("orders: read shipment %d's documents: %w", id, err)
	}
	for _, d := range docs {
		if d.Kind != string(kind) {
			continue
		}
		rc, err := s.objectGet(ctx, d.StorageKey)
		if err != nil {
			return nil, r.sh, fmt.Errorf("orders: read shipment %d's %s: %w", id, kind, err)
		}
		defer func() { _ = rc.Close() }()
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			return nil, r.sh, fmt.Errorf("orders: read shipment %d's %s: %w", id, kind, err)
		}
		return buf.Bytes(), r.sh, nil
	}
	if r.sh.ConsignmentNumber == nil {
		return nil, r.sh, nil
	}
	has := false
	for _, ref := range documentRefs(r.sh.DocumentRefs) {
		has = has || ref.Kind == kind
	}
	if !has {
		return nil, r.sh, nil
	}
	c, err := s.carrierOf(r.account)
	if err != nil {
		return nil, r.sh, nil
	}
	body, err := s.fetchShipmentDocument(ctx, c, r.sh, r.pkgs, kind)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "orders: a shipment's document could not be fetched", "shipment_id", id, "kind", kind, "error", err.Error())
		return nil, r.sh, nil
	}
	return body, r.sh, nil
}

// GetShipmentLabel Download a shipment's label
// (GET /api/v1/orders/shipments/{id}/label)
func (s *server) GetShipmentLabel(ctx context.Context, req gen.GetShipmentLabelRequestObject) (gen.GetShipmentLabelResponseObject, error) {
	body, sh, err := s.shipmentDocument(ctx, req.Id, shipping.DocumentLabel)
	if err != nil {
		return answer(err, answers[gen.GetShipmentLabelResponseObject]{
			notFound: func() gen.GetShipmentLabelResponseObject { return gen.GetShipmentLabel404Response{} },
		})
	}
	if body == nil {
		return gen.GetShipmentLabel404Response{}, nil
	}
	resp := gen.GetShipmentLabel200ApplicationpdfResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))}
	return shipmentPDF{label: &resp, disposition: fmt.Sprintf("inline; filename=%q", fmt.Sprintf("label-%s.pdf", deref(sh.ConsignmentNumber)))}, nil
}

// GetShipmentWaybill Download a shipment's waybill
// (GET /api/v1/orders/shipments/{id}/waybill)
//
// For a service with no waybill of its own, the label serves (O52).
func (s *server) GetShipmentWaybill(ctx context.Context, req gen.GetShipmentWaybillRequestObject) (gen.GetShipmentWaybillResponseObject, error) {
	nf := answers[gen.GetShipmentWaybillResponseObject]{
		notFound: func() gen.GetShipmentWaybillResponseObject { return gen.GetShipmentWaybill404Response{} },
	}
	body, sh, err := s.shipmentDocument(ctx, req.Id, shipping.DocumentWaybill)
	if err != nil {
		return answer(err, nf)
	}
	if body == nil {
		if body, sh, err = s.shipmentDocument(ctx, req.Id, shipping.DocumentLabel); err != nil {
			return answer(err, nf)
		}
	}
	if body == nil {
		return gen.GetShipmentWaybill404Response{}, nil
	}
	resp := gen.GetShipmentWaybill200ApplicationpdfResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))}
	return shipmentPDF{waybill: &resp, disposition: fmt.Sprintf("inline; filename=%q", fmt.Sprintf("fraktbrev-%s.pdf", deref(sh.ConsignmentNumber)))}, nil
}
