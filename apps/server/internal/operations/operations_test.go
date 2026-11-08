package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// These tests bind the dispatcher to a small synthetic contract, a table-driven
// Access and a recording handler. What they pin is the dispatcher's own
// decisions: the mode of every kind of operation, what a person may list, what
// a model is shown, and how a call is built and answered. That the router and
// identity refuse what they should is proved against the real ones in
// integration_test.go.

type fakeAccess struct {
	mu    sync.Mutex
	holds map[uuid.UUID]map[string]bool
	asked []string
	err   error
}

func (f *fakeAccess) Check(*http.Request, contracts.Rule) (contracts.Principal, error) {
	return contracts.Principal{}, errors.New("not used")
}
func (f *fakeAccess) Reject(http.ResponseWriter, *http.Request, contracts.Rule, error) {}
func (f *fakeAccess) UserHolds(_ context.Context, userID uuid.UUID, keys ...string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, strings.Join(keys, "+"))
	if f.err != nil {
		return false, f.err
	}
	for _, k := range keys {
		if !f.holds[userID][k] {
			return false, nil
		}
	}
	return true, nil
}

func (f *fakeAccess) questions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked)
}

// seen is what the recording handler saw of one request.
type seen struct {
	Method, Path, RawQuery, RemoteAddr string
	EscapedPath                        string
	Header                             http.Header
	Body                               string
	Grant                              uuid.UUID
	HasGrant                           bool
	PrincipalPresent                   bool
	CallerValue                        any
	Cancelled                          func() bool
}

type callerKey struct{}

// recordingHandler answers by respond (200 with {"ok":true} when nil) and
// remembers the last request.
type recordingHandler struct {
	mu      sync.Mutex
	last    seen
	lastRef contracts.GrantRef
	hasRef  bool
	count   int
	respond func(w http.ResponseWriter, r *http.Request)
}

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body := new(strings.Builder)
	if r.Body != nil {
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			body.Write(buf[:n])
			if err != nil {
				break
			}
		}
	}
	grant, hasGrant := contracts.AssistantGrantFrom(r.Context())
	ref, hasRef := contracts.GrantFrom(r.Context())
	_, principal := contracts.PrincipalFrom(r.Context())
	h.mu.Lock()
	h.count++
	h.lastRef, h.hasRef = ref, hasRef
	h.last = seen{
		Method: r.Method, Path: r.URL.Path, EscapedPath: r.URL.EscapedPath(), RawQuery: r.URL.RawQuery,
		RemoteAddr: r.RemoteAddr, Header: r.Header.Clone(), Body: body.String(),
		Grant: grant, HasGrant: hasGrant, PrincipalPresent: principal,
		CallerValue: r.Context().Value(callerKey{}),
		Cancelled:   func() bool { return r.Context().Err() != nil },
	}
	h.mu.Unlock()
	if h.respond != nil {
		h.respond(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (h *recordingHandler) seen() (seen, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.last, h.count
}

// The synthetic contract. Each operation is named for what it exercises.
const syntheticContract = `{
  "openapi": "3.0.3",
  "info": {"title": "t", "version": "1"},
  "paths": {
    "/api/v1/things": {
      "get": {
        "operationId": "listThings", "summary": "List the things", "description": "Every thing the caller may see, newest first.",
        "tags": ["Things"], "x-vantigo-access": "permission:things:view",
        "parameters": [
          {"in": "query", "name": "search", "schema": {"type": "string", "description": "Matches the name."}},
          {"in": "query", "name": "status", "schema": {"type": "string", "enum": ["open", "closed"]}},
          {"in": "query", "name": "tag", "schema": {"type": "array", "items": {"type": "string"}}},
          {"in": "query", "name": "page", "schema": {"type": "integer"}}
        ],
        "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ThingList"}}}}}
      },
      "post": {
        "operationId": "createThing", "summary": "Create a thing", "tags": ["Things"],
        "x-vantigo-access": "permission:things:edit",
        "requestBody": {"required": true, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ThingRequest"}}}},
        "responses": {"201": {"description": "Created", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Thing"}}}}}
      }
    },
    "/api/v1/things/{id}": {
      "parameters": [{"in": "path", "name": "id", "required": true, "schema": {"type": "string"}}],
      "get": {
        "operationId": "getThing", "summary": "Get a thing by id", "tags": ["Things"], "x-vantigo-access": "permission:things:view",
        "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Thing"}}}}, "404": {"description": "Not Found"}}
      },
      "put": {
        "operationId": "updateThing", "summary": "Update a thing", "tags": ["Things"], "x-vantigo-access": "permission:things:edit",
        "parameters": [
          {"in": "header", "name": "If-Match", "required": true, "schema": {"type": "string"}},
          {"in": "header", "name": "Idempotency-Key", "schema": {"type": "string"}}
        ],
        "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/ThingRequest"}}}},
        "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Thing"}}}}}
      },
      "delete": {
        "operationId": "deleteThing", "summary": "Delete a thing", "tags": ["Things"], "x-vantigo-access": "permission:things:edit",
        "responses": {"204": {"description": "Deleted"}}
      },
      "patch": {
        "operationId": "patchThing", "summary": "Change part of a thing", "tags": ["Things"], "x-vantigo-access": "permission:things:edit",
        "responses": {"204": {"description": "Changed"}}
      }
    },
    "/api/v1/things/{id}/notes/{noteId}": {
      "get": {
        "operationId": "getThingNote", "summary": "Get a note on a thing", "tags": ["Notes"], "x-vantigo-access": "permission:things:view",
        "parameters": [
          {"in": "path", "name": "id", "required": true, "schema": {"type": "string"}},
          {"in": "path", "name": "noteId", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"type": "object", "properties": {"text": {"type": "string"}}}}}}}
      }
    },
    "/api/v1/things/search": {
      "post": {
        "operationId": "searchThings", "summary": "Search the things by a filter in the body", "tags": ["Things"],
        "x-vantigo-access": "permission:things:view", "x-vantigo-assistant": "auto",
        "requestBody": {"content": {"application/json": {"schema": {"type": "object", "properties": {"name": {"type": "string"}}}}}},
        "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ThingList"}}}}}
      }
    },
    "/api/v1/things/export": {
      "get": {
        "operationId": "exportThings", "summary": "Export the things as CSV", "tags": ["Things"], "x-vantigo-access": "permission:things:view",
        "responses": {"200": {"description": "OK", "content": {"text/csv": {"schema": {"type": "string"}}}}}
      }
    },
    "/api/v1/things/{id}/photo": {
      "parameters": [{"in": "path", "name": "id", "required": true, "schema": {"type": "string"}}],
      "post": {
        "operationId": "uploadThingPhoto", "summary": "Upload a photo", "tags": ["Things"], "x-vantigo-access": "permission:things:edit",
        "requestBody": {"content": {"multipart/form-data": {"schema": {"type": "object"}}}},
        "responses": {"204": {"description": "Uploaded"}}
      },
      "put": {
        "operationId": "putThingPhotoRaw", "summary": "Put a photo as raw bytes", "tags": ["Things"], "x-vantigo-access": "permission:things:edit",
        "requestBody": {"content": {"image/png": {"schema": {"type": "string", "format": "binary"}}}},
        "responses": {"204": {"description": "Uploaded"}}
      },
      "get": {
        "operationId": "getThingPhoto", "summary": "Get the photo", "tags": ["Things"], "x-vantigo-access": "permission:things:view",
        "responses": {"200": {"description": "OK", "content": {"image/png": {"schema": {"type": "string", "format": "binary"}}}}}
      }
    },
    "/api/v1/things/{id}/either": {
      "parameters": [{"in": "path", "name": "id", "required": true, "schema": {"type": "string"}}],
      "post": {
        "operationId": "createEither", "summary": "Take JSON or a form", "tags": ["Things"], "x-vantigo-access": "permission:things:edit",
        "requestBody": {"content": {"multipart/form-data": {"schema": {"type": "object"}}, "application/json": {"schema": {"type": "object"}}}},
        "responses": {"204": {"description": "OK"}}
      }
    },
    "/api/v1/secrets/{id}": {
      "parameters": [{"in": "path", "name": "id", "required": true, "schema": {"type": "string"}}],
      "get": {
        "operationId": "getSecret", "summary": "Get a sensitive record", "tags": ["Secrets"], "x-vantigo-access": "permission:things:view+things:sensitive",
        "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"type": "object"}}}}}
      },
      "put": {
        "operationId": "putSecretMarked", "summary": "Change a sensitive record, marked confirm", "tags": ["Secrets"],
        "x-vantigo-access": "permission:things:sensitive", "x-vantigo-assistant": "confirm",
        "responses": {"204": {"description": "OK"}}
      }
    },
    "/api/v1/unlisted/{id}": {
      "parameters": [{"in": "path", "name": "id", "required": true, "schema": {"type": "string"}}],
      "get": {
        "operationId": "getUncatalogued", "summary": "Needs a permission nobody declared", "tags": ["Odd"], "x-vantigo-access": "permission:ghost:view",
        "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    },
    "/api/v1/rules/anonymous": {"get": {"operationId": "ruleAnonymous", "summary": "Anonymous", "x-vantigo-access": "anonymous", "responses": {"204": {"description": "OK"}}}},
    "/api/v1/rules/scim": {"get": {"operationId": "ruleScim", "summary": "Scim", "x-vantigo-access": "scim", "responses": {"204": {"description": "OK"}}}},
    "/api/v1/rules/session": {
      "get": {"operationId": "ruleSession", "summary": "Session", "x-vantigo-access": "session", "responses": {"204": {"description": "OK"}}},
      "post": {"operationId": "ruleSessionMarked", "summary": "Session, marked auto", "x-vantigo-access": "session", "x-vantigo-assistant": "auto", "responses": {"204": {"description": "OK"}}}
    },
    "/api/v1/rules/owner": {"get": {"operationId": "ruleOwner", "summary": "Owner", "x-vantigo-access": "policy:Owner", "responses": {"204": {"description": "OK"}}}},
    "/api/v1/rules/owner-management": {"get": {"operationId": "ruleOwnerManagement", "summary": "OwnerManagement", "x-vantigo-access": "policy:OwnerManagement", "responses": {"204": {"description": "OK"}}}},
    "/api/v1/rules/authorization": {"get": {"operationId": "ruleAuthorization", "summary": "AuthorizationManagement", "x-vantigo-access": "policy:AuthorizationManagement", "responses": {"204": {"description": "OK"}}}},
    "/api/v1/rules/admin": {"get": {"operationId": "ruleSystemAdmin", "summary": "SystemAdmin", "x-vantigo-access": "policy:SystemAdmin", "responses": {"204": {"description": "OK"}}}},
    "/api/v1/rules/compound": {"get": {"operationId": "ruleCompound", "summary": "ActiveAccount and Owner", "x-vantigo-access": "policy:ActiveAccount+Owner", "responses": {"204": {"description": "OK"}}}},
    "/api/v1/rules/active": {
      "get": {"operationId": "ruleActive", "summary": "ActiveAccount", "x-vantigo-access": "policy:ActiveAccount", "responses": {"204": {"description": "OK"}}},
      "post": {"operationId": "ruleActivePost", "summary": "ActiveAccount write", "x-vantigo-access": "policy:ActiveAccount", "responses": {"204": {"description": "OK"}}}
    },
    "/api/v1/rules/business": {"get": {"operationId": "ruleBusiness", "summary": "Business", "x-vantigo-access": "policy:Business", "responses": {"204": {"description": "OK"}}}},
    "/api/v1/rules/invalid": {"get": {"operationId": "ruleInvalid", "summary": "Broken rule", "x-vantigo-access": "permission:", "responses": {"204": {"description": "OK"}}}},
    "/api/v1/rules/never-marked": {"get": {"operationId": "markedNever", "summary": "A read marked never", "x-vantigo-access": "permission:things:view", "x-vantigo-assistant": "never", "responses": {"204": {"description": "OK"}}}},
    "/api/v1/rules/confirm-marked": {"get": {"operationId": "markedConfirm", "summary": "A read marked confirm", "x-vantigo-access": "permission:things:view", "x-vantigo-assistant": "confirm", "responses": {"204": {"description": "OK"}}}},
    "/api/v1/rules/bad-mark": {"get": {"operationId": "markedSometimes", "summary": "A read with a mark nobody validated", "x-vantigo-access": "permission:things:view", "x-vantigo-assistant": "sometimes", "responses": {"204": {"description": "OK"}}}},
    "/api/v1/rules/redirect": {"get": {"operationId": "redirects", "summary": "Redirects", "x-vantigo-access": "permission:things:view", "responses": {"302": {"description": "Found", "headers": {"Location": {"schema": {"type": "string"}}}}}}},
    "/api/v1/assistant/conversations": {"get": {"operationId": "assistantConversations", "summary": "The assistant's own API", "x-vantigo-access": "permission:things:view", "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"type": "object"}}}}}}},
    "/api/v1/deep": {
      "get": {
        "operationId": "getDeep", "summary": "Get a deeply nested thing", "tags": ["Odd"], "x-vantigo-access": "permission:things:view",
        "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Node"}}}}}
      }
    },
    "/api/v1/huge": {
      "post": {
        "operationId": "createHuge", "summary": "Create a thing with very many fields", "tags": ["Odd"], "x-vantigo-access": "permission:things:edit",
        "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/Huge"}}}},
        "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Huge"}}}}}
      }
    }
  },
  "components": {"schemas": {
    "Thing": {"type": "object", "required": ["id", "name"], "description": "One thing.", "properties": {
      "id": {"type": "string", "format": "uuid"},
      "name": {"type": "string", "description": "What it is called.", "maxLength": 80},
      "state": {"$ref": "#/components/schemas/ThingState"},
      "owner": {"$ref": "#/components/schemas/Person"}
    }},
    "ThingState": {"type": "string", "description": "Where the thing is in its life.", "enum": ["draft", "active", "retired"]},
    "Person": {"type": "object", "properties": {"name": {"type": "string"}, "manager": {"$ref": "#/components/schemas/Person"}}},
    "ThingList": {"type": "object", "properties": {"data": {"type": "array", "items": {"$ref": "#/components/schemas/Thing"}}, "total": {"type": "integer"}}},
    "ThingRequest": {"type": "object", "required": ["name"], "properties": {"name": {"type": "string"}, "state": {"$ref": "#/components/schemas/ThingState"}}},
    "Node": {"type": "object", "properties": {"label": {"type": "string"}, "child": {"$ref": "#/components/schemas/Level1"}}},
    "Level1": {"type": "object", "properties": {"label": {"type": "string"}, "child": {"$ref": "#/components/schemas/Level2"}}},
    "Level2": {"type": "object", "properties": {"label": {"type": "string"}, "child": {"$ref": "#/components/schemas/Level3"}}},
    "Level3": {"type": "object", "properties": {"label": {"type": "string"}, "child": {"$ref": "#/components/schemas/Level4"}}},
    "Level4": {"type": "object", "properties": {"label": {"type": "string"}, "child": {"$ref": "#/components/schemas/Level5"}}},
    "Level5": {"type": "object", "properties": {"label": {"type": "string"}, "child": {"$ref": "#/components/schemas/Level6"}}},
    "Level6": {"type": "object", "properties": {"label": {"type": "string"}, "child": {"$ref": "#/components/schemas/Level7"}}},
    "Level7": {"type": "object", "properties": {"label": {"type": "string"}, "child": {"$ref": "#/components/schemas/Level8"}}},
    "Level8": {"type": "object", "properties": {"label": {"type": "string"}, "child": {"$ref": "#/components/schemas/Level9"}}},
    "Level9": {"type": "object", "properties": {"label": {"type": "string"}}},
    "Huge": HUGE
  }}
}`

var thingsCatalog = map[string]contracts.Permission{
	"things:view":      {Key: "things:view"},
	"things:edit":      {Key: "things:edit"},
	"things:sensitive": {Key: "things:sensitive", Sensitive: true},
}

// contractJSON is syntheticContract with Huge filled in: 300 properties, each
// with a description, far more than 8 KB.
func contractJSON() []byte {
	var props []string
	for i := range 300 {
		props = append(props, fmt.Sprintf(`"field%03d": {"type": "string", "description": "Field number %d of the huge thing, which has far too many fields to show a model in full."}`, i, i))
	}
	huge := `{"type": "object", "properties": {` + strings.Join(props, ",") + `}}`
	return []byte(strings.Replace(syntheticContract, "HUGE", huge, 1))
}

type fixture struct {
	d       *Dispatcher
	access  *fakeAccess
	handler *recordingHandler
	user    uuid.UUID
	agents  *fakeAgentGrants
}

func newFixture(t *testing.T, held ...string) *fixture {
	t.Helper()
	user := uuid.New()
	f := &fixture{
		d:       New(),
		access:  &fakeAccess{holds: map[uuid.UUID]map[string]bool{user: {}}},
		handler: &recordingHandler{},
		user:    user,
		agents:  newFakeAgentGrants(),
	}
	for _, k := range held {
		f.access.holds[user][k] = true
	}
	if err := f.d.Bind(contractJSON(), thingsCatalog, f.access, f.handler, Options{Origin: "https://vantigo.example.com/", AgentGrants: f.agents}); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	return f
}

// ctx is the context of an assistant's run: the user and a grant.
func (f *fixture) ctx() (context.Context, uuid.UUID) {
	grant := uuid.New()
	ctx := contracts.AsUser(context.Background(), f.user)
	return contracts.WithAssistantGrant(ctx, grant), grant
}

func ids(infos []Info) []string {
	out := make([]string, len(infos))
	for i, info := range infos {
		out[i] = info.ID
	}
	return out
}

func TestMode_EveryKindOfOperation(t *testing.T) {
	f := newFixture(t)
	want := map[string]Mode{
		// Derived: GET is auto, every other method confirm.
		"listThings":  ModeAuto,
		"getThing":    ModeAuto,
		"createThing": ModeConfirm,
		"updateThing": ModeConfirm,
		"deleteThing": ModeConfirm,
		"patchThing":  ModeConfirm,
		// An explicit mark states an exception, in either direction.
		"searchThings":    ModeAuto, // a POST that only reads
		"markedNever":     ModeNever,
		"markedConfirm":   ModeConfirm,
		"putSecretMarked": ModeConfirm, // marked: a sensitive operation added deliberately
		// A mark nobody validated is no mark: the derived mode stands.
		"markedSometimes": ModeAuto,
		// The access rule.
		"ruleAnonymous":       ModeNever,
		"ruleScim":            ModeNever,
		"ruleSession":         ModeNever,
		"ruleSessionMarked":   ModeAuto,
		"ruleOwner":           ModeNever,
		"ruleOwnerManagement": ModeNever,
		"ruleAuthorization":   ModeNever,
		"ruleSystemAdmin":     ModeNever,
		"ruleCompound":        ModeNever,
		"ruleActive":          ModeAuto,
		"ruleActivePost":      ModeConfirm,
		"ruleBusiness":        ModeAuto,
		"ruleInvalid":         ModeNever,
		// A sensitive permission is the contract's concern no more (S16): the
		// contract mode stands, and the gate narrows it (TestGate_*, and the
		// fail-closed default in TestMode_TheDefaultGateFailsClosed). A key the
		// catalog lacks is never.
		"getSecret":       ModeAuto,
		"getUncatalogued": ModeNever,
		// Files and redirects.
		"exportThings":     ModeNever,
		"uploadThingPhoto": ModeNever,
		"putThingPhotoRaw": ModeNever,
		"getThingPhoto":    ModeNever,
		"redirects":        ModeNever,
		"createEither":     ModeConfirm, // it takes JSON as well
		"getThingNote":     ModeAuto,
		// The assistant's own API.
		"assistantConversations": ModeNever,
		"getDeep":                ModeAuto,
		"createHuge":             ModeConfirm,
	}
	got := map[string]Mode{}
	for _, info := range f.d.All() {
		got[info.ID] = info.Mode
	}
	for id, mode := range want {
		if got[id] != mode {
			t.Errorf("%s: mode %q, want %q", id, got[id], mode)
		}
	}
	for id := range got {
		if _, ok := want[id]; !ok {
			t.Errorf("%s has a mode (%q) this test does not pin", id, got[id])
		}
	}
}

func TestBind_RefusesWhatItCannotDispatchOver(t *testing.T) {
	d := New()
	if err := d.Bind([]byte(`{`), nil, &fakeAccess{}, &recordingHandler{}, Options{}); err == nil {
		t.Error("Bind accepted a contract that is not JSON")
	}
	if err := d.Bind([]byte(syntheticContract), nil, nil, &recordingHandler{}, Options{}); err == nil {
		t.Error("Bind accepted no Access")
	}
	if err := d.Bind([]byte(syntheticContract), nil, &fakeAccess{}, nil, Options{}); err == nil {
		t.Error("Bind accepted no handler")
	}
	dup := `{"openapi":"3.0.3","info":{"title":"t","version":"1"},"paths":{
	  "/a":{"get":{"operationId":"same","x-vantigo-access":"session","responses":{"204":{"description":"ok"}}}},
	  "/b":{"get":{"operationId":"same","x-vantigo-access":"session","responses":{"204":{"description":"ok"}}}}}}`
	if err := d.Bind([]byte(dup), nil, &fakeAccess{}, &recordingHandler{}, Options{}); err == nil {
		t.Error("Bind accepted an operationId declared twice")
	}
}

func TestUnbound_EverythingAnswersNotReady(t *testing.T) {
	d := New()
	ctx := contracts.WithAssistantGrant(contracts.AsUser(context.Background(), uuid.New()), uuid.New())
	if _, err := d.List(ctx, uuid.New()); !errors.Is(err, ErrNotReady) {
		t.Errorf("List: %v, want ErrNotReady", err)
	}
	if _, err := d.Search(ctx, uuid.New(), "x", 5); !errors.Is(err, ErrNotReady) {
		t.Errorf("Search: %v, want ErrNotReady", err)
	}
	if _, err := d.Describe("x"); !errors.Is(err, ErrNotReady) {
		t.Errorf("Describe: %v, want ErrNotReady", err)
	}
	if _, err := d.Call(ctx, CallRequest{OperationID: "x"}); !errors.Is(err, ErrNotReady) {
		t.Errorf("Call: %v, want ErrNotReady", err)
	}
	if len(d.All()) != 0 {
		t.Error("All answered operations of an unbound dispatcher")
	}
	if _, ok := d.Info("x"); ok {
		t.Error("Info found an operation of an unbound dispatcher")
	}
}

func TestList_OnlyWhatTheUserMayCall(t *testing.T) {
	t.Run("a viewer", func(t *testing.T) {
		f := newFixture(t, "things:view")
		got, err := f.d.List(context.Background(), f.user)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"markedConfirm", "markedSometimes", "ruleSessionMarked", "ruleActive", "ruleActivePost", "ruleBusiness",
			"getDeep", "listThings", "searchThings", "getThing", "getThingNote"}
		// By path then method; compare as sets and then check the order is stable.
		for _, id := range want {
			if !slices.Contains(ids(got), id) {
				t.Errorf("List omits %s, which a viewer may call", id)
			}
		}
		for _, id := range ids(got) {
			if !slices.Contains(want, id) {
				t.Errorf("List includes %s, which a viewer may not call (or is never)", id)
			}
		}
		for _, info := range got {
			if info.Mode == ModeNever {
				t.Errorf("List includes %s, whose mode is never", info.ID)
			}
		}
		paths := make([]string, len(got))
		for i, info := range got {
			paths[i] = info.Path + " " + info.Method
		}
		if !slices.IsSorted(paths) {
			t.Errorf("List is not ordered by path then method: %v", paths)
		}
	})
	t.Run("an editor sees the writes too", func(t *testing.T) {
		f := newFixture(t, "things:view", "things:edit")
		got, _ := f.d.List(context.Background(), f.user)
		for _, id := range []string{"createThing", "updateThing", "deleteThing", "patchThing", "createHuge", "createEither"} {
			if !slices.Contains(ids(got), id) {
				t.Errorf("List omits %s for an editor", id)
			}
		}
	})
	t.Run("without a gate a sensitive operation is not listed, marked or not", func(t *testing.T) {
		f := newFixture(t, "things:sensitive", "things:view")
		got, _ := f.d.List(context.Background(), f.user)
		for _, id := range []string{"putSecretMarked", "getSecret"} {
			if slices.Contains(ids(got), id) {
				t.Errorf("List includes %s with no gate installed: %v", id, ids(got))
			}
		}
	})
	t.Run("nobody holding nothing sees only the policy-ruled calls", func(t *testing.T) {
		f := newFixture(t)
		got, _ := f.d.List(context.Background(), f.user)
		want := []string{"ruleActive", "ruleActivePost", "ruleBusiness", "ruleSessionMarked"}
		if g := ids(got); !slices.Equal(sortedCopy(g), want) {
			t.Errorf("List = %v, want %v", g, want)
		}
	})
	t.Run("a key is asked once per call however many operations name it", func(t *testing.T) {
		f := newFixture(t, "things:view", "things:edit")
		if _, err := f.d.List(context.Background(), f.user); err != nil {
			t.Fatal(err)
		}
		asked := f.access.questions()
		counts := map[string]int{}
		for _, q := range asked {
			counts[q]++
		}
		for q, n := range counts {
			if n != 1 {
				t.Errorf("UserHolds(%q) was asked %d times, want once", q, n)
			}
		}
		if len(asked) == 0 {
			t.Error("UserHolds was never asked")
		}
	})
	t.Run("a failure to read what the user holds is an error, not an empty list", func(t *testing.T) {
		f := newFixture(t, "things:view")
		f.access.err = errors.New("database is down")
		if got, err := f.d.List(context.Background(), f.user); err == nil || got != nil {
			t.Errorf("List = %v, %v, want an error", got, err)
		}
	})
}

func sortedCopy(s []string) []string {
	c := slices.Clone(s)
	slices.Sort(c)
	return c
}

func TestSearch(t *testing.T) {
	f := newFixture(t, "things:view", "things:edit")
	ctx := context.Background()

	t.Run("the summary outranks the description and a word of the path", func(t *testing.T) {
		got, err := f.d.Search(ctx, f.user, "delete thing", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 || got[0].ID != "deleteThing" {
			t.Errorf("Search(delete thing) = %v, want deleteThing first", ids(got))
		}
	})
	t.Run("a plural finds the singular", func(t *testing.T) {
		got, _ := f.d.Search(ctx, f.user, "notes", 10)
		if len(got) != 1 || got[0].ID != "getThingNote" {
			t.Errorf("Search(notes) = %v, want getThingNote", ids(got))
		}
	})
	t.Run("the description and the tag count", func(t *testing.T) {
		if got, _ := f.d.Search(ctx, f.user, "newest", 10); len(got) != 1 || got[0].ID != "listThings" {
			t.Errorf("Search(newest) = %v, want listThings by its description", ids(got))
		}
		if got, _ := f.d.Search(ctx, f.user, "notes", 10); len(got) == 0 {
			t.Errorf("Search(notes) found nothing by tag and path")
		}
	})
	t.Run("an operation that answers every word outranks one that answers one well", func(t *testing.T) {
		got, _ := f.d.Search(ctx, f.user, "search things filter", 10)
		if len(got) == 0 || got[0].ID != "searchThings" {
			t.Errorf("Search = %v, want searchThings first", ids(got))
		}
	})
	t.Run("the operationId alone is not searched", func(t *testing.T) {
		if got, _ := f.d.Search(ctx, f.user, "createEither", 10); len(got) != 0 {
			t.Errorf("Search(createEither) = %v, want nothing: ids are not indexed", ids(got))
		}
	})
	t.Run("only what the user may call is searched", func(t *testing.T) {
		viewer := newFixture(t, "things:view")
		if got, _ := viewer.d.Search(ctx, viewer.user, "delete", 10); len(got) != 0 {
			t.Errorf("a viewer's Search(delete) = %v, want nothing", ids(got))
		}
		if got, _ := f.d.Search(ctx, f.user, "secret sensitive", 10); len(got) != 0 {
			t.Errorf("Search found a never operation: %v", ids(got))
		}
	})
	t.Run("limit", func(t *testing.T) {
		if got, _ := f.d.Search(ctx, f.user, "thing", 2); len(got) != 2 {
			t.Errorf("Search(limit 2) = %d results", len(got))
		}
		if got, _ := f.d.Search(ctx, f.user, "thing", 0); len(got) != 10 {
			t.Errorf("Search(limit 0) = %d results, want the default 10", len(got))
		}
		if got, _ := f.d.Search(ctx, f.user, "thing", 500); len(got) > 50 {
			t.Errorf("Search(limit 500) = %d results, want at most 50", len(got))
		}
	})
	t.Run("nothing to look for", func(t *testing.T) {
		for _, q := range []string{"", "   ", "!!!", "zzzzzz"} {
			if got, err := f.d.Search(ctx, f.user, q, 10); err != nil || len(got) != 0 {
				t.Errorf("Search(%q) = %v, %v, want nothing", q, ids(got), err)
			}
		}
	})
}

func describe(t *testing.T, f *fixture, id string) map[string]any {
	t.Helper()
	raw, err := f.d.Describe(id)
	if err != nil {
		t.Fatalf("Describe(%s): %v", id, err)
	}
	if len(raw) > describeMaxBytes {
		t.Errorf("Describe(%s) is %d bytes, want at most %d", id, len(raw), describeMaxBytes)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("Describe(%s) is not JSON: %v\n%s", id, err, raw)
	}
	return doc
}

func TestDescribe(t *testing.T) {
	f := newFixture(t)

	t.Run("references resolved, enums inline, descriptions kept", func(t *testing.T) {
		doc := describe(t, f, "getThing")
		if doc["id"] != "getThing" || doc["method"] != "GET" || doc["path"] != "/api/v1/things/{id}" ||
			doc["summary"] != "Get a thing by id" || doc["mode"] != "auto" {
			t.Errorf("header = %v", doc)
		}
		path := doc["pathParameters"].([]any)
		if len(path) != 1 || path[0].(map[string]any)["name"] != "id" || path[0].(map[string]any)["required"] != true {
			t.Errorf("pathParameters = %v", path)
		}
		resp := doc["response"].(map[string]any)
		if resp["status"] != "200" {
			t.Errorf("response status = %v", resp["status"])
		}
		schema := resp["schema"].(map[string]any)
		props := schema["properties"].(map[string]any)
		if schema["description"] != "One thing." || props["name"].(map[string]any)["description"] != "What it is called." {
			t.Errorf("descriptions lost: %v", schema)
		}
		state := props["state"].(map[string]any)
		if state["enum"] == nil || len(state["enum"].([]any)) != 3 || state["description"] != "Where the thing is in its life." {
			t.Errorf("the referenced enum is not inline: %v", state)
		}
		if raw, _ := json.Marshal(doc); strings.Contains(string(raw), "$ref") {
			t.Errorf("a $ref survived: %s", raw)
		}
		if req := schema["required"].([]any); len(req) != 2 {
			t.Errorf("required = %v", req)
		}
	})
	t.Run("a cycle is cut with a note", func(t *testing.T) {
		doc := describe(t, f, "getThing")
		owner := doc["response"].(map[string]any)["schema"].(map[string]any)["properties"].(map[string]any)["owner"].(map[string]any)
		manager := owner["properties"].(map[string]any)["manager"].(map[string]any)
		if note, _ := manager["note"].(string); !strings.Contains(note, "recursive") || !strings.Contains(note, "Person") {
			t.Errorf("manager = %v, want a recursion note naming Person", manager)
		}
	})
	t.Run("query, header and body parameters", func(t *testing.T) {
		list := describe(t, f, "listThings")
		var names []string
		for _, q := range list["queryParameters"].([]any) {
			names = append(names, q.(map[string]any)["name"].(string))
		}
		if !slices.Equal(sortedCopy(names), []string{"page", "search", "status", "tag"}) {
			t.Errorf("query parameters = %v", names)
		}
		update := describe(t, f, "updateThing")
		headers := update["headerParameters"].([]any)
		if len(headers) != 2 || headers[0].(map[string]any)["name"] != "If-Match" || headers[0].(map[string]any)["required"] != true {
			t.Errorf("headerParameters = %v, want the declared headers, If-Match required", headers)
		}
		body := update["requestBody"].(map[string]any)
		if body["schema"].(map[string]any)["required"].([]any)[0] != "name" {
			t.Errorf("requestBody = %v", body)
		}
		if create := describe(t, f, "createThing"); create["requestBody"].(map[string]any)["required"] != true {
			t.Errorf("createThing's body is not marked required: %v", create["requestBody"])
		}
		if update["response"].(map[string]any)["status"] != "200" {
			t.Errorf("update response = %v", update["response"])
		}
	})
	t.Run("path-level parameters are shown", func(t *testing.T) {
		if doc := describe(t, f, "deleteThing"); len(doc["pathParameters"].([]any)) != 1 {
			t.Errorf("deleteThing pathParameters = %v", doc["pathParameters"])
		}
	})
	t.Run("a deep chain is cut at the depth limit, not the cap", func(t *testing.T) {
		doc := describe(t, f, "getDeep")
		raw, _ := json.Marshal(doc)
		if !strings.Contains(string(raw), "nested deeper than shown") {
			t.Errorf("a nine-level chain was shown in full: %s", raw)
		}
	})
	t.Run("a schema larger than 8 KB is cut down to fit and stays valid JSON", func(t *testing.T) {
		doc := describe(t, f, "createHuge")
		if doc["id"] != "createHuge" || doc["summary"] != "Create a thing with very many fields" {
			t.Errorf("the identity was lost: %v", doc)
		}
		if note, _ := doc["note"].(string); !strings.Contains(note, "too large") {
			t.Errorf("note = %q, want it to say the schemas are too large", note)
		}
	})
	t.Run("an unknown id", func(t *testing.T) {
		if _, err := f.d.Describe("nope"); !errors.Is(err, ErrUnknownOperation) {
			t.Errorf("Describe(nope) = %v, want ErrUnknownOperation", err)
		}
	})
}

func TestCall_ARefusedCallIsRefusedBeforeDispatch(t *testing.T) {
	f := newFixture(t, "things:view", "things:edit")
	ctx, _ := f.ctx()
	noGrant := contracts.AsUser(context.Background(), f.user)
	noUser := contracts.WithAssistantGrant(context.Background(), uuid.New())

	cases := []struct {
		name string
		ctx  context.Context
		req  CallRequest
		want *Error
	}{
		{"an unknown operation", ctx, CallRequest{OperationID: "nope"}, ErrUnknownOperation},
		{"a never operation", ctx, CallRequest{OperationID: "ruleOwner"}, ErrNever},
		{"a never operation, even confirmed", ctx, CallRequest{OperationID: "ruleOwner", Confirmed: true}, ErrNever},
		{"a file download", ctx, CallRequest{OperationID: "exportThings", Confirmed: true}, ErrNever},
		{"a confirm operation not confirmed", ctx, CallRequest{OperationID: "createThing", Body: json.RawMessage(`{"name":"x"}`)}, ErrConfirmationRequired},
		{"a delete not confirmed", ctx, CallRequest{OperationID: "deleteThing", PathParams: map[string]any{"id": "1"}}, ErrConfirmationRequired},
		{"no grant", noGrant, CallRequest{OperationID: "listThings"}, ErrNoGrant},
		{"no user", noUser, CallRequest{OperationID: "listThings"}, ErrNoGrant},
		{"a missing path parameter", ctx, CallRequest{OperationID: "getThing"}, ErrInvalidRequest},
		{"a blank path parameter", ctx, CallRequest{OperationID: "getThing", PathParams: map[string]any{"id": ""}}, ErrInvalidRequest},
		{"a path parameter that is not a scalar", ctx, CallRequest{OperationID: "getThing", PathParams: map[string]any{"id": map[string]any{}}}, ErrInvalidRequest},
		{"a path parameter the operation lacks", ctx, CallRequest{OperationID: "getThing", PathParams: map[string]any{"id": "1", "extra": "2"}}, ErrInvalidRequest},
		{"a query parameter the operation lacks", ctx, CallRequest{OperationID: "listThings", Query: map[string]any{"bogus": "1"}}, ErrInvalidRequest},
		{"a query value that is an object", ctx, CallRequest{OperationID: "listThings", Query: map[string]any{"search": map[string]any{"a": 1}}}, ErrInvalidRequest},
		{"a header the operation lacks", ctx, CallRequest{OperationID: "listThings", Headers: map[string]string{"X-Forwarded-For": "1.2.3.4"}}, ErrInvalidRequest},
		{"a body that is not JSON", ctx, CallRequest{OperationID: "createThing", Confirmed: true, Body: json.RawMessage(`{"name":`)}, ErrInvalidRequest},
		{"a body for an operation that takes none", ctx, CallRequest{OperationID: "listThings", Body: json.RawMessage(`{}`)}, ErrInvalidRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, before := f.handler.seen()
			res, err := f.d.Call(tc.ctx, tc.req)
			var got *Error
			if !errors.As(err, &got) || got.Code != tc.want.Code || !errors.Is(err, tc.want) {
				t.Fatalf("Call = %+v, %v, want %s", res, err, tc.want.Code)
			}
			if got.Message == "" {
				t.Error("the refusal says nothing")
			}
			if _, after := f.handler.seen(); after != before {
				t.Errorf("the handler was reached by a refused call")
			}
		})
	}

	t.Run("the codes are stable", func(t *testing.T) {
		for err, code := range map[*Error]string{
			ErrNotReady: "not_ready", ErrUnknownOperation: "operation_unknown", ErrNever: "operation_never",
			ErrConfirmationRequired: "confirmation_required", ErrNoGrant: "no_grant", ErrInvalidRequest: "invalid_request", ErrCallFailed: "call_failed",
		} {
			if err.Code != code {
				t.Errorf("%v has code %q, want %q", err, err.Code, code)
			}
		}
	})
}

func TestCall_BuildsTheRequestAndCarriesOnlyTheGrant(t *testing.T) {
	f := newFixture(t, "things:view", "things:edit")
	type secretKey struct{}
	caller, _ := f.ctx()
	caller = context.WithValue(caller, callerKey{}, "the caller's own value")
	caller = contracts.WithPrincipal(caller, contracts.Principal{UserID: f.user, SessionID: uuid.New()})
	caller = context.WithValue(caller, secretKey{}, "x")
	_, grant := func() (context.Context, uuid.UUID) { id, _ := contracts.AssistantGrantFrom(caller); return caller, id }()

	t.Run("path, query, headers and body", func(t *testing.T) {
		res, err := f.d.Call(caller, CallRequest{
			OperationID: "updateThing",
			PathParams:  map[string]any{"id": "a/b c?d"},
			Headers:     map[string]string{"if-match": `"7"`, "Idempotency-Key": "key-1"},
			Body:        json.RawMessage(` {"name":"New"} `),
			Confirmed:   true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != 200 || string(res.Body) != `{"ok":true}` || res.Truncated {
			t.Errorf("result = %+v", res)
		}
		got, _ := f.handler.seen()
		if got.Method != http.MethodPut || got.Path != "/api/v1/things/a/b c?d" || got.EscapedPath != "/api/v1/things/a%2Fb%20c%3Fd" {
			t.Errorf("request = %s %s (%s), want the id escaped into one path segment", got.Method, got.Path, got.EscapedPath)
		}
		if got.Header.Get("If-Match") != `"7"` || got.Header.Get("Idempotency-Key") != "key-1" {
			t.Errorf("headers = %v", got.Header)
		}
		if got.Header.Get("Content-Type") != "application/json" || got.Body != `{"name":"New"}` {
			t.Errorf("body = %q (%s)", got.Body, got.Header.Get("Content-Type"))
		}
	})
	t.Run("the query string", func(t *testing.T) {
		_, err := f.d.Call(caller, CallRequest{OperationID: "listThings", Query: map[string]any{
			"search": "a&b", "page": float64(2), "tag": []any{"x", "y z"}, "status": nil,
		}})
		if err != nil {
			t.Fatal(err)
		}
		got, _ := f.handler.seen()
		q, _ := url.ParseQuery(got.RawQuery)
		if q.Get("search") != "a&b" || q.Get("page") != "2" || !slices.Equal(q["tag"], []string{"x", "y z"}) || q.Has("status") {
			t.Errorf("query = %q", got.RawQuery)
		}
		if got.Body != "" || got.Header.Get("Content-Type") != "" {
			t.Errorf("a GET carried a body or a content type: %q %q", got.Body, got.Header.Get("Content-Type"))
		}
	})
	t.Run("a number fills a path parameter", func(t *testing.T) {
		if _, err := f.d.Call(caller, CallRequest{OperationID: "getThingNote", PathParams: map[string]any{"id": "t1", "noteId": float64(42)}}); err != nil {
			t.Fatal(err)
		}
		if got, _ := f.handler.seen(); got.Path != "/api/v1/things/t1/notes/42" {
			t.Errorf("path = %q", got.Path)
		}
	})
	t.Run("an explicit null body is no body", func(t *testing.T) {
		if _, err := f.d.Call(caller, CallRequest{OperationID: "deleteThing", PathParams: map[string]any{"id": "1"}, Body: json.RawMessage(`null`), Confirmed: true}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("only the grant crosses, and the rate limit is the person's", func(t *testing.T) {
		if _, err := f.d.Call(caller, CallRequest{OperationID: "listThings"}); err != nil {
			t.Fatal(err)
		}
		got, _ := f.handler.seen()
		if !got.HasGrant || got.Grant != grant {
			t.Errorf("grant on the request = %v %v, want %v", got.HasGrant, got.Grant, grant)
		}
		if got.PrincipalPresent {
			t.Error("the caller's principal reached the request: the router must authenticate it afresh")
		}
		if got.CallerValue != nil {
			t.Errorf("a value of the caller's context reached the request: %v", got.CallerValue)
		}
		if got.RemoteAddr != "assistant-"+f.user.String() {
			t.Errorf("RemoteAddr = %q, want one that names the assistant and the person", got.RemoteAddr)
		}
		if got.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q", got.Header.Get("Accept"))
		}
	})
	t.Run("another person gets another bucket", func(t *testing.T) {
		other := newFixture(t, "things:view")
		ctx, _ := other.ctx()
		if _, err := other.d.Call(ctx, CallRequest{OperationID: "listThings"}); err != nil {
			t.Fatal(err)
		}
		a, _ := other.handler.seen()
		b, _ := f.handler.seen()
		if a.RemoteAddr == b.RemoteAddr {
			t.Errorf("two people share %q", a.RemoteAddr)
		}
	})
	t.Run("cancelling the caller's context cancels the request's", func(t *testing.T) {
		started := make(chan struct{})
		f.handler.mu.Lock()
		f.handler.respond = func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
			w.WriteHeader(http.StatusGatewayTimeout)
		}
		f.handler.mu.Unlock()
		t.Cleanup(func() { f.handler.mu.Lock(); f.handler.respond = nil; f.handler.mu.Unlock() })

		ctx, cancel := context.WithCancel(caller)
		done := make(chan CallResult, 1)
		go func() {
			res, _ := f.d.Call(ctx, CallRequest{OperationID: "listThings"})
			done <- res
		}()
		<-started
		cancel()
		select {
		case res := <-done:
			if res.Status != http.StatusGatewayTimeout {
				t.Errorf("status = %d", res.Status)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the call did not end when its context was cancelled")
		}
	})
}

func TestCall_TheRoutersAnswerIsAResult(t *testing.T) {
	f := newFixture(t, "things:view", "things:edit")
	ctx, _ := f.ctx()
	answer := func(respond func(w http.ResponseWriter, r *http.Request)) {
		f.handler.mu.Lock()
		f.handler.respond = respond
		f.handler.mu.Unlock()
	}
	jsonStatus := func(status int, body string) func(w http.ResponseWriter, r *http.Request) {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}
	}

	t.Run("a refusal is a status and a body, not an error", func(t *testing.T) {
		answer(jsonStatus(http.StatusForbidden, `{"error":{"code":"forbidden","message":"No."}}`))
		res, err := f.d.Call(ctx, CallRequest{OperationID: "listThings"})
		if err != nil || res.Status != 403 || string(res.Body) != `{"error":{"code":"forbidden","message":"No."}}` {
			t.Errorf("Call = %+v, %v, want the 403 and its body as a result", res, err)
		}
		answer(jsonStatus(http.StatusInternalServerError, `{"title":"boom"}`))
		if res, err := f.d.Call(ctx, CallRequest{OperationID: "listThings"}); err != nil || res.Status != 500 {
			t.Errorf("Call = %+v, %v, want the 500 as a result", res, err)
		}
	})
	t.Run("the response headers come back", func(t *testing.T) {
		answer(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("ETag", `"7"`)
			_, _ = w.Write([]byte(`{}`))
		})
		res, err := f.d.Call(ctx, CallRequest{OperationID: "listThings"})
		if err != nil || res.Header.Get("ETag") != `"7"` {
			t.Errorf("Call = %+v, %v, want the ETag header", res, err)
		}
	})
	t.Run("no body", func(t *testing.T) {
		answer(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
		res, err := f.d.Call(ctx, CallRequest{OperationID: "deleteThing", PathParams: map[string]any{"id": "1"}, Confirmed: true})
		if err != nil || res.Status != 204 || res.Body != nil || res.Truncated {
			t.Errorf("Call = %+v, %v", res, err)
		}
	})
	t.Run("an implicit 200", func(t *testing.T) {
		answer(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write([]byte(`[1,2]`))
		})
		if res, _ := f.d.Call(ctx, CallRequest{OperationID: "listThings"}); res.Status != 200 || string(res.Body) != `[1,2]` {
			t.Errorf("Call = %+v", res)
		}
	})
	t.Run("what is not JSON is carried as text", func(t *testing.T) {
		answer(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("<h1>Bad gateway</h1>\xff"))
		})
		res, _ := f.d.Call(ctx, CallRequest{OperationID: "listThings"})
		var text string
		if res.Status != 502 || json.Unmarshal(res.Body, &text) != nil || !strings.Contains(text, "Bad gateway") {
			t.Errorf("Call = %+v", res)
		}
	})
	t.Run("JSON the handler corrupted is carried as text too", func(t *testing.T) {
		answer(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"half":`))
		})
		res, _ := f.d.Call(ctx, CallRequest{OperationID: "listThings"})
		if !json.Valid(res.Body) {
			t.Errorf("Body = %q, want valid JSON", res.Body)
		}
	})
	t.Run("a body above the cap is dropped and says so", func(t *testing.T) {
		chunk := []byte(strings.Repeat("a", 64<<10))
		answer(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"rows":["`))
			for written := 0; written <= resultMaxBytes; written += len(chunk) {
				_, _ = w.Write(chunk)
			}
			_, _ = w.Write([]byte(`"]}`))
		})
		res, err := f.d.Call(ctx, CallRequest{OperationID: "listThings"})
		if err != nil || res.Status != 200 || !res.Truncated || res.Body != nil {
			t.Errorf("Call = %d, truncated %v, %d body bytes, %v; want the status, Truncated and no body", res.Status, res.Truncated, len(res.Body), err)
		}
	})
	t.Run("a body at the cap is kept", func(t *testing.T) {
		pad := strings.Repeat("a", resultMaxBytes-len(`{"p":""}`))
		answer(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"p":"` + pad + `"}`))
		})
		res, _ := f.d.Call(ctx, CallRequest{OperationID: "listThings"})
		if res.Truncated || len(res.Body) != resultMaxBytes {
			t.Errorf("Truncated = %v, %d body bytes, want a body of exactly the cap kept", res.Truncated, len(res.Body))
		}
	})
	t.Run("a panicking handler fails the call, not the run", func(t *testing.T) {
		answer(func(http.ResponseWriter, *http.Request) { panic("boom") })
		if _, err := f.d.Call(ctx, CallRequest{OperationID: "listThings"}); !errors.Is(err, ErrCallFailed) {
			t.Errorf("Call = %v, want ErrCallFailed", err)
		}
	})
}

func TestInfo(t *testing.T) {
	f := newFixture(t)
	info, ok := f.d.Info("updateThing")
	if !ok || info.Mode != ModeConfirm || info.Method != "PUT" || info.Tag != "Things" || info.Summary != "Update a thing" {
		t.Errorf("Info = %+v, %v", info, ok)
	}
	if _, ok := f.d.Info("ruleOwner"); !ok {
		t.Error("Info hides a never operation: it answers for every operation, List is what filters")
	}
	if _, ok := f.d.Info("nope"); ok {
		t.Error("Info found an operation that is not there")
	}
}

func TestMode_TheDefaultGateFailsClosed(t *testing.T) {
	f := newFixture(t, "things:view", "things:edit", "things:sensitive")
	for id, want := range map[string]Mode{
		"getSecret":       ModeNever, // sensitive permission: delivery 0's rule
		"putSecretMarked": ModeNever, // marked, but sensitive: still closed without a gate
		"getThing":        ModeAuto,
		"updateThing":     ModeConfirm,
		"ruleOwner":       ModeNever,
	} {
		got, ok := f.d.Effective(context.Background(), id)
		if !ok || got != want {
			t.Errorf("Effective(%s) = %q, %v, want %q", id, got, ok, want)
		}
	}
	info, _ := f.d.Info("getSecret")
	if !info.Sensitive || !slices.Equal(info.Permissions, []string{"things:view", "things:sensitive"}) || info.Mode != ModeAuto {
		t.Errorf("Info(getSecret) = %+v, want the contract's mode, both permissions and Sensitive", info)
	}
	if info, _ := f.d.Info("ruleActive"); len(info.Permissions) != 0 || info.Sensitive {
		t.Errorf("a policy rule names no permission: %+v", info)
	}
	_, err := f.d.Call(f.ctxOnly(), CallRequest{OperationID: "getSecret"})
	if !errors.Is(err, ErrNever) {
		t.Errorf("Call(getSecret) with no gate = %v, want ErrNever", err)
	}
	f.d.SetGate(contracts.GrantAssistant, func(context.Context, Info) Mode { return ModeAuto })
	f.d.SetGate(contracts.GrantAssistant, nil)
	if got, _ := f.d.Effective(context.Background(), "getSecret"); got != ModeNever {
		t.Errorf("after SetGate(nil) getSecret = %q, want the default gate back", got)
	}
}

// ctxOnly is a run's context.
func (f *fixture) ctxOnly() context.Context { ctx, _ := f.ctx(); return ctx }

func TestGate_NarrowsListSearchAndCall(t *testing.T) {
	f := newFixture(t, "things:view", "things:edit", "things:sensitive")
	// A gate that reads things:view, switches things:edit off and opens the
	// sensitive permission to reading.
	f.d.SetGate(contracts.GrantAssistant, func(_ context.Context, info Info) Mode {
		mode := info.Mode
		for _, key := range info.Permissions {
			switch key {
			case "things:edit":
				return ModeNever
			case "things:sensitive":
				if mode == ModeConfirm {
					mode = ModeNever
				}
			}
		}
		return mode
	})
	ctx := context.Background()
	got, _ := f.d.List(ctx, f.user)
	for _, id := range []string{"getSecret", "listThings"} {
		if !slices.Contains(ids(got), id) {
			t.Errorf("List omits %s the gate allows: %v", id, ids(got))
		}
	}
	for _, id := range []string{"createThing", "updateThing", "putSecretMarked"} {
		if slices.Contains(ids(got), id) {
			t.Errorf("List includes %s the gate narrowed to never", id)
		}
	}
	for _, info := range got {
		if info.ID == "getSecret" && info.Mode != ModeAuto {
			t.Errorf("getSecret listed as %q", info.Mode)
		}
	}
	found, _ := f.d.Search(ctx, f.user, "update a thing", 20)
	for _, info := range found {
		if info.ID == "updateThing" {
			t.Error("Search finds updateThing, which the gate narrowed to never")
		}
	}
	runCtx, _ := f.ctx()
	// The gate refuses before the confirmation check, confirmed or not.
	for _, confirmed := range []bool{false, true} {
		_, err := f.d.Call(runCtx, CallRequest{OperationID: "updateThing", PathParams: map[string]any{"id": "1"}, Confirmed: confirmed})
		if !errors.Is(err, ErrNever) {
			t.Errorf("Call(updateThing, confirmed %v) = %v, want ErrNever", confirmed, err)
		}
	}
	if _, err := f.d.Call(runCtx, CallRequest{OperationID: "getSecret", PathParams: map[string]any{"id": "1"}}); err != nil {
		t.Errorf("Call(getSecret) = %v, want it allowed by the gate", err)
	}
}

func TestGate_NeverWidensTheContract(t *testing.T) {
	f := newFixture(t, "things:view", "things:edit")
	f.d.SetGate(contracts.GrantAssistant, func(context.Context, Info) Mode { return ModeAuto })
	for id, want := range map[string]Mode{"ruleOwner": ModeNever, "updateThing": ModeConfirm, "getThing": ModeAuto, "ruleSession": ModeNever} {
		if got, _ := f.d.Effective(context.Background(), id); got != want {
			t.Errorf("a gate answering auto: Effective(%s) = %q, want the contract's %q", id, got, want)
		}
	}
	runCtx, _ := f.ctx()
	if _, err := f.d.Call(runCtx, CallRequest{OperationID: "updateThing", PathParams: map[string]any{"id": "1"}}); !errors.Is(err, ErrConfirmationRequired) {
		t.Errorf("Call(updateThing) = %v, want ErrConfirmationRequired", err)
	}
}

// UserSatisfies is not what this fake is about: false.
func (f *fakeAccess) UserSatisfies(context.Context, uuid.UUID, contracts.Rule) (bool, error) {
	return false, nil
}

func (f *fakeAccess) SessionLive(context.Context, uuid.UUID) (uuid.UUID, bool, error) {
	return uuid.Nil, false, nil
}
