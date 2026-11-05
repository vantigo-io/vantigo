package orders_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/shipping/bring/bringtest"
)

// Delivery D (orders design O49–O53): carrier accounts with sealed keys,
// shipments of posted deliveries booked once under their key, labels, the
// tracking worker, the shipping notice, the packing slip's shipments and the
// shipping line with its estimate — against bringtest, an httptest Bring.

const (
	carrierAccountsPath = "/api/v1/orders/carrier-accounts"
	siteDefaultsPath    = "/api/v1/orders/shipping/site-defaults"
	estimatePath        = "/api/v1/orders/shipping-estimate"

	// varFreight is the freight product: a Service variant.
	varFreight int32 = 106
)

// shippingKeys are the keys of a caller who sells, delivers, ships and keeps
// the carrier accounts and the settings.
var shippingKeys = []string{"orders:access", "orders:carriers-manage", "orders:deliveries-manage", "orders:quotes-manage",
	"orders:sales-confirm", "orders:sales-manage", "orders:settings"}

func shipmentPath(id int64, rest ...string) string {
	p := fmt.Sprintf("/api/v1/orders/shipments/%d", id)
	for _, r := range rest {
		p += "/" + r
	}
	return p
}

func shipmentOf(t *testing.T, res *modtest.Response) gen.OrdersShipment {
	t.Helper()
	var sh gen.OrdersShipment
	res.JSON(&sh)
	return sh
}

// addAccount adds a Bring account in test mode on bringtest's keys.
func addAccount(t *testing.T, c *modtest.Client, name string) gen.OrdersCarrierAccount {
	t.Helper()
	res := c.Do(http.MethodPost, carrierAccountsPath, gen.OrdersCarrierAccountInput{
		Carrier: gen.OrdersCarrierAccountInputCarrierBring, Name: name, Test: true,
		CustomerNumbers: []string{bringtest.CustomerParcels, bringtest.CustomerCargo},
		Credentials:     &gen.OrdersCarrierCredentials{Uid: bringtest.UID, Key: bringtest.Key},
		DefaultService:  str("5800"),
	})
	mustStatus(t, res, http.StatusCreated, "add a carrier account")
	var a gen.OrdersCarrierAccount
	res.JSON(&a)
	return a
}

// editSettings saves the settings with edit applied.
func editSettings(t *testing.T, c *modtest.Client, edit func(*gen.OrdersSettingsInput)) gen.OrdersSettings {
	t.Helper()
	res := c.Do(http.MethodGet, settingsPath, nil)
	mustStatus(t, res, http.StatusOK, "settings")
	var st gen.OrdersSettings
	res.JSON(&st)
	in := gen.OrdersSettingsInput{
		Revision: st.Revision, QuoteSeriesStart: st.QuoteSeriesStart, OrderSeriesStart: st.OrderSeriesStart,
		QuoteValidityDays: st.QuoteValidityDays, DefaultPaymentTermsDays: st.DefaultPaymentTermsDays,
		ConfirmationOnConfirm: st.ConfirmationOnConfirm, CombineByDefault: st.CombineByDefault, MailTexts: st.MailTexts,
		DefaultSiteId: st.DefaultSiteId, SendChannelId: st.SendChannelId, FreightVariantId: st.FreightVariantId,
	}
	edit(&in)
	res = c.Do(http.MethodPut, settingsPath, in)
	mustStatus(t, res, http.StatusOK, "save the settings")
	res.JSON(&st)
	return st
}

// The keys are sealed and never answered; Verify reads what the keys may
// do; an archived account keeps its row; the default account per site.
func TestCarrierAccounts_SealedKeysVerifyAndDefaults(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true, bring: true})
	c := h.SignIn(t, shippingKeys...)
	a := addAccount(t, c, "Bring test")
	if !a.HasCredentials || !a.CarrierAllowed || a.VerifiedAt != nil || a.Status != gen.OrdersCarrierAccountStatusActive {
		t.Fatalf("account %+v", a)
	}
	for _, path := range []string{carrierAccountsPath, fmt.Sprintf("%s/%d", carrierAccountsPath, a.Id)} {
		res := c.Do(http.MethodGet, path, nil)
		mustStatus(t, res, http.StatusOK, path)
		if strings.Contains(string(res.Body), bringtest.Key) || strings.Contains(string(res.Body), bringtest.UID) {
			t.Errorf("%s answered the keys: %s", path, res.Body)
		}
	}
	var sealed []byte
	if err := h.Pool().QueryRow(context.Background(), `SELECT credentials FROM orders.carrier_accounts WHERE id = $1`, a.Id).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(bringtest.Key)) || bytes.Contains(sealed, []byte(bringtest.UID)) || len(sealed) < 20 {
		t.Errorf("the stored keys are not sealed: %q", sealed)
	}

	// Create needs the keys; a property the contract does not name is refused.
	expectRefusal(t, c.Do(http.MethodPost, carrierAccountsPath, gen.OrdersCarrierAccountInput{
		Carrier: gen.OrdersCarrierAccountInputCarrierBring, Name: "No keys", CustomerNumbers: []string{"5"},
	}), http.StatusBadRequest, "", "no keys")
	expectRefusal(t, c.Do(http.MethodPost, carrierAccountsPath, map[string]any{
		"carrier": "bring", "name": "Odd", "customerNumbers": []string{"5"}, "test": true, "credentials": map[string]any{"uid": "u", "key": "k", "secret": "x"},
	}), http.StatusBadRequest, "", "an unknown property")
	expectRefusal(t, c.Do(http.MethodPost, carrierAccountsPath, gen.OrdersCarrierAccountInput{
		Carrier: gen.OrdersCarrierAccountInputCarrierBring, Name: "bring TEST", CustomerNumbers: []string{"5"},
		Credentials: &gen.OrdersCarrierCredentials{Uid: "u", Key: "k"},
	}), http.StatusConflict, "carrier_account_name_taken", "a name in use")
	// Only a holder of orders:carriers-manage adds one.
	mustStatus(t, h.SignIn(t, "orders:access", "orders:deliveries-manage").Do(http.MethodPost, carrierAccountsPath, gen.OrdersCarrierAccountInput{
		Carrier: gen.OrdersCarrierAccountInputCarrierBring, Name: "Sneaky", CustomerNumbers: []string{"5"},
		Credentials: &gen.OrdersCarrierCredentials{Uid: "u", Key: "k"},
	}), http.StatusForbidden, "add without the key")

	verify := func() gen.OrdersCarrierVerification {
		t.Helper()
		res := c.Do(http.MethodPost, fmt.Sprintf("%s/%d/verify", carrierAccountsPath, a.Id), nil)
		mustStatus(t, res, http.StatusOK, "verify")
		var v gen.OrdersCarrierVerification
		res.JSON(&v)
		return v
	}
	if v := verify(); v.Result != gen.Ok || !v.PricesAvailable || len(v.Services) < 9 {
		t.Errorf("verify %+v", v)
	}
	h.bring.SetNoFinancialRole(true)
	if v := verify(); v.Result != gen.Ok || v.PricesAvailable || len(v.Notes) == 0 {
		t.Errorf("verify without the financial role %+v", v)
	}
	h.bring.SetNoFinancialRole(false)

	// Wrong keys replace the stored ones and are refused; a save without keys
	// keeps them.
	get := func() gen.OrdersCarrierAccount {
		t.Helper()
		res := c.Do(http.MethodGet, fmt.Sprintf("%s/%d", carrierAccountsPath, a.Id), nil)
		var out gen.OrdersCarrierAccount
		res.JSON(&out)
		return out
	}
	a = get()
	if a.VerifiedAt == nil {
		t.Error("a passed Verify was not recorded")
	}
	put := func(in gen.OrdersCarrierAccountInput) gen.OrdersCarrierAccount {
		t.Helper()
		res := c.Do(http.MethodPut, fmt.Sprintf("%s/%d", carrierAccountsPath, a.Id), in)
		mustStatus(t, res, http.StatusOK, "save the account")
		var out gen.OrdersCarrierAccount
		res.JSON(&out)
		return out
	}
	base := gen.OrdersCarrierAccountInput{Carrier: gen.OrdersCarrierAccountInputCarrierBring, Name: a.Name, Test: true,
		CustomerNumbers: a.CustomerNumbers, DefaultService: str("5800")}
	in := base
	in.Version, in.Credentials = &a.Version, &gen.OrdersCarrierCredentials{Uid: bringtest.UID, Key: "wrong"}
	a = put(in)
	if a.VerifiedAt != nil {
		t.Error("new keys kept verifiedAt")
	}
	if v := verify(); v.Result != gen.Unauthorized {
		t.Errorf("verify with wrong keys %+v", v)
	}
	in = base
	in.Version, in.Credentials = &a.Version, &gen.OrdersCarrierCredentials{Uid: bringtest.UID, Key: bringtest.Key}
	a = put(in)
	in = base
	in.Version, in.Sender = &a.Version, &gen.OrdersCarrierSender{Name: str("Lager AS"), PostalCode: str("0150"), City: str("OSLO")}
	a = put(in)
	if v := verify(); v.Result != gen.Ok {
		t.Errorf("a save without keys did not keep them: %+v", v)
	}
	stale := base
	stale.Version = ptrI32(a.Version - 1)
	expectRefusal(t, c.Do(http.MethodPut, fmt.Sprintf("%s/%d", carrierAccountsPath, a.Id), stale), http.StatusConflict, "version_conflict", "stale")

	res := c.Do(http.MethodGet, fmt.Sprintf("%s/%d/services", carrierAccountsPath, a.Id), nil)
	mustStatus(t, res, http.StatusOK, "services")
	var services gen.OrdersCarrierServiceList
	res.JSON(&services)
	if len(services.Items) < 9 || services.Items[0].Code != "5800" || !services.Items[0].PickupPoint {
		t.Errorf("services %+v", services.Items)
	}
	res = c.Do(http.MethodGet, fmt.Sprintf("%s/%d/pickup-points?postalCode=0150", carrierAccountsPath, a.Id), nil)
	mustStatus(t, res, http.StatusOK, "pickup points")
	var points gen.OrdersPickupPointList
	res.JSON(&points)
	if len(points.Items) != 2 || !points.Items[1].Locker {
		t.Errorf("pickup points %+v", points.Items)
	}

	// The default account per site.
	res = c.Do(http.MethodPut, siteDefaultsPath, gen.OrdersSiteCarrierDefaults{Items: []gen.OrdersSiteCarrierDefault{{SiteId: siteOslo, AccountId: a.Id}}})
	mustStatus(t, res, http.StatusOK, "site defaults")
	res = c.Do(http.MethodGet, siteDefaultsPath, nil)
	var defaults gen.OrdersSiteCarrierDefaults
	res.JSON(&defaults)
	if len(defaults.Items) != 1 || defaults.Items[0].AccountId != a.Id {
		t.Errorf("site defaults %+v", defaults.Items)
	}
	expectRefusal(t, c.Do(http.MethodPut, siteDefaultsPath, gen.OrdersSiteCarrierDefaults{Items: []gen.OrdersSiteCarrierDefault{{SiteId: siteOslo, AccountId: 999}}}),
		http.StatusBadRequest, "", "an unknown account")

	// Archived: kept, not offered.
	in = base
	in.Version, in.Status = &a.Version, ptr(gen.OrdersCarrierAccountInputStatusArchived)
	if a = put(in); a.Status != gen.OrdersCarrierAccountStatusArchived {
		t.Errorf("archive %+v", a)
	}
}

func ptrI32(v int32) *int32 { return &v }

func ptr[T any](v T) *T { return &v }

// shippedOrder is a confirmed order of four bolts (250 g each) and a shipping
// line, delivered in full by a posted delivery from Oslo.
func shippedOrder(t *testing.T, h *harness, c *modtest.Client) (gen.OrdersOrder, gen.OrdersDelivery) {
	t.Helper()
	bolt, _ := h.catalog.Variant(context.Background(), varBolt)
	bolt.WeightGrams = ptr(int64(250))
	h.catalog.seed(*bolt)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "10")
	freight := line(gen.OrdersLineInputKindShipping)
	freight.Name, freight.UnitPrice = str("Frakt"), f64(99)
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 4), freight))
	res := c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id})
	mustStatus(t, res, http.StatusCreated, "create delivery")
	d := deliveryOf(t, res)
	expectRefusal(t, c.Do(http.MethodPost, deliveryPath(d.Id, "shipments"), gen.OrdersShipmentInput{}), http.StatusConflict,
		"delivery_not_posted", "a draft delivery")
	res = c.Do(http.MethodPost, deliveryPath(d.Id, "post"), gen.OrdersVersionInput{Version: d.Version, ExpectedUpdatedAt: d.UpdatedAt})
	mustStatus(t, res, http.StatusOK, "post")
	return getOrder(t, c, o.Id), deliveryOf(t, res)
}

func book(c *modtest.Client, sh gen.OrdersShipment) *modtest.Response {
	return c.Do(http.MethodPost, shipmentPath(sh.Id, "book"), gen.OrdersVersionInput{Version: sh.Version, ExpectedUpdatedAt: sh.UpdatedAt})
}

// A shipment is proposed from the delivery, priced, refused with the
// carrier's words, booked once under its key — a retry after an unknown
// outcome finds the first booking — its label stored; the shipping line
// counts delivered, the shipping notice is queued once and sent, the packing
// slip prints the consignment, tracking moves it on a pool of one, and the
// cancel outcomes.
func TestShipments_BookOnceTrackAndNotice(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true, bring: true, communications: true})
	c := h.SignIn(t, shippingKeys...)
	o, d := shippedOrder(t, h, c)
	editSettings(t, c, func(in *gen.OrdersSettingsInput) { in.SendChannelId = &h.channel })
	expectRefusal(t, c.Do(http.MethodPost, deliveryPath(d.Id, "shipments"), gen.OrdersShipmentInput{}), http.StatusConflict,
		"no_carrier_account", "no account yet")
	a := addAccount(t, c, "Bring")

	res := c.Do(http.MethodPost, deliveryPath(d.Id, "shipments"), gen.OrdersShipmentInput{})
	mustStatus(t, res, http.StatusCreated, "create shipment")
	sh := shipmentOf(t, res)
	if sh.Status != gen.OrdersShipmentStatusDraft || sh.CarrierAccountId != a.Id || sh.Service != "5800" || len(sh.Packages) != 1 ||
		sh.Packages[0].WeightGrams != 1000 || sh.Recipient.Email != "faktura@acme.example" || !sh.Test {
		t.Fatalf("proposed shipment %+v", sh)
	}

	// Two packages, priced.
	res = c.Do(http.MethodPut, shipmentPath(sh.Id), gen.OrdersShipmentUpdate{
		Version: sh.Version, ExpectedUpdatedAt: sh.UpdatedAt, CarrierAccountId: a.Id, Service: "5800",
		Packages:       []gen.OrdersShipmentPackageInput{{WeightGrams: 600, LengthMm: ptrI32(300)}, {WeightGrams: 400}},
		RecipientEmail: str("test@example.invalid"), RecipientPhone: str("+4700000000"),
	})
	mustStatus(t, res, http.StatusOK, "save shipment")
	sh = shipmentOf(t, res)
	expectRefusal(t, c.Do(http.MethodPut, shipmentPath(sh.Id), gen.OrdersShipmentUpdate{
		Version: sh.Version, ExpectedUpdatedAt: sh.UpdatedAt, CarrierAccountId: a.Id, Service: "9650",
		Packages: []gen.OrdersShipmentPackageInput{{WeightGrams: 1}},
	}), http.StatusBadRequest, "service_unknown", "a withdrawn product")
	res = c.Do(http.MethodGet, shipmentPath(sh.Id, "rates"), nil)
	mustStatus(t, res, http.StatusOK, "rates")
	var rates gen.OrdersShippingOptionList
	res.JSON(&rates)
	if len(rates.Items) < 2 || rates.Items[0].Price != 88 || rates.Items[0].Vat != 22 || !rates.Items[0].Estimate || rates.Items[0].Currency != "NOK" {
		t.Errorf("rates %+v", rates.Items)
	}

	// The carrier refuses: its words, still a draft, nothing booked.
	h.bring.Fail(bringtest.OpBook, bringtest.Failure{Status: 400, Times: 1,
		Body: `{"consignments":[{"errors":[{"code":"X1","messages":[{"lang":"en","message":"The recipient's address is incomplete."}]}]}]}`})
	p := expectRefusal(t, book(c, sh), http.StatusUnprocessableEntity, "carrier_refused", "refused")
	if p.Detail == nil || *p.Detail != "The recipient's address is incomplete." {
		t.Errorf("refusal %+v", p)
	}
	sh = shipmentOf(t, c.Do(http.MethodGet, shipmentPath(sh.Id), nil))
	if sh.Status != gen.OrdersShipmentStatusDraft || len(h.bring.Consignments()) != 0 {
		t.Fatalf("after a refusal %s, %d bookings", sh.Status, len(h.bring.Consignments()))
	}

	res = book(c, sh)
	mustStatus(t, res, http.StatusOK, "book")
	sh = shipmentOf(t, res)
	if sh.Status != gen.OrdersShipmentStatusBooked || sh.ConsignmentNumber == nil || sh.Packages[1].PackageNumber == nil ||
		sh.TrackingUrl == nil || !sh.Label || sh.Waybill || sh.WaybillNeeded {
		t.Fatalf("booked %+v", sh)
	}
	booked := h.bring.Consignments()
	if len(booked) != 1 || booked[0].WeightsKg[0] != "0.600" || !booked[0].Test {
		t.Fatalf("bookings %+v", booked)
	}
	var key string
	if err := h.Pool().QueryRow(context.Background(), `SELECT idempotency_key::text FROM orders.shipments WHERE id = $1`, sh.Id).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if booked[0].CorrelationID != key {
		t.Errorf("booked under %s, the shipment's key is %s", booked[0].CorrelationID, key)
	}
	// A second submit of the same booking books nothing.
	expectRefusal(t, book(c, sh), http.StatusConflict, "", "book again")
	if len(h.bring.Consignments()) != 1 {
		t.Error("a second submit booked again")
	}
	res = c.Do(http.MethodGet, shipmentPath(sh.Id, "label"), nil)
	mustStatus(t, res, http.StatusOK, "label")
	if !bytes.HasPrefix(res.Body, []byte("%PDF")) {
		t.Errorf("label %q", res.Body)
	}
	// No waybill of its own: the label serves.
	mustStatus(t, c.Do(http.MethodGet, shipmentPath(sh.Id, "waybill"), nil), http.StatusOK, "waybill")

	// The shipping line counts delivered; the event and the notice, once.
	o = getOrder(t, c, o.Id)
	for _, l := range o.Lines {
		if l.Kind == gen.OrdersLineKindShipping && l.QtyDelivered != 1 {
			t.Errorf("shipping line delivered %v, want 1", l.QtyDelivered)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM platform.events WHERE name = 'orders.shipment.booked'`); n != 1 {
		t.Errorf("%d booked events, want 1", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.mailings WHERE kind = 'shipping_notice' AND owner_kind = 'shipment' AND status = 'queued'`); n != 1 {
		t.Errorf("%d shipping notices queued, want 1", n)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if n, err := orders.RunMailCycle(ctx, h.Deps()); err != nil || n != 1 {
		t.Fatalf("mail cycle %d, %v", n, err)
	}
	mails := h.mail.Sent()
	if len(mails) != 1 || !strings.Contains(mails[0].Out.Subject, fmt.Sprintf("Ordre %d er sendt", o.Number)) ||
		!strings.Contains(mails[0].Out.TextBody, *sh.ConsignmentNumber) || !strings.Contains(mails[0].Out.TextBody, *sh.TrackingUrl) ||
		mails[0].Out.To[0] != "test@example.invalid" || len(mails[0].Out.Attachments) != 0 {
		var why string
		_ = h.Pool().QueryRow(context.Background(), `SELECT coalesce(last_error, '') FROM orders.mailings WHERE kind = 'shipping_notice'`).Scan(&why)
		t.Fatalf("notice %+v (%s)", mails, why)
	}

	// The packing slip prints the consignment.
	mustStatus(t, c.Do(http.MethodGet, deliveryPath(d.Id, "packing-slip"), nil), http.StatusOK, "packing slip")
	m, ok := pdfModels.find("Pakkseddel", fmt.Sprintf("Leveranse %d – Ordre %d", d.Number, o.Number))
	found := false
	for _, kv := range m.Meta {
		found = found || (kv[0] == "Sending" && strings.Contains(kv[1], *sh.ConsignmentNumber) && strings.Contains(kv[1], "Bring"))
	}
	if !ok || !found {
		t.Errorf("packing slip meta %+v", m.Meta)
	}

	// A second shipment whose answer is lost after Bring booked: the retry
	// asks Bring first and records the first booking.
	res = c.Do(http.MethodPost, deliveryPath(d.Id, "shipments"), gen.OrdersShipmentInput{CarrierAccountId: &a.Id, Service: str("5000")})
	mustStatus(t, res, http.StatusCreated, "second shipment")
	second := shipmentOf(t, res)
	h.bring.Fail(bringtest.OpBook, bringtest.Failure{AfterBook: true, Status: 502, Body: "<html>bad gateway</html>", Times: 1})
	expectRefusal(t, book(c, second), http.StatusServiceUnavailable, "carrier_unreachable", "the answer lost")
	second = shipmentOf(t, c.Do(http.MethodGet, shipmentPath(second.Id), nil))
	if second.Status != gen.OrdersShipmentStatusDraft || !second.BookAttempted || len(h.bring.Consignments()) != 2 {
		t.Fatalf("after a lost answer %+v, %d bookings", second, len(h.bring.Consignments()))
	}
	res = book(c, second)
	mustStatus(t, res, http.StatusOK, "book again")
	second = shipmentOf(t, res)
	if len(h.bring.Consignments()) != 2 || second.ConsignmentNumber == nil || *second.ConsignmentNumber != h.bring.Consignments()[1].Number {
		t.Fatalf("the retry booked again: %d bookings, %+v", len(h.bring.Consignments()), second)
	}

	// Tracking on a pool of one: in transit, then delivered.
	day := h.Now()
	h.bring.SetEvents(*sh.ConsignmentNumber,
		bringtest.Event{Status: "PRE_NOTIFIED", Description: "Pre-notified", At: day.Add(time.Minute)},
		bringtest.Event{Status: "IN_TRANSIT", Description: "On its way", City: "OSLO", PostalCode: "0150", At: day.Add(2 * time.Hour)})
	h.Advance(31 * time.Minute)
	one, err := db.Open(ctx, h.Pool().Config().ConnString(), db.WithMaxConns(1))
	if err != nil {
		t.Fatalf("open a pool of one: %v", err)
	}
	defer one.Close()
	if n, err := orders.RunTrackingCycle(ctx, h.DepsOn(one)); err != nil || n != 2 {
		t.Fatalf("tracking cycle %d, %v; want both shipments polled", n, err)
	}
	if n, _ := orders.RunTrackingCycle(ctx, h.DepsOn(one)); n != 0 {
		t.Errorf("a second cycle polled %d, want none before the interval", n)
	}
	sh = shipmentOf(t, c.Do(http.MethodGet, shipmentPath(sh.Id), nil))
	if sh.Status != gen.OrdersShipmentStatusInTransit || len(sh.Events) != 2 || sh.Events[0].Code != "IN_TRANSIT" || sh.Events[0].Place != "0150 OSLO" {
		for _, r := range h.bring.Requests() {
			t.Logf("%s %s?%s", r.Method, r.Path, r.Query)
		}
		t.Fatalf("tracked %+v", sh)
	}
	h.bring.SetEvents(*sh.ConsignmentNumber, bringtest.Event{Status: "DEVIATION", Description: "Damaged", At: day.Add(3 * time.Hour)})
	h.Advance(31 * time.Minute)
	if _, err := orders.RunTrackingCycle(ctx, h.DepsOn(one)); err != nil {
		t.Fatal(err)
	}
	if sh = shipmentOf(t, c.Do(http.MethodGet, shipmentPath(sh.Id), nil)); !sh.Exception || sh.Status != gen.OrdersShipmentStatusInTransit {
		t.Errorf("after a deviation %s exception %v", sh.Status, sh.Exception)
	}
	h.bring.SetEvents(*sh.ConsignmentNumber, bringtest.Event{Status: "DELIVERED", Description: "Delivered", At: day.Add(5 * time.Hour)})
	h.Advance(31 * time.Minute)
	if _, err := orders.RunTrackingCycle(ctx, h.DepsOn(one)); err != nil {
		t.Fatal(err)
	}
	sh = shipmentOf(t, c.Do(http.MethodGet, shipmentPath(sh.Id), nil))
	if sh.Status != gen.OrdersShipmentStatusDelivered || sh.Exception || len(sh.Events) != 4 {
		t.Errorf("delivered %s exception %v events %d", sh.Status, sh.Exception, len(sh.Events))
	}
	if n := h.Count(t, `SELECT count(*) FROM platform.events WHERE name = 'orders.shipment.delivered'`); n != 1 {
		t.Errorf("%d delivered events, want 1", n)
	}

	// Cancel: delivered is too late; Bring has no cancel, so a booked one is
	// voided; a draft is cancelled outright.
	expectRefusal(t, c.Do(http.MethodPost, shipmentPath(sh.Id, "cancel"), gen.OrdersVersionInput{Version: sh.Version, ExpectedUpdatedAt: sh.UpdatedAt}),
		http.StatusConflict, "shipment_past_cancel", "cancel delivered")
	second = shipmentOf(t, c.Do(http.MethodGet, shipmentPath(second.Id), nil))
	res = c.Do(http.MethodPost, shipmentPath(second.Id, "cancel"), gen.OrdersVersionInput{Version: second.Version, ExpectedUpdatedAt: second.UpdatedAt})
	mustStatus(t, res, http.StatusOK, "cancel booked")
	if second = shipmentOf(t, res); second.Status != gen.OrdersShipmentStatusCancelled || !second.Voided {
		t.Errorf("cancelled %s voided %v", second.Status, second.Voided)
	}
	res = c.Do(http.MethodPost, deliveryPath(d.Id, "shipments"), gen.OrdersShipmentInput{})
	draft := shipmentOf(t, res)
	res = c.Do(http.MethodPost, shipmentPath(draft.Id, "cancel"), gen.OrdersVersionInput{Version: draft.Version, ExpectedUpdatedAt: draft.UpdatedAt})
	mustStatus(t, res, http.StatusOK, "cancel draft")
	if draft = shipmentOf(t, res); draft.Status != gen.OrdersShipmentStatusCancelled || draft.Voided {
		t.Errorf("cancelled draft %s voided %v", draft.Status, draft.Voided)
	}

	var list gen.OrdersShipmentList
	c.Do(http.MethodGet, deliveryPath(d.Id, "shipments"), nil).JSON(&list)
	if len(list.Items) != 3 {
		t.Errorf("delivery shipments %d, want 3", len(list.Items))
	}
	c.Do(http.MethodGet, orderPath(o.Id, "shipments"), nil).JSON(&list)
	if len(list.Items) != 3 || list.Items[0].TrackingUrl == nil || list.Items[0].DeliveryNumber != d.Number {
		t.Errorf("order shipments %+v", list.Items)
	}
}

// The shipping line (O53): the estimate from the carrier with the markup and
// the freight product's name and VAT; carrier as its price source until a
// price is typed over it; quantity 1; quotes and orders only.
func TestShippingLine_EstimateAndMarkup(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true, bring: true})
	c := h.SignIn(t, shippingKeys...)
	bolt, _ := h.catalog.Variant(context.Background(), varBolt)
	bolt.WeightGrams = ptr(int64(250))
	freightVariant := contracts.VariantEntry{ID: varFreight, ProductID: varFreight, ProductName: "Frakt", SKU: "FRAKT", Unit: "stk",
		ProductType: "Service", ProductStatus: "Active", TaxCategoryID: catStandard}
	h.catalog.seed(*bolt, freightVariant)
	h.catalog.setPrice(varFreight, "NOK", "150")
	setDefaultSite(t, c, siteOslo)
	a := addAccount(t, c, "Bring")
	mustStatus(t, c.Do(http.MethodPut, siteDefaultsPath, gen.OrdersSiteCarrierDefaults{Items: []gen.OrdersSiteCarrierDefault{{SiteId: siteOslo, AccountId: a.Id}}}),
		http.StatusOK, "site default")

	// The freight product must be a service.
	res := c.Do(http.MethodGet, settingsPath, nil)
	var st gen.OrdersSettings
	res.JSON(&st)
	if !st.ShippingNoticeOnBook || st.FreightMarkupPercent != 0 {
		t.Errorf("shipping settings default %+v", st)
	}
	expectRefusal(t, c.Do(http.MethodPut, settingsPath, gen.OrdersSettingsInput{
		Revision: st.Revision, QuoteSeriesStart: 1, OrderSeriesStart: 1, QuoteValidityDays: 30, DefaultPaymentTermsDays: 14,
		MailTexts: []gen.OrdersMailText{}, DefaultSiteId: st.DefaultSiteId, FreightVariantId: i32(varBolt),
	}), http.StatusBadRequest, "freight_product_invalid", "goods as freight")
	st = editSettings(t, c, func(in *gen.OrdersSettingsInput) {
		in.FreightVariantId, in.FreightMarkupPercent, in.ShippingNoticeOnBook = i32(varFreight), f64(10), ptr(false)
	})
	if st.FreightVariantId == nil || *st.FreightVariantId != varFreight || st.FreightMarkupPercent != 10 || st.ShippingNoticeOnBook {
		t.Fatalf("shipping settings %+v", st)
	}

	q := createQuote(t, c, custBusiness, productLine(varBolt, 4))
	expectRefusal(t, c.Do(http.MethodPost, estimatePath, gen.OrdersShippingEstimateInput{QuoteId: &q.Id, OrderId: ptrI64(1)}),
		http.StatusBadRequest, "", "both ids")
	res = c.Do(http.MethodPost, estimatePath, gen.OrdersShippingEstimateInput{QuoteId: &q.Id})
	mustStatus(t, res, http.StatusOK, "estimate")
	var e gen.OrdersShippingEstimate
	res.JSON(&e)
	// 1 kg: list 110.00, net 88.00; + 10 % = 96.80.
	if e.WeightGrams != 1000 || e.CarrierPrice != 88 || e.MarkupPercent != 10 || e.NetPrice != 96.8 || e.UnitPrice != 96.8 ||
		e.Service != "5800" || e.VariantId == nil || *e.VariantId != varFreight || e.VatCodeId == nil || *e.VatCodeId != codeStandard ||
		e.Name != "Frakt" || e.AccountId != a.Id || !e.Estimate {
		t.Fatalf("estimate %+v", e)
	}

	// Saved as the carrier's price; typed over, manual; quantity 1 only.
	ship := line(gen.OrdersLineInputKindShipping)
	ship.VariantId, ship.UnitPrice, ship.PriceSource = e.VariantId, &e.UnitPrice, ptr(gen.OrdersLineInputPriceSourceCarrier)
	res = c.Do(http.MethodPut, quotePath(q.Id), quoteUpdate(q, append(keptLines(q), ship)))
	mustStatus(t, res, http.StatusOK, "save the shipping line")
	q = quoteOf(t, res)
	sl := q.Lines[len(q.Lines)-1]
	if sl.Kind != gen.OrdersLineKindShipping || sl.PriceSource == nil || *sl.PriceSource != gen.OrdersLinePriceSourceCarrier ||
		sl.Quantity == nil || *sl.Quantity != 1 || sl.VatCodeId == nil || *sl.VatCodeId != codeStandard || sl.LineNet != 96.8 {
		t.Fatalf("shipping line %+v", sl)
	}
	if q.Totals.Net != 4*100+96.8 {
		t.Errorf("quote net %v, want the goods and the freight", q.Totals.Net)
	}
	typed := gen.OrdersLineInput{Id: &sl.Id, Kind: gen.OrdersLineInputKindShipping, VariantId: sl.VariantId, UnitPrice: f64(120)}
	res = c.Do(http.MethodPut, quotePath(q.Id), quoteUpdate(q, append(keptLines(q)[:1], typed)))
	mustStatus(t, res, http.StatusOK, "type a price")
	q = quoteOf(t, res)
	if sl = q.Lines[1]; sl.PriceSource == nil || *sl.PriceSource != gen.OrdersLinePriceSourceManual {
		t.Errorf("typed shipping line source %v", sl.PriceSource)
	}
	two := line(gen.OrdersLineInputKindShipping)
	two.Name, two.Quantity, two.UnitPrice = str("Frakt"), f64(2), f64(10)
	expectRefusal(t, c.Do(http.MethodPut, quotePath(q.Id), quoteUpdate(q, append(keptLines(q), two))), http.StatusBadRequest,
		"quantity_invalid", "two of a shipping line")

	// An order's estimate reads its delivery address and site.
	o := createOrder(t, c, custBusiness, productLine(varBolt, 8))
	res = c.Do(http.MethodPost, estimatePath, gen.OrdersShippingEstimateInput{OrderId: &o.Id})
	mustStatus(t, res, http.StatusOK, "order estimate")
	res.JSON(&e)
	if e.WeightGrams != 2000 || e.CarrierPrice != 96 {
		t.Errorf("order estimate %+v", e)
	}
}

// keptLines are a quote's lines handed back as they are.
func keptLines(q gen.OrdersQuote) []gen.OrdersLineInput {
	out := make([]gen.OrdersLineInput, 0, len(q.Lines))
	for _, l := range q.Lines {
		id := l.Id
		out = append(out, gen.OrdersLineInput{Id: &id, Kind: gen.OrdersLineInputKind(l.Kind), VariantId: l.VariantId, Quantity: l.Quantity})
	}
	return out
}

// quoteUpdate is a save of q with lines.
func quoteUpdate(q gen.OrdersQuote, lines []gen.OrdersLineInput) gen.OrdersQuoteUpdate {
	return gen.OrdersQuoteUpdate{ExpectedUpdatedAt: q.UpdatedAt, Revision: q.Revision, CustomerId: custBusiness, Lines: lines}
}
