package module

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// personAccess admits every request as a signed-in person.
func personAccess() *fakeAccess {
	return &fakeAccess{check: func(*http.Request, contracts.Rule) (contracts.Principal, error) {
		return contracts.Principal{UserID: uuid.New(), SessionID: uuid.New()}, nil
	}}
}

// keyAccess admits every request as an API key whose scope is scope.
type keyAccess struct {
	fakeAccess
	permissions []string
	readOnly    bool
}

func (k *keyAccess) Check(r *http.Request, rule contracts.Rule) (contracts.Principal, error) {
	k.checked = append(k.checked, rule)
	return contracts.Principal{UserID: uuid.New(), Via: &contracts.Via{Kind: contracts.ViaAPIKey, KeyID: uuid.New()}}, nil
}

func (k *keyAccess) Scope(context.Context, contracts.Principal) ([]string, bool, error) {
	return k.permissions, k.readOnly, nil
}

// customersCatalog is the customers contract's permissions, every one
// delegable, and identity's, which no key holds.
func customersModules() []Module {
	var perms []contracts.Permission
	for _, key := range []string{
		"associations-manage", "associations-view", "billing-manage", "contacts-manage", "contacts-view",
		"create", "delete", "import", "legal-identity-manage", "legal-identity-view", "lookup-view", "merge",
		"personal-data", "timeline-manage", "timeline-view", "update", "view",
	} {
		perms = append(perms, contracts.Permission{Key: "customers:" + key, Display: key, Description: key, Category: "Customers", Delegable: true})
	}
	none := func(Deps) (http.Handler, error) { return http.NotFoundHandler(), nil }
	return []Module{
		{Name: "identity", Mount: none, Permissions: []contracts.Permission{{Key: "identity:manage", Display: "d", Description: "d", Category: "c"}}},
		{Name: "customers", Mount: none, Permissions: perms},
	}
}

func composeDocuments(t *testing.T, access contracts.Access) http.Handler {
	t.Helper()
	handler, err := Compose(Deps{Access: access, Config: &config.Config{
		Modules: []string{"customers"}, AppOrigin: "https://vantigo.example.com", BasePath: "/erp",
	}}, customersModules()...)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	return handler
}

func getDocument(t *testing.T, handler http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// checkIntegratorDocument asserts what every integrator's document holds: it
// validates, it names APP_URL, every operation declares 401, 403 and 429,
// and no x-vantigo-* extension is left. It answers the operation ids.
func checkIntegratorDocument(t *testing.T, body []byte) map[string]bool {
	t.Helper()
	if bytes.Contains(body, []byte("x-vantigo-")) {
		t.Error("the document carries an x-vantigo-* extension")
	}
	doc, err := openapi3.NewLoader().LoadFromData(body)
	if err != nil {
		t.Fatalf("load the document: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Errorf("the document does not validate: %v", err)
	}
	if len(doc.Servers) != 1 || doc.Servers[0].URL != "https://vantigo.example.com/erp" {
		t.Errorf("servers = %v, want APP_URL", doc.Servers)
	}
	if doc.Components.SecuritySchemes["bearerAuth"] == nil || len(doc.Security) != 1 {
		t.Error("the document declares no bearerAuth security")
	}
	ids := map[string]bool{}
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			ids[op.OperationID] = true
			for _, status := range []string{"401", "403", "429"} {
				if op.Responses.Value(status) == nil {
					t.Errorf("%s %s declares no %s", method, path, status)
				}
			}
		}
	}
	return ids
}

func TestOpenAPIJSON_NoUserIsUnauthenticated(t *testing.T) {
	access := &fakeAccess{} // admits, with no user: no credentials at all
	rec := getDocument(t, composeDocuments(t, access), "/api/openapi.json")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(access.rejected) != 1 || !errors.Is(access.rejected[0].err, contracts.ErrUnauthenticated) {
		t.Errorf("rejected = %+v, want one ErrUnauthenticated", access.rejected)
	}
	if got := access.checked[0]; got.Kind != contracts.RuleAnonymous || got.API != contracts.APIRead {
		t.Errorf("checked rule = %+v, want anonymous, APIRead", got)
	}
}

func TestOpenAPIJSON_SessionGetsTheMergedContract(t *testing.T) {
	handler := composeDocuments(t, personAccess())
	rec := getDocument(t, handler, "/api/openapi.json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("x-vantigo-access")) {
		t.Error("the merged contract lost its x-vantigo-access rules")
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("bearerAuth")) {
		t.Error("a session without ?view=api got the integrator's document")
	}
}

// identityOperation matches identity's own operation ids, none of which a key
// may call: every identity: permission is APINever, and the rest are session
// or policy rules.
var identityOperation = regexp.MustCompile(`^(get|post|put|patch|delete)Identity`)

func TestOpenAPIJSON_SessionWithViewAPIGetsTheIntegratorsDocument(t *testing.T) {
	rec := getDocument(t, composeDocuments(t, personAccess()), "/api/openapi.json?view=api")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	ids := checkIntegratorDocument(t, rec.Body.Bytes())
	if !ids["getCustomers"] || !ids["postCustomers"] {
		t.Errorf("the document lacks the customers listing and create: %v", ids)
	}
	for id := range ids {
		if identityOperation.MatchString(id) {
			t.Errorf("the document holds %s, which no key may call", id)
		}
	}
}

func TestOpenAPIJSON_KeyGetsItsScope(t *testing.T) {
	t.Run("narrowed", func(t *testing.T) {
		access := &keyAccess{permissions: []string{"customers:view"}}
		rec := getDocument(t, composeDocuments(t, access), "/api/openapi.json")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body)
		}
		ids := checkIntegratorDocument(t, rec.Body.Bytes())
		if !ids["getCustomers"] || ids["postCustomers"] {
			t.Errorf("a customers:view key's document: getCustomers %v, postCustomers %v; want only the read", ids["getCustomers"], ids["postCustomers"])
		}
	})
	t.Run("read-only", func(t *testing.T) {
		access := &keyAccess{permissions: []string{"customers:view", "customers:create"}, readOnly: true}
		handler := composeDocuments(t, access)
		rec := getDocument(t, handler, "/api/openapi.json?view=api") // the parameter changes nothing for a key
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body)
		}
		ids := checkIntegratorDocument(t, rec.Body.Bytes())
		if !ids["getCustomers"] || ids["postCustomers"] {
			t.Errorf("a read-only key's document: getCustomers %v, postCustomers %v; want only the read", ids["getCustomers"], ids["postCustomers"])
		}
	})
}

func TestOpenAPIJSON_KeyWithoutAScopeIsAnError(t *testing.T) {
	access := &fakeAccess{check: func(*http.Request, contracts.Rule) (contracts.Principal, error) {
		return contracts.Principal{UserID: uuid.New(), Via: &contracts.Via{Kind: contracts.ViaAPIKey}}, nil
	}}
	if rec := getDocument(t, composeDocuments(t, access), "/api/openapi.json"); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// The filtered documents are kept in an LRU of filteredDocuments entries
// (K46), keyed by the sorted permission set and the read-only flag.
func TestAPIDocuments_TheCacheIsBounded(t *testing.T) {
	contract := []byte(`{"openapi":"3.0.3","info":{"title":"t","version":"1"},"paths":{}}`)
	d := newAPIDocuments(&fakeAccess{}, nil, contract, map[string]contracts.Permission{}, "")
	first, err := d.scoped([]string{"b:x", "a:x"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := d.scoped([]string{"a:x", "b:x", "a:x"}, false); !bytes.Equal(first, again) || d.cached() != 1 {
		t.Errorf("the same set in another order was cached again (%d entries)", d.cached())
	}
	if _, err := d.scoped([]string{"a:x", "b:x"}, true); err != nil || d.cached() != 2 {
		t.Errorf("the read-only flag did not key a document of its own (%d entries)", d.cached())
	}
	for i := range filteredDocuments + 10 {
		if _, err := d.scoped([]string{fmt.Sprintf("m:p%d", i)}, false); err != nil {
			t.Fatal(err)
		}
	}
	if n := d.cached(); n != filteredDocuments {
		t.Errorf("cached %d documents, want the bound %d", n, filteredDocuments)
	}
	d.mu.Lock()
	_, oldest := d.filtered["a:x b:x"]
	_, newest := d.filtered[fmt.Sprintf("m:p%d", filteredDocuments+9)]
	d.mu.Unlock()
	if oldest || !newest {
		t.Errorf("eviction kept the oldest (%v) or dropped the newest (%v)", oldest, !newest)
	}
	var parsed map[string]any
	if err := json.Unmarshal(first, &parsed); err != nil {
		t.Errorf("a cached document is not JSON: %v", err)
	}
}

// scopedRecordingAccess admits as check says, is the request log, and
// answers a key's scope.
type scopedRecordingAccess struct {
	recordingAccess
	permissions []string
}

func (a *scopedRecordingAccess) Scope(context.Context, contracts.Principal) ([]string, bool, error) {
	return a.permissions, false, nil
}

func composeLimitedDocuments(t *testing.T, access contracts.Access) http.Handler {
	t.Helper()
	pool, _ := testdb.Migrated(t)
	// A frozen clock: the limiter's window never rolls over mid-test, however
	// slowly the requests run (under -race, say).
	frozen := time.Date(2026, 1, 1, 12, 0, 30, 0, time.UTC)
	limiter := ratelimit.NewWithClock(pool, func() time.Time { return frozen })
	handler, err := Compose(Deps{Access: access, Limiter: limiter, Config: &config.Config{
		Modules: []string{"customers"}, AppOrigin: "https://vantigo.example.com", BasePath: "/erp",
	}}, customersModules()...)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	return handler
}

// The document is the caller's own: no shared cache may keep it (S1).
func TestOpenAPIJSON_IsNeverCachedByOthers(t *testing.T) {
	for name, access := range map[string]*fakeAccess{"a session": personAccess(), "nobody": {}} {
		rec := getDocument(t, composeDocuments(t, access), "/api/openapi.json")
		if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
			t.Errorf("%s: Cache-Control %q, want private, no-store", name, got)
		}
		if got := rec.Header().Get("Vary"); got != "Authorization, Cookie" {
			t.Errorf("%s: Vary %q, want Authorization, Cookie", name, got)
		}
	}
}

// A key loading its document is counted against its integration's rate
// limit, carries the RateLimit headers, is refused 429 over it, and every
// answer is in the request log under the document's operation (K23, K24).
func TestOpenAPIJSON_AKeyIsLimitedAndRecorded(t *testing.T) {
	integration, key := uuid.New(), uuid.New()
	access := &scopedRecordingAccess{permissions: []string{"customers:view"}}
	access.check = func(*http.Request, contracts.Rule) (contracts.Principal, error) {
		return keyPrincipal(integration, key, 2), nil
	}
	handler := composeLimitedDocuments(t, access)
	for i, want := range []string{"1", "0"} {
		rec := getDocument(t, handler, "/api/openapi.json")
		if rec.Code != http.StatusOK || rec.Header().Get("RateLimit-Limit") != "2" || rec.Header().Get("RateLimit-Remaining") != want {
			t.Fatalf("request %d: %d %v, want 200 with %s remaining", i+1, rec.Code, rec.Header(), want)
		}
	}
	rec := getDocument(t, handler, "/api/openapi.json")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("third request: %d %v, want 429 with Retry-After", rec.Code, rec.Header())
	}

	access.mu.Lock()
	defer access.mu.Unlock()
	if len(access.recorded) != 3 {
		t.Fatalf("recorded %d requests, want 3", len(access.recorded))
	}
	for i, wantStatus := range []int{200, 200, 429} {
		got := access.recorded[i]
		if got.Status != wantStatus || got.OperationID != documentOperationID || got.KeyID != key || got.IntegrationID != integration {
			t.Errorf("record %d = %+v, want status %d under %s", i, got, wantStatus, documentOperationID)
		}
	}
	if access.recorded[2].Reason != contracts.APIReasonRateLimited {
		t.Errorf("the 429's reason = %q, want %q", access.recorded[2].Reason, contracts.APIReasonRateLimited)
	}
}

// A key identity found and refused is recorded with its reason, as the
// router records one.
func TestOpenAPIJSON_ARefusedKeyIsRecorded(t *testing.T) {
	integration, key := uuid.New(), uuid.New()
	access := &scopedRecordingAccess{}
	access.check = func(*http.Request, contracts.Rule) (contracts.Principal, error) {
		return contracts.Principal{}, &contracts.APIKeyRefusal{KeyID: key, IntegrationID: integration, Reason: "expired", Err: contracts.ErrAPIKeyExpired}
	}
	rec := getDocument(t, composeLimitedDocuments(t, access), "/api/openapi.json")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
	access.mu.Lock()
	defer access.mu.Unlock()
	if len(access.recorded) != 1 || access.recorded[0].Reason != "expired" || access.recorded[0].Status != http.StatusUnauthorized || access.recorded[0].OperationID != documentOperationID {
		t.Errorf("recorded %+v, want one 401 expired under %s", access.recorded, documentOperationID)
	}
}
