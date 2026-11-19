package identity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The Idempotency-Key middleware against identity's real store (idempotency
// design §7): a router over a small contract of two creating operations whose
// handlers count their runs, with identity's Access as the store, the request
// log and the Reject, and a principal each request names in X-Test-As.

const idempotencyTestContract = `
openapi: 3.0.3
info: { title: Test, version: "1" }
paths:
  /api/v1/t/things:
    post:
      operationId: postThings
      x-vantigo-access: permission:t:write
      requestBody: { content: { application/json: { schema: { type: object } } } }
      responses: { "201": { description: ok, content: { application/json: { schema: { type: object } } } } }
  /api/v1/t/things/{id}/act:
    post:
      operationId: postThingsByIdAct
      x-vantigo-access: permission:t:write
      parameters: [ { in: path, name: id, required: true, schema: { type: string } } ]
      requestBody: { content: { application/json: { schema: { type: object } } } }
      responses: { "201": { description: ok, content: { application/json: { schema: { type: object } } } } }
`

// idemAccess is identity's Access with Check answered by the test: every
// other method, the store and the request log among them, is identity's.
type idemAccess struct {
	*identity.Access
	principal func(*http.Request) contracts.Principal
}

func (a idemAccess) Check(r *http.Request, _ contracts.Rule) (contracts.Principal, error) {
	return a.principal(r), nil
}

// idemRig is one router over the test contract.
type idemRig struct {
	h           *harness
	router      *module.Router
	runs        atomic.Int64
	respond     func(w http.ResponseWriter, r *http.Request)
	mu          sync.Mutex
	integration uuid.UUID
	person      uuid.UUID
	other       uuid.UUID
	agent       uuid.UUID
	grant       uuid.UUID
	customer    int32
}

func newIdemRig(t *testing.T, opts ...harnessOption) *idemRig {
	t.Helper()
	h := newHarness(t, opts...)
	rig := &idemRig{h: h, integration: uuid.New(), person: uuid.New(), other: uuid.New(), agent: uuid.New(), grant: uuid.New(), customer: 42}
	rig.respond = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}
	doc, err := openapi3.NewLoader().LoadFromData([]byte(idempotencyTestContract))
	if err != nil {
		t.Fatal(err)
	}
	access := idemAccess{Access: h.access, principal: rig.principal}
	rig.router = module.NewRouter(module.RouterOptions{
		Doc: doc, Access: access, Limiter: h.deps.Limiter,
		Catalog: map[string]contracts.Permission{"t:write": {Key: "t:write", Display: "d", Description: "d", Delegable: true}},
	})
	handler := func(w http.ResponseWriter, r *http.Request) {
		rig.runs.Add(1)
		rig.mu.Lock()
		respond := rig.respond
		rig.mu.Unlock()
		respond(w, r)
	}
	rig.router.HandleFunc("POST /api/v1/t/things", handler)
	rig.router.HandleFunc("POST /api/v1/t/things/{id}/act", handler)
	if err := rig.router.Err(); err != nil {
		t.Fatal(err)
	}
	return rig
}

func (rig *idemRig) setRespond(f func(w http.ResponseWriter, r *http.Request)) {
	rig.mu.Lock()
	defer rig.mu.Unlock()
	rig.respond = f
}

// principal is who X-Test-As names: an integration's key ("key", the
// default), a person by their session ("person", "other"), the person through
// the Assistant ("assistant") or an agent's run scoped to a customer ("agent").
func (rig *idemRig) principal(r *http.Request) contracts.Principal {
	switch r.Header.Get("X-Test-As") {
	case "person":
		return contracts.Principal{UserID: rig.person, SessionID: uuid.New()}
	case "other":
		return contracts.Principal{UserID: rig.other, SessionID: uuid.New()}
	case "assistant":
		return contracts.Principal{UserID: rig.person, Via: &contracts.Via{Kind: contracts.ViaAssistant}}
	case "agent":
		c := rig.customer
		return contracts.Principal{UserID: rig.agent, Via: &contracts.Via{Kind: contracts.ViaAgent, AgentID: rig.agent, CustomerID: &c}}
	}
	return contracts.Principal{UserID: rig.integration, Via: &contracts.Via{Kind: contracts.ViaAPIKey, KeyID: rig.integration, RateLimitPerMinute: 100}}
}

type idemCall struct {
	as, path, key, body string
	ctx                 context.Context
	// noGrant leaves an assistant's or agent's request without the grant the
	// dispatcher puts on its context.
	noGrant bool
}

// do sends c and answers the response; a handler's panic is answered 500, as
// the recovery further out would.
func (rig *idemRig) do(t *testing.T, c idemCall) (rec *httptest.ResponseRecorder) {
	t.Helper()
	if c.path == "" {
		c.path = "/api/v1/t/things"
	}
	if c.body == "" {
		c.body = `{"name":"A"}`
	}
	req := httptest.NewRequest(http.MethodPost, c.path, strings.NewReader(c.body))
	if c.ctx != nil {
		req = req.WithContext(c.ctx)
	}
	switch {
	case c.noGrant:
	case c.as == "assistant":
		req = req.WithContext(contracts.WithGrant(req.Context(), contracts.GrantRef{Kind: contracts.GrantAssistant, ID: rig.grant}))
	case c.as == "agent":
		req = req.WithContext(contracts.WithGrant(req.Context(), contracts.GrantRef{Kind: contracts.GrantAgent, ID: rig.grant}))
	}
	req.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		req.Header.Set("Idempotency-Key", c.key)
	}
	req.Header.Set("X-Test-As", c.as)
	rec = httptest.NewRecorder()
	defer func() {
		if v := recover(); v != nil {
			rec = httptest.NewRecorder()
			rec.WriteHeader(http.StatusInternalServerError)
		}
	}()
	rig.router.ServeHTTP(rec, req)
	return rec
}

func idemErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error.Code
}

func wantRefusal(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status || idemErrorCode(t, rec) != code {
		t.Errorf("%d %s, want %d %s", rec.Code, rec.Body.String(), status, code)
	}
}

// The first answer is stored and replayed byte for byte: status, the headers
// a replay keeps, the body, and Idempotent-Replayed. Its body is sealed in the
// table, never kept in the clear.
func TestIdempotency_TheFirstAnswerIsReplayedByteForByte(t *testing.T) {
	t.Parallel()
	rig := newIdemRig(t)
	const secretish = `{"id":7,"name":"Kari Nordmann","email":"kari@example.test"}`
	rig.setRespond(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Location", "/api/v1/t/things/7")
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Language", "nb")
		w.Header().Set("X-Other", "not kept")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(secretish))
	})
	first := rig.do(t, idemCall{key: "k-1"})
	second := rig.do(t, idemCall{key: "k-1", body: " { \"name\" : \"A\" } "})
	if first.Code != http.StatusCreated || first.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("first: %d %v", first.Code, first.Header())
	}
	if second.Code != http.StatusCreated || second.Body.String() != secretish || second.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay: %d %q %v", second.Code, second.Body.String(), second.Header())
	}
	for _, name := range []string{"Content-Type", "Location", "ETag", "Content-Language"} {
		if second.Header().Get(name) != first.Header().Get(name) {
			t.Errorf("replay %s = %q, want %q", name, second.Header().Get(name), first.Header().Get(name))
		}
	}
	if second.Header().Get("X-Other") != "" {
		t.Error("the replay carries a header it should not keep")
	}
	if n := rig.runs.Load(); n != 1 {
		t.Errorf("the handler ran %d times, want 1", n)
	}
	var answer, fingerprint []byte
	if err := rig.h.pool.QueryRow(context.Background(), `SELECT answer, fingerprint FROM identity.idempotency_keys WHERE principal_id = $1 AND key = 'k-1'`, rig.integration).Scan(&answer, &fingerprint); err != nil {
		t.Fatal(err)
	}
	for _, plain := range []string{"Kari", `"id"`, "/api/v1/t/things/7", "Location", "201"} {
		if len(answer) == 0 || bytes.Contains(answer, []byte(plain)) {
			t.Errorf("the stored answer shows %q in the table", plain)
		}
	}
	if len(fingerprint) != 32 {
		t.Errorf("fingerprint of %d bytes", len(fingerprint))
	}
}

// A key used again for another request is 422; a header that is no key is 400
// before any work.
func TestIdempotency_AnotherRequestAndABadKeyAreRefused(t *testing.T) {
	t.Parallel()
	rig := newIdemRig(t)
	rig.do(t, idemCall{key: "k"})
	wantRefusal(t, rig.do(t, idemCall{key: "k", body: `{"name":"B"}`}), http.StatusUnprocessableEntity, "idempotency_key_reused")
	wantRefusal(t, rig.do(t, idemCall{key: "k", path: "/api/v1/t/things/1/act"}), http.StatusCreated, "")
	wantRefusal(t, rig.do(t, idemCall{key: strings.Repeat("x", 256)}), http.StatusBadRequest, "idempotency_key_invalid")
	wantRefusal(t, rig.do(t, idemCall{key: "a\x01"}), http.StatusBadRequest, "idempotency_key_invalid")
	if n := rig.runs.Load(); n != 2 {
		t.Errorf("the handler ran %d times, want 2", n)
	}
	// Without the header every request runs.
	rig.do(t, idemCall{})
	rig.do(t, idemCall{})
	if n := rig.runs.Load(); n != 4 {
		t.Errorf("the handler ran %d times, want 4", n)
	}
}

// Two requests at once with one key: one runs, the other is 409 in progress
// with Retry-After 1. Once the lease has run out a retry is 409 outcome
// unknown and the handler is not run again; the first, finishing after its
// lease, still stores its answer, which is replayed from then on (I26).
func TestIdempotency_ConcurrentDuplicatesAndAStaleLease(t *testing.T) {
	t.Parallel()
	rig := newIdemRig(t)
	entered, release := make(chan struct{}), make(chan struct{})
	rig.setRespond(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	})
	done := make(chan *httptest.ResponseRecorder)
	go func() { done <- rig.do(t, idemCall{key: "k"}) }()
	<-entered
	busy := rig.do(t, idemCall{key: "k"})
	wantRefusal(t, busy, http.StatusConflict, "idempotency_in_progress")
	if busy.Header().Get("Retry-After") != "1" {
		t.Errorf("Retry-After = %q, want 1", busy.Header().Get("Retry-After"))
	}
	rig.h.advance(3 * time.Minute)
	wantRefusal(t, rig.do(t, idemCall{key: "k"}), http.StatusConflict, "idempotency_outcome_unknown")
	close(release)
	if first := <-done; first.Code != http.StatusCreated {
		t.Fatalf("first: %d", first.Code)
	}
	if replay := rig.do(t, idemCall{key: "k"}); replay.Code != http.StatusCreated || replay.Header().Get("Idempotent-Replayed") != "true" {
		t.Errorf("after the first finished: %d %v, want its answer replayed", replay.Code, replay.Header())
	}
	if n := rig.runs.Load(); n != 1 {
		t.Errorf("the handler ran %d times, want 1", n)
	}
}

// A 5xx, a panic, an answer over 256 KB and a no-store success leave the key
// unknown: a retry is 409 outcome unknown and never runs the handler again.
// The client of the first still gets the real answer.
func TestIdempotency_AnUnknownOutcomeIsNeverRunAgain(t *testing.T) {
	t.Parallel()
	rig := newIdemRig(t)
	cases := map[string]func(w http.ResponseWriter, r *http.Request){
		"500": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
		"panic": func(http.ResponseWriter, *http.Request) {
			panic("the handler failed")
		},
		"oversize": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`"` + strings.Repeat("a", 300<<10) + `"`))
		},
		"no-store": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"secret":"s"}`))
		},
	}
	for name, respond := range cases {
		rig.setRespond(respond)
		before := rig.runs.Load()
		first := rig.do(t, idemCall{key: name})
		if name == "oversize" && first.Body.Len() < 300<<10 {
			t.Errorf("%s: the client got %d bytes", name, first.Body.Len())
		}
		wantRefusal(t, rig.do(t, idemCall{key: name}), http.StatusConflict, "idempotency_outcome_unknown")
		if n := rig.runs.Load() - before; n != 1 {
			t.Errorf("%s: the handler ran %d times, want 1", name, n)
		}
		var state string
		var body []byte
		if err := rig.h.pool.QueryRow(context.Background(), `SELECT state, answer FROM identity.idempotency_keys WHERE key = $1`, name).Scan(&state, &body); err != nil {
			t.Fatal(err)
		}
		if state != "unknown" || body != nil {
			t.Errorf("%s: %s with %d bytes stored, want unknown with none", name, state, len(body))
		}
	}
}

// A deterministic refusal (400, 404, 410, 422) is replayed; a 409 releases
// the claim, so an identical retry runs again.
func TestIdempotency_RefusalsAreReplayedAndAConflictReleases(t *testing.T) {
	t.Parallel()
	rig := newIdemRig(t)
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusGone, http.StatusUnprocessableEntity} {
		rig.setRespond(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":"x","message":"y"}}`))
		})
		before := rig.runs.Load()
		key := http.StatusText(status)
		rig.do(t, idemCall{key: key})
		replay := rig.do(t, idemCall{key: key})
		if replay.Code != status || replay.Header().Get("Idempotent-Replayed") != "true" || rig.runs.Load()-before != 1 {
			t.Errorf("%d: %d %v after %d runs, want the refusal replayed", status, replay.Code, replay.Header(), rig.runs.Load()-before)
		}
	}
	conflicts := atomic.Int64{}
	rig.setRespond(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if conflicts.Add(1) == 1 {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":"invoice_changed","message":"y"}}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":2}`))
	})
	if r := rig.do(t, idemCall{key: "conflict"}); r.Code != http.StatusConflict {
		t.Fatalf("first: %d", r.Code)
	}
	if r := rig.do(t, idemCall{key: "conflict"}); r.Code != http.StatusCreated || r.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("retry after a 409: %d %v, want it run again", r.Code, r.Header())
	}
	if r := rig.do(t, idemCall{key: "conflict"}); r.Code != http.StatusCreated || r.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("third: %d %v, want the 201 replayed", r.Code, r.Header())
	}
	if conflicts.Load() != 2 {
		t.Errorf("the handler ran %d times, want 2", conflicts.Load())
	}
}

// One key from two principals, on two operations, and from one person by two
// ways in (the session, the Assistant), is five keys.
func TestIdempotency_TheScopeIsThePrincipalTheOperationAndTheWayIn(t *testing.T) {
	t.Parallel()
	rig := newIdemRig(t)
	for _, c := range []idemCall{
		{as: "key", key: "same"},
		{as: "person", key: "same"},
		{as: "other", key: "same"},
		{as: "assistant", key: "same"},
		{as: "person", key: "same", path: "/api/v1/t/things/9/act"},
	} {
		if r := rig.do(t, c); r.Code != http.StatusCreated || r.Header().Get("Idempotent-Replayed") != "" {
			t.Errorf("%s %s: %d %v, want a run of its own", c.as, c.path, r.Code, r.Header())
		}
	}
	if n := rig.runs.Load(); n != 5 {
		t.Errorf("the handler ran %d times, want 5", n)
	}
	// The path parameter is part of the request.
	if r := rig.do(t, idemCall{as: "person", key: "same", path: "/api/v1/t/things/8/act"}); r.Code != http.StatusUnprocessableEntity {
		t.Errorf("another path parameter: %d, want 422", r.Code)
	}
}

// A key that has expired is claimed again, for any request.
func TestIdempotency_AnExpiredKeyIsClaimedAgain(t *testing.T) {
	t.Parallel()
	rig := newIdemRig(t)
	rig.do(t, idemCall{key: "k"})
	rig.h.advance(24*time.Hour + time.Second)
	if r := rig.do(t, idemCall{key: "k", body: `{"name":"B"}`}); r.Code != http.StatusCreated || r.Header().Get("Idempotent-Replayed") != "" {
		t.Errorf("after 24 hours: %d %v, want a new run", r.Code, r.Header())
	}
	if n := rig.runs.Load(); n != 2 {
		t.Errorf("the handler ran %d times, want 2", n)
	}
}

// A replay is a request: it counts against the integration's rate limit once
// and is logged with the reason idempotent_replay.
func TestIdempotency_AReplayIsCountedAndLogged(t *testing.T) {
	t.Parallel()
	rig := newIdemRig(t)
	first := rig.do(t, idemCall{key: "k"})
	replay := rig.do(t, idemCall{key: "k"})
	if first.Header().Get("RateLimit-Remaining") != "99" || replay.Header().Get("RateLimit-Remaining") != "98" {
		t.Errorf("RateLimit-Remaining %q then %q, want 99 then 98", first.Header().Get("RateLimit-Remaining"), replay.Header().Get("RateLimit-Remaining"))
	}
	got := rig.h.loggedRequests(t, rig.integration)
	if len(got) != 2 {
		t.Fatalf("%d logged, want 2", len(got))
	}
	reasons := map[string]int{}
	for _, r := range got {
		if r.operationID != "postThings" || r.status != http.StatusCreated {
			t.Errorf("logged %+v", r)
		}
		reasons[r.reason]++
	}
	if reasons[""] != 1 || reasons[contracts.APIReasonIdempotentReplay] != 1 {
		t.Errorf("reasons %v, want one run and one idempotent_replay", reasons)
	}
}

// A request whose caller goes away while the handler runs still stores its
// answer (I26).
func TestIdempotency_ACancelledRequestStillCompletes(t *testing.T) {
	t.Parallel()
	rig := newIdemRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	rig.setRespond(func(w http.ResponseWriter, _ *http.Request) {
		cancel()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":3}`))
	})
	rig.do(t, idemCall{key: "k", ctx: ctx})
	if r := rig.do(t, idemCall{key: "k"}); r.Code != http.StatusCreated || r.Body.String() != `{"id":3}` || r.Header().Get("Idempotent-Replayed") != "true" {
		t.Errorf("retry: %d %q %v, want the first answer", r.Code, r.Body.String(), r.Header())
	}
}

// A principal holding 2,000 live keys is refused another with 429
// idempotency_quota before any work.
func TestIdempotency_TheQuota(t *testing.T) {
	t.Parallel()
	rig := newIdemRig(t)
	rig.h.exec(t, `INSERT INTO identity.idempotency_keys (principal_id, via, operation_id, key, fingerprint, state, lease_id, lease_until, created_at, expires_at)
		SELECT $1, 'api_key', 'postThings', 'q-' || n, decode(repeat('00', 32), 'hex'), 'unknown', gen_random_uuid(), $2, $2, $2::timestamptz + interval '1 hour'
		FROM generate_series(1, 2000) n`, rig.integration, rig.h.now())
	r := rig.do(t, idemCall{key: "one-more"})
	wantRefusal(t, r, http.StatusTooManyRequests, "idempotency_quota")
	if r.Header().Get("Retry-After") != "60" {
		t.Errorf("Retry-After = %q, want 60", r.Header().Get("Retry-After"))
	}
	if n := rig.runs.Load(); n != 0 {
		t.Errorf("the handler ran %d times", n)
	}
	// Another principal is not affected.
	if r := rig.do(t, idemCall{as: "person", key: "one-more"}); r.Code != http.StatusCreated {
		t.Errorf("another principal: %d", r.Code)
	}
}

// The worker prunes every expired key, past one batch, and keeps the live
// ones, with the API keys' part switched off.
func TestIdempotency_TheWorkerPrunesExpiredKeys(t *testing.T) {
	t.Parallel()
	rig := newIdemRig(t, withEnv("IDENTITY_API_KEYS_ENABLED", "0"))
	rig.do(t, idemCall{key: "live"})
	rig.h.exec(t, `INSERT INTO identity.idempotency_keys (principal_id, via, operation_id, key, fingerprint, state, lease_id, lease_until, created_at, expires_at)
		SELECT gen_random_uuid(), 'session', 'postThings', 'old-' || n, decode(repeat('00', 32), 'hex'), 'unknown', gen_random_uuid(), $1, $1, $1::timestamptz - interval '1 second'
		FROM generate_series(1, 2500) n`, rig.h.now())
	w := apiKeysWorker(rig.h)
	if w == nil {
		t.Fatal("no worker prunes the keys")
	}
	runCycle(t, w)
	var expired, live int
	if err := rig.h.pool.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE expires_at <= $1), count(*) FILTER (WHERE expires_at > $1) FROM identity.idempotency_keys`, rig.h.now()).Scan(&expired, &live); err != nil {
		t.Fatal(err)
	}
	if expired != 0 || live != 1 {
		t.Errorf("%d expired and %d live keys left, want 0 and 1", expired, live)
	}
	if rig.h.partitionExists(t, "api_requests_20260911") {
		t.Error("the API keys' part ran while switched off")
	}
}

// Erasing a person deletes their keys; anonymising a customer deletes the
// keys of agent runs scoped to it.
func TestIdempotency_ErasureDeletesTheKeys(t *testing.T) {
	t.Parallel()
	rig := newIdemRig(t)
	ctx := context.Background()
	employeeRole(t, rig.h)
	p := rig.h.accounts(t)
	ref := mustPrepare(t, p, contracts.AccountSpec{Email: "erase.me@example.test", DisplayName: "Erase Me", PreferredLanguage: "nb", EmployeeNumber: "E-IDEM"})
	mustActivate(t, p, ref.UserID, contracts.ActivateOpts{})
	if _, err := p.Deactivate(ctx, systemActor, ref.UserID); err != nil {
		t.Fatal(err)
	}
	rig.person = ref.UserID
	rig.do(t, idemCall{as: "person", key: "k"})
	rig.do(t, idemCall{as: "agent", key: "k"})
	rig.do(t, idemCall{as: "other", key: "k"})
	count := func(principal uuid.UUID) (n int) {
		t.Helper()
		if err := rig.h.pool.QueryRow(ctx, `SELECT count(*) FROM identity.idempotency_keys WHERE principal_id = $1`, principal).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(rig.person) != 1 || count(rig.agent) != 1 {
		t.Fatal("the keys were not stored")
	}
	if err := p.Erase(ctx, contracts.Actor{System: contracts.SystemActorStaffRetention}, ref.UserID); err != nil {
		t.Fatalf("Erase: %v", err)
	}
	if count(rig.person) != 0 {
		t.Error("the erased person's keys are kept")
	}

	data := identity.Module(rig.h.access).CustomerPersonalData(rig.h.deps)
	tx, err := rig.h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	erased, err := data.EraseCustomerData(ctx, tx, rig.customer)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if len(erased) != 1 || erased[0].Kind != "identity.idempotencyKeys" || erased[0].Count != 1 {
		t.Errorf("erased %+v, want one identity.idempotencyKeys", erased)
	}
	if count(rig.agent) != 0 || count(rig.other) != 1 {
		t.Error("the anonymisation deleted the wrong keys")
	}
}

// IDEMPOTENCY_ENABLED=0: the header is ignored, every request runs, nothing
// is stored.
func TestIdempotency_TheKillSwitch(t *testing.T) {
	t.Parallel()
	rig := newIdemRig(t, withEnv("IDEMPOTENCY_ENABLED", "0"))
	for range 2 {
		// The answer says the key was ignored (security review S3).
		if r := rig.do(t, idemCall{key: "k"}); r.Code != http.StatusCreated || r.Header().Get("Idempotent-Replayed") != "" ||
			r.Header().Get("Idempotency-Key-Ignored") != "true" {
			t.Errorf("%d %v", r.Code, r.Header())
		}
	}
	if r := rig.do(t, idemCall{}); r.Header().Get("Idempotency-Key-Ignored") != "" {
		t.Errorf("a request without a key is marked: %v", r.Header())
	}
	if r := rig.do(t, idemCall{key: "a\x01"}); r.Code != http.StatusCreated {
		t.Errorf("a bad key with the switch off: %d, want the header ignored", r.Code)
	}
	if n := rig.runs.Load(); n != 4 {
		t.Errorf("the handler ran %d times, want 4", n)
	}
	var n int
	if err := rig.h.pool.QueryRow(context.Background(), `SELECT count(*) FROM identity.idempotency_keys`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d keys stored (%v), want none", n, err)
	}
}

// A caller gone while the handler runs, which answers its error through the
// module's ResponseError (and so writes nothing), leaves the key unknown: a
// retry is 409 outcome unknown, never an empty 200.
func TestIdempotency_AGoneCallerIsUnknown(t *testing.T) {
	t.Parallel()
	rig := newIdemRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	rig.setRespond(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		module.ResponseError()(w, r, r.Context().Err())
	})
	rig.do(t, idemCall{key: "gone", ctx: ctx})
	wantRefusal(t, rig.do(t, idemCall{key: "gone"}), http.StatusConflict, "idempotency_outcome_unknown")
	if n := rig.runs.Load(); n != 1 {
		t.Errorf("the handler ran %d times, want 1", n)
	}
}

// At its quota a principal is refused a new key, and still gets the stored
// answer for a key it already has.
func TestIdempotency_AtTheQuotaAnExistingKeyIsAnswered(t *testing.T) {
	t.Parallel()
	rig := newIdemRig(t)
	rig.do(t, idemCall{key: "kept"})
	rig.h.exec(t, `INSERT INTO identity.idempotency_keys (principal_id, via, operation_id, key, fingerprint, state, lease_id, lease_until, created_at, expires_at)
		SELECT $1, 'api_key', 'postThings', 'q-' || n, decode(repeat('00', 32), 'hex'), 'unknown', gen_random_uuid(), $2, $2, $2::timestamptz + interval '1 hour'
		FROM generate_series(1, 1999) n`, rig.integration, rig.h.now())
	wantRefusal(t, rig.do(t, idemCall{key: "new"}), http.StatusTooManyRequests, "idempotency_quota")
	if r := rig.do(t, idemCall{key: "kept"}); r.Code != http.StatusCreated || r.Header().Get("Idempotent-Replayed") != "true" {
		t.Errorf("an existing key at the quota: %d %v, want its answer replayed", r.Code, r.Header())
	}
}

// Claims at once never take a principal past its quota: the count and the
// insert are one decision.
func TestIdempotency_TheQuotaHoldsUnderConcurrentClaims(t *testing.T) {
	t.Parallel()
	rig := newIdemRig(t)
	rig.h.exec(t, `INSERT INTO identity.idempotency_keys (principal_id, via, operation_id, key, fingerprint, state, lease_id, lease_until, created_at, expires_at)
		SELECT $1, 'api_key', 'postThings', 'q-' || n, decode(repeat('00', 32), 'hex'), 'unknown', gen_random_uuid(), $2, $2, $2::timestamptz + interval '1 hour'
		FROM generate_series(1, 1995) n`, rig.integration, rig.h.now())
	var wg sync.WaitGroup
	codes := make(chan int, 20)
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- rig.do(t, idemCall{key: "c-" + strings.Repeat("x", i+1)}).Code
		}()
	}
	wg.Wait()
	close(codes)
	got := map[int]int{}
	for c := range codes {
		got[c]++
	}
	if got[http.StatusCreated] != 5 || got[http.StatusTooManyRequests] != 15 {
		t.Errorf("answers %v, want 5 created and 15 refused at the quota", got)
	}
}

// Anonymising a customer stops every answer stored before it from being
// replayed, whoever stored it; a key claimed after it replays as usual.
func TestIdempotency_AnAnonymisationStopsEarlierReplays(t *testing.T) {
	t.Parallel()
	rig := newIdemRig(t)
	ctx := context.Background()
	rig.do(t, idemCall{key: "before"})
	rig.h.advance(time.Second)
	data := identity.Module(rig.h.access).CustomerPersonalData(rig.h.deps)
	tx, err := rig.h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.EraseCustomerData(ctx, tx, 7); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wantRefusal(t, rig.do(t, idemCall{key: "before"}), http.StatusConflict, "idempotency_outcome_unknown")
	rig.h.advance(time.Second)
	rig.do(t, idemCall{key: "after"})
	if r := rig.do(t, idemCall{key: "after"}); r.Code != http.StatusCreated || r.Header().Get("Idempotent-Replayed") != "true" {
		t.Errorf("after the anonymisation: %d %v, want a replay", r.Code, r.Header())
	}
	if n := rig.runs.Load(); n != 2 {
		t.Errorf("the handler ran %d times, want 2", n)
	}
}

// The Assistant's or an agent's principal with no grant on the context has no
// scope to keep a key in: the header is not honoured, and each request runs.
func TestIdempotency_AGrantPrincipalWithoutItsGrantIsNotKept(t *testing.T) {
	t.Parallel()
	rig := newIdemRig(t)
	for _, as := range []string{"assistant", "agent"} {
		for range 2 {
			if r := rig.do(t, idemCall{as: as, key: "k", noGrant: true}); r.Code != http.StatusCreated || r.Header().Get("Idempotent-Replayed") != "" {
				t.Errorf("%s: %d %v, want a run", as, r.Code, r.Header())
			}
		}
	}
	if n := rig.runs.Load(); n != 4 {
		t.Errorf("the handler ran %d times, want 4", n)
	}
	var n int
	if err := rig.h.pool.QueryRow(context.Background(), `SELECT count(*) FROM identity.idempotency_keys`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d keys stored (%v), want none", n, err)
	}
}

// An Owner deleting a person deletes the person's keys.
func TestIdempotency_DeletingAUserDeletesTheKeys(t *testing.T) {
	t.Parallel()
	rig := newIdemRig(t)
	owner, _ := rig.h.bootstrapOwner(t)
	rig.person = rig.h.createUser(t, owner, "deleted@example.test", identity.RoleUser)
	rig.do(t, idemCall{as: "person", key: "k"})
	if r := owner.do(http.MethodDelete, "/api/v1/identity/owner/users/"+rig.person.String(), nil); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if n := rig.h.count(t, `SELECT count(*) FROM identity.idempotency_keys WHERE principal_id = $1`, rig.person); n != 0 {
		t.Errorf("%d keys kept for the deleted user", n)
	}
}

// With IDEMPOTENCY_ENABLED off the worker still prunes what was kept before.
func TestIdempotency_ThePruneRunsWithTheSwitchOff(t *testing.T) {
	t.Parallel()
	rig := newIdemRig(t, withEnv("IDEMPOTENCY_ENABLED", "0"))
	rig.h.exec(t, `INSERT INTO identity.idempotency_keys (principal_id, via, operation_id, key, fingerprint, state, lease_id, lease_until, created_at, expires_at)
		VALUES (gen_random_uuid(), 'session', 'postThings', 'old', decode(repeat('00', 32), 'hex'), 'unknown', gen_random_uuid(), $1, $1, $1::timestamptz - interval '1 second')`, rig.h.now())
	w := apiKeysWorker(rig.h)
	if w == nil {
		t.Fatal("no worker")
	}
	runCycle(t, w)
	if n := rig.h.count(t, `SELECT count(*) FROM identity.idempotency_keys`); n != 0 {
		t.Errorf("%d expired keys left", n)
	}
}
