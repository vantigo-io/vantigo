package integration_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/knowledge"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// The document against the mounted router (code review 9): an operation the
// key's /api/openapi.json marks with the Idempotency-Key is replayed on a
// retry with the key, and one it does not mark runs again, unmarked.

var documentOffContract = modtest.SkipContract("GET /api/openapi.json is not an operation of the contract")

// marksKey reports whether the key's document marks operationID with the
// general Idempotency-Key.
func marksKey(t *testing.T, c *modtest.Client, operationID string) bool {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/openapi.json", nil, documentOffContract)
	if r.Status != http.StatusOK {
		t.Fatalf("the document: %d %s", r.Status, r.Body)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string           `json:"operationId"`
			Parameters  []map[string]any `json:"parameters"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(r.Body, &doc); err != nil {
		t.Fatal(err)
	}
	for _, item := range doc.Paths {
		for _, op := range item {
			if op.OperationID != operationID {
				continue
			}
			for _, p := range op.Parameters {
				if p["$ref"] == idempotencyParameterRef {
					return true
				}
			}
			return false
		}
	}
	t.Fatalf("%s is not in the key's document", operationID)
	return false
}

func TestIdempotency_TheDocumentMatchesTheMountedRouter(t *testing.T) {
	t.Run("marked", func(t *testing.T) {
		h := modtest.New(t, modtest.WithModule(customers.Module()), modtest.WithRecorder(contracttest.NewForModule("customers")))
		integration := h.Integration(t, "Shop", "customers:view", "customers:create")
		key, _ := h.APIKeyFor(t, integration, modtest.APIKey{})
		c := h.APIKeyClient(t, key)
		if !marksKey(t, c, "postCustomers") {
			t.Fatal("the document does not mark postCustomers")
		}
		body := map[string]any{"name": "Nordvik AS"}
		first := c.Do(http.MethodPost, "/api/v1/customers", body, modtest.Header("Idempotency-Key", "router-1"))
		again := c.Do(http.MethodPost, "/api/v1/customers", body, modtest.Header("Idempotency-Key", "router-1"))
		if first.Status != http.StatusCreated || again.Status != http.StatusCreated || again.Header("Idempotent-Replayed") != "true" ||
			string(again.Body) != string(first.Body) {
			t.Errorf("first %d, again %d %q: %s", first.Status, again.Status, again.Header("Idempotent-Replayed"), again.Body)
		}
		if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE name = 'Nordvik AS'`); n != 1 {
			t.Errorf("%d customers created", n)
		}
	})
	t.Run("not marked", func(t *testing.T) {
		h := modtest.New(t, modtest.WithModule(knowledge.Module()), modtest.WithRecorder(contracttest.NewForModule("knowledge")))
		integration := h.Integration(t, "Wiki", "knowledge:articles")
		key, _ := h.APIKeyFor(t, integration, modtest.APIKey{})
		c := h.APIKeyClient(t, key)
		// A read marked as one for keys: out of scope.
		if marksKey(t, c, "resolveKnowledgeLinks") {
			t.Fatal("the document marks resolveKnowledgeLinks")
		}
		body := map[string]any{"ids": []string{}}
		for range 2 {
			r := c.Do(http.MethodPost, "/api/v1/knowledge/links/resolve", body, modtest.Header("Idempotency-Key", "router-2"))
			if r.Status != http.StatusOK || r.Header("Idempotent-Replayed") != "" {
				t.Errorf("%d %q: %s", r.Status, r.Header("Idempotent-Replayed"), r.Body)
			}
		}
		if n := h.Count(t, `SELECT count(*) FROM identity.idempotency_keys`); n != 0 {
			t.Errorf("%d keys kept for an operation out of scope", n)
		}
	})
}
