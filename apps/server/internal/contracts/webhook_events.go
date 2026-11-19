package contracts

import (
	"net/url"
	"strings"
)

// WebhookEvent is one domain event an integration's webhook endpoint may
// subscribe to (outgoing webhooks design W4, W5, W26). The catalogue is an
// allow-list: an event is exposed deliberately, with the resource a receiver
// fetches when it is told of one, never by "all events".
//
// A message carries only the resource's type, id and path (W12); the
// receiver fetches it with its API key, so the permission check stays the
// GET's. Permissions are exactly the permission keys of that GET's
// x-vantigo-access rule, all of them required, and an integration receives
// the event only while it holds every one (W7). internal/integration's
// TestWebhookEvents_CatalogueMatchesTheCode holds every entry to the
// composed contract and catalog.
type WebhookEvent struct {
	// Event is the event's zero value, which gives its name and version:
	// contracts has no event registry.
	Event Event
	// Version is the payload version the entry was written against; only
	// version 1 is exposed.
	Version int
	// Resource is the type a receiver sees, as sales_order.
	Resource string
	// IDField is the JSON field of the event's payload holding the
	// resource's id, as orderId.
	IDField string
	// Path is the REST path of the resource's GET, as the contract writes it,
	// with one path parameter that the id fills.
	Path string
	// Permissions are the permission keys the GET at Path requires.
	Permissions []string
}

// Name is the event's name, as orders.sales_order.confirmed.
func (w WebhookEvent) Name() string { return w.Event.EventName() }

// Module is the module that publishes the event, its name's first segment.
func (w WebhookEvent) Module() string {
	module, _, _ := strings.Cut(w.Name(), ".")
	return module
}

// Href is the path of the resource id names (outgoing webhooks design W12):
// Path with its one parameter, whatever its name, replaced by id escaped as
// a path segment. A Path with no parameter is answered as it is; the
// catalogue test holds every entry to exactly one.
func (w WebhookEvent) Href(id string) string {
	start := strings.IndexByte(w.Path, '{')
	end := strings.IndexByte(w.Path, '}')
	if start < 0 || end < start {
		return w.Path
	}
	return w.Path[:start] + url.PathEscape(id) + w.Path[end+1:]
}

// webhookEvents is the catalogue, grouped by module in the order the screen
// shows them. Left out on purpose (W5): communications.* (user ids and
// conversations), time.week.unsubmitted (a person's hours) and expenses.* (a
// person's claims), which concern people; and
// accounting.supplier_invoice.deleted, whose draft is deleted with it, so
// its GET can only answer 404 (W26).
var webhookEvents = []WebhookEvent{
	// Orders.
	{Event: QuoteSent{}, Version: 1, Resource: "quote", IDField: "quoteId", Path: "/api/v1/orders/quotes/{id}", Permissions: []string{"orders:access"}},
	{Event: QuoteAccepted{}, Version: 1, Resource: "quote", IDField: "quoteId", Path: "/api/v1/orders/quotes/{id}", Permissions: []string{"orders:access"}},
	{Event: SalesOrderConfirmed{}, Version: 1, Resource: "sales_order", IDField: "orderId", Path: "/api/v1/orders/orders/{id}", Permissions: []string{"orders:access"}},
	{Event: SalesOrderChanged{}, Version: 1, Resource: "sales_order", IDField: "orderId", Path: "/api/v1/orders/orders/{id}", Permissions: []string{"orders:access"}},
	{Event: SalesOrderCancelled{}, Version: 1, Resource: "sales_order", IDField: "orderId", Path: "/api/v1/orders/orders/{id}", Permissions: []string{"orders:access"}},
	{Event: SalesOrderInvoiced{}, Version: 1, Resource: "sales_order", IDField: "orderId", Path: "/api/v1/orders/orders/{id}", Permissions: []string{"orders:access"}},
	{Event: SubscriptionStarted{}, Version: 1, Resource: "subscription", IDField: "subscriptionId", Path: "/api/v1/orders/subscriptions/{id}", Permissions: []string{"orders:access"}},
	{Event: SubscriptionChanged{}, Version: 1, Resource: "subscription", IDField: "subscriptionId", Path: "/api/v1/orders/subscriptions/{id}", Permissions: []string{"orders:access"}},
	{Event: SubscriptionEnded{}, Version: 1, Resource: "subscription", IDField: "subscriptionId", Path: "/api/v1/orders/subscriptions/{id}", Permissions: []string{"orders:access"}},
	{Event: DeliveryPosted{}, Version: 1, Resource: "delivery", IDField: "deliveryId", Path: "/api/v1/orders/deliveries/{id}", Permissions: []string{"orders:access"}},
	{Event: ShipmentBooked{}, Version: 1, Resource: "shipment", IDField: "shipmentId", Path: "/api/v1/orders/shipments/{id}", Permissions: []string{"orders:access"}},
	{Event: ShipmentDelivered{}, Version: 1, Resource: "shipment", IDField: "shipmentId", Path: "/api/v1/orders/shipments/{id}", Permissions: []string{"orders:access"}},

	// Invoices. A credit note is a document GET /invoices/{id} serves, its
	// kind credit_note; a payment has no GET of its own, so its resource is
	// the invoice it was registered against (W26). A customer's credit is
	// read by the customer's id, which can be a private person's.
	{Event: DocumentIssued{}, Version: 1, Resource: "invoice", IDField: "documentId", Path: "/api/v1/invoices/{id}", Permissions: []string{"invoices:access"}},
	{Event: InvoiceOverdue{}, Version: 1, Resource: "invoice", IDField: "invoiceId", Path: "/api/v1/invoices/{id}", Permissions: []string{"invoices:access"}},
	{Event: PaymentReceived{}, Version: 1, Resource: "invoice", IDField: "invoiceId", Path: "/api/v1/invoices/{id}", Permissions: []string{"invoices:access"}},
	{Event: PaymentRemoved{}, Version: 1, Resource: "invoice", IDField: "invoiceId", Path: "/api/v1/invoices/{id}", Permissions: []string{"invoices:access"}},
	{Event: CustomerCreditChanged{}, Version: 1, Resource: "customer_credit", IDField: "customerId", Path: "/api/v1/invoices/customers/{customerId}/credit", Permissions: []string{"invoices:access"}},

	// Purchasing.
	{Event: PurchaseOrderSent{}, Version: 1, Resource: "purchase_order", IDField: "orderId", Path: "/api/v1/purchasing/orders/{orderId}", Permissions: []string{"purchasing:access"}},
	{Event: PurchaseOrderClosed{}, Version: 1, Resource: "purchase_order", IDField: "orderId", Path: "/api/v1/purchasing/orders/{orderId}", Permissions: []string{"purchasing:access"}},
	{Event: PurchaseReceiptPosted{}, Version: 1, Resource: "purchase_receipt", IDField: "receiptId", Path: "/api/v1/purchasing/receipts/{receiptId}", Permissions: []string{"purchasing:access"}},
	{Event: PurchaseReceiptUndone{}, Version: 1, Resource: "purchase_receipt", IDField: "receiptId", Path: "/api/v1/purchasing/receipts/{receiptId}", Permissions: []string{"purchasing:access"}},

	// Manufacturing.
	{Event: ManufacturingOrderDone{}, Version: 1, Resource: "manufacturing_order", IDField: "manufacturingOrderId", Path: "/api/v1/manufacturing/orders/{id}", Permissions: []string{"manufacturing:access"}},

	// Accounting.
	{Event: SupplierInvoicePosted{}, Version: 1, Resource: "supplier_invoice", IDField: "invoiceId", Path: "/api/v1/accounting/supplier-invoices/{id}", Permissions: []string{"accounting:read"}},
	{Event: SupplierInvoiceReversed{}, Version: 1, Resource: "supplier_invoice", IDField: "invoiceId", Path: "/api/v1/accounting/supplier-invoices/{id}", Permissions: []string{"accounting:read"}},
}

// WebhookEvents is the catalogue (W4), a copy the caller may keep.
func WebhookEvents() []WebhookEvent {
	out := make([]WebhookEvent, len(webhookEvents))
	for i, e := range webhookEvents {
		e.Permissions = append([]string(nil), e.Permissions...)
		out[i] = e
	}
	return out
}

// LookupWebhookEvent is the catalogue's entry for the event named name.
func LookupWebhookEvent(name string) (WebhookEvent, bool) {
	for _, e := range webhookEvents {
		if e.Name() == name {
			e.Permissions = append([]string(nil), e.Permissions...)
			return e, true
		}
	}
	return WebhookEvent{}, false
}
