package agentloop_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agentloop"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// The dispatcher is what the three tools are made for.
var _ agentloop.Operations = (*operations.Dispatcher)(nil)

type fakeOps struct {
	infos []operations.Info
	doc   string
	calls []operations.CallRequest
	res   operations.CallResult
	err   error
}

func (f *fakeOps) Search(context.Context, uuid.UUID, string, int) ([]operations.Info, error) {
	return f.infos, nil
}
func (f *fakeOps) List(context.Context, uuid.UUID) ([]operations.Info, error) { return f.infos, nil }
func (f *fakeOps) Describe(string) (json.RawMessage, error)                   { return json.RawMessage(f.doc), nil }
func (f *fakeOps) Call(_ context.Context, req operations.CallRequest) (operations.CallResult, error) {
	f.calls = append(f.calls, req)
	return f.res, f.err
}

func tool(t *testing.T, d *agentloop.Dispatch, name string) agentloop.Tool {
	t.Helper()
	for _, tl := range d.Tools() {
		if tl.Name == name {
			return tl
		}
	}
	t.Fatalf("no tool %q", name)
	return agentloop.Tool{}
}

func invoke(t *testing.T, tl agentloop.Tool, args string) agentloop.Result {
	t.Helper()
	res, err := tl.Call(t.Context(), json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestDispatch_OffersTheThreeToolsInOrder(t *testing.T) {
	t.Parallel()
	var names []string
	for _, tl := range (&agentloop.Dispatch{Ops: &fakeOps{}}).Tools() {
		names = append(names, tl.Name)
	}
	if strings.Join(names, ",") != "search_operations,describe_operation,call_operation" {
		t.Errorf("tools = %v", names)
	}
}

func TestDispatch_CallRoutesParametersByWhereTheOperationPutsThem(t *testing.T) {
	t.Parallel()
	ops := &fakeOps{
		infos: []operations.Info{{ID: "getThing", Summary: "Read a thing", Mode: operations.ModeAuto}},
		doc:   `{"pathParameters":[{"name":"id"}],"queryParameters":[{"name":"q"}],"headerParameters":[{"name":"If-Match"}]}`,
		res:   operations.CallResult{Status: 200, Body: json.RawMessage(`{"name":"x"}`)},
	}
	d := &agentloop.Dispatch{Ops: ops}
	res := invoke(t, tool(t, d, "call_operation"), `{"id":"getThing","params":{"id":"7","q":"abc","If-Match":"\"3\"","other":1}}`)
	if res.Status != "ok" || res.Operation != "getThing" || res.Mode != "auto" || res.Detail != "Read a thing" || res.HTTPStatus == nil || *res.HTTPStatus != 200 {
		t.Fatalf("result = %+v", res)
	}
	got := ops.calls[0]
	if got.PathParams["id"] != "7" || got.Query["q"] != "abc" || got.Query["other"] != float64(1) || got.Headers["If-Match"] != `"3"` {
		t.Errorf("request = %+v", got)
	}
	if !strings.Contains(res.Content, "not instructions") {
		t.Errorf("the answer must be wrapped as data: %s", res.Content)
	}
}

func TestDispatch_AnOperationThePrincipalMayNotUseIsRefusedLikeOneThatDoesNotExist(t *testing.T) {
	t.Parallel()
	d := &agentloop.Dispatch{Ops: &fakeOps{}}
	res := invoke(t, tool(t, d, "call_operation"), `{"id":"missing"}`)
	if res.Status != "refused" || !strings.Contains(res.Content, "operation_unavailable") {
		t.Errorf("result = %+v", res)
	}
}

func TestDispatch_AChangeGoesToConfirmAndNeverToTheDispatcher(t *testing.T) {
	t.Parallel()
	ops := &fakeOps{infos: []operations.Info{{ID: "patchThing", Summary: "Change a thing", Mode: operations.ModeConfirm}}, doc: `{}`}
	var asked operations.CallRequest
	var explained string
	d := &agentloop.Dispatch{Ops: ops, Confirm: func(_ context.Context, info operations.Info, req operations.CallRequest, explain string) agentloop.Result {
		asked, explained = req, explain
		return agentloop.Result{Content: "{}", Status: "ok", Attach: info.ID}
	}}
	res := invoke(t, tool(t, d, "call_operation"), `{"id":"patchThing","body":{"a":1},"explain":"Sets a"}`)
	if res.Attach != "patchThing" || asked.OperationID != "patchThing" || explained != "Sets a" || len(ops.calls) != 0 {
		t.Errorf("result %+v asked %+v explained %q calls %d, want the change handed to Confirm and nothing called", res, asked, explained, len(ops.calls))
	}

	d.Confirm = nil
	res = invoke(t, tool(t, d, "call_operation"), `{"id":"patchThing"}`)
	if res.Status != "refused" || len(ops.calls) != 0 {
		t.Errorf("without a Confirm a change is refused: %+v", res)
	}
}

func TestDispatch_AnOperationErrorIsTheModelsToReadAndAnUnexpectedOneIsNot(t *testing.T) {
	t.Parallel()
	ops := &fakeOps{infos: []operations.Info{{ID: "getThing", Mode: operations.ModeAuto}}, doc: `{}`}
	d := &agentloop.Dispatch{Ops: ops}
	ops.err = &operations.Error{Code: "bad_request", Message: "fix it"}
	if res := invoke(t, tool(t, d, "call_operation"), `{"id":"getThing"}`); res.Status != "refused" || !strings.Contains(res.Content, "fix it") {
		t.Errorf("result = %+v", res)
	}
	ops.err = errors.New("db down")
	if res := invoke(t, tool(t, d, "call_operation"), `{"id":"getThing"}`); res.Status != "error" || strings.Contains(res.Content, "db down") {
		t.Errorf("result = %+v, want call_failed without the cause", res)
	}
}

func TestDispatch_DescribeTellsTheEffectiveModeAndRemembersWhereParametersGo(t *testing.T) {
	t.Parallel()
	ops := &fakeOps{infos: []operations.Info{{ID: "getThing", Mode: operations.ModeAuto}}, doc: `{"mode":"never","pathParameters":[{"name":"id"}]}`}
	d := &agentloop.Dispatch{Ops: ops}
	res := invoke(t, tool(t, d, "describe_operation"), `{"id":"getThing"}`)
	if res.Status != "ok" || !strings.Contains(res.Content, `"mode":"auto"`) {
		t.Errorf("result = %+v, want the gate's mode", res)
	}
	ops.doc = `{}`
	if got := d.Locations("getThing"); got["id"] != "path" {
		t.Errorf("locations = %v, want the ones read when it was described", got)
	}
}

func TestDispatch_SearchListsWhatIsFound(t *testing.T) {
	t.Parallel()
	ops := &fakeOps{infos: []operations.Info{{ID: "listThings", Method: "GET", Path: "/api/v1/things", Summary: "List", Mode: operations.ModeAuto}}}
	res := invoke(t, tool(t, &agentloop.Dispatch{Ops: ops}, "search_operations"), `{"query":"things"}`)
	if !strings.Contains(res.Content, `"id":"listThings"`) || res.Operation != "tool:search_operations" {
		t.Errorf("result = %+v", res)
	}
	if res := invoke(t, tool(t, &agentloop.Dispatch{Ops: ops}, "search_operations"), `{"query":" "}`); res.Status != "refused" {
		t.Errorf("an empty query must be refused: %+v", res)
	}
}
