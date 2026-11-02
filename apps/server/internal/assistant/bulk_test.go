package assistant_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/assistant"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// Bulk changes (the Assistant design S13): the job, its claim, its stops, its
// grant and its end, proved here against the real database with a scripted
// dispatcher, because a module test may not compose another module's API. What
// needs a real list and a real write — the selector against real pages, the
// freeze, the preview that writes nothing, the canary of five — is proved in
// internal/integration.

// fakeOps is a scripted dispatcher: it answers each call with respond (a 200 by
// default), and says every operation is confirm unless mode says otherwise.
type fakeOps struct {
	mu      sync.Mutex
	calls   []operations.CallRequest
	respond func(n int, req operations.CallRequest) (operations.CallResult, error)
	mode    func() operations.Mode
}

func (f *fakeOps) Call(_ context.Context, req operations.CallRequest) (operations.CallResult, error) {
	f.mu.Lock()
	n := len(f.calls)
	f.calls = append(f.calls, req)
	respond := f.respond
	f.mu.Unlock()
	if respond != nil {
		return respond(n, req)
	}
	return operations.CallResult{Status: 200, Body: json.RawMessage(`{"ok":true}`)}, nil
}

func (f *fakeOps) Validate(context.Context, operations.CallRequest) error { return nil }

func (f *fakeOps) Effective(context.Context, string) (operations.Mode, bool) {
	if f.mode != nil {
		return f.mode(), true
	}
	return operations.ModeConfirm, true
}

func (f *fakeOps) Describe(string) (json.RawMessage, error) { return nil, errors.New("not scripted") }
func (f *fakeOps) Info(string) (operations.Info, bool)      { return operations.Info{}, false }

func (f *fakeOps) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// ids are the item ids the calls were made for, in order.
func (f *fakeOps) ids() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = toString(c.PathParams["id"])
	}
	return out
}

func toString(v any) string {
	b, _ := json.Marshal(v)
	return strings.Trim(string(b), `"`)
}

func refusedWith(status int, code, title string) operations.CallResult {
	body, _ := json.Marshal(map[string]any{"status": status, "code": code, "title": title, "detail": "a long sentence that must not be kept"})
	return operations.CallResult{Status: status, Body: body, Header: http.Header{}}
}

// bulkSeed is what a plan leaves behind once its card has been decided.
type bulkSeed struct {
	conv, run, action, job, toolMessage uuid.UUID
}

type bulkOpts struct {
	state     string
	items     int
	grantLeft time.Duration
	claimedBy string
	claimedAt time.Duration // claimed_until relative to now
	pending   string        // the pending action's status
	done      int           // how many items are already succeeded
	updated   time.Duration // updated_at relative to now
	runAge    time.Duration // how long ago the run began
}

func seedBulk(t *testing.T, h *modtest.Harness, user uuid.UUID, o bulkOpts) bulkSeed {
	t.Helper()
	if o.state == "" {
		o.state = "running"
	}
	if o.pending == "" {
		o.pending = "confirmed"
	}
	if o.grantLeft == 0 {
		o.grantLeft = 50 * time.Minute
	}
	s := bulkSeed{conv: uuid.New(), run: uuid.New(), action: uuid.New(), job: uuid.New(), toolMessage: uuid.New()}
	now := h.Now()
	h.Exec(t, `INSERT INTO assistant.conversations (id, user_id, title, origin_app, origin_route, created_at, last_activity_at)
	           VALUES ($1, $2, 'Bulk', 'energy', '/energy', $3, $3)`, s.conv, user, now)
	h.Exec(t, `INSERT INTO assistant.runs (id, conversation_id, user_id, state, started_at) VALUES ($1, $2, $3, 'awaiting_confirmation', $4)`, s.run, s.conv, user, now.Add(-o.runAge))
	h.Exec(t, `INSERT INTO assistant.messages (id, conversation_id, run_id, role, content, created_at)
	           VALUES ($1, $2, $3, 'tool', '[{"type":"tool_result","callId":"c1","name":"propose_bulk","status":"ok","content":"{}"}]', $4)`, s.toolMessage, s.conv, s.run, now)
	decided := now
	h.Exec(t, `INSERT INTO assistant.pending_actions (id, run_id, conversation_id, user_id, operation_id, call_id, tool_message_id, request, preview, status, created_at, decided_at, expires_at, kind)
	           VALUES ($1, $2, $3, $4, 'putThing', 'c1', $5,
	                   '{"operationId":"putThing","fields":["name"]}',
	                   '{"summary":"Change a thing","method":"PUT","fields":[{"name":"name","next":"Renamed"}],"bulk":{"sample":[{"label":"Item 0","fields":[{"name":"name","current":"Old","next":"Renamed"}]}],"excluded":{"count":0,"reasons":[]}}}',
	                   $6, $7, $8, $9, 'bulk')`, s.action, s.run, s.conv, user, s.toolMessage, o.pending, now, decided, now.Add(24*time.Hour))
	var claimedBy *string
	var claimedUntil *time.Time
	if o.claimedBy != "" {
		claimedBy = &o.claimedBy
		until := now.Add(o.claimedAt)
		claimedUntil = &until
	}
	grant := uuid.New()
	h.Exec(t, `INSERT INTO assistant.bulk_jobs (id, pending_action_id, run_id, conversation_id, user_id, operation_id, selector, template, state, total, processed, succeeded,
	                                            grant_id, grant_expires_at, claimed_by, claimed_until, created_at, updated_at)
	           VALUES ($1, $2, $3, $4, $5, 'putThing', '{"operationId":"listThings","query":{"area":"NO5"}}',
	                   '{"operationId":"putThing","pathParams":{},"body":{"name":"Renamed"},"bind":{"id":"id"},"bindIn":{"id":"path"},"fields":["name"]}',
	                   $6, $7, $8, $8, $9, $10, $11, $12, $13, $13)`,
		s.job, s.action, s.run, s.conv, user, o.state, o.items, o.done, grant, now.Add(o.grantLeft), claimedBy, claimedUntil, now.Add(o.updated))
	h.Exec(t, `INSERT INTO assistant.bulk_job_items (job_id, seq, item, status, http_status)
	           SELECT $1, g, jsonb_build_object('bound', jsonb_build_object('id', g), 'label', 'Item ' || g),
	                  CASE WHEN g < $3 THEN 'succeeded' ELSE 'pending' END, CASE WHEN g < $3 THEN 200 END
	           FROM generate_series(0, $2 - 1) AS g`, s.job, o.items, o.done)
	return s
}

type jobRow struct {
	State, Error                 string
	Processed, Succeeded, Failed int
	Skipped                      int
	Claimed                      bool
}

func jobOf(t *testing.T, h *modtest.Harness, job uuid.UUID) jobRow {
	t.Helper()
	var r jobRow
	r.State = modtest.One[string](t, h, `SELECT state FROM assistant.bulk_jobs WHERE id = $1`, job)
	r.Error = modtest.One[string](t, h, `SELECT COALESCE(error_code, '') FROM assistant.bulk_jobs WHERE id = $1`, job)
	r.Processed = modtest.One[int](t, h, `SELECT processed FROM assistant.bulk_jobs WHERE id = $1`, job)
	r.Succeeded = modtest.One[int](t, h, `SELECT succeeded FROM assistant.bulk_jobs WHERE id = $1`, job)
	r.Failed = modtest.One[int](t, h, `SELECT failed FROM assistant.bulk_jobs WHERE id = $1`, job)
	r.Skipped = modtest.One[int](t, h, `SELECT skipped FROM assistant.bulk_jobs WHERE id = $1`, job)
	r.Claimed = modtest.One[bool](t, h, `SELECT claimed_by IS NOT NULL FROM assistant.bulk_jobs WHERE id = $1`, job)
	return r
}

func pendingItems(t *testing.T, h *modtest.Harness, job uuid.UUID) int {
	t.Helper()
	return modtest.One[int](t, h, `SELECT count(*) FROM assistant.bulk_job_items WHERE job_id = $1 AND status = 'pending'`, job)
}

// summarising is a model that answers every turn with a short sentence.
func summarising() *modtest.FakeChatModel {
	return modtest.NewFakeChatModelFunc(func(int, modtest.ChatRequest) modtest.ChatTurn {
		return modtest.ChatTurn{Text: "Done, with a few exceptions."}
	})
}

// claimed runs the job here, as a replica that holds its claim.
func runHere(t *testing.T, h *modtest.Harness, job uuid.UUID) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := assistant.RunBulk(ctx, h.Pool(), job); err != nil {
		t.Fatalf("run the job: %v", err)
	}
}

func TestBulkRun_RunsEveryItemInChunksAsThePersonAndEndsOnce(t *testing.T) {
	t.Parallel()
	model := summarising()
	h := newHarness(t, model)
	_, user := person(t, h)
	ops := &fakeOps{}
	assistant.UseBulkOps(h.Pool(), ops)
	seed := seedBulk(t, h, user, bulkOpts{items: 250})

	runHere(t, h, seed.job)

	if got := ops.callCount(); got != 250 {
		t.Fatalf("%d calls, want one for each of the 250 items", got)
	}
	for i, c := range ops.calls {
		if !c.Confirmed || c.OperationID != "putThing" || toString(c.PathParams["id"]) != toString(i) || !strings.Contains(string(c.Body), `"name":"Renamed"`) {
			t.Fatalf("call %d = %+v, want the confirmed change for item %d", i, c, i)
		}
	}
	if j := jobOf(t, h, seed.job); j.State != "done" || j.Processed != 250 || j.Succeeded != 250 || j.Failed != 0 || j.Claimed {
		t.Errorf("job = %+v, want done, 250 of 250 and no claim held", j)
	}
	// The end redacts the plan: the operation, the names and the binding stay.
	tpl := modtest.One[string](t, h, `SELECT template::text FROM assistant.bulk_jobs WHERE id = $1`, seed.job)
	sel := modtest.One[string](t, h, `SELECT selector::text FROM assistant.bulk_jobs WHERE id = $1`, seed.job)
	if strings.Contains(tpl, "Renamed") || !strings.Contains(tpl, "putThing") || !strings.Contains(tpl, `"name"`) || strings.Contains(sel, "NO5") || !strings.Contains(sel, "area") {
		t.Errorf("a finished job kept its values:\ntemplate %s\nselector %s", tpl, sel)
	}
	prev := modtest.One[string](t, h, `SELECT preview::text FROM assistant.pending_actions WHERE id = $1`, seed.action)
	if strings.Contains(prev, "Renamed") || strings.Contains(prev, "Old") || !strings.Contains(prev, "Item 0") {
		t.Errorf("the decided card kept a value or lost its label: %s", prev)
	}
	if st := modtest.One[string](t, h, `SELECT status || ':' || (outcome->>'ok') FROM assistant.pending_actions WHERE id = $1`, seed.action); st != "confirmed:true" {
		t.Errorf("pending action = %s, want confirmed and ok", st)
	}
	// The run goes on once, the model reads the summary where it asked for the plan, and answers.
	waitFor(t, "the run to be done", func() bool {
		return modtest.One[string](t, h, `SELECT state FROM assistant.runs WHERE id = $1`, seed.run) == "done"
	})
	reqs := model.Requests()
	if len(reqs) != 1 || len(reqs[0].Tools) != 0 {
		t.Fatalf("model requests = %d (tools %v), want one without tools", len(reqs), reqs)
	}
	var sawSummary bool
	for _, m := range reqs[0].Messages {
		if m.Role == "tool" && strings.Contains(m.Content, `"succeeded":250`) && strings.Contains(m.Content, `"bulk":true`) {
			sawSummary = true
		}
	}
	if !sawSummary {
		t.Errorf("the model was not given the summary: %+v", reqs[0].Messages)
	}
	if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.messages WHERE conversation_id = $1 AND role = 'assistant'`, seed.conv); n != 1 {
		t.Errorf("%d assistant messages, want the one short summary", n)
	}
	if live := modtest.One[int](t, h, `SELECT count(*) FROM identity.assistant_grants WHERE revoked_at IS NULL`); live != 0 {
		t.Errorf("%d grants live after the job", live)
	}
}

func TestBulkRun_TheCanaryRunsExactlyFiveAndNothingMoreWithoutContinue(t *testing.T) {
	t.Parallel()
	h := newHarness(t, summarising())
	_, user := person(t, h)
	ops := &fakeOps{}
	assistant.UseBulkOps(h.Pool(), ops)
	seed := seedBulk(t, h, user, bulkOpts{state: "canary", items: 12})

	runHere(t, h, seed.job)
	if got := ops.ids(); strings.Join(got, ",") != "0,1,2,3,4" {
		t.Fatalf("calls for items %v, want exactly the first five", got)
	}
	j := jobOf(t, h, seed.job)
	if j.State != "awaiting_continue" || j.Processed != 5 || j.Claimed {
		t.Errorf("job = %+v, want awaiting_continue after five, with no claim", j)
	}
	if reason := modtest.One[string](t, h, `SELECT pause_reason FROM assistant.bulk_jobs WHERE id = $1`, seed.job); reason != "canary_done" {
		t.Errorf("pause reason = %s, want canary_done", reason)
	}
	// Nothing runs on its own: a lapsed-claim sweep finds nothing to take, and the run still waits.
	assistant.ResumeLapsed(context.Background(), h.Pool())
	time.Sleep(200 * time.Millisecond)
	if got := ops.callCount(); got != 5 {
		t.Errorf("%d calls after the canary without a Continue, want still 5", got)
	}
	if run := modtest.One[string](t, h, `SELECT state FROM assistant.runs WHERE id = $1`, seed.run); run != "awaiting_confirmation" {
		t.Errorf("run = %s, want it to go on waiting", run)
	}
}

func TestBulkRun_ACanaryOfNoMoreThanFiveEndsTheJob(t *testing.T) {
	t.Parallel()
	h := newHarness(t, summarising())
	_, user := person(t, h)
	ops := &fakeOps{}
	assistant.UseBulkOps(h.Pool(), ops)
	seed := seedBulk(t, h, user, bulkOpts{state: "canary", items: 3})
	runHere(t, h, seed.job)
	if j := jobOf(t, h, seed.job); j.State != "done" || j.Succeeded != 3 {
		t.Errorf("job = %+v, want done after the three items", j)
	}
}

func TestBulkRun_ARowLevelRefusalFailsOneItemAndTheRestGoOn(t *testing.T) {
	t.Parallel()
	h := newHarness(t, summarising())
	_, user := person(t, h)
	ops := &fakeOps{respond: func(n int, req operations.CallRequest) (operations.CallResult, error) {
		switch toString(req.PathParams["id"]) {
		case "3":
			return refusedWith(403, "forbidden_row", "You may not change this record"), nil
		case "7":
			return refusedWith(500, "boom", "Something broke"), nil
		}
		return operations.CallResult{Status: 200}, nil
	}}
	assistant.UseBulkOps(h.Pool(), ops)
	seed := seedBulk(t, h, user, bulkOpts{items: 10})
	runHere(t, h, seed.job)

	j := jobOf(t, h, seed.job)
	if j.State != "done" || j.Succeeded != 8 || j.Failed != 2 || j.Processed != 10 {
		t.Fatalf("job = %+v, want done with 8 succeeded and 2 failed", j)
	}
	got := modtest.One[string](t, h, `SELECT string_agg(seq || ':' || http_status || ':' || problem_code || ':' || problem_title, '|' ORDER BY seq) FROM assistant.bulk_job_items WHERE status = 'failed'`)
	if got != "3:403:forbidden_row:You may not change this record|7:500:boom:Something broke" {
		t.Errorf("failures = %s, want the status, the problem's code and title and nothing else", got)
	}
	if kept := modtest.One[int](t, h, `SELECT count(*) FROM assistant.bulk_job_items WHERE problem_title LIKE '%long sentence%' OR problem_code LIKE '%long sentence%'`); kept != 0 {
		t.Error("a failure kept the problem's detail")
	}
}

func TestBulkRun_ARateLimitIsWaitedOutOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t, summarising())
	_, user := person(t, h)
	var mu sync.Mutex
	seen := map[string]int{}
	ops := &fakeOps{respond: func(_ int, req operations.CallRequest) (operations.CallResult, error) {
		id := toString(req.PathParams["id"])
		mu.Lock()
		seen[id]++
		n := seen[id]
		mu.Unlock()
		if id == "1" && n == 1 {
			return operations.CallResult{Status: 429, Header: http.Header{"Retry-After": []string{"1"}}}, nil
		}
		if id == "2" {
			return operations.CallResult{Status: 429, Header: http.Header{"Retry-After": []string{"1"}}}, nil
		}
		return operations.CallResult{Status: 200}, nil
	}}
	assistant.UseBulkOps(h.Pool(), ops)
	seed := seedBulk(t, h, user, bulkOpts{items: 4})
	start := time.Now()
	runHere(t, h, seed.job)

	if time.Since(start) < 2*time.Second {
		t.Errorf("finished in %v: the 429 was not waited out", time.Since(start))
	}
	j := jobOf(t, h, seed.job)
	if j.Succeeded != 3 || j.Failed != 1 {
		t.Errorf("job = %+v, want the retried item to succeed and the one limited twice to fail", j)
	}
	if seen["1"] != 2 || seen["2"] != 2 {
		t.Errorf("attempts = %v, want exactly one retry each", seen)
	}
}

func TestBulkRun_AStopTakesEffectBetweenChunks(t *testing.T) {
	t.Parallel()
	h := newHarness(t, summarising())
	_, user := person(t, h)
	seed := seedBulk(t, h, user, bulkOpts{items: 250})
	ops := &fakeOps{respond: func(n int, _ operations.CallRequest) (operations.CallResult, error) {
		if n == 10 { // the person presses Stop in the middle of the first chunk
			h.Exec(t, `UPDATE assistant.bulk_jobs SET stop_requested = true, state = 'stopping' WHERE id = $1`, seed.job)
		}
		return operations.CallResult{Status: 200}, nil
	}}
	assistant.UseBulkOps(h.Pool(), ops)
	runHere(t, h, seed.job)

	if got := ops.callCount(); got != 100 {
		t.Errorf("%d calls, want the chunk in progress finished and no more (100)", got)
	}
	j := jobOf(t, h, seed.job)
	if j.State != "stopped" || j.Error != "stopped_by_person" || j.Processed != 100 || j.Claimed {
		t.Errorf("job = %+v, want stopped by the person after one chunk", j)
	}
	if st := modtest.One[string](t, h, `SELECT status FROM assistant.pending_actions WHERE id = $1`, seed.action); st != "confirmed" {
		t.Errorf("pending action = %s, want confirmed: it started", st)
	}
	waitFor(t, "the run to go on", func() bool {
		return modtest.One[string](t, h, `SELECT state FROM assistant.runs WHERE id = $1`, seed.run) == "done"
	})
}

func TestBulkRun_AnOwnerNarrowingMidJobStopsIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t, summarising())
	_, user := person(t, h)
	var narrowed bool
	var mu sync.Mutex
	ops := &fakeOps{
		respond: func(n int, _ operations.CallRequest) (operations.CallResult, error) {
			if n == 50 { // the Owner narrows the level during the first chunk
				mu.Lock()
				narrowed = true
				mu.Unlock()
			}
			return operations.CallResult{Status: 200}, nil
		},
		mode: func() operations.Mode {
			mu.Lock()
			defer mu.Unlock()
			if narrowed {
				return operations.ModeNever
			}
			return operations.ModeConfirm
		},
	}
	assistant.UseBulkOps(h.Pool(), ops)
	seed := seedBulk(t, h, user, bulkOpts{items: 250})
	runHere(t, h, seed.job)

	j := jobOf(t, h, seed.job)
	if j.State != "stopped" || j.Error != "narrowed" || j.Processed != 100 {
		t.Errorf("job = %+v, want stopped (narrowed) at the end of the chunk", j)
	}
	if got := ops.callCount(); got != 100 {
		t.Errorf("%d calls, want the chunk finished and none after the Owner narrowed", got)
	}
}

func TestBulkRun_AGrantAboutToLapsePausesTheJobForContinue(t *testing.T) {
	t.Parallel()
	h := newHarness(t, summarising())
	_, user := person(t, h)
	ops := &fakeOps{}
	assistant.UseBulkOps(h.Pool(), ops)
	seed := seedBulk(t, h, user, bulkOpts{items: 250, done: 100, grantLeft: 4 * time.Minute})
	runHere(t, h, seed.job)

	j := jobOf(t, h, seed.job)
	if j.State != "awaiting_continue" || j.Claimed || j.Processed != 100 {
		t.Errorf("job = %+v, want awaiting_continue with 100 done and no claim", j)
	}
	if reason := modtest.One[string](t, h, `SELECT pause_reason FROM assistant.bulk_jobs WHERE id = $1`, seed.job); reason != "grant_expiring" {
		t.Errorf("pause reason = %s, want grant_expiring", reason)
	}
	if got := ops.callCount(); got != 0 {
		t.Errorf("%d calls made on a grant about to lapse, want none", got)
	}
}

func TestBulkRun_SigningOutStopsTheJobAtTheNextItem(t *testing.T) {
	t.Parallel()
	h := newHarness(t, summarising())
	_, user := person(t, h)
	ops := &fakeOps{respond: func(n int, _ operations.CallRequest) (operations.CallResult, error) {
		if n >= 3 { // the grant fails: its session ended
			return operations.CallResult{Status: 401, Body: json.RawMessage(`{"error":{"code":"unauthenticated"}}`)}, nil
		}
		return operations.CallResult{Status: 200}, nil
	}}
	assistant.UseBulkOps(h.Pool(), ops)
	seed := seedBulk(t, h, user, bulkOpts{items: 20})
	runHere(t, h, seed.job)

	j := jobOf(t, h, seed.job)
	if j.State != "stopped" || j.Error != "session_ended" || j.Succeeded != 3 || j.Failed != 0 {
		t.Errorf("job = %+v, want stopped (session_ended) after 3 items, the refused one not counted as failed", j)
	}
	if got := ops.callCount(); got != 4 {
		t.Errorf("%d calls, want the 3 that ran and the one that found the session ended", got)
	}
	if pending := pendingItems(t, h, seed.job); pending != 17 {
		t.Errorf("%d items pending, want 17: none is lost, a later Continue takes them", pending)
	}
}

func TestBulkRun_ALapsedClaimIsResumedOnceFromTheNextPendingItem(t *testing.T) {
	t.Parallel()
	h := newHarness(t, summarising())
	_, user := person(t, h)
	var mu sync.Mutex
	ran := map[string]int{}
	ops := &fakeOps{respond: func(_ int, req operations.CallRequest) (operations.CallResult, error) {
		mu.Lock()
		ran[toString(req.PathParams["id"])]++
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		return operations.CallResult{Status: 200}, nil
	}}
	assistant.UseBulkOps(h.Pool(), ops)
	// A replica that died: 40 items done, its claim lapsed a minute ago.
	seed := seedBulk(t, h, user, bulkOpts{items: 120, done: 40, claimedBy: "a-replica-that-died", claimedAt: -time.Minute})

	var wg sync.WaitGroup
	for range 4 { // several replicas see the job at once
		wg.Add(1)
		go func() { defer wg.Done(); assistant.ResumeLapsed(context.Background(), h.Pool()) }()
	}
	wg.Wait()
	waitFor(t, "the job to finish", func() bool {
		return modtest.One[string](t, h, `SELECT state FROM assistant.bulk_jobs WHERE id = $1`, seed.job) == "done"
	})

	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 80 {
		t.Errorf("%d distinct items ran, want the 80 that were pending", len(ran))
	}
	for id, n := range ran {
		if n != 1 {
			t.Errorf("item %s ran %d times, want once", id, n)
		}
		if toString(id) < "0" {
			t.Errorf("unexpected item %s", id)
		}
	}
	if _, again := ran["5"]; again {
		t.Error("an item already done ran again")
	}
	if j := jobOf(t, h, seed.job); j.Succeeded != 120 || j.Processed != 120 {
		t.Errorf("job = %+v, want every item counted once", j)
	}
}

func TestBulkRun_AClaimHeldByAnotherReplicaIsNotTaken(t *testing.T) {
	t.Parallel()
	h := newHarness(t, summarising())
	_, user := person(t, h)
	ops := &fakeOps{}
	assistant.UseBulkOps(h.Pool(), ops)
	seed := seedBulk(t, h, user, bulkOpts{items: 20, claimedBy: "another-replica", claimedAt: time.Minute})
	assistant.ResumeLapsed(context.Background(), h.Pool())
	if err := assistant.RunBulk(context.Background(), h.Pool(), seed.job); err == nil {
		t.Error("RunBulk took a claim another replica holds")
	}
	time.Sleep(150 * time.Millisecond)
	if got := ops.callCount(); got != 0 {
		t.Errorf("%d calls made on a job another replica runs", got)
	}
}

// The runner holds no connection while it calls the dispatcher and waits for
// another: on a pool of one the job, its summary and the worker cycle finish.
func TestBulkRun_NeedsOneConnection(t *testing.T) {
	t.Parallel()
	h := newHarness(t, summarising(), modtest.WithPoolMaxConns(1))
	_, user := person(t, h)
	ops := &fakeOps{respond: func(int, operations.CallRequest) (operations.CallResult, error) {
		// A real call reads and writes through the same pool, from inside the call.
		h.Exec(t, `SELECT 1`)
		return operations.CallResult{Status: 200}, nil
	}}
	assistant.UseBulkOps(h.Pool(), ops)
	seed := seedBulk(t, h, user, bulkOpts{items: 30})

	done := make(chan error, 1)
	go func() { done <- assistant.RunBulk(context.Background(), h.Pool(), seed.job) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunBulk: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the job did not finish on a pool of one connection")
	}
	if j := jobOf(t, h, seed.job); j.State != "done" || j.Succeeded != 30 {
		t.Errorf("job = %+v, want done", j)
	}
	waitFor(t, "the summary run", func() bool {
		return modtest.One[string](t, h, `SELECT state FROM assistant.runs WHERE id = $1`, seed.run) == "done"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := assistant.StaleRunsCycle(ctx, h.Deps()); err != nil {
		t.Errorf("the stale-runs cycle on a pool of one: %v", err)
	}
}

func TestBulkJobs_AreThePersonsOwnAnd404ToAnyoneElse(t *testing.T) {
	t.Parallel()
	h := newHarness(t, summarising())
	c, user := person(t, h)
	other, _ := person(t, h)
	seed := seedBulk(t, h, user, bulkOpts{state: "awaiting_continue", items: 8, done: 5})
	base := "/api/v1/assistant/bulk-jobs/" + seed.job.String()

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, base}, {http.MethodPost, base + "/continue"}, {http.MethodPost, base + "/stop"}, {http.MethodGet, base + "/failures.csv"},
	} {
		if r := other.Do(tc.method, tc.path, nil); r.Status != http.StatusNotFound {
			t.Errorf("another person's %s %s = %d, want 404", tc.method, tc.path, r.Status)
		}
		if r := other.Do(tc.method, "/api/v1/assistant/bulk-jobs/"+uuid.NewString()+strings.TrimPrefix(tc.path, base), nil); r.Status != http.StatusNotFound {
			t.Errorf("%s of a job that does not exist = %d, want 404", tc.method, r.Status)
		}
	}
	if j := jobOf(t, h, seed.job); j.State != "awaiting_continue" {
		t.Fatalf("job = %+v: another person changed it", j)
	}
	// And the person's own read shows the job, the counters and the plan's card.
	r := c.Do(http.MethodGet, base, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get the job: %d %s", r.Status, r.Body)
	}
	var job struct {
		State     string `json:"state"`
		Total     int    `json:"total"`
		Processed int    `json:"processed"`
		Succeeded int    `json:"succeeded"`
		Sample    []struct {
			Label string `json:"label"`
		} `json:"sample"`
		Canary []struct {
			Seq    int    `json:"seq"`
			Status string `json:"status"`
		} `json:"canary"`
	}
	r.JSON(&job)
	if job.State != "awaiting_continue" || job.Total != 8 || job.Processed != 5 || job.Succeeded != 5 || len(job.Sample) != 1 || len(job.Canary) != 5 || job.Canary[0].Status != "succeeded" {
		t.Errorf("job = %+v, want the counters, the sample and the canary's five", job)
	}
	var detail struct {
		PendingActions []struct {
			Kind string `json:"kind"`
			Bulk *struct {
				JobID string `json:"jobId"`
				State string `json:"state"`
			} `json:"bulk"`
		} `json:"pendingActions"`
	}
	c.Do(http.MethodGet, "/api/v1/assistant/conversations/"+seed.conv.String(), nil).JSON(&detail)
	if len(detail.PendingActions) != 1 || detail.PendingActions[0].Kind != "bulk" || detail.PendingActions[0].Bulk == nil || detail.PendingActions[0].Bulk.JobID != seed.job.String() {
		t.Errorf("the conversation's card = %+v, want the bulk plan with its job", detail.PendingActions)
	}
}

func TestBulkJobs_ContinueRunsTheRestOnceAndAJobNotWaitingIsAConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t, summarising())
	c, user := person(t, h)
	ops := &fakeOps{}
	assistant.UseBulkOps(h.Pool(), ops)
	seed := seedBulk(t, h, user, bulkOpts{state: "awaiting_continue", items: 12, done: 5})
	base := "/api/v1/assistant/bulk-jobs/" + seed.job.String()

	r := c.Do(http.MethodPost, base+"/continue", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("continue: %d %s", r.Status, r.Body)
	}
	waitFor(t, "the job to finish", func() bool {
		return modtest.One[string](t, h, `SELECT state FROM assistant.bulk_jobs WHERE id = $1`, seed.job) == "done"
	})
	if got := strings.Join(ops.ids(), ","); got != "5,6,7,8,9,10,11" {
		t.Errorf("calls for items %s, want the seven that were left", got)
	}
	if r := c.Do(http.MethodPost, base+"/continue", nil); r.Status != http.StatusConflict {
		t.Errorf("continue on a job that is over = %d, want 409", r.Status)
	}
	if r := c.Do(http.MethodPost, base+"/stop", nil); r.Status != http.StatusOK {
		t.Errorf("stop on a job that is over = %d, want it answered as it is", r.Status)
	}
	if st := modtest.One[string](t, h, `SELECT state FROM assistant.bulk_jobs WHERE id = $1`, seed.job); st != "done" {
		t.Errorf("job = %s: a stop on a finished job changed it", st)
	}
}

func TestBulkJobs_ContinueRefusesWhenTheOwnerHasNarrowedTheLevel(t *testing.T) {
	t.Parallel()
	h := newHarness(t, summarising())
	c, user := person(t, h)
	ops := &fakeOps{mode: func() operations.Mode { return operations.ModeNever }}
	assistant.UseBulkOps(h.Pool(), ops)
	seed := seedBulk(t, h, user, bulkOpts{state: "awaiting_continue", items: 12, done: 5})
	r := c.Do(http.MethodPost, "/api/v1/assistant/bulk-jobs/"+seed.job.String()+"/continue", nil)
	if r.Status != http.StatusForbidden {
		t.Fatalf("continue after the Owner narrowed = %d %s, want 403", r.Status, r.Body)
	}
	if got := ops.callCount(); got != 0 {
		t.Errorf("%d calls made", got)
	}
}

func TestBulkJobs_StopEndsAJobWaitingForContinueAndAsksARunningOneToStop(t *testing.T) {
	t.Parallel()
	h := newHarness(t, summarising())
	c, user := person(t, h)
	waiting := seedBulk(t, h, user, bulkOpts{state: "awaiting_continue", items: 12, done: 5})
	running := seedBulk(t, h, user, bulkOpts{state: "running", items: 12, done: 5, claimedBy: "x", claimedAt: time.Minute})

	if r := c.Do(http.MethodPost, "/api/v1/assistant/bulk-jobs/"+waiting.job.String()+"/stop", nil); r.Status != http.StatusOK {
		t.Fatalf("stop a waiting job: %d %s", r.Status, r.Body)
	}
	if j := jobOf(t, h, waiting.job); j.State != "stopped" || j.Error != "stopped_by_person" {
		t.Errorf("waiting job = %+v, want stopped at once", j)
	}
	waitFor(t, "the run of the stopped job to go on", func() bool {
		return modtest.One[string](t, h, `SELECT state FROM assistant.runs WHERE id = $1`, waiting.run) == "done"
	})

	var job struct {
		State string `json:"state"`
	}
	r := c.Do(http.MethodPost, "/api/v1/assistant/bulk-jobs/"+running.job.String()+"/stop", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("stop a running job: %d %s", r.Status, r.Body)
	}
	r.JSON(&job)
	if job.State != "stopping" || !modtest.One[bool](t, h, `SELECT stop_requested FROM assistant.bulk_jobs WHERE id = $1`, running.job) {
		t.Errorf("running job = %s, want stopping with the request recorded", job.State)
	}
	// Stopping the run is stopping the job.
	other := seedBulk(t, h, user, bulkOpts{state: "awaiting_continue", items: 12, done: 5})
	if r := c.Do(http.MethodPost, "/api/v1/assistant/runs/"+other.run.String()+"/stop", nil); r.Status != http.StatusOK {
		t.Fatalf("stop the run of a waiting job: %d %s", r.Status, r.Body)
	}
	if j := jobOf(t, h, other.job); j.State != "stopped" {
		t.Errorf("job of a stopped run = %+v, want stopped", j)
	}
}

func TestBulkJobs_TheFailedItemsDownloadAsACSV(t *testing.T) {
	t.Parallel()
	h := newHarness(t, summarising())
	c, user := person(t, h)
	seed := seedBulk(t, h, user, bulkOpts{state: "done", items: 3, done: 0})
	h.Exec(t, `UPDATE assistant.bulk_job_items SET status = 'failed', http_status = 403, problem_code = '=cmd|calc', problem_title = 'No; "quotes"', done_at = now() WHERE job_id = $1 AND seq = 1`, seed.job)
	h.Exec(t, `UPDATE assistant.bulk_job_items SET item = '{"bound":{"id":2},"label":"=HYPERLINK(\"http://evil\")"}', status = 'failed', http_status = 500, problem_code = 'boom', problem_title = '+SUM(1)', done_at = now() WHERE job_id = $1 AND seq = 2`, seed.job)
	h.Exec(t, `UPDATE assistant.bulk_jobs SET failed = 2 WHERE id = $1`, seed.job)

	r := c.Do(http.MethodGet, "/api/v1/assistant/bulk-jobs/"+seed.job.String()+"/failures.csv", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("csv: %d %s", r.Status, r.Body)
	}
	if ct := r.Header("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("content type = %s", ct)
	}
	if cd := r.Header("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, seed.job.String()) {
		t.Errorf("content disposition = %s", cd)
	}
	body := string(r.Body)
	if !strings.HasPrefix(body, "\ufeffitem;reference;http_status;problem_code;problem_title\r\n") {
		t.Errorf("csv header = %q", body)
	}
	for _, want := range []string{
		"'=HYPERLINK(", // a label that would run as a formula is neutralised
		"'=cmd|calc",
		`"No; ""quotes"""`, // the library quotes a cell with a separator and quotes
		"'+SUM(1)",
		";403;",
		";500;",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("csv lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, ";=") || strings.Contains(body, "\r\n=") {
		t.Errorf("csv has a cell that starts with =:\n%s", body)
	}
}

func TestBulkCSV_NeutralisesEveryFormulaStart(t *testing.T) {
	t.Parallel()
	labels := []string{"=1+1", "+1", "-1", "@SUM(1)", "\tx", "\rx", "fine"}
	codes := make([]string, len(labels))
	titles := make([]string, len(labels))
	raw, err := assistant.FailuresCSV(labels, codes, titles)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimPrefix(string(raw), "\ufeff"), "\r\n")
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		cell, _, _ := strings.Cut(line, ";")
		cell = strings.Trim(cell, `"`)
		if strings.ContainsAny(cell[:1], "=+-@\t\r") {
			t.Errorf("line %q starts with a formula character", line)
		}
	}
}

func TestBulkWorkers_ExpireWhatNobodyWentOnWithAndRedactIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t, summarising())
	_, user := person(t, h)
	preview := seedBulk(t, h, user, bulkOpts{state: "previewed", pending: "pending", items: 12, updated: -25 * time.Hour})
	waiting := seedBulk(t, h, user, bulkOpts{state: "awaiting_continue", items: 12, done: 5, updated: -25 * time.Hour})
	fresh := seedBulk(t, h, user, bulkOpts{state: "awaiting_continue", items: 12, done: 5, updated: -time.Hour})
	h.Exec(t, `UPDATE assistant.pending_actions SET expires_at = $1 WHERE id = $2`, h.Now().Add(-time.Hour), preview.action)

	n, err := assistant.StaleRunsCycle(context.Background(), h.Deps())
	if err != nil || n < 2 {
		t.Fatalf("StaleRunsCycle = %d, %v, want the two expired runs counted", n, err)
	}
	for name, s := range map[string]bulkSeed{"preview": preview, "waiting job": waiting} {
		if j := jobOf(t, h, s.job); j.State != "expired" {
			t.Errorf("%s = %+v, want expired", name, j)
		}
		if run := modtest.One[string](t, h, `SELECT state FROM assistant.runs WHERE id = $1`, s.run); run != "expired" {
			t.Errorf("run of the %s = %s, want expired", name, run)
		}
		if tpl := modtest.One[string](t, h, `SELECT template::text FROM assistant.bulk_jobs WHERE id = $1`, s.job); strings.Contains(tpl, "Renamed") {
			t.Errorf("%s kept its values: %s", name, tpl)
		}
	}
	if j := jobOf(t, h, fresh.job); j.State != "awaiting_continue" {
		t.Errorf("a job waiting for under a day = %+v, want it left alone", j)
	}
}

func TestBulkRetention_JobsGoWithTheirConversationAndThePersonsErasure(t *testing.T) {
	t.Parallel()
	h := newHarness(t, summarising())
	_, user := person(t, h)
	old := seedBulk(t, h, user, bulkOpts{state: "done", items: 6})
	h.Exec(t, `UPDATE assistant.conversations SET last_activity_at = $1 WHERE id = $2`, h.Now().AddDate(0, -13, 0), old.conv)
	kept := seedBulk(t, h, user, bulkOpts{state: "done", items: 6})
	gone := uuid.New() // a user identity no longer has
	erased := seedBulk(t, h, gone, bulkOpts{state: "done", items: 6})

	expired, erasedN, err := assistant.RetentionCycle(context.Background(), h.Deps())
	if err != nil || expired != 1 || erasedN != 1 {
		t.Fatalf("RetentionCycle = %d expired, %d erased, %v", expired, erasedN, err)
	}
	for name, s := range map[string]bulkSeed{"an old conversation": old, "an erased person": erased} {
		if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.bulk_jobs WHERE id = $1`, s.job); n != 0 {
			t.Errorf("a job of %s was kept", name)
		}
		if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.bulk_job_items WHERE job_id = $1`, s.job); n != 0 {
			t.Errorf("%d items of %s were kept", n, name)
		}
	}
	if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.bulk_job_items WHERE job_id = $1`, kept.job); n != 6 {
		t.Errorf("%d items of the kept job, want 6", n)
	}
}

func TestBulkJobs_DeletingTheConversationTakesTheJobWithIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t, summarising())
	c, user := person(t, h)
	seed := seedBulk(t, h, user, bulkOpts{state: "awaiting_continue", items: 6, done: 5})
	if r := c.Do(http.MethodDelete, "/api/v1/assistant/conversations/"+seed.conv.String(), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.bulk_jobs WHERE id = $1`, seed.job); n != 0 {
		t.Error("the job survived its conversation")
	}
}

// A run that waited an hour for its job began an hour ago. When the job ends
// and the run goes on to tell the person, the sweep that fails the runs still
// running after ten minutes must not take it for one the server lost.
func TestBulkRun_TheRunThatWaitedForTheJobIsNotStaleWhenItAnswers(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	model := modtest.NewFakeChatModel(modtest.ChatTurn{Text: "Done.", Block: release})
	h := newHarness(t, model)
	_, user := person(t, h)
	assistant.UseBulkOps(h.Pool(), &fakeOps{})
	seed := seedBulk(t, h, user, bulkOpts{items: 4, runAge: 3 * time.Hour})
	runHere(t, h, seed.job)

	waitFor(t, "the run to be answering", func() bool {
		return modtest.One[string](t, h, `SELECT state FROM assistant.runs WHERE id = $1`, seed.run) == "running"
	})
	if n, err := assistant.StaleRunsCycle(context.Background(), h.Deps()); err != nil || n != 0 {
		t.Fatalf("StaleRunsCycle = %d, %v: the sweep took the answering run for a lost one", n, err)
	}
	if st := modtest.One[string](t, h, `SELECT state FROM assistant.runs WHERE id = $1`, seed.run); st != "running" {
		t.Fatalf("run = %s while it answers", st)
	}
	close(release)
	waitFor(t, "the run to be done", func() bool {
		return modtest.One[string](t, h, `SELECT state FROM assistant.runs WHERE id = $1`, seed.run) == "done"
	})
}
