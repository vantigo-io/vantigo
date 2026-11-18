package integration_test

import (
	"net/http"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/vantigo-io/vantigo/server/internal/accounting"
	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/knowledge"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// An API key through the router and the handlers' own checks (API access
// design K9, K19, K33).

func keyHarness(t *testing.T, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	return modtest.New(t, append([]modtest.Option{modtest.WithRecorder(contracttest.NewForModule("customers"))}, opts...)...)
}

// TestAPIKey_TheRouterJudgesTheMarks is K9 through a module's router: a GET
// is a read, a POST is not, a narrowing cuts the roles down, and the
// handler is reached once the key passes.
func TestAPIKey_TheRouterJudgesTheMarks(t *testing.T) {
	t.Parallel()
	h := keyHarness(t, modtest.WithModule(customers.Module()))
	perms := []string{"customers:view", "customers:create"}
	full, _ := h.SignInAPIKey(t, modtest.APIKey{}, perms...)
	readOnly, _ := h.SignInAPIKey(t, modtest.APIKey{ReadOnly: true}, perms...)
	narrowed, _ := h.SignInAPIKey(t, modtest.APIKey{Permissions: []string{"customers:view"}}, perms...)
	skip := modtest.SkipContract("an API key's refusals")

	for name, c := range map[string]*modtest.Client{"full": full, "read-only": readOnly, "narrowed": narrowed} {
		if r := c.Do(http.MethodGet, "/api/v1/customers", nil, skip); r.Status != http.StatusOK {
			t.Errorf("%s GET: %d %s", name, r.Status, r.Body)
		}
	}
	r := full.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "Made by a key"}, skip)
	if r.Status != http.StatusCreated {
		t.Errorf("full POST: %d %s", r.Status, r.Body)
	}
	if r := readOnly.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "x"}, skip); r.Status != http.StatusForbidden || r.Code() != "api_key_read_only" {
		t.Errorf("read-only POST: %d %s", r.Status, r.Body)
	}
	if r := narrowed.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "x"}, skip); r.Status != http.StatusForbidden || r.Code() != "forbidden" {
		t.Errorf("narrowed POST: %d %s", r.Status, r.Body)
	}
}

// TestAPIKey_HandlerChecksAnswerAsForAPerson is K33 at a handler-side check:
// Accounting's meta asks HasPermission for each flag on a GET, and a
// read-only key holding a role gets what a person holding it gets — except
// accounting:close, which no key ever holds (K7).
func TestAPIKey_HandlerChecksAnswerAsForAPerson(t *testing.T) {
	t.Parallel()
	h := keyHarness(t, modtest.WithModule(accounting.Module()))
	perms := []string{"accounting:read", "accounting:post", "accounting:close", "accounting:export"}
	person := h.SignIn(t, perms...)
	key, _ := h.SignInAPIKey(t, modtest.APIKey{ReadOnly: true}, perms...)
	type meta struct {
		CanPost   bool `json:"canPost"`
		CanClose  bool `json:"canClose"`
		CanExport bool `json:"canExport"`
	}
	read := func(c *modtest.Client) meta {
		r := c.Do(http.MethodGet, "/api/v1/accounting/meta", nil, modtest.SkipContract("accounting's meta from another module's recorder"))
		if r.Status != http.StatusOK {
			t.Fatalf("meta: %d %s", r.Status, r.Body)
		}
		var m meta
		r.JSON(&m)
		return m
	}
	if got := read(person); got != (meta{true, true, true}) {
		t.Errorf("the person's meta %+v", got)
	}
	if got := read(key); got != (meta{CanPost: true, CanClose: false, CanExport: true}) {
		t.Errorf("the read-only key's meta %+v, want the person's but for close", got)
	}
}

// TestAPIKey_KnowledgeManageIsNoKeys is K7 and K50 at Knowledge's canManage:
// a person holding knowledge:manage administers a workspace article another
// person wrote; a key whose role holds it does not, as it holds no
// non-delegable key.
func TestAPIKey_KnowledgeManageIsNoKeys(t *testing.T) {
	t.Parallel()
	h := keyHarness(t, modtest.WithModule(knowledge.Module()))
	skip := modtest.SkipContract("knowledge from another module's recorder")
	writer := h.SignIn(t, "knowledge:articles")
	r := writer.Do(http.MethodPost, "/api/v1/knowledge/articles", map[string]any{"title": "Rutiner", "section": "workspace"}, skip)
	if r.Status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	var article struct {
		ID string `json:"id"`
	}
	r.JSON(&article)
	// Read by everyone, so writing it is no one's reason to administer it.
	h.Exec(t, `UPDATE knowledge.articles SET internal_permission = 'read' WHERE id = $1`, article.ID)
	h.Exec(t, `UPDATE knowledge.article_access_default SET permission = 'read' WHERE article_id = $1`, article.ID)
	manager := h.SignIn(t, "knowledge:articles", "knowledge:manage")
	key, _ := h.SignInAPIKey(t, modtest.APIKey{}, "knowledge:articles", "knowledge:manage")
	canManage := func(c *modtest.Client) bool {
		r := c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+article.ID+"/access", nil, skip)
		if r.Status != http.StatusOK {
			t.Fatalf("access: %d %s", r.Status, r.Body)
		}
		var a struct {
			CanManage bool `json:"canManage"`
		}
		r.JSON(&a)
		return a.CanManage
	}
	if !canManage(manager) || canManage(key) {
		t.Errorf("canManage: the manager %v, the key %v; want true and false", canManage(manager), canManage(key))
	}
}

// TestAPIKey_NoSecretInALogLineOrASpan is K11: a key's use, refused or not,
// leaves no vtg_ in any log line or span attribute.
func TestAPIKey_NoSecretInALogLineOrASpan(t *testing.T) {
	t.Parallel()
	spans := tracetest.NewSpanRecorder()
	h := keyHarness(t, modtest.WithModule(customers.Module()), modtest.WithSpanRecorder(spans))
	c, integration := h.SignInAPIKey(t, modtest.APIKey{}, "customers:view", "customers:create")
	skip := modtest.SkipContract("an API key's refusals")
	c.Do(http.MethodGet, "/api/v1/customers", nil, skip)
	c.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "Made by a key"}, skip)
	c.Do(http.MethodGet, "/api/v1/identity/integrations", nil, skip)
	key, _ := h.APIKeyFor(t, integration, modtest.APIKey{ReadOnly: true})
	h.APIKeyClient(t, key).Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "x"}, skip)
	h.APIKeyClient(t, "vtg_"+strings.Repeat("x", 49)).Do(http.MethodGet, "/api/v1/customers", nil, skip)

	if strings.Contains(h.Logs(), "vtg_") {
		t.Errorf("a log line carries a key:\n%s", h.Logs())
	}
	ended := spans.Ended()
	if len(ended) == 0 {
		t.Fatal("no span recorded")
	}
	for _, s := range ended {
		for _, a := range s.Attributes() {
			if strings.Contains(a.Value.Emit(), "vtg_") {
				t.Errorf("span %s attribute %s carries a key", s.Name(), a.Key)
			}
		}
		for _, e := range s.Events() {
			for _, a := range e.Attributes {
				if strings.Contains(a.Value.Emit(), "vtg_") {
					t.Errorf("span %s event %s carries a key", s.Name(), e.Name)
				}
			}
		}
	}
}
