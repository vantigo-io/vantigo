package module

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// Compose gives the operations dispatcher to the module named assistant, and
// to no other, and binds it to the router it composed (module boundaries
// rule 15).

const assistantContract = `
openapi: 3.0.3
info: { title: Assistant, version: "1" }
paths:
  /api/v1/assistant/ping:
    get:
      operationId: getAssistantPing
      summary: Ping the assistant
      x-vantigo-access: permission:assistant:use
      responses:
        "204": { description: ok }
`

const widgetContract = `
openapi: 3.0.3
info: { title: Widgets, version: "1" }
paths:
  /api/v1/widgets/{id}:
    get:
      operationId: getWidget
      summary: Get a widget
      x-vantigo-access: permission:widgets:view
      parameters:
        - { in: path, name: id, required: true, schema: { type: string } }
      responses:
        "200":
          description: ok
          content: { application/json: { schema: { type: object } } }
`

var (
	widgetsView = contracts.Permission{Key: "widgets:view", Display: "View widgets", Description: "See widgets.", Category: "Widgets"}
	assistUse   = contracts.Permission{Key: "assistant:use", Display: "Use", Description: "Use.", Category: "Assistant"}
)

func TestCompose_OnlyTheAssistantModuleReceivesTheOperationsDispatcher(t *testing.T) {
	given := map[string]*operations.Dispatcher{}
	mount := func(name string) func(Deps) (http.Handler, error) {
		return func(d Deps) (http.Handler, error) {
			given[name] = d.Operations
			return staticHandler(name)(d)
		}
	}
	loads := fakeLoad(map[string]string{"assistant": assistantContract, "widgets": widgetContract})

	// A preset on the Deps Compose is given is not passed on either.
	preset := operations.New()
	_, err := compose(Deps{Access: &fakeAccess{}, Operations: preset}, loads,
		Module{Name: "widgets", Permissions: []contracts.Permission{widgetsView}, Mount: mount("widgets")},
		Module{Name: "assistant", Permissions: []contracts.Permission{assistUse}, Mount: mount("assistant")},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if given["widgets"] != nil {
		t.Error("a module other than the assistant was handed the operations dispatcher")
	}
	if given["assistant"] == nil {
		t.Fatal("the assistant module was not handed the operations dispatcher")
	}
	if given["assistant"] == preset {
		t.Error("the assistant was handed the preset dispatcher, not the one Compose binds")
	}
}

func TestCompose_NoAssistantNoDispatcher(t *testing.T) {
	got := operations.New()
	_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"widgets": widgetContract}),
		Module{Name: "widgets", Permissions: []contracts.Permission{widgetsView}, Mount: func(d Deps) (http.Handler, error) {
			got = d.Operations
			return staticHandler("widgets")(d)
		}},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if got != nil {
		t.Error("a composition with no assistant module still built a dispatcher")
	}
}

// The dispatcher is bound to the composed router and the merged contract: it
// knows every enabled module's operations (the assistant's own never), and a
// call goes through the router's access check carrying the grant.
func TestCompose_TheDispatcherIsBoundToTheComposedRouter(t *testing.T) {
	var ops *operations.Dispatcher
	user, grantSeen := uuid.New(), uuid.Nil
	access := &fakeAccess{check: func(r *http.Request, rule contracts.Rule) (contracts.Principal, error) {
		id, ok := contracts.AssistantGrantFrom(r.Context())
		if !ok {
			return contracts.Principal{}, contracts.ErrUnauthenticated
		}
		grantSeen = id
		return contracts.Principal{UserID: user, Via: &contracts.Via{Kind: contracts.ViaAssistant}}, nil
	}}
	// widgets mounts a router of its own, as a real module does.
	widgets := func(d Deps) (http.Handler, error) {
		router := NewRouter(RouterOptions{Doc: d.Doc, Access: d.Access, Catalog: d.Catalog})
		router.HandleFunc("GET /api/v1/widgets/{id}", func(w http.ResponseWriter, r *http.Request) {
			p, _ := contracts.PrincipalFrom(r.Context())
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"` + r.PathValue("id") + `","via":"` + p.Via.Kind + `"}`))
		})
		return router, router.Err()
	}
	_, err := compose(Deps{Access: access}, fakeLoad(map[string]string{"assistant": assistantContract, "widgets": widgetContract}),
		Module{Name: "widgets", Permissions: []contracts.Permission{widgetsView}, Mount: widgets},
		Module{Name: "assistant", Permissions: []contracts.Permission{assistUse}, Mount: func(d Deps) (http.Handler, error) {
			ops = d.Operations
			return staticHandler("assistant")(d)
		}},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	byID := map[string]operations.Mode{}
	for _, info := range ops.All() {
		byID[info.ID] = info.Mode
	}
	if byID["getWidget"] != operations.ModeAuto || byID["getAssistantPing"] != operations.ModeNever || len(byID) != 2 {
		t.Errorf("operations = %v, want getWidget auto and the assistant's own API never", byID)
	}

	grant := uuid.New()
	ctx := contracts.WithAssistantGrant(contracts.AsUser(context.Background(), user), grant)
	res, err := ops.Call(ctx, operations.CallRequest{OperationID: "getWidget", PathParams: map[string]any{"id": "w1"}})
	if err != nil || res.Status != http.StatusOK || string(res.Body) != `{"id":"w1","via":"assistant"}` {
		t.Fatalf("Call = %+v, %v, want the widget as the grant's principal", res, err)
	}
	if grantSeen != grant {
		t.Errorf("Access.Check saw grant %v, want %v", grantSeen, grant)
	}
	if _, err := ops.Call(ctx, operations.CallRequest{OperationID: "getAssistantPing"}); !errors.Is(err, operations.ErrNever) {
		t.Errorf("the assistant's own API: %v, want ErrNever", err)
	}
}
