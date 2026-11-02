package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// listOps is a dispatcher with one list operation: it declares the query
// parameters it is given and answers pages of n items from handler.
type listOps struct {
	params  []string
	handler func(q map[string]any) operations.CallResult
	queries []map[string]any
}

func (l *listOps) Call(_ context.Context, req operations.CallRequest) (operations.CallResult, error) {
	l.queries = append(l.queries, req.Query)
	return l.handler(req.Query), nil
}
func (l *listOps) Validate(context.Context, operations.CallRequest) error { return nil }
func (l *listOps) Effective(context.Context, string) (operations.Mode, bool) {
	return operations.ModeAuto, true
}
func (l *listOps) Describe(string) (json.RawMessage, error) {
	type p struct {
		Name string `json:"name"`
	}
	var q []p
	for _, n := range l.params {
		q = append(q, p{n})
	}
	return json.Marshal(map[string]any{"queryParameters": q})
}
func (l *listOps) Info(string) (operations.Info, bool) { return operations.Info{}, false }

func jsonResult(v any) operations.CallResult {
	body, _ := json.Marshal(v)
	return operations.CallResult{Status: 200, Body: body, Header: http.Header{}}
}

func items(from, to int) []map[string]any {
	out := make([]map[string]any, 0, to-from)
	for i := from; i < to; i++ {
		out = append(out, map[string]any{"id": i, "name": fmt.Sprintf("Item %d", i)})
	}
	return out
}

func asInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case string:
		n, _ := strconv.Atoi(x)
		return n
	case json.Number:
		n, _ := strconv.Atoi(x.String())
		return n
	}
	return 0
}

func resolve(t *testing.T, ops *listOps, query map[string]any) ([]map[string]any, *selectorFailure) {
	t.Helper()
	return resolveSelector(context.Background(), ops, operations.Info{}, bulkSelector{OperationID: "list", Query: query}, nil)
}

// pagedBy answers a list of total items by page and pageSize, as 37 of the
// contracts' lists do.
func pagedBy(total int) func(q map[string]any) operations.CallResult {
	return func(q map[string]any) operations.CallResult {
		page, size := asInt(q["page"]), asInt(q["pageSize"])
		if size == 0 {
			size = 50
		}
		from := (page - 1) * size
		to := min(total, from+size)
		data := []map[string]any{}
		if from < total {
			data = items(from, to)
		}
		return jsonResult(map[string]any{"data": data, "pagination": map[string]any{
			"page": page, "pageSize": size, "totalCount": total, "totalPages": (total + size - 1) / size, "hasNextPage": to < total, "hasPreviousPage": page > 1,
		}})
	}
}

func TestSelector_FollowsPageAndPageSizeToTheEnd(t *testing.T) {
	t.Parallel()
	ops := &listOps{params: []string{"page", "pageSize", "search"}, handler: pagedBy(250)}
	got, fail := resolve(t, ops, map[string]any{"search": "x", "page": 7, "pageSize": 3})
	if fail != nil || len(got) != 250 {
		t.Fatalf("resolve = %d items, %v, want all 250", len(got), fail)
	}
	if len(ops.queries) != 3 {
		t.Errorf("%d calls, want 3 pages of 100", len(ops.queries))
	}
	for i, q := range ops.queries {
		if q["search"] != "x" || asInt(q["page"]) != i+1 || asInt(q["pageSize"]) != bulkPageSize {
			t.Errorf("query %d = %v, want the filter kept and the paging the server's own", i, q)
		}
	}
	for i, it := range got {
		if asInt(it["id"]) != i {
			t.Fatalf("item %d has id %v: the order the list gave is not kept", i, it["id"])
		}
	}
}

func TestSelector_FollowsACursor(t *testing.T) {
	t.Parallel()
	ops := &listOps{params: []string{"cursor", "limit"}, handler: func(q map[string]any) operations.CallResult {
		start := 0
		if c, ok := q["cursor"].(string); ok {
			start, _ = strconv.Atoi(strings.TrimPrefix(c, "c"))
		}
		end := min(start+100, 230)
		out := map[string]any{"data": items(start, end)}
		if end < 230 {
			out["nextCursor"] = "c" + strconv.Itoa(end)
		} else {
			out["nextCursor"] = nil
		}
		return jsonResult(out)
	}}
	got, fail := resolve(t, ops, nil)
	if fail != nil || len(got) != 230 || len(ops.queries) != 3 {
		t.Fatalf("resolve = %d items in %d calls, %v, want 230 in 3", len(got), len(ops.queries), fail)
	}
}

func TestSelector_ReadsALimitAndOffset(t *testing.T) {
	t.Parallel()
	ops := &listOps{params: []string{"offset", "limit"}, handler: func(q map[string]any) operations.CallResult {
		off, lim := asInt(q["offset"]), asInt(q["limit"])
		return jsonResult(map[string]any{"items": items(off, min(off+lim, 130))})
	}}
	got, fail := resolve(t, ops, nil)
	if fail != nil || len(got) != 130 {
		t.Fatalf("resolve = %d, %v, want 130", len(got), fail)
	}
}

func TestSelector_AListWithNoPagingIsOneAnswerWhateverShapeItHas(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]any{"a bare array": items(0, 7), "data": map[string]any{"data": items(0, 7)}, "items": map[string]any{"items": items(0, 7)}} {
		ops := &listOps{handler: func(map[string]any) operations.CallResult { return jsonResult(body) }}
		got, fail := resolve(t, ops, nil)
		if fail != nil || len(got) != 7 || len(ops.queries) != 1 {
			t.Errorf("%s: %d items in %d calls, %v, want 7 in 1", name, len(got), len(ops.queries), fail)
		}
	}
}

func TestSelector_RefusesAListItCannotFollowWithAReasonTheModelCanActOn(t *testing.T) {
	t.Parallel()
	for _, param := range []string{"after", "pageToken", "skip", "perPage"} {
		ops := &listOps{params: []string{param}, handler: func(map[string]any) operations.CallResult { return jsonResult(items(0, 3)) }}
		_, fail := resolve(t, ops, nil)
		if fail == nil || fail.code != "paging_not_supported" || !strings.Contains(fail.message, param) {
			t.Errorf("a list paged by %q = %+v, want paging_not_supported naming it", param, fail)
		}
		if len(ops.queries) != 0 {
			t.Errorf("%q: the list was called before it was refused", param)
		}
	}
	// A page parameter with no pagination in the answer, and a full page, cannot be followed either.
	ops := &listOps{params: []string{"page", "pageSize"}, handler: func(q map[string]any) operations.CallResult {
		return jsonResult(map[string]any{"data": items(0, asInt(q["pageSize"]))})
	}}
	if _, fail := resolve(t, ops, nil); fail == nil || fail.code != "paging_not_supported" {
		t.Errorf("no pagination = %+v, want paging_not_supported", fail)
	}
	// A limit that is reached, with no way past it.
	ops = &listOps{params: []string{"limit"}, handler: func(q map[string]any) operations.CallResult {
		return jsonResult(items(0, asInt(q["limit"])))
	}}
	if _, fail := resolve(t, ops, nil); fail == nil || fail.code != "paging_not_supported" {
		t.Errorf("a limit with no way past it = %+v, want paging_not_supported", fail)
	}
}

func TestSelector_RefusesMoreThanTheCapWithTheCount(t *testing.T) {
	t.Parallel()
	ops := &listOps{params: []string{"page", "pageSize"}, handler: pagedBy(maxBulkItems + 400)}
	_, fail := resolve(t, ops, nil)
	if fail == nil || fail.code != "selection_too_large" || !strings.Contains(fail.message, "2400") || !strings.Contains(fail.message, "2000") {
		t.Fatalf("over the cap = %+v, want selection_too_large with the count", fail)
	}
	if len(ops.queries) != 1 {
		t.Errorf("%d pages read before refusing, want the first, which says the total", len(ops.queries))
	}
	// Exactly the cap is accepted.
	ops = &listOps{params: []string{"page", "pageSize"}, handler: pagedBy(maxBulkItems)}
	if got, fail := resolve(t, ops, nil); fail != nil || len(got) != maxBulkItems {
		t.Errorf("exactly the cap = %d, %v, want accepted", len(got), fail)
	}
	// A cursor list that never says how many is refused once it passes the cap.
	cursor := &listOps{params: []string{"cursor", "limit"}, handler: func(q map[string]any) operations.CallResult {
		start := 0
		if c, ok := q["cursor"].(string); ok {
			start, _ = strconv.Atoi(c)
		}
		return jsonResult(map[string]any{"data": items(start, start+100), "nextCursor": strconv.Itoa(start + 100)})
	}}
	if _, fail := resolve(t, cursor, nil); fail == nil || fail.code != "selection_too_large" || !strings.Contains(fail.message, "more than") {
		t.Errorf("an endless cursor = %+v, want selection_too_large, more than the cap", fail)
	}
}

func TestSelector_HalvesThePageWhenItIsTooLargeToRead(t *testing.T) {
	t.Parallel()
	ops := &listOps{params: []string{"page", "pageSize"}}
	ops.handler = func(q map[string]any) operations.CallResult {
		if asInt(q["pageSize"]) > 25 {
			return operations.CallResult{Status: 200, Truncated: true, Header: http.Header{}}
		}
		return pagedBy(60)(q)
	}
	got, fail := resolve(t, ops, nil)
	if fail != nil || len(got) != 60 {
		t.Fatalf("resolve = %d, %v, want all 60 read at a smaller page", len(got), fail)
	}
}

func TestSelector_ARefusedOrNonListAnswerIsReportedPlainly(t *testing.T) {
	t.Parallel()
	denied := &listOps{handler: func(map[string]any) operations.CallResult {
		r := operations.CallResult{Status: 403, Header: http.Header{}}
		r.Body, _ = json.Marshal(map[string]any{"title": "You may not list these"})
		return r
	}}
	if _, fail := resolve(t, denied, nil); fail == nil || fail.code != "selector_refused" || !strings.Contains(fail.message, "403") {
		t.Errorf("a refused list = %+v", fail)
	}
	notList := &listOps{handler: func(map[string]any) operations.CallResult { return jsonResult(map[string]any{"name": "one record"}) }}
	if _, fail := resolve(t, notList, nil); fail == nil || fail.code != "selector_not_a_list" {
		t.Errorf("a record = %+v, want selector_not_a_list", fail)
	}
	scalars := &listOps{handler: func(map[string]any) operations.CallResult { return jsonResult([]int{1, 2, 3}) }}
	if _, fail := resolve(t, scalars, nil); fail == nil || fail.code != "selector_not_a_list" {
		t.Errorf("an array of numbers = %+v, want selector_not_a_list", fail)
	}
}

func TestBind_FreezesTheValuesAndExcludesWhatCannotBeChanged(t *testing.T) {
	t.Parallel()
	tpl := bulkTemplate{OperationID: "put", Bind: map[string]string{"id": "id", "revision": "meta.rev"}, BindIn: map[string]string{"id": "path", "revision": "body"}, Body: map[string]any{"name": "X"}}
	found := []map[string]any{
		{"id": json.Number("1"), "name": "A", "meta": map[string]any{"rev": json.Number("4")}},
		{"id": json.Number("2"), "name": "B"},                                                        // no revision
		{"id": json.Number("1"), "name": "A again", "meta": map[string]any{"rev": json.Number("4")}}, // the same item twice
		{"id": "", "meta": map[string]any{"rev": json.Number("1")}},                                  // an empty id
		{"id": json.Number("3"), "name": "C", "meta": map[string]any{"rev": json.Number("9")}},
	}
	run, excluded := tpl.bind(found)
	if len(run) != 2 || run[0].Label != "A" || run[1].Label != "C" {
		t.Fatalf("runnable = %+v, want A and C in the order given", run)
	}
	reasons := map[string]int{}
	for _, e := range excluded {
		reasons[e.reason]++
	}
	if reasons[reasonMissingID] != 2 || reasons[reasonDuplicateID] != 1 {
		t.Errorf("excluded = %v, want two missing a value and one duplicate", reasons)
	}
	req := tpl.request(run[1].Bound)
	if req.PathParams["id"] != json.Number("3") || !strings.Contains(string(req.Body), `"revision":9`) || !strings.Contains(string(req.Body), `"name":"X"`) {
		t.Errorf("request = %+v %s, want the item's id in the path and its revision in the body beside the values", req.PathParams, req.Body)
	}
}
