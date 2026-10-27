package graphtest_test

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
)

const marker = "String {7b1c5e3a-6f0d-4d5e-9a40-3c7a5e2b9d11} Name VantigoKey"

// timedEvent is an event body as the module writes one, its marker carrying
// key when key is not "".
func timedEvent(subject, key string) map[string]any {
	e := map[string]any{
		"subject":      subject,
		"body":         map[string]any{"contentType": "text", "content": "Kept by Vantigo"},
		"isAllDay":     false,
		"start":        map[string]any{"dateTime": "2026-11-02T08:00:00", "timeZone": "UTC"},
		"end":          map[string]any{"dateTime": "2026-11-02T09:00:00", "timeZone": "UTC"},
		"showAs":       "busy",
		"sensitivity":  "normal",
		"isReminderOn": false,
	}
	if key != "" {
		e["singleValueExtendedProperties"] = []any{map[string]any{"id": marker, "value": "vantigo:" + key}}
	}
	return e
}

// listQuery is the marker's filter and expand, Find's query.
func listQuery(filter bool, top int) string {
	v := url.Values{
		"$expand": {"singleValueExtendedProperties($filter=id eq '" + marker + "')"},
		"$select": {"id"},
		"$top":    {strconv.Itoa(top)},
	}
	if filter {
		v.Set("$filter", "singleValueExtendedProperties/Any(ep: ep/id eq '"+marker+"' and startswith(ep/value, 'vantigo:'))")
	}
	return "?" + strings.ReplaceAll(v.Encode(), "+", "%20")
}

// The mailbox (plan reading 12): calendars made, read, refused twice by
// name and deleted with their events; events made with the marker, patched
// without it, deleted, and listed filtered on the marker with the property
// expanded, in pages.
func TestMailbox(t *testing.T) {
	now := time.Now()
	g := tenantFake(&now)
	c := newClient(t, g)
	u := g.AddUser(graphtest.User{UserPrincipalName: "kari@nordlys.no", AccountEnabled: true})
	base := "/users/" + u.ID

	// A calendar.
	status, _, cal := c.do(http.MethodPost, base+"/calendars", map[string]any{"name": "Vantigo"})
	calID, _ := cal["id"].(string)
	if status != http.StatusCreated || calID == "" {
		t.Fatalf("POST calendars: %d %v", status, cal)
	}
	if status, _, body := c.do(http.MethodPost, base+"/calendars", map[string]any{"name": "vantigo"}); status != http.StatusConflict || errorCode(body) != "ErrorFolderExists" {
		t.Errorf("a second calendar of the name: %d %v, want 409 ErrorFolderExists", status, body)
	}
	if status, _, body := c.do(http.MethodGet, base+"/calendars/"+url.PathEscape(calID), nil); status != http.StatusOK || body["name"] != "Vantigo" {
		t.Errorf("GET the calendar: %d %v", status, body)
	}
	if status, _, body := c.do(http.MethodGet, base+"/calendars?%24filter="+url.QueryEscape("name eq 'Vantigo'"), nil); status != http.StatusOK || len(body["value"].([]any)) != 1 {
		t.Errorf("the calendars named Vantigo: %d %v, want one", status, body)
	}
	if status, _, body := c.do(http.MethodGet, base+"/calendars/AAMknothere", nil); status != http.StatusNotFound || errorCode(body) != "ErrorItemNotFound" {
		t.Errorf("GET a calendar not there: %d %v, want 404 ErrorItemNotFound", status, body)
	}

	// Events: two marked in the default calendar, one of a person's own, one
	// in the secondary calendar.
	status, _, ev := c.do(http.MethodPost, base+"/events", timedEvent("Standup", "appointment:1"))
	eventID, _ := ev["id"].(string)
	if status != http.StatusCreated || eventID == "" || ev["changeKey"] == "" {
		t.Fatalf("POST events: %d %v", status, ev)
	}
	c.do(http.MethodPost, base+"/events", timedEvent("Review", "appointment:2"))
	c.do(http.MethodPost, base+"/events", timedEvent("Dentist", ""))
	if status, _, body := c.do(http.MethodPost, base+"/calendars/"+url.PathEscape(calID)+"/events", timedEvent("Ferie", "")); status != http.StatusCreated {
		t.Fatalf("POST a secondary calendar's event: %d %v", status, body)
	}
	if got := g.Events(u.ID, ""); len(got) != 3 || got[0].Properties[marker] != "vantigo:appointment:1" || got[0].IsReminderOn {
		t.Errorf("the default calendar: %+v", got)
	}

	// A PATCH writes what it names and keeps the property.
	patch := timedEvent("Standup moved", "")
	if status, _, body := c.do(http.MethodPatch, base+"/events/"+url.PathEscape(eventID), patch); status != http.StatusOK || body["subject"] != "Standup moved" || body["changeKey"] == ev["changeKey"] {
		t.Errorf("PATCH: %d %v", status, body)
	}
	got := g.Events(u.ID, "")[0]
	if got.Properties[marker] != "vantigo:appointment:1" {
		t.Errorf("the marker after a PATCH: %v", got.Properties)
	}
	if _, written := got.LastWrite["singleValueExtendedProperties"]; written {
		t.Error("LastWrite kept the create's body, not the PATCH's")
	}
	if status, _, body := c.do(http.MethodPatch, base+"/events/AAMknothere", patch); status != http.StatusNotFound || errorCode(body) != "ErrorItemNotFound" {
		t.Errorf("PATCH of an event not there: %d %v", status, body)
	}
	allDay := timedEvent("Ferie", "")
	allDay["isAllDay"] = true
	allDay["start"] = map[string]any{"dateTime": "2026-11-02T08:00:00", "timeZone": "W. Europe Standard Time"}
	allDay["end"] = map[string]any{"dateTime": "2026-11-03T00:00:00", "timeZone": "W. Europe Standard Time"}
	if status, _, _ := c.do(http.MethodPost, base+"/events", allDay); status != http.StatusBadRequest {
		t.Errorf("an all-day event off midnight: %d, want 400", status)
	}

	// The marked events, filtered and expanded, two to a page.
	status, _, page := c.do(http.MethodGet, base+"/events"+listQuery(true, 1), nil)
	values, _ := page["value"].([]any)
	next, _ := page["@odata.nextLink"].(string)
	if status != http.StatusOK || len(values) != 1 || next == "" {
		t.Fatalf("the first page: %d %v", status, page)
	}
	first := values[0].(map[string]any)
	props, _ := first["singleValueExtendedProperties"].([]any)
	if len(first) != 2 || len(props) != 1 || props[0].(map[string]any)["value"] != "vantigo:appointment:1" {
		t.Errorf("an event under $select=id and the expand: %v", first)
	}
	rest, ok := strings.CutPrefix(next, "https://graph.microsoft.com/v1.0")
	if !ok {
		t.Fatalf("a next link off Graph: %s", next)
	}
	_, _, page = c.do(http.MethodGet, rest, nil)
	if values, _ := page["value"].([]any); len(values) != 1 || page["@odata.nextLink"] != nil {
		t.Errorf("the second page: %v, want the last marked event", page)
	}
	// The secondary calendar lists every event, with no property to expand.
	_, _, page = c.do(http.MethodGet, base+"/calendars/"+url.PathEscape(calID)+"/events"+listQuery(false, 100), nil)
	if values, _ := page["value"].([]any); len(values) != 1 || values[0].(map[string]any)["singleValueExtendedProperties"] != nil {
		t.Errorf("the secondary calendar: %v", page)
	}
	if status, _, _ := c.do(http.MethodGet, base+"/events?%24filter="+url.QueryEscape("subject eq 'x'"), nil); status != http.StatusBadRequest {
		t.Errorf("another filter: %d, want 400", status)
	}

	// Deletes: the event, then 404; the calendar with its event.
	if status, _, _ := c.do(http.MethodDelete, base+"/events/"+url.PathEscape(eventID), nil); status != http.StatusNoContent {
		t.Errorf("DELETE the event: %d", status)
	}
	if status, _, _ := c.do(http.MethodDelete, base+"/events/"+url.PathEscape(eventID), nil); status != http.StatusNotFound {
		t.Errorf("DELETE it again: %d, want 404", status)
	}
	if status, _, _ := c.do(http.MethodDelete, base+"/calendars/"+url.PathEscape(calID), nil); status != http.StatusNoContent {
		t.Errorf("DELETE the calendar: %d", status)
	}
	if n := len(g.Calendars(u.ID)); n != 1 || len(g.Events(u.ID, calID)) != 0 {
		t.Errorf("after the calendar's delete: %d calendars", n)
	}
}

// The mailbox's refusals: 403 outside the scope, 404
// MailboxNotEnabledForRESTAPI without a mailbox, 404 for a user that does
// not exist.
func TestMailboxRefusals(t *testing.T) {
	now := time.Now()
	g := tenantFake(&now)
	c := newClient(t, g)
	denied := g.AddUser(graphtest.User{UserPrincipalName: "ola@nordlys.no", MailboxDenied: true})
	none := g.AddUser(graphtest.User{UserPrincipalName: "per@nordlys.no", NoMailbox: true})

	if status, _, body := c.do(http.MethodPost, "/users/"+denied.ID+"/events", timedEvent("x", "k")); status != http.StatusForbidden || errorCode(body) != "ErrorAccessDenied" {
		t.Errorf("a denied mailbox: %d %v", status, body)
	}
	if status, _, body := c.do(http.MethodGet, "/users/"+none.ID+"/calendars", nil); status != http.StatusNotFound || errorCode(body) != "MailboxNotEnabledForRESTAPI" {
		t.Errorf("no mailbox: %d %v", status, body)
	}
	if status, _, body := c.do(http.MethodGet, "/users/"+uuidLike+"/events", nil); status != http.StatusNotFound || errorCode(body) != "Request_ResourceNotFound" {
		t.Errorf("no such user: %d %v", status, body)
	}
}

// A $batch reaches the mailbox's routes, needs Content-Type on a request
// with a body, and ThrottleBatch answers its requests 429 with Retry-After
// one by one.
func TestMailboxBatch(t *testing.T) {
	now := time.Now()
	g := tenantFake(&now)
	c := newClient(t, g)
	u := g.AddUser(graphtest.User{UserPrincipalName: "kari@nordlys.no"})
	jsonHeader := map[string]any{"Content-Type": "application/json"}
	g.ThrottleBatch(1)
	status, _, body := c.do(http.MethodPost, "/$batch", map[string]any{"requests": []any{
		map[string]any{"id": "1", "method": "POST", "url": "/users/" + u.ID + "/events", "body": timedEvent("a", "k:1"), "headers": jsonHeader},
		map[string]any{"id": "2", "method": "POST", "url": "/users/" + u.ID + "/events", "body": timedEvent("b", "k:2"), "headers": jsonHeader},
		map[string]any{"id": "3", "method": "POST", "url": "/users/" + u.ID + "/events", "body": timedEvent("c", "k:3")},
		map[string]any{"id": "4", "method": "DELETE", "url": "/users/" + u.ID + "/events/AAMknothere"},
	}})
	if status != http.StatusOK {
		t.Fatalf("$batch: %d %v", status, body)
	}
	got := map[string]map[string]any{}
	for _, r := range body["responses"].([]any) {
		m := r.(map[string]any)
		got[m["id"].(string)] = m
	}
	if got["1"]["status"] != float64(429) || got["1"]["headers"].(map[string]any)["Retry-After"] != "1" {
		t.Errorf("the throttled request: %v", got["1"])
	}
	if got["2"]["status"] != float64(201) || got["2"]["body"].(map[string]any)["id"] == "" {
		t.Errorf("the create: %v", got["2"])
	}
	if got["3"]["status"] != float64(400) {
		t.Errorf("a body without Content-Type: %v, want 400", got["3"])
	}
	if got["4"]["status"] != float64(404) {
		t.Errorf("a delete of an event not there: %v, want 404", got["4"])
	}
	if n := len(g.Events(u.ID, "")); n != 1 {
		t.Errorf("%d events, want the one create", n)
	}
}
