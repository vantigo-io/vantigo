package planning_test

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

const requestsPath = "/api/v1/planning/requests"

func requestPath(id int64) string { return requestsPath + "/" + strconv.FormatInt(id, 10) }

// The catalogue the request tests seed (seedCatalogue): a certificate
// without a level, a skill with one, and a hidden skill — still held, offered
// for no new requirement, but accepted on one.
var (
	catalogueHMSCard = contracts.CompetenceEntry{ID: 7001, Key: "hms-kort", NameEn: "HSE card", NameNb: "HMS-kort",
		CategoryKey: "sertifikat", Kind: "certificate", ValidityMonths: ptr(int16(60))}
	catalogueJava = contracts.CompetenceEntry{ID: 7101, Key: "java", NameEn: "Java", NameNb: "Java",
		CategoryKey: "programmering", Kind: "skill", HasLevel: true}
	catalogueCobol = contracts.CompetenceEntry{ID: 7102, Key: "cobol", NameEn: "COBOL", NameNb: "COBOL",
		CategoryKey: "programmering", Kind: "skill", HasLevel: true, Hidden: true}
)

// seedCatalogue gives the fake register the catalogue above.
func seedCatalogue(h *harness) {
	h.workforce.setCompetences(catalogueHMSCard, catalogueJava, catalogueCobol)
}

// demand is a request's body: a developer on the active project at 18.75
// hours a week over the four weeks from Monday 5 October 2026, with what
// fields replace (a nil value deletes the key).
func demand(fields map[string]any) map[string]any {
	body := map[string]any{
		"projectId": projectActive, "title": "Utvikler",
		"fromDate": "2026-10-05", "toDate": "2026-10-30", "hoursPerWeek": 18.75,
	}
	maps.Copy(body, fields)
	for k, v := range body {
		if v == nil {
			delete(body, k)
		}
	}
	return body
}

// createRequest files body and answers the written request, failing unless
// it is 201.
func createRequest(t *testing.T, c *modtest.Client, body map[string]any) gen.PlanningRequestWritten {
	t.Helper()
	res := c.Do(http.MethodPost, requestsPath, body)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST /requests %v = %d %s, want 201", body, res.Status, res.Body)
	}
	var out gen.PlanningRequestWritten
	res.JSON(&out)
	return out
}

// fillRequest fills id with body and answers the fill, failing unless it is
// 201.
func fillRequest(t *testing.T, c *modtest.Client, id int64, body map[string]any) gen.PlanningRequestFilled {
	t.Helper()
	res := c.Do(http.MethodPost, requestPath(id)+"/fill", body)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST /requests/%d/fill %v = %d %s, want 201", id, body, res.Status, res.Body)
	}
	var out gen.PlanningRequestFilled
	res.JSON(&out)
	return out
}

// getRequest reads id as c, failing unless it is 200.
func getRequest(t *testing.T, c *modtest.Client, id int64) gen.PlanningRequest {
	t.Helper()
	var out gen.PlanningRequest
	getJSON(t, c, requestPath(id), http.StatusOK, &out)
	return out
}

// seedReq is one request a read test plans against, written straight into
// planning.requests: the reads do not depend on the write lane. filledBy, an
// allocation's id, makes it filled by that row, as a fill leaves it.
type seedReq struct {
	project      int32
	from, to     string
	hours        float64 // 37.5 by default
	title        string  // "Utvikler" by default
	status       string  // "open" by default; "filled" when filledBy is set
	note         string
	requirements []seedRequirement
	filledBy     int64
}

// seedRequirement is one requirement of a seeded request.
type seedRequirement struct {
	competence int64
	minLevel   *int16
}

// seedRequest writes r, its requirements and its filling row's link, and
// answers its id.
func seedRequest(t *testing.T, h *harness, r seedReq) int64 {
	t.Helper()
	if r.hours == 0 {
		r.hours = 37.5
	}
	if r.title == "" {
		r.title = "Utvikler"
	}
	if r.status == "" {
		r.status = "open"
	}
	if r.filledBy != 0 {
		r.status = "filled"
	}
	var note *string
	if r.note != "" {
		note = &r.note
	}
	id := modtest.One[int64](t, h.Harness, `INSERT INTO planning.requests
    (project_id, title, from_date, to_date, hours_per_week, status, note, created_at, updated_at)
VALUES ($1, $2, $3::date, $4::date, $5, $6, $7, $8::timestamptz, $8::timestamptz)
RETURNING id`, r.project, r.title, date(r.from), date(r.to), r.hours, r.status, note, h.Now())
	for _, req := range r.requirements {
		h.Exec(t, `INSERT INTO planning.request_requirements (request_id, competence_id, min_level) VALUES ($1, $2, $3)`,
			id, req.competence, req.minLevel)
	}
	if r.filledBy != 0 {
		h.Exec(t, `UPDATE planning.allocations SET request_id = $1 WHERE id = $2`, id, r.filledBy)
	}
	return id
}

// requestEvents is the kinds of the events recorded about request id, in
// order.
func requestEvents(t *testing.T, h *harness, id int64) []string {
	t.Helper()
	return modtest.One[[]string](t, h.Harness,
		`SELECT coalesce(array_agg(kind ORDER BY id), '{}') FROM planning.events WHERE entity_kind = 'request' AND entity_id = $1`, id)
}

// requestPayload is the payload of the last event of kind about request id.
func requestPayload(t *testing.T, h *harness, id int64, kind string) map[string]any {
	t.Helper()
	raw := modtest.One[string](t, h.Harness,
		`SELECT payload::text FROM planning.events WHERE entity_kind = 'request' AND entity_id = $1 AND kind = $2 ORDER BY id DESC LIMIT 1`, id, kind)
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("payload %s: %v", raw, err)
	}
	return out
}

// TestRequests_CreateAndRead is S16's create: an open request with its
// requirements named from the catalogue, its event's recorded values with
// the note by its name alone, and the read answering the same.
func TestRequests_CreateAndRead(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	seedCatalogue(h)
	c := planner(t, h)

	w := createRequest(t, c, demand(map[string]any{
		"title": "  Utvikler  ", "note": "Hemmelig notat",
		"requirements": []map[string]any{
			{"competenceId": catalogueJava.ID, "minLevel": 3},
			{"competenceId": catalogueHMSCard.ID},
			{"competenceId": catalogueCobol.ID},
		},
	}))
	r := w.Request
	if r.Status != gen.Open || r.Title != "Utvikler" || r.HoursPerWeek != 18.75 || r.Revision != 1 ||
		r.Project.Code == nil || *r.Project.Code != "KVEM1000" || r.FilledBy != nil || r.Note == nil || *r.Note != "Hemmelig notat" {
		t.Errorf("request = %+v, want open, trimmed, named, with the note", r)
	}
	if len(r.Requirements) != 3 {
		t.Fatalf("requirements = %+v, want three", r.Requirements)
	}
	java := r.Requirements[0]
	if java.CompetenceId != catalogueJava.ID || java.MinLevel == nil || *java.MinLevel != 3 || java.Key == nil || *java.Key != "java" ||
		java.Kind == nil || *java.Kind != gen.PlanningRequirementKindSkill || java.HasLevel == nil || !*java.HasLevel {
		t.Errorf("the Java requirement = %+v, want it named, level 3", java)
	}
	if hms := r.Requirements[1]; hms.MinLevel != nil || hms.NameNb == nil || *hms.NameNb != "HMS-kort" {
		t.Errorf("the HMS requirement = %+v, want it named, no level", hms)
	}
	if len(w.Warnings) != 0 {
		t.Errorf("warnings = %+v, want none inside the project's dates", w.Warnings)
	}
	if got := getRequest(t, c, r.Id); got.Title != r.Title || len(got.Requirements) != 3 || got.Note == nil {
		t.Errorf("GET = %+v, want what the create answered", got)
	}

	if kinds := requestEvents(t, h, r.Id); !slices.Equal(kinds, []string{"request_created"}) {
		t.Errorf("events = %v, want request_created", kinds)
	}
	fields := requestPayload(t, h, r.Id, "request_created")["fields"].(map[string]any)
	if to := fields["title"].(map[string]any)["to"]; to != "Utvikler" {
		t.Errorf("title recorded = %v", to)
	}
	if note := fields["note"].(map[string]any); len(note) != 0 {
		t.Errorf("note recorded = %v, want its name alone", note)
	}
	requirements := fields["requirements"].(map[string]any)["to"].([]any)
	if len(requirements) != 3 || requirements[0].(map[string]any)["competenceId"] != float64(catalogueHMSCard.ID) ||
		requirements[1].(map[string]any)["minLevel"] != float64(3) {
		t.Errorf("requirements recorded = %v, want by competence with the level", requirements)
	}
	for _, name := range []string{"projectId", "fromDate", "toDate", "hoursPerWeek"} {
		if _, ok := fields[name]; !ok {
			t.Errorf("request_created has no %s: %v", name, fields)
		}
	}

	late := createRequest(t, c, demand(map[string]any{"fromDate": "2027-06-28", "toDate": "2027-07-09"}))
	if len(late.Warnings) != 1 || late.Warnings[0].Code != gen.PlanningWarningCodeOutsideProjectDates {
		t.Errorf("warnings = %+v, want outside_project_dates", late.Warnings)
	}
}

// TestRequests_FieldErrors is S16's field rules, each a 400 on its own field
// and nothing written; a hidden competence and a title of 100 are accepted.
func TestRequests_FieldErrors(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	seedCatalogue(h)
	c := planner(t, h)

	eleven := make([]map[string]any, 11)
	for i := range eleven {
		eleven[i] = map[string]any{"competenceId": 8000 + i}
	}
	cases := []struct {
		name   string
		fields map[string]any
		field  string
		code   string
	}{
		{"no title", map[string]any{"title": "   "}, "title", ""},
		{"a long title", map[string]any{"title": strings.Repeat("x", 101)}, "title", ""},
		{"no project", map[string]any{"projectId": nil}, "projectId", ""},
		{"an unknown project", map[string]any{"projectId": projectUnknown}, "projectId", ""},
		{"to before from", map[string]any{"toDate": "2026-10-04"}, "toDate", ""},
		{"a period too long", map[string]any{"toDate": "2029-10-06"}, "toDate", "period_too_long"},
		{"a period wholly past the bound", map[string]any{"fromDate": "2026-01-05", "toDate": "2026-01-30"}, "fromDate", "window_out_of_bounds"},
		{"no hours", map[string]any{"hoursPerWeek": 0}, "hoursPerWeek", ""},
		{"hours over 80", map[string]any{"hoursPerWeek": 80.5}, "hoursPerWeek", ""},
		{"three decimals", map[string]any{"hoursPerWeek": 12.345}, "hoursPerWeek", ""},
		{"a long note", map[string]any{"note": strings.Repeat("x", 501)}, "note", ""},
		{"eleven requirements", map[string]any{"requirements": eleven}, "requirements", ""},
		{"a competence twice", map[string]any{"requirements": []map[string]any{
			{"competenceId": catalogueJava.ID}, {"competenceId": catalogueJava.ID, "minLevel": 2}}}, "requirements[1].competenceId", ""},
		{"an unknown competence", map[string]any{"requirements": []map[string]any{
			{"competenceId": catalogueJava.ID}, {"competenceId": 7999}}}, "requirements[1].competenceId", ""},
		{"a level on a competence without one", map[string]any{"requirements": []map[string]any{
			{"competenceId": catalogueHMSCard.ID, "minLevel": 1}}}, "requirements[0].minLevel", ""},
		{"a level over 4", map[string]any{"requirements": []map[string]any{
			{"competenceId": catalogueJava.ID, "minLevel": 5}}}, "requirements[0].minLevel", ""},
		{"a property not taken", map[string]any{"status": "filled"}, "status", ""},
		{"a property not taken inside a requirement", map[string]any{"requirements": []map[string]any{
			{"competenceId": catalogueJava.ID, "min_level": 3}}}, "requirements[0].min_level", ""},
	}
	for _, tc := range cases {
		res := c.Do(http.MethodPost, requestsPath, demand(tc.fields))
		if res.Status != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", tc.name, res.Status, res.Body)
			continue
		}
		var p gen.PlanningProblem
		res.JSON(&p)
		if p.Errors == nil || len((*p.Errors)[tc.field]) == 0 {
			t.Errorf("%s: no field error on %s: %s", tc.name, tc.field, res.Body)
		}
		if tc.code != "" {
			wantCode(t, p, tc.code)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM planning.requests`); n != 0 {
		t.Errorf("%d requests written by refusals, want none", n)
	}

	createRequest(t, c, demand(map[string]any{"title": strings.Repeat("x", 100), "hoursPerWeek": 80,
		"requirements": []map[string]any{{"competenceId": catalogueCobol.ID, "minLevel": 0}}}))

	// A caller whom Staff answers no catalogue names no requirement: every
	// id is unknown, by Staff's rule, not restated here.
	h.workforce.setVisible(false)
	res := c.Do(http.MethodPost, requestsPath, demand(map[string]any{"requirements": []map[string]any{{"competenceId": catalogueJava.ID}}}))
	if p := problemOf(t, res, http.StatusBadRequest); p.Errors == nil || len((*p.Errors)["requirements[0].competenceId"]) == 0 {
		t.Errorf("a requirement Staff does not answer = %+v, want a field error", p)
	}
}

// TestRequests_ProjectClosed is S16: a completed or cancelled project takes
// no request, and a PUT may move a request neither onto nor off one.
func TestRequests_ProjectClosed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)

	wantCode(t, problemOf(t, c.Do(http.MethodPost, requestsPath, demand(map[string]any{"projectId": projectCompleted})), http.StatusConflict), "project_closed")

	r := createRequest(t, c, demand(nil)).Request
	res := c.Do(http.MethodPut, requestPath(r.Id), demand(map[string]any{"projectId": projectCompleted}), ifMatch(r.Revision))
	wantCode(t, problemOf(t, res, http.StatusConflict), "project_closed")

	draft := createRequest(t, c, demand(map[string]any{"projectId": projectPlanned})).Request
	closed, _ := h.projects.Project(t.Context(), projectPlanned)
	closed.Status = "cancelled"
	h.projects.setProject(*closed)
	res = c.Do(http.MethodPut, requestPath(draft.Id), demand(nil), ifMatch(draft.Revision))
	wantCode(t, problemOf(t, res, http.StatusConflict), "project_closed")
	wantCode(t, problemOf(t, c.Do(http.MethodPost, requestPath(draft.Id)+"/fill", map[string]any{"personId": personKari}), http.StatusConflict), "project_closed")
	if res := c.Do(http.MethodPost, requestPath(draft.Id)+"/withdraw", nil); res.Status != http.StatusOK {
		t.Errorf("withdraw on a cancelled project = %d %s, want 200", res.Status, res.Body)
	}
}

// TestRequests_Put is §4.8's replace: If-Match, the fields and the
// requirements replaced whole, request_changed with the changed fields only,
// a move to another project, and a put that changes nothing writing nothing.
func TestRequests_Put(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	seedCatalogue(h)
	c := planner(t, h)
	r := createRequest(t, c, demand(map[string]any{"note": "a", "requirements": []map[string]any{
		{"competenceId": catalogueJava.ID, "minLevel": 2}, {"competenceId": catalogueHMSCard.ID}}})).Request

	if res := c.Do(http.MethodPut, requestPath(r.Id), demand(nil), modtest.Header("If-Match", "x")); res.Status != http.StatusBadRequest {
		t.Errorf("a bad If-Match = %d, want 400", res.Status)
	}
	wantCode(t, problemOf(t, c.Do(http.MethodPut, requestPath(r.Id), demand(nil), ifMatch(r.Revision+1)), http.StatusConflict), "revision_conflict")
	if res := c.Do(http.MethodPut, requestPath(9999), demand(nil), ifMatch(1)); res.Status != http.StatusNotFound {
		t.Errorf("PUT of an unknown request = %d, want 404", res.Status)
	}

	// The same fields, the requirements reordered: nothing changes.
	same := demand(map[string]any{"note": "a", "requirements": []map[string]any{
		{"competenceId": catalogueHMSCard.ID}, {"competenceId": catalogueJava.ID, "minLevel": 2}}})
	res := c.Do(http.MethodPut, requestPath(r.Id), same, ifMatch(r.Revision))
	var w gen.PlanningRequestWritten
	if res.Status != http.StatusOK {
		t.Fatalf("PUT unchanged = %d %s", res.Status, res.Body)
	}
	res.JSON(&w)
	if w.Request.Revision != r.Revision || len(requestEvents(t, h, r.Id)) != 1 {
		t.Errorf("a put that changes nothing = revision %d, events %v; want nothing written", w.Request.Revision, requestEvents(t, h, r.Id))
	}

	changed := demand(map[string]any{"projectId": projectInternal, "title": "Arkitekt", "hoursPerWeek": 10, "note": "b",
		"requirements": []map[string]any{{"competenceId": catalogueJava.ID, "minLevel": 3}}})
	res = c.Do(http.MethodPut, requestPath(r.Id), changed, ifMatch(r.Revision))
	if res.Status != http.StatusOK {
		t.Fatalf("PUT = %d %s", res.Status, res.Body)
	}
	res.JSON(&w)
	got := w.Request
	if got.Revision != r.Revision+1 || got.Title != "Arkitekt" || got.HoursPerWeek != 10 || got.Project.Id != projectInternal ||
		len(got.Requirements) != 1 || *got.Requirements[0].MinLevel != 3 || got.Note == nil || *got.Note != "b" {
		t.Errorf("PUT answered %+v", got)
	}
	if read := getRequest(t, c, r.Id); len(read.Requirements) != 1 || read.Project.Id != projectInternal {
		t.Errorf("GET after PUT = %+v, want the requirements replaced", read)
	}
	fields := requestPayload(t, h, r.Id, "request_changed")["fields"].(map[string]any)
	names := slices.Sorted(maps.Keys(fields))
	if !slices.Equal(names, []string{"hoursPerWeek", "note", "projectId", "requirements", "title"}) {
		t.Errorf("request_changed fields = %v, want the five changed", names)
	}
	if note := fields["note"].(map[string]any); len(note) != 0 {
		t.Errorf("note recorded = %v, want its name alone", note)
	}
	if n := h.Count(t, `SELECT count(*) FROM planning.events WHERE entity_kind = 'request' AND kind = 'request_changed' AND project_id = $1`, projectInternal); n != 1 {
		t.Errorf("request_changed filed under the new project %d times, want once", n)
	}
}

// TestRequests_FillCreatesTheRow is S16's fill: the allocation written from
// the request — its project, period, hours a week and title, the status by
// the project — naming the request, the request filled, both events in one
// transaction, the person mailed when confirmed, and the row's own reads
// carrying requestId.
func TestRequests_FillCreatesTheRow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	seedCatalogue(h)
	c := planner(t, h)
	kari := linkUser(t, h, personKari)
	r := createRequest(t, c, demand(map[string]any{"requirements": []map[string]any{{"competenceId": catalogueJava.ID}}})).Request

	f := fillRequest(t, c, r.Id, map[string]any{"personId": personKari})
	a := f.Allocation
	if a.RequestId == nil || *a.RequestId != r.Id || a.Person.Id != personKari || a.Project.Id != projectActive ||
		a.LoadUnit != gen.HoursPerWeek || a.Load != 18.75 || a.Status != gen.Confirmed || a.Title == nil || *a.Title != "Utvikler" ||
		a.FromDate.String() != "2026-10-05" || a.ToDate.String() != "2026-10-30" {
		t.Errorf("allocation = %+v, want the request's as a confirmed hours row naming it", a)
	}
	if f.Request.Status != gen.Filled || f.Request.Revision != r.Revision+1 || f.Request.FilledBy == nil ||
		f.Request.FilledBy.AllocationId != a.Id || f.Request.FilledBy.Person.DisplayName == nil || len(f.Request.Requirements) != 1 {
		t.Errorf("request = %+v, want filled by the row", f.Request)
	}
	if read := getRequest(t, c, r.Id); read.FilledBy == nil || read.FilledBy.AllocationId != a.Id || *read.FilledBy.Person.DisplayName != "Kari Nordmann" {
		t.Errorf("GET = %+v, want filledBy named", read)
	}
	var row gen.PlanningAllocation
	getJSON(t, c, allocationPath(a.Id), http.StatusOK, &row)
	if row.RequestId == nil || *row.RequestId != r.Id {
		t.Errorf("GET /allocations/{id} = %+v, want requestId", row)
	}

	if kinds := requestEvents(t, h, r.Id); !slices.Equal(kinds, []string{"request_created", "request_filled"}) {
		t.Errorf("request events = %v", kinds)
	}
	if p := requestPayload(t, h, r.Id, "request_filled"); p["allocationId"] != float64(a.Id) || p["personId"] != float64(personKari) {
		t.Errorf("request_filled payload = %v", p)
	}
	if n := h.Count(t, `SELECT count(*) FROM planning.events WHERE kind = 'request_filled' AND person_id = $1 AND project_id = $2`, personKari, projectActive); n != 1 {
		t.Errorf("request_filled filed under the person and the project %d times, want once", n)
	}
	if fields := eventPayload(t, h, a.Id, "allocation_created")["fields"]; fields["requestId"]["to"] != float64(r.Id) {
		t.Errorf("allocation_created fields = %v, want requestId", fields)
	}
	if got := noticesTo(h, kari); len(got) != 1 || !strings.Contains(got[0].Texts["en"].Subject, "You are planned on") {
		t.Errorf("notices = %+v, want planned", got)
	}

	// On a planned project the fill is tentative unless asked (plan
	// reading 7), and nobody is mailed.
	bid := createRequest(t, c, demand(map[string]any{"projectId": projectPlanned})).Request
	if f := fillRequest(t, c, bid.Id, map[string]any{"personId": personKari}); f.Allocation.Status != gen.Tentative {
		t.Errorf("a fill on a planned project = %s, want tentative", f.Allocation.Status)
	}
	other := createRequest(t, c, demand(map[string]any{"projectId": projectPlanned})).Request
	if f := fillRequest(t, c, other.Id, map[string]any{"personId": personOla, "status": "confirmed"}); f.Allocation.Status != gen.Confirmed {
		t.Errorf("a fill asked confirmed = %s", f.Allocation.Status)
	}
	if len(noticesTo(h, kari)) != 1 {
		t.Errorf("a tentative fill mailed the person")
	}
}

// TestRequests_FillRefusals is the fill's refusals: S4's and S7's under the
// allocation's rules, the person's field errors, and request_not_open on a
// request already filled — each leaving the request open and nothing
// written.
func TestRequests_FillRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	r := createRequest(t, c, demand(nil)).Request
	fill := func(body map[string]any) *modtest.Response {
		return c.Do(http.MethodPost, requestPath(r.Id)+"/fill", body)
	}

	for name, body := range map[string]map[string]any{
		"no person":            {},
		"an unknown person":    {"personId": 1999},
		"a status":             {"personId": personKari, "status": "maybe"},
		"a property not taken": {"personId": personKari, "load": 50},
	} {
		field := "personId"
		if _, ok := body["status"]; ok {
			field = "status"
		}
		if _, ok := body["load"]; ok {
			field = "load"
		}
		p := problemOf(t, fill(body), http.StatusBadRequest)
		if p.Errors == nil || len((*p.Errors)[field]) == 0 {
			t.Errorf("%s: no field error on %s: %+v", name, field, p)
		}
	}
	siri := createRequest(t, c, demand(map[string]any{"fromDate": "2026-11-23", "toDate": "2026-12-11"})).Request
	wantCode(t, problemOf(t, c.Do(http.MethodPost, requestPath(siri.Id)+"/fill", map[string]any{"personId": personSiri}), http.StatusConflict), "outside_employment")
	if res := c.Do(http.MethodPost, requestPath(9999)+"/fill", map[string]any{"personId": personKari}); res.Status != http.StatusNotFound {
		t.Errorf("fill of an unknown request = %d, want 404", res.Status)
	}
	// A request whose project the directory no longer resolves: the fill's
	// body names no project, so the field error is the request's, under its
	// title, while the fill's own fields answer under the allocation's.
	lost := seedRequest(t, h, seedReq{project: 2999, from: "2026-10-05", to: "2026-10-30"})
	p := problemOf(t, c.Do(http.MethodPost, requestPath(lost)+"/fill", map[string]any{"personId": personKari}), http.StatusBadRequest)
	if p.Title == nil || *p.Title != "Invalid request" || p.Errors == nil || len((*p.Errors)["projectId"]) == 0 {
		t.Errorf("fill on an unresolved project = %+v, want projectId under Invalid request", p)
	}
	if p := problemOf(t, fill(map[string]any{"personId": 1999}), http.StatusBadRequest); p.Title == nil || *p.Title != "Invalid allocation" {
		t.Errorf("fill of an unknown person = %+v, want it under Invalid allocation", p)
	}

	if res := c.Do(http.MethodPut, settingsPath, map[string]any{"overbooking": "refuse"}, ifMatch(1)); res.Status != http.StatusOK {
		t.Fatalf("PUT /settings = %d %s", res.Status, res.Body)
	}
	seedAllocation(t, h, seedRow{person: personKari, project: projectInternal, from: "2026-10-05", to: "2026-10-30", load: 100})
	wantCode(t, problemOf(t, fill(map[string]any{"personId": personKari}), http.StatusConflict), "overbooked")
	if got := getRequest(t, c, r.Id); got.Status != gen.Open || got.Revision != r.Revision {
		t.Errorf("after the refusals the request = %+v, want open and untouched", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM planning.allocations WHERE request_id IS NOT NULL`); n != 0 {
		t.Errorf("%d rows written naming a request, want none", n)
	}
	if kinds := requestEvents(t, h, r.Id); !slices.Equal(kinds, []string{"request_created"}) {
		t.Errorf("request events = %v, want the creation alone", kinds)
	}

	fillRequest(t, c, r.Id, map[string]any{"personId": personOla, "status": "tentative"})
	wantCode(t, problemOf(t, fill(map[string]any{"personId": personKari}), http.StatusConflict), "request_not_open")
}

// TestRequests_FillConcurrent is S23's project lock: two planners filling
// one request at once — one fills it, the other is request_not_open.
func TestRequests_FillConcurrent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	r := createRequest(t, c, demand(nil)).Request

	responses := make([]*modtest.Response, 2)
	var wg sync.WaitGroup
	for i, person := range []int64{personKari, personOla} {
		wg.Go(func() {
			responses[i] = c.Do(http.MethodPost, requestPath(r.Id)+"/fill", map[string]any{"personId": person})
		})
	}
	wg.Wait()
	filled, refused := 0, 0
	for _, res := range responses {
		switch res.Status {
		case http.StatusCreated:
			filled++
		case http.StatusConflict:
			refused++
			wantCode(t, problemOf(t, res, http.StatusConflict), "request_not_open")
		}
	}
	if filled != 1 || refused != 1 {
		t.Errorf("concurrent fills = %d filled, %d refused (%d, %d); want one of each", filled, refused, responses[0].Status, responses[1].Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM planning.allocations WHERE request_id = $1`, r.Id); n != 1 {
		t.Errorf("%d rows name the request, want one", n)
	}
}

// TestRequests_DeleteReopens is S16's "filled exactly while a row names it":
// deleting the filling row reopens the request in the delete's transaction,
// both events recorded; changing the row leaves it filled.
func TestRequests_DeleteReopens(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	r := createRequest(t, c, demand(nil)).Request
	a := fillRequest(t, c, r.Id, map[string]any{"personId": personKari}).Allocation

	res := put(c, a.Id, a.Revision, booking(map[string]any{"loadUnit": "hours_per_week", "load": 20, "status": "confirmed", "title": "Utvikler"}))
	changed := written(t, res).Allocation
	if changed.RequestId == nil || *changed.RequestId != r.Id {
		t.Errorf("PUT of the row = %+v, want requestId kept", changed)
	}
	if got := getRequest(t, c, r.Id); got.Status != gen.Filled || got.FilledBy == nil {
		t.Errorf("after a PUT of the row the request = %+v, want still filled", got)
	}

	if res := c.Do(http.MethodDelete, allocationPath(a.Id), nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", res.Status, res.Body)
	}
	got := getRequest(t, c, r.Id)
	if got.Status != gen.Open || got.FilledBy != nil {
		t.Errorf("after the delete the request = %+v, want open again", got)
	}
	if kinds := requestEvents(t, h, r.Id); !slices.Equal(kinds, []string{"request_created", "request_filled", "request_reopened"}) {
		t.Errorf("request events = %v", kinds)
	}
	if p := requestPayload(t, h, r.Id, "request_reopened"); p["allocationId"] != float64(a.Id) {
		t.Errorf("request_reopened payload = %v", p)
	}
	if fields := eventPayload(t, h, a.Id, "allocation_deleted")["fields"]; fields["requestId"]["from"] != float64(r.Id) {
		t.Errorf("allocation_deleted fields = %v, want requestId", fields)
	}
	// One transaction: the request's reopening is the record's next row
	// after the row's deletion.
	if n := h.Count(t, `SELECT count(*) FROM planning.events d JOIN planning.events r ON r.id = d.id + 1
        WHERE d.entity_kind = 'allocation' AND d.entity_id = $1 AND d.kind = 'allocation_deleted'
          AND r.entity_kind = 'request' AND r.entity_id = $2 AND r.kind = 'request_reopened'`, a.Id, r.Id); n != 1 {
		t.Errorf("request_reopened does not follow allocation_deleted in the record")
	}
	fillRequest(t, c, r.Id, map[string]any{"personId": personOla})
}

// TestRequests_DeleteReopensAMovedRow is the reopen after the filling row
// was moved by its own PUT — to another project, then to another person:
// the request has not moved (a filled request takes no change), so the
// delete takes the request's project's lock, not the row's, waits for it
// while another holds it, and then reopens the request as ever.
func TestRequests_DeleteReopensAMovedRow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	r := createRequest(t, c, demand(nil)).Request

	for _, move := range []struct {
		name   string
		fields map[string]any
	}{
		{"to another project", map[string]any{"projectId": projectInternal}},
		{"to another person", map[string]any{"personId": personOla}},
	} {
		a := fillRequest(t, c, r.Id, map[string]any{"personId": personKari}).Allocation
		body := booking(map[string]any{"loadUnit": "hours_per_week", "load": 18.75, "status": "confirmed"})
		maps.Copy(body, move.fields)
		moved := written(t, put(c, a.Id, a.Revision, body)).Allocation
		if moved.RequestId == nil || *moved.RequestId != r.Id {
			t.Fatalf("%s: the moved row = %+v, want requestId kept", move.name, moved)
		}

		holder, err := h.Pool().Acquire(t.Context())
		if err != nil {
			t.Fatalf("acquire a connection: %v", err)
		}
		lock := "planning-project-" + strconv.Itoa(int(projectActive))
		if _, err := holder.Exec(t.Context(), `SELECT pg_advisory_lock(hashtext($1))`, lock); err != nil {
			t.Fatalf("hold %s: %v", lock, err)
		}
		done := make(chan *modtest.Response, 1)
		go func() { done <- c.Do(http.MethodDelete, allocationPath(a.Id), nil) }()
		select {
		case res := <-done:
			t.Errorf("%s: DELETE = %d while the request's project was locked, want it to wait", move.name, res.Status)
		case <-time.After(300 * time.Millisecond):
		}
		if _, err := holder.Exec(t.Context(), `SELECT pg_advisory_unlock(hashtext($1))`, lock); err != nil {
			t.Fatalf("release %s: %v", lock, err)
		}
		holder.Release()
		if res := <-done; res.Status != http.StatusNoContent {
			t.Fatalf("%s: DELETE = %d %s, want 204", move.name, res.Status, res.Body)
		}
		if got := getRequest(t, c, r.Id); got.Status != gen.Open || got.FilledBy != nil {
			t.Errorf("%s: after the delete the request = %+v, want open again", move.name, got)
		}
		if p := requestPayload(t, h, r.Id, "request_reopened"); p["allocationId"] != float64(a.Id) {
			t.Errorf("%s: request_reopened payload = %v", move.name, p)
		}
	}
	want := []string{"request_created", "request_filled", "request_reopened", "request_filled", "request_reopened"}
	if kinds := requestEvents(t, h, r.Id); !slices.Equal(kinds, want) {
		t.Errorf("request events = %v, want %v", kinds, want)
	}
}

// TestRequests_Withdraw is S16's withdrawal: an open request closed for
// good — never filled, changed or reopened afterwards — and request_not_open
// on a filled one.
func TestRequests_Withdraw(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	r := createRequest(t, c, demand(nil)).Request

	res := c.Do(http.MethodPost, requestPath(r.Id)+"/withdraw", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("withdraw = %d %s", res.Status, res.Body)
	}
	var got gen.PlanningRequest
	res.JSON(&got)
	if got.Status != gen.Withdrawn || got.Revision != r.Revision+1 {
		t.Errorf("withdrawn = %+v", got)
	}
	fields := requestPayload(t, h, r.Id, "request_withdrawn")["fields"].(map[string]any)
	if status := fields["status"].(map[string]any); status["from"] != "open" || status["to"] != "withdrawn" {
		t.Errorf("request_withdrawn fields = %v", fields)
	}
	wantCode(t, problemOf(t, c.Do(http.MethodPost, requestPath(r.Id)+"/withdraw", nil), http.StatusConflict), "request_not_open")
	wantCode(t, problemOf(t, c.Do(http.MethodPost, requestPath(r.Id)+"/fill", map[string]any{"personId": personKari}), http.StatusConflict), "request_not_open")
	wantCode(t, problemOf(t, c.Do(http.MethodPut, requestPath(r.Id), demand(nil), ifMatch(got.Revision)), http.StatusConflict), "request_not_open")

	filled := createRequest(t, c, demand(nil)).Request
	f := fillRequest(t, c, filled.Id, map[string]any{"personId": personKari})
	wantCode(t, problemOf(t, c.Do(http.MethodPost, requestPath(filled.Id)+"/withdraw", nil), http.StatusConflict), "request_not_open")
	wantCode(t, problemOf(t, c.Do(http.MethodPut, requestPath(filled.Id), demand(nil), ifMatch(f.Request.Revision)), http.StatusConflict), "request_not_open")
	if res := c.Do(http.MethodPost, requestPath(9999)+"/withdraw", nil); res.Status != http.StatusNotFound {
		t.Errorf("withdraw of an unknown request = %d, want 404", res.Status)
	}
	if kinds := requestEvents(t, h, r.Id); !slices.Equal(kinds, []string{"request_created", "request_withdrawn"}) {
		t.Errorf("events = %v", kinds)
	}
}

// TestRequests_List is GET /requests: each filter, a window matching the
// requests touching it, the order, and to before from a 400.
func TestRequests_List(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "planning:access", "planning:view")
	row := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-11-02", to: "2026-11-27", load: 50})
	early := seedRequest(t, h, seedReq{project: projectActive, from: "2026-10-05", to: "2026-10-30"})
	late := seedRequest(t, h, seedReq{project: projectActive, from: "2026-11-02", to: "2026-11-27", filledBy: row})
	bid := seedRequest(t, h, seedReq{project: projectPlanned, from: "2026-10-12", to: "2026-12-18", status: "withdrawn"})

	ids := func(path string) []int64 {
		var list gen.PlanningRequestList
		getJSON(t, c, path, http.StatusOK, &list)
		out := make([]int64, len(list.Requests))
		for i, r := range list.Requests {
			out[i] = r.Id
		}
		return out
	}
	for path, want := range map[string][]int64{
		requestsPath:                                       {early, bid, late},
		requestsPath + "?projectId=2001":                   {early, late},
		requestsPath + "?status=filled":                    {late},
		requestsPath + "?status=open&projectId=2002":       {},
		requestsPath + "?from=2026-11-01":                  {bid, late},
		requestsPath + "?to=2026-10-11":                    {early},
		requestsPath + "?from=2026-10-31&to=2026-11-01":    {bid},
		requestsPath + "?from=2026-10-30&to=2026-11-02":    {early, bid, late},
		requestsPath + "?projectId=2001&from=2026-12-01":   {},
		requestsPath + "?status=withdrawn&from=2026-12-18": {bid},
	} {
		if got := ids(path); !slices.Equal(got, want) {
			t.Errorf("GET %s = %v, want %v", path, got, want)
		}
	}
	var list gen.PlanningRequestList
	getJSON(t, c, requestsPath+"?status=filled", http.StatusOK, &list)
	if f := list.Requests[0].FilledBy; f == nil || f.AllocationId != row || f.Person.Id != personKari {
		t.Errorf("the filled request's filledBy = %+v", f)
	}
	getJSON(t, c, requestsPath+"?from=2026-11-02&to=2026-11-01", http.StatusBadRequest, nil)
	getJSON(t, c, requestPath(9999), http.StatusNotFound, nil)
}

// TestRequests_NamesFromTheContracts is rule 4 on a request: the note to
// planning:manage alone, a requirement the catalogue no longer answers read
// by its id, and the filling row's person Staff does not answer read with
// displayName null.
func TestRequests_NamesFromTheContracts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	seedCatalogue(h)
	row := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-10-05", to: "2026-10-30", load: 50})
	id := seedRequest(t, h, seedReq{project: projectActive, from: "2026-10-05", to: "2026-10-30", note: "Hemmelig", filledBy: row,
		requirements: []seedRequirement{{competence: catalogueJava.ID, minLevel: ptr(int16(2))}, {competence: 7999}}})

	viewer := h.SignIn(t, "planning:access", "planning:view")
	if r := getRequest(t, viewer, id); r.Note != nil {
		t.Errorf("a viewer read the note: %+v", r)
	}
	r := getRequest(t, planner(t, h), id)
	if r.Note == nil || *r.Note != "Hemmelig" {
		t.Errorf("a planner's read = %+v, want the note", r)
	}
	if len(r.Requirements) != 2 || r.Requirements[0].Key == nil || *r.Requirements[0].MinLevel != 2 ||
		r.Requirements[1].CompetenceId != 7999 || r.Requirements[1].Key != nil {
		t.Errorf("requirements = %+v, want Java named and 7999 by its id", r.Requirements)
	}

	h.workforce.setVisible(false)
	r = getRequest(t, viewer, id)
	if r.FilledBy == nil || r.FilledBy.Person.Id != personKari || r.FilledBy.Person.DisplayName != nil || r.Requirements[0].Key != nil {
		t.Errorf("unseen by Staff the request = %+v, want the person and the competences by id", r)
	}
}

// TestRequests_Permissions is the contract's rules: planning:view reads,
// planning:manage writes, planning:access alone reads nothing.
func TestRequests_Permissions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	r := createRequest(t, planner(t, h), demand(nil)).Request
	viewer, _ := signIn(t, h, "planning:view")
	employee, _ := signIn(t, h)

	for name, res := range map[string]*modtest.Response{
		"POST with view":       viewer.Do(http.MethodPost, requestsPath, demand(nil)),
		"PUT with view":        viewer.Do(http.MethodPut, requestPath(r.Id), demand(nil), ifMatch(r.Revision)),
		"fill with view":       viewer.Do(http.MethodPost, requestPath(r.Id)+"/fill", map[string]any{"personId": personKari}),
		"withdraw with view":   viewer.Do(http.MethodPost, requestPath(r.Id)+"/withdraw", nil),
		"candidates with view": viewer.Do(http.MethodGet, requestPath(r.Id)+"/candidates", nil),
		"list with access":     employee.Do(http.MethodGet, requestsPath, nil),
		"get with access":      employee.Do(http.MethodGet, requestPath(r.Id), nil),
	} {
		if res.Status != http.StatusForbidden {
			t.Errorf("%s = %d, want 403", name, res.Status)
		}
	}
	if got := getRequest(t, viewer, r.Id); got.Status != gen.Open {
		t.Errorf("after the refusals the request = %+v, want open", got)
	}
}

// TestRequests_EventsListedByProjectAndPerson is plan reading 8: a request's
// events are listed with its project's, and its fill and reopen with the
// person's too.
func TestRequests_EventsListedByProjectAndPerson(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	r := createRequest(t, c, demand(map[string]any{"projectId": projectInternal})).Request
	a := fillRequest(t, c, r.Id, map[string]any{"personId": personOla}).Allocation
	if res := c.Do(http.MethodDelete, allocationPath(a.Id), nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", res.Status, res.Body)
	}

	var list gen.PlanningEventList
	getJSON(t, c, "/api/v1/planning/events?projectId=2004", http.StatusOK, &list)
	var kinds []gen.PlanningEventKind
	for _, e := range list.Events {
		if e.EntityKind == gen.Request {
			kinds = append(kinds, e.Kind)
		}
	}
	if !slices.Equal(kinds, []gen.PlanningEventKind{gen.RequestReopened, gen.RequestFilled, gen.RequestCreated}) {
		t.Errorf("the project's request events = %v", kinds)
	}
	getJSON(t, c, "/api/v1/planning/events?personId=1002", http.StatusOK, &list)
	kinds = nil
	for _, e := range list.Events {
		if e.EntityKind == gen.Request {
			kinds = append(kinds, e.Kind)
		}
	}
	if !slices.Equal(kinds, []gen.PlanningEventKind{gen.RequestReopened, gen.RequestFilled}) {
		t.Errorf("Ola's request events = %v", kinds)
	}
}
