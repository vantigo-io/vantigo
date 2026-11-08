package integration_test

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents"
	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/knowledge"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/operations"
	"github.com/vantigo-io/vantigo/server/internal/products"
)

// The locks on what an agent may do (virtual employees design V9) against the
// real contracts of the modules the front desk works with.

var agentsRecorder = mergedRecorder("agents", modCustomers, modInvoices, modProducts, "communications", "knowledge")

// TestAgentNever_CoversEveryFamilyInTheCatalog fails when a permission that
// matches one of the families an agent may never hold is added and not listed
// in agents' agentNever, and when the list names a key no module declares.
func TestAgentNever_CoversEveryFamilyInTheCatalog(t *testing.T) {
	t.Parallel()
	catalog := map[string]contracts.Permission{}
	for _, m := range append([]module.Module{identityPermissions()}, everyModule()...) {
		for _, p := range m.Permissions {
			catalog[p.Key] = p
		}
	}
	never, families := agents.NeverPermissions()
	for _, pattern := range families {
		re := regexp.MustCompile(pattern)
		for key := range catalog {
			if re.MatchString(key) && !slices.Contains(never, key) {
				t.Errorf("%s matches %s and is not in agentNever: classify it (lock it off for agents, or narrow the family)", key, pattern)
			}
		}
	}
	for _, key := range never {
		if _, ok := catalog[key]; !ok {
			t.Errorf("agentNever lists %s, which no module declares", key)
		}
	}
}

// notDelegableWaivers are permissions that cannot be delegated and that an
// agent may nevertheless be given a level for, each with why. A permission that
// only the Owner may hand on is Owner-tier, and an agent must not hold it
// unless it is listed here on purpose.
var notDelegableWaivers = map[string]string{}

// TestAgentNever_EveryNonDelegablePermissionIsLockedOrWaived holds the catalog
// to agentNever: a permission with Delegable false is Owner-tier, so no level
// may raise it for an agent unless this list says why it may.
func TestAgentNever_EveryNonDelegablePermissionIsLockedOrWaived(t *testing.T) {
	t.Parallel()
	never, _ := agents.NeverPermissions()
	for _, m := range append([]module.Module{identityPermissions()}, everyModule()...) {
		for _, p := range m.Permissions {
			if p.Delegable || slices.Contains(never, p.Key) {
				continue
			}
			if _, waived := notDelegableWaivers[p.Key]; !waived {
				t.Errorf("%s is not delegable and is neither in agentNever nor waived", p.Key)
			}
		}
	}
	for key := range notDelegableWaivers {
		if slices.Contains(never, key) {
			t.Errorf("%s is waived and also in agentNever", key)
		}
	}
}

type agentsInstall struct {
	h     *modtest.Harness
	owner *modtest.Client
}

func newAgentsInstall(t *testing.T) *agentsInstall {
	t.Helper()
	h := modtest.New(t,
		modtest.WithRecorder(agentsRecorder),
		modtest.WithModule(moduleNamed(t, modCustomers)),
		modtest.WithModule(moduleNamed(t, modInvoices)),
		modtest.WithModule(products.Module()),
		modtest.WithModule(communications.Module()),
		modtest.WithModule(knowledge.Module()),
		modtest.WithModule(agents.Module()),
		modtest.WithEnv("AI_PROVIDER", "openai-compatible"),
		modtest.WithEnv("AI_BASE_URL", "http://ollama:11434/v1"),
		modtest.WithEnv("AI_MODELS", "test-model"),
		modtest.WithEnv("OWNERS_REQUIRE_MFA", "0"),
	)
	o, id := h.SignInUser(t)
	h.Exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, id, ownerRoleID)
	return &agentsInstall{h: h, owner: o}
}

// agent makes a front desk agent through the API and answers its ids.
func (a *agentsInstall) agent(t *testing.T) (agentID, userID uuid.UUID) {
	t.Helper()
	r := a.owner.Do(http.MethodPut, "/api/v1/agents/settings", map[string]any{"companyName": "Hansen Rør", "retentionMonths": 6}, modtest.Header("If-Match", "1"))
	if r.Status != http.StatusOK {
		t.Fatalf("set the company's name: %d %s", r.Status, r.Body)
	}
	var ag struct {
		ID     uuid.UUID `json:"id"`
		UserID uuid.UUID `json:"userId"`
	}
	okJSON(t, a.owner, http.MethodPost, "/api/v1/agents/agents", map[string]any{
		"name": "Hanna", "style": "", "languages": "customer", "disclosureWording": "assistant", "model": "",
	}, &ag)
	okJSON(t, a.owner, http.MethodPost, "/api/v1/agents/agents/"+ag.ID.String()+"/roles", map[string]any{"template": "front_desk", "name": "Front desk", "instructions": ""}, nil)
	return ag.ID, ag.UserID
}

// level sets the agent's level for a permission as the Owner.
func (a *agentsInstall) level(t *testing.T, agentID uuid.UUID, key, level string) {
	t.Helper()
	okJSON(t, a.owner, http.MethodPut, fmt.Sprintf("/api/v1/agents/agents/%s/permissions/%s", agentID, key), map[string]any{"level": level}, nil)
}

// roles gives the agent's user an Identity role holding keys, as the Owner does
// on the roles screen.
func (a *agentsInstall) roles(t *testing.T, userID uuid.UUID, keys ...string) {
	t.Helper()
	role := uuid.New()
	a.h.Exec(t, `INSERT INTO identity.roles (id, name, normalized_name, display_name, version, created_at, updated_at)
	             VALUES ($1, $2, upper($2), $2, $3, now(), now())`, role, "agent-"+role.String(), uuid.New())
	for _, key := range keys {
		a.h.Exec(t, `INSERT INTO identity.role_permissions (role_id, permission_key) VALUES ($1, $2)`, role, key)
	}
	a.h.Exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, userID, role)
}

// needs is what the agent's role needs of the modules this installation
// composes, from the setup the API answers.
func (a *agentsInstall) needs(t *testing.T, agentID uuid.UUID) map[string]string {
	t.Helper()
	var out struct {
		Items []struct {
			PermissionKey string `json:"permissionKey"`
			Needed        string `json:"needed"`
		} `json:"items"`
	}
	okJSON(t, a.owner, http.MethodGet, "/api/v1/agents/agents/"+agentID.String()+"/setup", nil, &out)
	needs := map[string]string{}
	for _, n := range out.Items {
		needs[n.PermissionKey] = n.Needed
	}
	return needs
}

func TestAgentGate_AllowlistLevelsAndLocks(t *testing.T) {
	t.Parallel()
	a := newAgentsInstall(t)
	agentID, userID := a.agent(t)
	ops := a.h.Operations(t)
	ctx := a.h.AgentGrantContext(t, userID, agentID, nil)
	effective := func(id string) operations.Mode {
		t.Helper()
		// A gate of its own each time: its cache is by the agent's version and
		// rechecked once a second, and the test changes levels in between.
		ops.SetGate(contracts.GrantAgent, agents.GateFor(a.h.Deps()))
		mode, ok := ops.Effective(ctx, id)
		if !ok {
			t.Fatalf("%s is not in the dispatcher", id)
		}
		return mode
	}

	// Every permission of an agent is off until the Owner raises it.
	for _, id := range []string{"getCommunicationsConversations", "postCommunicationsConversationsByIdReply", "getInvoices", "searchKnowledge", "getProducts"} {
		if mode := effective(id); mode != operations.ModeNever {
			t.Errorf("%s with no level = %q, want never", id, mode)
		}
	}

	needs := a.needs(t, agentID)
	var keys []string
	for key, level := range needs {
		a.level(t, agentID, key, level)
		keys = append(keys, key)
	}
	a.roles(t, userID, keys...)

	want := map[string]operations.Mode{
		"getCommunicationsConversations":                   operations.ModeAuto,
		"getCommunicationsConversationsById":               operations.ModeAuto,
		"getCommunicationsTags":                            operations.ModeAuto,
		"postCommunicationsConversationsByIdReply":         operations.ModeConfirm,
		"postCommunicationsConversationsByIdNotes":         operations.ModeConfirm,
		"patchCommunicationsConversationsById":             operations.ModeConfirm,
		"putCommunicationsConversationsByIdTagsByTagId":    operations.ModeConfirm,
		"deleteCommunicationsConversationsByIdTagsByTagId": operations.ModeConfirm,
		"getCustomer":              operations.ModeAuto,
		"getCustomersByIdContacts": operations.ModeAuto,
		"getInvoices":              operations.ModeAuto,
		"getInvoicesById":          operations.ModeAuto,
		"getProducts":              operations.ModeAuto,
		"getProduct":               operations.ModeAuto,
		"searchKnowledge":          operations.ModeAuto,
		"getKnowledgeArticleText":  operations.ModeAuto,
	}
	for id, mode := range want {
		if got := effective(id); got != mode {
			t.Errorf("%s with the levels raised = %q, want %q", id, got, mode)
		}
	}

	// What is not on the allowlist is never, whatever the levels: the inbox's
	// counts need only a permission the agent has been given.
	for _, id := range []string{"getCommunicationsInboxCounts", "getCommunicationsStatsSummary", "postCommunicationsConversations", "getProductsCategories", "listKnowledgeTree"} {
		if mode := effective(id); mode != operations.ModeNever {
			t.Errorf("%s is off the allowlist and = %q, want never", id, mode)
		}
	}
	// What is locked off is never, though the agent holds the permission.
	a.roles(t, userID, "invoices:issue", "invoices:payments", "customers:personal-data")
	for _, id := range []string{"postInvoicesByIdIssue", "postInvoicesByIdCredit", "postInvoicesByIdPayments", "getInvoicesSettingsPayments"} {
		if mode := effective(id); mode != operations.ModeNever {
			t.Errorf("%s = %q, want never", id, mode)
		}
	}

	// Read turns a change into never; off turns a read into never.
	a.level(t, agentID, "communications:conversations-reply", "read")
	if mode := effective("postCommunicationsConversationsByIdReply"); mode != operations.ModeNever {
		t.Errorf("reply with the level read = %q, want never", mode)
	}
	if mode := effective("getCommunicationsConversations"); mode != operations.ModeAuto {
		t.Errorf("a read is unaffected by the reply level: %q", mode)
	}
	a.level(t, agentID, "invoices:access", "off")
	if mode := effective("getInvoices"); mode != operations.ModeNever {
		t.Errorf("getInvoices with the level off = %q, want never", mode)
	}

	// A paused agent still has its levels; a deleted one has none.
	okJSON(t, a.owner, http.MethodPost, "/api/v1/agents/agents/"+agentID.String()+"/pause", nil, nil)
	if mode := effective("getCommunicationsConversations"); mode != operations.ModeAuto {
		t.Errorf("a paused agent = %q, want its levels kept", mode)
	}
	if r := a.owner.Do(http.MethodDelete, "/api/v1/agents/agents/"+agentID.String(), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if mode := effective("getCommunicationsConversations"); mode != operations.ModeNever {
		t.Errorf("a deleted agent = %q, want never", mode)
	}
}

func TestAgentGate_ACallIsTiedToTheRunsCustomer(t *testing.T) {
	t.Parallel()
	a := newAgentsInstall(t)
	agentID, userID := a.agent(t)
	var own, other struct {
		Id int32 `json:"id"`
	}
	okJSON(t, a.owner, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Acme AS"}, &own)
	okJSON(t, a.owner, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Beta AS"}, &other)

	needs := a.needs(t, agentID)
	var keys []string
	for key, level := range needs {
		a.level(t, agentID, key, level)
		keys = append(keys, key)
	}
	a.roles(t, userID, keys...)
	ops := a.h.Operations(t)
	ops.SetGate(contracts.GrantAgent, agents.GateFor(a.h.Deps()))

	ctx := a.h.AgentGrantContext(t, userID, agentID, &own.Id)
	res, err := ops.Call(ctx, operations.CallRequest{OperationID: "getCustomer"})
	if err != nil || res.Status != http.StatusOK {
		t.Fatalf("getCustomer for the run's customer: %v %d %s", err, res.Status, res.Body)
	}
	if _, err := ops.Call(ctx, operations.CallRequest{OperationID: "getCustomer", PathParams: map[string]any{"id": other.Id}}); !errors.Is(err, operations.ErrCustomerScope) {
		t.Errorf("getCustomer for another customer = %v, want customer_scope", err)
	}
	if _, err := ops.Call(ctx, operations.CallRequest{OperationID: "getInvoices", Query: map[string]any{"customerId": other.Id}}); !errors.Is(err, operations.ErrCustomerScope) {
		t.Errorf("getInvoices for another customer = %v, want customer_scope", err)
	}
	none := a.h.AgentGrantContext(t, userID, agentID, nil)
	if _, err := ops.Call(none, operations.CallRequest{OperationID: "getCustomer", PathParams: map[string]any{"id": own.Id}}); !errors.Is(err, operations.ErrCustomerScope) {
		t.Errorf("getCustomer in a run with no customer = %v, want customer_scope", err)
	}
	// The gate's refusal is checked before anything is called: an operation off
	// the allowlist is never, not a 403 from the router.
	if _, err := ops.Call(ctx, operations.CallRequest{OperationID: "getCommunicationsInboxCounts"}); err == nil {
		t.Errorf("an operation off the allowlist was called")
	}
}
