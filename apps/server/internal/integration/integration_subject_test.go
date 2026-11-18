package integration_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/assistant"
	"github.com/vantigo-io/vantigo/server/internal/calendar"
	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/knowledge"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff"
)

// subjectRecorder is every contract the sweep's installation answers.
var subjectRecorder = mergedRecorder(modCustomers, modProducts, "communications", modProjects, modTime, modExpenses, modInvoices,
	"staff", modInventory, modOrders, modManufacturing, modAccounting, modPurchasing, "knowledge", "calendar", "assistant")

// subjectPermissions are what the person who names the integration holds.
var subjectPermissions = []string{
	"customers:view", "customers:create", "customers:update", "customers:timeline-manage", "customers:timeline-view",
	"communications:channels-manage", "communications:conversations-manage", "communications:conversations-view",
	"time:access", "time:manage",
	"projects:access", "projects:create", "projects:manage-all",
	"expenses:access", "expenses:manage",
	"staff:access", "staff:manage",
	"purchasing:access", "purchasing:orders-manage", "accounting:suppliers-manage",
	"orders:access", "orders:quotes-manage",
	"manufacturing:access", "manufacturing:orders-manage",
	"calendar:access",
	"knowledge:articles",
}

// guarded is one record field the K56 sweep guards, and its refusal: the
// status, and the field of a 400's errors or the code of another answer.
type guarded struct {
	name, method, path string
	body               map[string]any
	status             int
	field, code        string
	opts               []modtest.RequestOption
}

// TestIntegration_IsTheSubjectOfNoRecord is API access design K56's sweep:
// wherever a module stores a person picked by id, naming an integration's
// user is refused as naming no one is.
func TestIntegration_IsTheSubjectOfNoRecord(t *testing.T) {
	t.Parallel()
	h := newInstallationWith(t, []modtest.Option{
		modtest.WithRecorder(subjectRecorder),
		modtest.WithModule(communications.Module()), modtest.WithModule(staff.Module()), modtest.WithModule(knowledge.Module()),
		modtest.WithModule(calendar.Module()), modtest.WithModule(assistant.Module()),
		modtest.WithEnv("AI_PROVIDER", "openai-compatible"), modtest.WithEnv("AI_BASE_URL", "http://ollama:11434/v1"),
		modtest.WithEnv("AI_MODELS", "test-model"), modtest.WithEnv("OWNERS_REQUIRE_MFA", "0"),
	}, modCustomers, modProducts, modProjects, modTime, modExpenses, modInvoices, modInventory, modOrders, modManufacturing, modAccounting, modPurchasing)
	c, _ := h.SignInUser(t, subjectPermissions...)
	owner, ownerID := h.SignInUser(t)
	h.Exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, ownerID, ownerRoleID)
	integration := h.Integration(t, "Webshop sync")
	who := integration.String()

	var customer struct {
		Id int32 `json:"id"`
	}
	okJSON(t, c, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Owned Co"}, &customer)
	var project struct {
		Id int32 `json:"id"`
	}
	okJSON(t, c, http.MethodPost, "/api/v1/projects", map[string]any{
		"code": "KVEM1000", "name": "Kraft-Verket modernisering", "customerId": customer.Id, "billingType": "time-and-materials",
	}, &project)
	var channel struct {
		Id string `json:"id"`
	}
	okJSON(t, c, http.MethodPost, "/api/v1/communications/channels", map[string]any{
		"type": "email", "address": "chan-1@example.test", "smtp": map[string]any{"host": "smtp.example.test", "port": 587},
	}, &channel)
	conversation := uuid.New()
	h.Exec(t, `INSERT INTO communications.conversations (id, channel_id, status, last_activity_at, created_at) VALUES ($1, $2, 'open', $3, $3)`,
		conversation, uuid.MustParse(channel.Id), h.Now())
	var supplier struct {
		Id int64 `json:"id"`
	}
	okJSON(t, c, http.MethodPost, "/api/v1/accounting/suppliers", map[string]any{"name": "Norsk Stål AS"}, &supplier)
	person := modtest.One[int64](t, h, `INSERT INTO staff.people (employee_number, given_name, family_name, display_name, created_at, updated_at)
		VALUES ('T1', 'Kari', 'Nordmann', 'Kari Nordmann', $1, $1) RETURNING id`, h.Now())
	today := h.Now().UTC()

	for _, g := range []guarded{
		{name: "a customer's owner", method: http.MethodPut, path: fmt.Sprintf("/api/v1/customers/%d/owner", customer.Id),
			body: map[string]any{"ownerUserId": who}, status: http.StatusBadRequest, field: "ownerUserId"},
		{name: "a follow-up's assignee", method: http.MethodPost, path: fmt.Sprintf("/api/v1/customers/%d/timeline", customer.Id),
			body: map[string]any{"eventType": "note", "occurredOn": today.Format(time.DateOnly), "note": "n",
				"followUp": map[string]any{"dueOn": today.AddDate(0, 0, 3).Format(time.DateOnly), "assigneeUserId": who}},
			status: http.StatusBadRequest, field: "followUp.assigneeUserId"},
		{name: "a conversation's assignee", method: http.MethodPatch, path: "/api/v1/communications/conversations/" + conversation.String(),
			body: map[string]any{"assignedUserId": who}, status: http.StatusUnprocessableEntity, code: "assignee_unknown"},
		{name: "a person's rate", method: http.MethodPost, path: "/api/v1/time/rates",
			body: map[string]any{"userId": who, "validFrom": "2026-09-01", "billRate": 1, "currency": "NOK"}, status: http.StatusBadRequest, field: "userId"},
		{name: "a task's assignee", method: http.MethodPost, path: fmt.Sprintf("/api/v1/projects/%d/tasks", project.Id),
			body: map[string]any{"title": "Skriv spesifikasjonen", "assigneeUserId": who}, status: http.StatusBadRequest, field: "assigneeUserId"},
		{name: "a project role", method: http.MethodPut, path: fmt.Sprintf("/api/v1/projects/%d/roles/%s", project.Id, who),
			body: map[string]any{"role": "member"}, status: http.StatusBadRequest, field: "userId"},
		{name: "an expense's owner", method: http.MethodPost, path: "/api/v1/expenses/entries",
			body: map[string]any{"kind": "outlay", "entryDate": "2026-03-10", "description": "Kabel og kontakter", "categoryId": materialsCategory,
				"paidBy": "employee", "currency": "NOK", "grossAmount": 1250.0, "userId": who}, status: http.StatusBadRequest, field: "userId"},
		{name: "a claim's owner", method: http.MethodPost, path: "/api/v1/expenses/claims",
			body: map[string]any{"purpose": "Montasje hos kunden", "destination": "Bergen", "departureAt": "2026-03-09T07:00:00Z",
				"returnAt": "2026-03-11T16:00:00Z", "userId": who}, status: http.StatusBadRequest, field: "userId"},
		{name: "a staff link", method: http.MethodPut, path: fmt.Sprintf("/api/v1/staff/people/%d/user", person),
			body: map[string]any{"userId": who}, status: http.StatusBadRequest, field: "userId"},
		{name: "a purchase order's buyer", method: http.MethodPost, path: "/api/v1/purchasing/orders",
			body: map[string]any{"supplierId": supplier.Id, "buyerId": who}, status: http.StatusBadRequest, field: "buyerId"},
		{name: "a quote's salesperson", method: http.MethodPost, path: "/api/v1/orders/quotes",
			body:   map[string]any{"clientRequestId": uuid.New(), "customerId": customer.Id, "lines": []any{}, "salespersonId": who},
			status: http.StatusBadRequest, field: "salespersonId"},
		{name: "a time log's person", method: http.MethodPost, path: mfgBase + "/work-orders/1/time-logs",
			body: map[string]any{"userId": who, "startedAt": h.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
				"endedAt": h.Now().Add(-time.Hour).UTC().Format(time.RFC3339), "note": "Glemt"}, status: http.StatusBadRequest, field: "userId"},
		{name: "an appointment's attendee", method: http.MethodPost, path: "/api/v1/calendar/appointments",
			body: map[string]any{"title": "Møte", "allDay": true, "timeZone": "Europe/Oslo", "startDate": "2026-11-02", "endDate": "2026-11-02",
				"attendees": []map[string]any{{"kind": "user", "userId": who}}}, status: http.StatusBadRequest, field: "attendees[0]"},
		{name: "an article's member", method: http.MethodPut, path: "/api/v1/knowledge/articles/" + uuid.NewString() + "/access",
			body:   map[string]any{"internalPermission": "read", "members": []map[string]any{{"userId": who, "permission": "read"}}},
			status: http.StatusBadRequest, field: "members"},
		{name: "an Assistant switch", method: http.MethodPut, path: "/api/v1/assistant/people/" + who,
			body: map[string]any{"enabled": true}, status: http.StatusNotFound, code: "not_found", opts: []modtest.RequestOption{modtest.Header("If-Match", "0")}},
	} {
		caller := c
		if g.name == "an Assistant switch" {
			caller = owner
		}
		r := caller.Do(g.method, g.path, g.body, g.opts...)
		if r.Status != g.status {
			t.Errorf("%s: status %d body %s, want %d", g.name, r.Status, r.Body, g.status)
			continue
		}
		if g.field != "" {
			var problem struct {
				Errors map[string][]string `json:"errors"`
			}
			r.JSON(&problem)
			if len(problem.Errors[g.field]) == 0 {
				t.Errorf("%s: errors %v, want one on %s", g.name, problem.Errors, g.field)
			}
		}
		if g.code != "" && !strings.Contains(string(r.Body), `"`+g.code+`"`) {
			t.Errorf("%s: body %s, want code %s", g.name, r.Body, g.code)
		}
	}

	// A manufacturing order's responsible, on an installation that makes jam.
	s := newLotsInstallation(t)
	r := s.c.Do(http.MethodPost, mfgBase+"/orders", map[string]any{"variantId": s.jam, "quantity": "1", "responsibleUserId": s.h.Integration(t, "Webshop sync").String()})
	var problem struct {
		Errors map[string][]string `json:"errors"`
	}
	r.JSON(&problem)
	if r.Status != http.StatusBadRequest || len(problem.Errors["responsibleUserId"]) == 0 {
		t.Errorf("a manufacturing order's responsible: status %d body %s, want 400 on responsibleUserId", r.Status, r.Body)
	}
}
