package integration_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/knowledge"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// knowledgeRecorder validates the Knowledge exchanges of these tests against
// knowledge.yaml; Knowledge's own suite is its coverage gate.
var knowledgeRecorder = contracttest.NewForModule("knowledge")

// The Assistant, acting for the person through its dispatcher, never finds
// an article hidden from it nor anything below one, and reading one answers
// 404; the person still finds them, and every other Knowledge operation is
// not the Assistant's (knowledge design K14, K16).
func TestKnowledge_TheAssistantNeverSeesHiddenArticles(t *testing.T) {
	t.Parallel()
	h := modtest.New(t, modtest.WithRecorder(knowledgeRecorder), modtest.WithModule(knowledge.Module()))
	c := h.SignIn(t, "knowledge:articles")
	newArticle := func(parent, title string) string {
		t.Helper()
		body := map[string]any{"title": title, "content": map[string]any{"type": "doc", "content": []any{
			map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": title + " søkes i kalenderen."}}},
		}}}
		if parent != "" {
			body["parentId"] = parent
		}
		r := c.Do(http.MethodPost, "/api/v1/knowledge/articles", body)
		if r.Status != http.StatusCreated {
			t.Fatalf("create %s: %d %s", title, r.Status, r.Body)
		}
		var a struct{ ID string }
		r.JSON(&a)
		return a.ID
	}
	open := newArticle("", "Ferie")
	hidden := newArticle("", "Ferie for ledere")
	below := newArticle(hidden, "Ferie for direktører")
	if r := c.Do(http.MethodPatch, "/api/v1/knowledge/articles/"+hidden, map[string]any{"aiHidden": true}); r.Status != http.StatusOK {
		t.Fatalf("hide: %d %s", r.Status, r.Body)
	}
	found := func(body []byte) map[string]bool {
		t.Helper()
		var out struct{ Items []struct{ ID string } }
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("search body %s: %v", body, err)
		}
		ids := map[string]bool{}
		for _, i := range out.Items {
			ids[i.ID] = true
		}
		return ids
	}

	r := c.Do(http.MethodGet, "/api/v1/knowledge/search?q="+url.QueryEscape("ferie"), nil)
	if got := found(r.Body); !got[open] || !got[hidden] || !got[below] {
		t.Errorf("the person's search = %v, want all three", got)
	}

	ctx := h.AssistantContext(t, c, time.Hour)
	ops := h.Operations(t)
	res, err := ops.Call(ctx, operations.CallRequest{OperationID: "searchKnowledge", Query: map[string]any{"q": "ferie"}})
	if err != nil || res.Status != http.StatusOK {
		t.Fatalf("the Assistant's search: %v %d %s", err, res.Status, res.Body)
	}
	if got := found(res.Body); !got[open] || got[hidden] || got[below] {
		t.Errorf("the Assistant's search = %v, want only the open article", got)
	}
	for id, want := range map[string]int{open: http.StatusOK, hidden: http.StatusNotFound, below: http.StatusNotFound} {
		res, err := ops.Call(ctx, operations.CallRequest{OperationID: "getKnowledgeArticleText", PathParams: map[string]any{"id": id}})
		if err != nil || res.Status != want {
			t.Errorf("the Assistant reads %s: %v %d, want %d", id, err, res.Status, want)
		}
	}
	if _, err := ops.Call(ctx, operations.CallRequest{OperationID: "getKnowledgeArticle", PathParams: map[string]any{"id": open}}); err == nil {
		t.Error("the Assistant reached getKnowledgeArticle")
	}
}
