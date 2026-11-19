package contracts

import "testing"

func TestWebhookEvent_HrefFillsThePathsOneParameter(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		event WebhookEvent
		id    string
		want  string
	}{
		{WebhookEvent{Path: "/api/v1/orders/orders/{id}"}, "42", "/api/v1/orders/orders/42"},
		{WebhookEvent{Path: "/api/v1/purchasing/orders/{orderId}"}, "7", "/api/v1/purchasing/orders/7"},
		{WebhookEvent{Path: "/api/v1/invoices/customers/{customerId}/credit"}, "9", "/api/v1/invoices/customers/9/credit"},
		{WebhookEvent{Path: "/api/v1/orders/orders/{id}"}, "a/b c", "/api/v1/orders/orders/a%2Fb%20c"},
		{WebhookEvent{Path: "/api/v1/plain"}, "1", "/api/v1/plain"},
	} {
		if got := c.event.Href(c.id); got != c.want {
			t.Errorf("Href(%q) of %s = %s, want %s", c.id, c.event.Path, got, c.want)
		}
	}
	// Every catalogue entry's resource path is filled.
	for _, e := range WebhookEvents() {
		if got := e.Href("1"); got == e.Path {
			t.Errorf("%s: Href left %s unfilled", e.Name(), e.Path)
		}
	}
}

func TestWebhookEvent_LookupAndCopies(t *testing.T) {
	t.Parallel()
	e, ok := LookupWebhookEvent("orders.sales_order.confirmed")
	if !ok || e.Resource != "sales_order" || e.IDField != "orderId" || e.Module() != "orders" {
		t.Fatalf("lookup %+v %v", e, ok)
	}
	if _, ok := LookupWebhookEvent("accounting.supplier_invoice.deleted"); ok {
		t.Error("a left-out event is in the catalogue")
	}
	e.Permissions[0] = "changed"
	if again, _ := LookupWebhookEvent("orders.sales_order.confirmed"); again.Permissions[0] != "orders:access" {
		t.Error("a lookup's permissions alias the catalogue")
	}
}
