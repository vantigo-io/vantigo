package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/agentloop"
	"github.com/vantigo-io/vantigo/server/internal/assistant/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// Bulk changes (the Assistant design S13). A bulk plan is a selector, the one
// change to make to each item it finds, and the values. propose_bulk reads the
// selector to its end once, freezes the items, and shows the person a preview;
// nothing is written. Only the person's own Start runs the first five items (the
// canary), and only their Continue runs the rest, in chunks, as a job.
//
// It is a preview, not a dry run: handlers write directly and some have side
// effects outside their transaction, so nothing can be rolled back generically,
// and nothing here, in the interface or in the documentation says otherwise.

const (
	toolProposeBulk = "propose_bulk"

	// maxBulkItems is the most items a plan may hold. A selector that finds more
	// is refused with its count, so the model can narrow the filter.
	maxBulkItems = 2000
	// bulkSampleSize is how many items the preview shows, each with its current
	// value and the new one.
	bulkSampleSize = 10
	// bulkCanarySize is how many items run for real on Start before the person
	// is asked to go on.
	bulkCanarySize = 5
	// bulkChunkSize is how many items run between two looks at the stop request,
	// the Owner's levels and the grant.
	bulkChunkSize = 100

	// bulkPageSize is the page the selector asks for, and bulkMinPageSize the
	// smallest it falls back to when a page is too large to read. maxBulkPages
	// bounds a list that never says it has ended.
	bulkPageSize    = 100
	bulkMinPageSize = 10
	maxBulkPages    = 60

	// bulkGrantTTL is the life of the grant a job carries: the most identity
	// issues. A job with less than bulkGrantMargin of it left waits for the
	// person's Continue, which issues a fresh one from their session.
	bulkGrantTTL    = contracts.MaxAssistantGrantTTL
	bulkGrantMargin = 5 * time.Minute
	// bulkClaimTTL is how long one replica's claim on a running job lasts
	// without renewal; every item renews it.
	bulkClaimTTL = 2 * time.Minute
	// bulkWaitTTL is how long a preview, or a job waiting for Continue, waits
	// for the person.
	bulkWaitTTL = 24 * time.Hour
	// bulkCallTimeout bounds one call through the dispatcher.
	bulkCallTimeout = 60 * time.Second
	// bulkRetryAfterMax bounds how long a 429 is waited out.
	bulkRetryAfterMax = 10 * time.Second
	// maxBulkFailuresShown is how many failed items the job's answer lists.
	maxBulkFailuresShown = 50

	// The states of a job (assistant.bulk_jobs.state).
	jobPreviewed = "previewed"
	jobCanary    = "canary"
	jobAwaiting  = "awaiting_continue"
	jobRunning   = "running"
	jobStopping  = "stopping"
	jobDone      = "done"
	jobStopped   = "stopped"
	jobFailed    = "failed"
	jobExpired   = "expired"

	// Why a job ended other than done, or waits.
	reasonCancelled   = "cancelled"
	reasonByPerson    = "stopped_by_person"
	reasonNarrowed    = "narrowed"
	reasonSessionEnd  = "session_ended"
	reasonInternal    = "internal"
	reasonGrantLapse  = "grant_expiring"
	reasonCanaryDone  = "canary_done"
	reasonMissingID   = "missing_id"
	reasonDuplicateID = "duplicate"

	codeJobNotWaiting = "job_not_waiting"

	kindChange = "change"
	kindBulk   = "bulk"
)

// bulkOps is what bulk changes ask of the dispatcher. The dispatcher is the
// implementation; a test supplies a scripted one.
type bulkOps interface {
	Call(ctx context.Context, req operations.CallRequest) (operations.CallResult, error)
	Validate(ctx context.Context, req operations.CallRequest) error
	Effective(ctx context.Context, id string) (operations.Mode, bool)
	Describe(id string) (json.RawMessage, error)
	Info(id string) (operations.Info, bool)
}

// bulkSelector is the list operation a plan reads its items with.
type bulkSelector struct {
	OperationID string          `json:"operationId"`
	PathParams  map[string]any  `json:"pathParams,omitempty"`
	Query       map[string]any  `json:"query,omitempty"`
	Body        json.RawMessage `json:"body,omitempty"`
}

// bulkTemplate is the change made to each item: the operation, the values that
// are the same for every item, and which item field feeds which parameter. Path
// parameters and body fields are bound; BindIn says which each is.
type bulkTemplate struct {
	OperationID string            `json:"operationId"`
	PathParams  map[string]any    `json:"pathParams,omitempty"`
	Query       map[string]any    `json:"query,omitempty"`
	Body        map[string]any    `json:"body,omitempty"`
	Bind        map[string]string `json:"bind"`
	BindIn      map[string]string `json:"bindIn"`
	Fields      []string          `json:"fields"`
}

// request is the call for one item, with the item's values bound in.
func (tpl bulkTemplate) request(bound map[string]any) operations.CallRequest {
	req := operations.CallRequest{OperationID: tpl.OperationID, PathParams: map[string]any{}, Query: tpl.Query}
	for k, v := range tpl.PathParams {
		req.PathParams[k] = v
	}
	body := make(map[string]any)
	for k, v := range tpl.Body {
		body[k] = v
	}
	for name, v := range bound {
		if tpl.BindIn[name] == "path" {
			req.PathParams[name] = v
		} else {
			body[name] = v
		}
	}
	if len(body) > 0 {
		req.Body, _ = json.Marshal(body)
	}
	return req
}

// bulkItem is one frozen item: the values it feeds the operation, by parameter
// name, and the label the lists show it by.
type bulkItem struct {
	Bound map[string]any `json:"bound"`
	Label string         `json:"label,omitempty"`
}

// bulkDraft is a bulk plan before it is stored: the frozen items (those that
// run first, then those the pre-check excluded, with why).
type bulkDraft struct {
	selector bulkSelector
	template bulkTemplate
	items    []bulkItem
	excluded []bulkExcludedItem
}

type bulkExcludedItem struct {
	item   bulkItem
	reason string
}

// bulkPreview is what the card keeps of the preview, beside the generic card.
type bulkPreview struct {
	Sample   []gen.AssistantBulkSample `json:"sample"`
	Excluded gen.AssistantBulkExcluded `json:"excluded"`
}

// selectorFailure is a selector that cannot be used, in words the model can
// act on.
type selectorFailure struct{ code, message string }

func (f *selectorFailure) outcome() outcome { return refused(f.code, f.message) }

// pagingMode is how a list is read to its end.
type pagingMode int

const (
	pagingNone   pagingMode = iota // the list answers everything at once
	pagingPage                     // page and pageSize; the answer has a pagination
	pagingCursor                   // cursor (and limit); the answer has a nextCursor
	pagingOffset                   // offset and limit
	pagingLimit                    // a limit alone: whole if it was not reached
)

// otherPaging are parameter names that mean paging this code cannot follow.
var otherPaging = map[string]bool{
	"skip": true, "take": true, "after": true, "before": true, "pagetoken": true, "pagenumber": true,
	"perpage": true, "per_page": true, "size": true, "start": true, "token": true, "next": true, "from": true,
}

// pagingOf decides how a list is paged from the query parameters it declares.
func pagingOf(names map[string]bool) (pagingMode, *selectorFailure) {
	switch {
	case names["page"]:
		return pagingPage, nil
	case names["cursor"]:
		return pagingCursor, nil
	case names["offset"]:
		return pagingOffset, nil
	}
	for name := range names {
		if otherPaging[strings.ToLower(name)] {
			return 0, &selectorFailure{"paging_not_supported", fmt.Sprintf("that list is paged by %q, which a bulk change cannot follow; use another list operation for the same records, or change them one at a time", name)}
		}
	}
	if names["limit"] || names["pageSize"] {
		return pagingLimit, nil
	}
	return pagingNone, nil
}

// pagingKeys are the query parameters the selector sets itself.
var pagingKeys = []string{"page", "pageSize", "cursor", "limit", "offset"}

// listCaller calls the selector's operation through the dispatcher, waits out
// one rate limit, and records the call for the action log.
type listCaller struct {
	ops    bulkOps
	logged *[]loggedCall
}

func (l listCaller) call(ctx context.Context, req operations.CallRequest) (operations.CallResult, error) {
	start := time.Now()
	res, _, err := callWithRetry(ctx, l.ops, req)
	if l.logged != nil && err == nil {
		*l.logged = append(*l.logged, loggedCall{operation: req.OperationID, mode: string(operations.ModeAuto), status: int32(res.Status), elapsed: time.Since(start)})
	}
	return res, err
}

// callWithRetry is one call, bounded in time, retried once after the wait a
// 429 asks for.
// timedOut reports that the last attempt ran into bulkCallTimeout while ctx
// itself was still live: the handler was cut off part-way, so whatever it
// answered, its write may already have been committed.
func callWithRetry(ctx context.Context, ops bulkOps, req operations.CallRequest) (res operations.CallResult, timedOut bool, err error) {
	return callWithin(ctx, ops, req, bulkCallTimeout)
}

// callWithin is callWithRetry with the per-call bound given.
func callWithin(ctx context.Context, ops bulkOps, req operations.CallRequest, bound time.Duration) (res operations.CallResult, timedOut bool, err error) {
	for attempt := 0; ; attempt++ {
		cctx, cancel := context.WithTimeout(ctx, bound)
		res, err = ops.Call(cctx, req)
		timedOut = errors.Is(cctx.Err(), context.DeadlineExceeded) && ctx.Err() == nil
		cancel()
		if err != nil || timedOut || res.Status != 429 || attempt > 0 {
			return res, timedOut, err
		}
		wait := retryAfter(res.Header.Get("Retry-After"))
		select {
		case <-ctx.Done():
			return res, false, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// retryAfter is the wait a Retry-After header asks for, in seconds, between a
// second and bulkRetryAfterMax.
func retryAfter(header string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || secs < 1 {
		return time.Second
	}
	return min(time.Duration(secs)*time.Second, bulkRetryAfterMax)
}

// extractList finds the array of items in a list answer: the answer itself, or
// its "data" or "items". Every element must be an object.
func extractList(raw json.RawMessage) (list []map[string]any, meta map[string]any, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, nil, false
	}
	var arr []any
	switch x := v.(type) {
	case []any:
		arr = x
	case map[string]any:
		meta = x
		for _, key := range []string{"data", "items"} {
			if a, isArr := x[key].([]any); isArr {
				arr = a
				break
			}
		}
		if arr == nil {
			return nil, meta, false
		}
	default:
		return nil, nil, false
	}
	list = make([]map[string]any, 0, len(arr))
	for _, e := range arr {
		obj, isObj := e.(map[string]any)
		if !isObj {
			return nil, meta, false
		}
		list = append(list, obj)
	}
	return list, meta, true
}

// intOf reads a whole number out of a decoded JSON value.
func intOf(v any) (int, bool) {
	text, ok := scalarString(v)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(text)
	return n, err == nil
}

// problemFailure turns a refused list call into a failure the model can act on.
func problemFailure(res operations.CallResult) *selectorFailure {
	msg := problemText(res.Body)
	if msg == "" {
		msg = "the list was refused"
	}
	return &selectorFailure{"selector_refused", fmt.Sprintf("reading the list answered %d: %s", res.Status, msg)}
}

// resolveSelector reads the selector's list to its end, as the person, through
// the dispatcher: following the paging the operation declares, never more than
// maxBulkItems, and keeping the order the list gave. It writes nothing.
func resolveSelector(ctx context.Context, ops bulkOps, info operations.Info, sel bulkSelector, logged *[]loggedCall) ([]map[string]any, *selectorFailure) {
	doc, err := ops.Describe(sel.OperationID)
	if err != nil {
		return nil, &selectorFailure{"selector_unknown", "there is no such list operation; search again with search_operations"}
	}
	var described struct {
		Query []struct {
			Name string `json:"name"`
		} `json:"queryParameters"`
	}
	_ = json.Unmarshal(doc, &described)
	names := map[string]bool{}
	for _, q := range described.Query {
		names[q.Name] = true
	}
	mode, fail := pagingOf(names)
	if fail != nil {
		return nil, fail
	}

	caller := listCaller{ops: ops, logged: logged}
	var items []map[string]any
	pageSize := bulkPageSize
	cursor, offset := "", 0
	for page := 1; page <= maxBulkPages; page++ {
		query := make(map[string]any)
		for k, v := range sel.Query {
			query[k] = v
		}
		for _, k := range pagingKeys {
			delete(query, k)
		}
		switch mode {
		case pagingPage:
			query["page"] = page
			if names["pageSize"] {
				query["pageSize"] = pageSize
			}
		case pagingCursor:
			if cursor != "" {
				query["cursor"] = cursor
			}
			if names["limit"] {
				query["limit"] = pageSize
			}
		case pagingOffset:
			query["offset"] = offset
			if names["limit"] {
				query["limit"] = pageSize
			}
		case pagingLimit:
			if names["limit"] {
				query["limit"] = pageSize
			} else {
				query["pageSize"] = pageSize
			}
		}
		req := operations.CallRequest{OperationID: sel.OperationID, PathParams: sel.PathParams, Query: query, Body: sel.Body}
		res, err := caller.call(ctx, req)
		if err != nil {
			var oe *operations.Error
			if errors.As(err, &oe) {
				return nil, &selectorFailure{oe.Code, oe.Message}
			}
			if ctx.Err() != nil {
				return nil, &selectorFailure{"selector_failed", "the run was stopped while the list was read"}
			}
			return nil, &selectorFailure{"selector_failed", "the list could not be read"}
		}
		if res.Truncated {
			if mode != pagingNone && pageSize > bulkMinPageSize {
				pageSize = max(bulkMinPageSize, pageSize/2)
				page--
				continue
			}
			return nil, &selectorFailure{"selector_too_large", "the list answered more than can be read at once; narrow it with filters"}
		}
		if res.Status < 200 || res.Status >= 300 {
			return nil, problemFailure(res)
		}
		list, meta, ok := extractList(res.Body)
		if !ok {
			return nil, &selectorFailure{"selector_not_a_list", "that operation does not answer a list of records (an array under data or items, or an array); choose a list operation"}
		}
		items = append(items, list...)

		more := false
		switch mode {
		case pagingPage:
			pg, _ := meta["pagination"].(map[string]any)
			if pg == nil {
				if len(list) >= pageSize {
					return nil, &selectorFailure{"paging_not_supported", "that list does not say whether there is another page, so a bulk change cannot read it to the end"}
				}
				break
			}
			if total, ok := intOf(pg["totalCount"]); ok && total > maxBulkItems {
				return nil, tooMany(total, false)
			}
			if applied, ok := intOf(pg["pageSize"]); ok && applied > 0 {
				pageSize = applied
			}
			more, _ = pg["hasNextPage"].(bool)
		case pagingCursor:
			if next, ok := meta["nextCursor"].(string); ok && next != "" && len(list) > 0 {
				cursor, more = next, true
			}
		case pagingOffset:
			offset += len(list)
			more = len(list) >= pageSize && len(list) > 0
		case pagingLimit:
			if len(list) >= pageSize {
				return nil, &selectorFailure{"paging_not_supported", "that list takes a limit but cannot be continued past it, so a bulk change cannot read it to the end"}
			}
		}
		if len(items) > maxBulkItems {
			return nil, tooMany(len(items), more)
		}
		if !more {
			return items, nil
		}
	}
	return nil, &selectorFailure{"selection_too_large", fmt.Sprintf("that list has more than %d items; narrow the filter", maxBulkItems)}
}

func tooMany(n int, atLeast bool) *selectorFailure {
	count := strconv.Itoa(n)
	if atLeast {
		count = "more than " + strconv.Itoa(n)
	}
	return &selectorFailure{"selection_too_large", fmt.Sprintf("the selector finds %s items, and a bulk change takes at most %d; narrow the filter and try again", count, maxBulkItems)}
}

// lookup reads a (possibly dotted) field of an item.
func lookup(item map[string]any, path string) (any, bool) {
	var cur any = item
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[part]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// boundValue is an item's value for a parameter: a non-empty string, a number
// or a boolean, nothing else.
func boundValue(v any) (any, bool) {
	switch x := v.(type) {
	case string:
		if strings.TrimSpace(x) == "" {
			return nil, false
		}
		return x, true
	case json.Number:
		return x, true
	case bool:
		return x, true
	}
	return nil, false
}

// fieldNames are the scalar fields the items carry, for a refusal that tells the
// model which to bind.
func fieldNames(item map[string]any) []string {
	var out []string
	for k, v := range item {
		if _, ok := boundValue(v); ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

// bind freezes the selector's items: each becomes the values it feeds the
// operation, in the order the list gave; an item without a value for a bound
// parameter, or that repeats one before it, is excluded with its reason.
func (tpl bulkTemplate) bind(items []map[string]any) (runnable []bulkItem, excluded []bulkExcludedItem) {
	seen := map[string]bool{}
	for _, raw := range items {
		bound := make(map[string]any, len(tpl.Bind))
		ok := true
		for param, field := range tpl.Bind {
			v, found := lookup(raw, field)
			if bv, isScalar := boundValue(v); found && isScalar {
				bound[param] = bv
			} else {
				ok = false
				break
			}
		}
		item := bulkItem{Label: labelOf(raw)}
		if !ok {
			excluded = append(excluded, bulkExcludedItem{item: item, reason: reasonMissingID})
			continue
		}
		item.Bound = bound
		key, _ := json.Marshal(bound)
		if seen[string(key)] {
			excluded = append(excluded, bulkExcludedItem{item: item, reason: reasonDuplicateID})
			continue
		}
		seen[string(key)] = true
		runnable = append(runnable, item)
	}
	return runnable, excluded
}

// bulkArgs are propose_bulk's arguments.
type bulkArgs struct {
	Selector struct {
		OperationID string          `json:"operationId"`
		PathParams  map[string]any  `json:"pathParams"`
		Query       map[string]any  `json:"query"`
		Body        json.RawMessage `json:"body"`
	} `json:"selector"`
	Operation struct {
		OperationID string          `json:"operationId"`
		PathParams  map[string]any  `json:"pathParams"`
		Query       map[string]any  `json:"query"`
		Body        json.RawMessage `json:"body"`
	} `json:"operation"`
	Bind    map[string]string `json:"bind"`
	Explain string            `json:"explain"`
}

// proposeBulk turns a bulk request into a plan the person has yet to start: it
// reads the selector to its end and freezes the items, checks the per-item
// operation and the binding, and builds the preview. It writes nothing.
func (t *toolbox) proposeBulk(raw json.RawMessage) outcome {
	fail := func(code, message string) outcome {
		o := refused(code, message)
		o.operation, o.mode = "tool:"+toolProposeBulk, "tool"
		return o
	}
	var args bulkArgs
	if err := agentloop.ToolArgs(raw, &args); err != nil || args.Selector.OperationID == "" || args.Operation.OperationID == "" {
		return fail("invalid_arguments", "selector.operationId and operation.operationId are required")
	}
	if len(args.Bind) == 0 {
		return fail("invalid_arguments", "bind is required: it names which field of each item feeds which parameter of the operation, such as {\"id\": \"id\"}")
	}
	if t.proposed {
		return fail("one_change_at_a_time", "one change at a time: wait for the person to decide the change already shown, then propose the next")
	}
	ops := t.s.bulkOps()
	if ops == nil {
		return fail("operation_unavailable", "bulk changes are not available")
	}

	selInfo, ok, err := t.ops.MayUse(t.ctx, args.Selector.OperationID)
	if err != nil {
		return outcome{content: agentloop.Wrap(map[string]string{"error": "call_failed"}), status: "error", operation: "tool:" + toolProposeBulk, mode: "tool"}
	}
	if !ok || selInfo.Mode != operations.ModeAuto {
		return fail("selector_unavailable", "the selector must be an operation that reads data and that you may use, found with search_operations")
	}
	if selInfo.Method != "GET" && selInfo.Method != "POST" {
		return fail("selector_unavailable", "the selector must be a GET list, or a POST that only reads")
	}
	opInfo, ok, _ := t.ops.MayUse(t.ctx, args.Operation.OperationID)
	switch {
	case !ok:
		return fail("operation_unavailable", "that operation does not exist or you may not use it; search again with search_operations")
	case opInfo.Mode == operations.ModeAuto:
		return fail("not_a_change", "that operation only reads; use call_operation for it, and propose_bulk only for a change")
	case opInfo.Mode != operations.ModeConfirm:
		return fail("operation_unavailable", "that operation does not exist or you may not use it; search again with search_operations")
	}

	// Where each parameter of the operation goes.
	locations := t.ops.Locations(args.Operation.OperationID)
	tpl := bulkTemplate{
		OperationID: args.Operation.OperationID, PathParams: args.Operation.PathParams, Query: args.Operation.Query,
		Bind: args.Bind, BindIn: map[string]string{},
	}
	if len(bytes.TrimSpace(args.Operation.Body)) > 0 && string(bytes.TrimSpace(args.Operation.Body)) != "null" {
		body, isObj := decodeObject(args.Operation.Body)
		if !isObj {
			return fail("invalid_arguments", "operation.body must be a JSON object")
		}
		tpl.Body = body
	}
	for param := range args.Bind {
		switch locations[param] {
		case "path":
			tpl.BindIn[param] = "path"
			delete(tpl.PathParams, param)
		case "query", "header":
			return fail("bind_not_supported", fmt.Sprintf("%q is a %s parameter; a bulk change binds an item's value to a path parameter or a field of the body", param, locations[param]))
		default:
			tpl.BindIn[param] = "body"
			delete(tpl.Body, param)
		}
	}
	for name := range tpl.Body {
		tpl.Fields = append(tpl.Fields, name)
	}
	sort.Strings(tpl.Fields)
	if len(tpl.Fields) > maxFields {
		tpl.Fields = tpl.Fields[:maxFields]
	}

	var logged []loggedCall
	sel := bulkSelector{OperationID: args.Selector.OperationID, PathParams: args.Selector.PathParams, Query: args.Selector.Query, Body: args.Selector.Body}
	found, failure := resolveSelector(t.ctx, ops, selInfo, sel, &logged)
	if failure != nil {
		o := failure.outcome()
		o.operation, o.mode, o.extra = "tool:"+toolProposeBulk, "tool", logged
		return o
	}
	if len(found) == 0 {
		o := fail("no_items", "the selector finds no items; tell the person nothing matches, and check the filters")
		o.extra = logged
		return o
	}
	runnable, excluded := tpl.bind(found)
	if len(runnable) == 0 {
		o := fail("binding_not_found", fmt.Sprintf("none of the %d items carries a value for what bind names (%s); the items have these fields: %s", len(found), bindList(args.Bind), strings.Join(fieldNames(found[0]), ", ")))
		o.extra = logged
		return o
	}

	// The change must be valid for the first item as bound, as a single change
	// is checked before its card is made.
	if err := ops.Validate(t.ctx, tpl.request(runnable[0].Bound)); err != nil {
		var oe *operations.Error
		o := refused("invalid_request", "the request could not be checked")
		if errors.As(err, &oe) {
			o = refused(oe.Code, oe.Message)
		}
		o.operation, o.mode, o.extra = "tool:"+toolProposeBulk, "tool", logged
		return o
	}

	// The preview: the first items with the value each has now and the one it
	// would have, read as the person.
	var sample []gen.AssistantBulkSample
	for i := 0; i < len(runnable) && i < bulkSampleSize; i++ {
		req := tpl.request(runnable[i].Bound)
		prior, calls := t.readBefore(opInfo, req)
		logged = append(logged, calls...)
		row := gen.AssistantBulkSample{Fields: []gen.AssistantPendingField{}}
		var current map[string]any
		if prior != nil {
			current = recordFields(prior.object, tpl.Fields)
		}
		row.Fields = cardFields(tpl.Fields, tpl.Body, current)
		label := runnable[i].Label
		if label == "" && current != nil {
			label = labelOf(current)
		}
		if label != "" {
			row.Label = &label
		}
		if route := routeFor(opInfo.Path, req.PathParams); route != "" {
			row.Route = &route
		}
		sample = append(sample, row)
	}

	reasons := map[string]int32{}
	for _, e := range excluded {
		reasons[e.reason]++
	}
	ex := gen.AssistantBulkExcluded{Count: int32(len(excluded)), Reasons: []gen.AssistantBulkReason{}}
	for code, n := range reasons {
		ex.Reasons = append(ex.Reasons, gen.AssistantBulkReason{Code: code, Count: n})
	}
	sort.Slice(ex.Reasons, func(i, j int) bool { return ex.Reasons[i].Code < ex.Reasons[j].Code })

	doc := previewDoc{
		Summary: opInfo.Summary, Explain: clip(strings.TrimSpace(args.Explain), maxExplainChars), Method: opInfo.Method,
		Fields: cardFields(tpl.Fields, tpl.Body, nil), Bulk: &bulkPreview{Sample: sample, Excluded: ex},
	}
	t.proposed = true
	return outcome{
		operation: "tool:" + toolProposeBulk, mode: "tool", detail: opInfo.Summary, extra: logged, status: "ok",
		pending: &pendingDraft{
			request: storedRequest{OperationID: tpl.OperationID, Fields: append([]string{}, tpl.Fields...)},
			preview: doc,
			bulk:    &bulkDraft{selector: sel, template: tpl, items: runnable, excluded: excluded},
		},
		content: agentloop.Wrap(map[string]any{
			"status":   stateAwaiting,
			"summary":  opInfo.Summary,
			"items":    len(runnable),
			"excluded": len(excluded),
			"note":     "Nothing has been changed. The plan is shown to the person on a card with the count and a sample, and starts only if they press Start; the first 5 items run for real, then they decide whether to go on. Say in one or two sentences what you proposed and how many items it covers, and that it waits for them, then stop. Do not call another operation that changes data, and never say anything has been changed.",
		}),
	}
}

func bindList(bind map[string]string) string {
	parts := make([]string, 0, len(bind))
	for param, field := range bind {
		parts = append(parts, param+" <- "+field)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}
