package operations_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
	"github.com/vantigo-io/vantigo/server/internal/operations"
	"github.com/vantigo-io/vantigo/server/internal/projects"
)

// These tests drive the dispatcher through the real router and the real
// identity: the projects module composed beside identity by module.Compose, a
// migrated database, and a person's assistant grant issued from their session.
// What they prove is the S2 promise: a call through the dispatcher is checked
// exactly as the person's own call is, and the dispatcher adds no path round it.
//
// No exchange here goes through modtest's contract recorder except the ones the
// test makes itself as the person; the dispatcher's calls are in-process.

var recorder = contracttest.NewForModule("projects")

func newHarness(t *testing.T) *modtest.Harness {
	t.Helper()
	return modtest.New(t,
		modtest.WithRecorder(recorder),
		modtest.WithModule(projects.Module()),
	)
}

type person struct {
	client *modtest.Client
	ctx    context.Context
}

// signIn is a person holding projects:access and the permissions named, with
// the context of an assistant run for them.
func signIn(t *testing.T, h *modtest.Harness, ttl time.Duration, permissions ...string) person {
	t.Helper()
	c := h.SignIn(t, append([]string{"projects:access"}, permissions...)...)
	return person{client: c, ctx: h.AssistantContext(t, c, ttl)}
}

// createProject makes an internal project as p, through the interface's own
// endpoint, and answers its id.
func createProject(t *testing.T, p person) int32 {
	t.Helper()
	r := p.client.Do(http.MethodPost, "/api/v1/projects", map[string]any{
		"code": "ASSIST1", "name": "The assistant's project", "billingType": "non-billable",
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("create project: status %d body %s", r.Status, r.Body)
	}
	var created struct {
		ID int32 `json:"id"`
	}
	r.JSON(&created)
	return created.ID
}

func call(t *testing.T, h *modtest.Harness, ctx context.Context, req operations.CallRequest) operations.CallResult {
	t.Helper()
	res, err := h.Operations(t).Call(ctx, req)
	if err != nil {
		t.Fatalf("Call(%s): %v", req.OperationID, err)
	}
	return res
}

func TestCall_ARowLevelRefusalStaysARefusal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner := signIn(t, h, time.Hour, "projects:create")
	outsider := signIn(t, h, time.Hour)
	id := createProject(t, owner)
	get := func(ctx context.Context, id any) operations.CallResult {
		return call(t, h, ctx, operations.CallRequest{OperationID: "getProjectsById", PathParams: map[string]any{"id": id}})
	}

	// The member reads their project, in the interface's own shape.
	own := get(owner.ctx, float64(id))
	var project struct {
		ID   int32  `json:"id"`
		Code string `json:"code"`
	}
	if own.Status != http.StatusOK || json.Unmarshal(own.Body, &project) != nil || project.ID != id || project.Code != "ASSIST1" {
		t.Fatalf("the owner's call = %d %s, want their project", own.Status, own.Body)
	}

	// An outsider holds projects:access, so the router lets them in, and the
	// handler's own row-level rule answers 404 — the same 404 an unknown id
	// gets, as it does in the interface. The dispatcher does not turn it into
	// an error nor around it.
	outside := get(outsider.ctx, float64(id))
	unknown := get(outsider.ctx, float64(999999))
	if outside.Status != http.StatusNotFound || unknown.Status != http.StatusNotFound {
		t.Fatalf("outsider = %d %s, unknown = %d %s, want both 404", outside.Status, outside.Body, unknown.Status, unknown.Body)
	}
	if string(outside.Body) != string(unknown.Body) {
		t.Errorf("an existing project's refusal %q differs from an unknown one's %q: the dispatcher leaks that it exists", outside.Body, unknown.Body)
	}
	// And the interface itself says the same.
	if direct := outsider.client.Do(http.MethodGet, fmt.Sprintf("/api/v1/projects/%d", id), nil); direct.Status != http.StatusNotFound {
		t.Errorf("the interface answers the outsider %d, want 404", direct.Status)
	}
}

func TestCall_APermissionThePersonLacksAnswers403(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// No projects:access at all.
	c := h.SignIn(t)
	ctx := h.AssistantContext(t, c, time.Hour)

	res := call(t, h, ctx, operations.CallRequest{OperationID: "getProjectsById", PathParams: map[string]any{"id": 1}})
	if res.Status != http.StatusForbidden {
		t.Fatalf("status %d body %s, want the router's 403", res.Status, res.Body)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(res.Body, &body) != nil || body.Error.Code != "forbidden" {
		t.Errorf("body = %s, want the access layer's forbidden body, readable by the model", res.Body)
	}

	// Holding projects:access but not projects:create: the write is refused by
	// the router's rule even though the dispatcher let the confirmed call through.
	viewer := signIn(t, h, time.Hour)
	create := call(t, h, viewer.ctx, operations.CallRequest{
		OperationID: "postProjects", Confirmed: true,
		Body: json.RawMessage(`{"code":"NOPE","name":"Not allowed","billingType":"non-billable"}`),
	})
	if create.Status != http.StatusForbidden {
		t.Errorf("create without projects:create: status %d body %s, want 403", create.Status, create.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM projects.projects`); n != 0 {
		t.Errorf("%d projects exist, want none", n)
	}
}

func TestCall_NeverAndUnconfirmedAreRefusedBeforeAnythingIsDispatched(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	p := signIn(t, h, time.Hour, "projects:create")
	ops := h.Operations(t)
	body := json.RawMessage(`{"code":"CONF1","name":"Needs a yes","billingType":"non-billable"}`)

	if _, err := ops.Call(p.ctx, operations.CallRequest{OperationID: "postProjects", Body: body}); !errors.Is(err, operations.ErrConfirmationRequired) {
		t.Fatalf("an unconfirmed create: %v, want ErrConfirmationRequired", err)
	}
	if n := h.Count(t, `SELECT count(*) FROM projects.projects`); n != 0 {
		t.Fatalf("an unconfirmed create reached the handler: %d projects", n)
	}
	res, err := ops.Call(p.ctx, operations.CallRequest{OperationID: "postProjects", Body: body, Confirmed: true})
	if err != nil || res.Status != http.StatusCreated {
		t.Fatalf("a confirmed create: %d %s %v, want 201", res.Status, res.Body, err)
	}
	if n := h.Count(t, `SELECT count(*) FROM projects.projects`); n != 1 {
		t.Errorf("%d projects after the confirmed create, want 1", n)
	}

	// Sign-out is a session operation: never, whatever the person confirms.
	if _, err := ops.Call(p.ctx, operations.CallRequest{OperationID: "postIdentityLogout", Confirmed: true}); !errors.Is(err, operations.ErrNever) {
		t.Errorf("logout: %v, want ErrNever", err)
	}
	if n := h.Count(t, `SELECT count(*) FROM identity.sessions WHERE revoked_at IS NOT NULL`); n != 0 {
		t.Errorf("%d sessions were revoked by a call that should never be dispatched", n)
	}
	// The Owner tier is never, and would be refused 403 by identity anyway.
	if _, err := ops.Call(p.ctx, operations.CallRequest{OperationID: "getIdentityOwnerUsers"}); !errors.Is(err, operations.ErrNever) {
		t.Errorf("owner users: %v, want ErrNever", err)
	}
}

func TestCall_WithoutAGrantIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := h.SignInUser(t, "projects:access")
	_ = c
	ops := h.Operations(t)
	for name, ctx := range map[string]context.Context{
		"a user and no grant": contracts.AsUser(context.Background(), userID),
		"nothing at all":      context.Background(),
	} {
		if _, err := ops.Call(ctx, operations.CallRequest{OperationID: "getProjects"}); !errors.Is(err, operations.ErrNoGrant) {
			t.Errorf("%s: %v, want ErrNoGrant", name, err)
		}
	}
}

func TestCall_AnEndedGrantAnswers401AsAnEndedSessionWould(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	list := func(p person) operations.CallResult {
		return call(t, h, p.ctx, operations.CallRequest{OperationID: "getProjects"})
	}

	t.Run("expiry", func(t *testing.T) {
		p := signIn(t, h, time.Minute)
		if res := list(p); res.Status != http.StatusOK {
			t.Fatalf("before expiry: %d %s", res.Status, res.Body)
		}
		h.Advance(2 * time.Minute)
		if res := list(p); res.Status != http.StatusUnauthorized {
			t.Errorf("after expiry: %d %s, want 401 as a result", res.Status, res.Body)
		}
	})
	t.Run("revocation", func(t *testing.T) {
		p := signIn(t, h, time.Hour)
		h.Exec(t, `UPDATE identity.assistant_grants SET revoked_at = $1`, h.Now())
		if res := list(p); res.Status != http.StatusUnauthorized {
			t.Errorf("after revocation: %d %s, want 401", res.Status, res.Body)
		}
	})
	t.Run("the session signed out", func(t *testing.T) {
		p := signIn(t, h, time.Hour)
		if res := list(p); res.Status != http.StatusOK {
			t.Fatalf("before sign-out: %d %s", res.Status, res.Body)
		}
		h.Exec(t, `UPDATE identity.sessions SET revoked_at = $1`, h.Now())
		if res := list(p); res.Status != http.StatusUnauthorized {
			t.Errorf("after sign-out: %d %s, want 401", res.Status, res.Body)
		}
	})
}

func TestList_HidesWhatThePersonCannotCall(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ops := h.Operations(t)
	member := signIn(t, h, time.Hour, "projects:create")
	nobody := h.SignIn(t)

	ids := func(userCtx context.Context) []string {
		userID, _ := contracts.UserID(userCtx)
		infos, err := ops.List(userCtx, userID)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(infos))
		for i, info := range infos {
			if info.Mode == operations.ModeNever {
				t.Errorf("List includes %s, whose mode is never", info.ID)
			}
			out[i] = info.ID
		}
		return out
	}

	memberIDs := ids(member.ctx)
	for _, want := range []string{"getProjects", "getProjectsById", "postProjects", "putProjectsById"} {
		if !slices.Contains(memberIDs, want) {
			t.Errorf("List omits %s for a member who may call it", want)
		}
	}
	if slices.Contains(memberIDs, "getIdentityOwnerUsers") || slices.Contains(memberIDs, "postIdentityLogout") {
		t.Errorf("List includes an operation that is never: %v", memberIDs)
	}

	nobodyIDs := ids(h.AssistantContext(t, nobody, time.Hour))
	for _, id := range nobodyIDs {
		if strings.Contains(id, "Projects") {
			t.Errorf("List includes %s for a person with no projects permission", id)
		}
	}
	if slices.Contains(ids(h.AssistantContext(t, h.SignIn(t, "projects:access"), time.Hour)), "postProjects") {
		t.Error("List includes postProjects for a person without projects:create")
	}
}

func TestSearchAndDescribe_OverTheRealContract(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ops := h.Operations(t)
	p := signIn(t, h, time.Hour, "projects:create")
	userID, _ := contracts.UserID(p.ctx)

	found, err := ops.Search(p.ctx, userID, "create a project", 5)
	if err != nil || len(found) == 0 || found[0].ID != "postProjects" {
		t.Fatalf("Search(create a project) = %v, %v, want postProjects first", found, err)
	}
	if found[0].Mode != operations.ModeConfirm || found[0].Summary != "Create a project" {
		t.Errorf("result = %+v", found[0])
	}

	raw, err := ops.Describe("postProjects")
	if err != nil || len(raw) > 8<<10 {
		t.Fatalf("Describe: %d bytes, %v", len(raw), err)
	}
	var doc struct {
		Mode        string `json:"mode"`
		RequestBody struct {
			Schema struct {
				Required   []string                  `json:"required"`
				Properties map[string]map[string]any `json:"properties"`
			} `json:"schema"`
		} `json:"requestBody"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Mode != "confirm" || !slices.Contains(doc.RequestBody.Schema.Required, "name") || !strings.Contains(doc.RequestBody.Schema.Properties["billingType"]["description"].(string), "non-billable") {
		t.Errorf("Describe(postProjects) = %s, want the body's required fields and the fields' descriptions", raw)
	}
}
