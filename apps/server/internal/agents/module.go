// Package agents is the Agents module (nb Virtuelle ansatte): virtual employees
// that do part of a person's work — in the first delivery, the front desk, who
// answers the mail and the messages that come in on the channels the Owner
// binds (the virtual employees design,
// docs/superpowers/specs/2026-11-08-virtual-employees-design.md §5). It serves
// openapi/agents.yaml under /api/v1/agents/.
//
// This delivery (A1) holds the agents with their persona, their roles bound to
// channels, the levels the Owner gave their permissions and the gate that
// applies them (V9, V10, V12). An agent is a user of kind agent in identity,
// made through contracts.AgentAccounts; it holds identity roles like a person,
// and what it may do is the narrowest of its roles, its levels and its role's
// allowlist. The trigger, the runs and the review queue come after.
//
// It is the second module that receives the platform's Operations dispatcher
// (module boundaries rule 18): its runs will call the merged contract's
// operations in-process under an agent grant, and mount installs the gate that
// decides, per agent, what they may call.
//
// Nothing here imports another module — depguard enforces it, tests included —
// and no SQL of this module crosses a schema. A user, a channel and a customer
// are other modules', kept as opaque ids.
package agents

import (
	"errors"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/agents/gen"
	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/operations"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// The permission keys. Raising a level, choosing an Auto mode and binding a
// channel need agents:manage and are also the Owner's alone (V12): a person
// with agents:manage but narrow roles of their own could otherwise steer an
// agent with broad ones.
const (
	permView     = "agents:view"
	permManage   = "agents:manage"
	permReview   = "agents:review"
	permActivity = "agents:activity"
)

var permissions = []contracts.Permission{
	{
		Key: permView, Display: "View virtual employees",
		Description: "See the agents, their personas, roles and settings.",
		Category:    "Agents", Sensitive: false, Delegable: true,
	},
	{
		Key: permManage, Display: "Manage virtual employees",
		Description: "Create agents, edit their personas and instructions, and pause or delete them. Raising a level, choosing an Auto mode and binding a channel stay the Owner's.",
		Category:    "Agents", Sensitive: true, Delegable: false,
	},
	{
		Key: permReview, Display: "Review virtual employees' work",
		Description: "Approve or discard what an agent proposes in the review queue.",
		Category:    "Agents", Sensitive: false, Delegable: true,
	},
	{
		Key: permActivity, Display: "Read virtual employees' activity",
		Description: "Read an agent's runs and their transcripts, which hold customers' data.",
		Category:    "Agents", Sensitive: true, Delegable: false,
	},
}

// The AI tasks this module performs. agents.run is the agent at work, whose
// model the Owner chooses per agent (V7); agents.check is the content check of
// V18, which uses the workspace's choice.
const (
	aiTaskRun   = "agents.run"
	aiTaskCheck = "agents.check"
)

var aiTasks = []contracts.AITask{
	{
		Key:         aiTaskRun,
		Module:      "agents",
		Display:     "Agent at work",
		Description: "Runs a virtual employee on a conversation. The model is sent the conversation's messages, the persona and instructions the Owner wrote and what the operations the agent may call answer when it reads data for the conversation's customer. It needs a model that supports tool calls.",
		PerSubject:  true,
	},
	{
		Key:         aiTaskCheck,
		Module:      "agents",
		Display:     "Agent content check",
		Description: "Checks a virtual employee's draft against a fixed rubric before it is sent: whether it claims to be a person and whether it promises what the instructions do not allow. The model is sent the instructions and the draft, never the conversation.",
	},
}

// limits are the module's rate limits; none beyond the platform's.
var limits = map[string]ratelimit.Policy{}

// maxAvatarRequestBytes caps an avatar upload: the picture (identity refuses
// one over 5 MiB) and 64 KiB of multipart framing.
const maxAvatarRequestBytes int64 = 5*1024*1024 + 64*1024

var bodyLimits = map[string]int64{"putAgentAvatar": maxAvatarRequestBytes}

// Module is the Agents module: its contract mounted under /api/v1/agents/, its
// permissions and its two AI tasks.
func Module() module.Module {
	return module.Module{
		Name:        "agents",
		Permissions: permissions,
		AITasks:     aiTasks,
		Mount:       mount,
	}
}

// mount registers every contract operation on the platform router and installs
// the gate that applies an agent's levels and role allowlist to the dispatcher.
func mount(d module.Deps) (http.Handler, error) {
	if d.Operations == nil {
		return nil, errors.New("agents: no operations dispatcher; Compose gives it to the modules named assistant and agents only")
	}
	if d.AgentAccounts == nil || d.AgentGrants == nil {
		return nil, errors.New("agents: no agent accounts or grants; identity provides them")
	}
	s := newServer(d)
	d.Operations.SetGate(contracts.GrantAgent, s.gate)
	router := module.NewRouter(module.RouterOptions{
		Doc:        d.Doc,
		Access:     d.Access,
		Limiter:    d.Limiter,
		Limits:     limits,
		Catalog:    d.Catalog,
		BodyLimits: bodyLimits,
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

// GateFor is the gate mount installs, built over d, for a test that binds a
// dispatcher of its own (modtest.Harness.Operations) and so cannot reach the
// one Compose gave the module. It needs d.Pool only.
func GateFor(d module.Deps) operations.Gate { return newServer(d).gate }
