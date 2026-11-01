// Package assistant is the Assistant module (Norwegian Assistent): every
// person's assistant, which answers "how do I…" from the documentation, reads
// the data the person may read and, from a later delivery, does what the
// person may do (the Assistant design,
// docs/superpowers/specs/2026-11-01-assistant-design.md). It serves
// openapi/assistant.yaml under /api/v1/assistant/.
//
// It is the one module that receives the platform's Operations dispatcher
// (module boundaries rule 18): its tools call the merged contract's operations
// in-process, through the router, as the person, under an assistant grant that
// identity issues for each run. It has no permissions of its own: whether a
// person has the assistant is the Owner's mode and the person's switch (S8),
// and what the assistant may use is the Owner's permission levels (S16), which
// this module applies by installing the dispatcher's gate at mount.
//
// Nothing here imports another module — depguard enforces it, tests included —
// and no SQL of this module crosses a schema. A user is identity's, an opaque
// id; the module learns that one was erased by asking contracts.UserDirectory
// (erasure.go).
package assistant

import (
	"errors"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/assistant/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// aiTaskChat is the AI task key an Owner chooses the assistant's model for
// (Workspace admin › AI models).
const aiTaskChat = "assistant.chat"

// aiTasks are the AI tasks this module performs.
var aiTasks = []contracts.AITask{
	{
		Key:         aiTaskChat,
		Module:      "assistant",
		Display:     "Assistant chat",
		Description: "Answers a person's questions in the Assistant, in a conversation. The model is sent the person's messages, the documentation sections the assistant finds, and what the operations the person may call answer when the assistant reads data for them; nothing is sent until the person writes a message. It needs a model that supports tool calls.",
	},
}

// limits maps each rate-limited operationId to its policy: a person may start
// 30 runs a minute (S14), and a run is itself capped at 12 tool calls.
var limits = map[string]ratelimit.Policy{
	"sendAssistantMessage": {Name: "assistant-chat", Limit: 30, Window: 60e9},
}

// Module is the assistant as a platform module: its contract mounted under
// /api/v1/assistant/, its AI task, and its background workers (retention, stale
// runs and the erasure sweep). It has no permissions of its own.
func Module() module.Module {
	return module.Module{
		Name:    "assistant",
		AITasks: aiTasks,
		Mount:   mount,
		Workers: workers,
	}
}

// mount registers every contract operation on the platform router and installs
// the gate that applies the Owner's permission levels to the dispatcher.
func mount(d module.Deps) (http.Handler, error) {
	if d.Operations == nil {
		return nil, errors.New("assistant: no operations dispatcher; Compose gives it to the module named assistant only")
	}
	s := newServer(d)
	d.Operations.SetGate(s.gate)
	router := module.NewRouter(module.RouterOptions{
		Doc:     d.Doc,
		Access:  d.Access,
		Limiter: d.Limiter,
		Limits:  limits,
		Catalog: d.Catalog,
	})
	strict := gen.NewStrictHandlerWithOptions(s, nil, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  module.DecodeError(apicommon.WriteDecodeError),
		ResponseErrorHandlerFunc: module.ResponseError(),
	})
	handler := gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{
		BaseRouter:       router,
		ErrorHandlerFunc: module.DecodeError(apicommon.WriteDecodeError),
	})
	if err := router.Err(); err != nil {
		return nil, err
	}
	return handler, nil
}
