package agents

import (
	"context"
	"slices"

	"github.com/vantigo-io/vantigo/server/internal/agents/gen"
)

// templateFrontDesk is the one role template of this delivery (V12): the front
// desk, who answers the conversations that come in on its channels.
const templateFrontDesk = "front_desk"

// The levels of a permission (V9). An agent's every permission is off until the
// Owner raises it.
const (
	levelOff    = "off"
	levelRead   = "read"
	levelChange = "change"
)

// allowedOperation is one operation a template lets its role's agent call,
// whatever its levels (V9b). CustomerData says it returns a customer's data, so
// the contract must declare its x-vantigo-customer-scope and the dispatcher
// then ties every call to the run's customer. OwnConversation says its path
// names a conversation: the run's tools must pin it to the run's own
// conversation, because the operation does not itself tie it to the customer.
type allowedOperation struct {
	ID              string
	CustomerData    bool
	OwnConversation bool
}

// need is a level a template's role needs of a permission.
type need struct {
	Permission string
	Level      string
}

// hardRule is a rule of V18 the pipeline applies after the model has written.
// Locked rules hold in every mode, Auto too; the others hold in Review and Auto
// above X %.
type hardRule struct {
	Code   string
	Locked bool
}

// template is a kind of role, in code: what its agent may call, the levels it
// needs, the instructions it starts from and the rules that hold whatever the
// instructions say.
type template struct {
	Key                 string
	DefaultName         string
	DefaultInstructions string
	Allowlist           []allowedOperation
	Needs               []need
	HardRules           []hardRule
}

// frontDesk answers customers on the channels it is bound to. Its allowlist is
// short (V9b): the conversation's own reads and writes, the customer's orders,
// invoices and contacts, Products, and Knowledge. Shipments are not on it:
// Orders' shipments name no customer, so they cannot be tied to one.
//
// patchCommunicationsConversationsById carries customerId and assignedUserId
// besides the status, so a run's tools must set only the status and the
// hand-off; the dispatcher's scope cannot tell a write to another customer.
var frontDesk = template{
	Key:         templateFrontDesk,
	DefaultName: "Front desk",
	DefaultInstructions: `You answer customers who write to the company. Be polite, brief and concrete.

Answer from what you have read in this conversation's customer record, the product catalog and the articles in Knowledge, and say which. If you do not know, or the customer asks for something you may not decide, hand the conversation to a colleague with a note that says what the customer wants.

Never promise a date, a discount, a refund or a price you have not read. Answer truthfully if you are asked whether you are a person.`,
	Allowlist: []allowedOperation{
		{ID: "getCommunicationsConversations", CustomerData: true},
		{ID: "getCommunicationsConversationsById", CustomerData: true, OwnConversation: true},
		{ID: "postCommunicationsConversationsByIdReply", OwnConversation: true},
		{ID: "postCommunicationsConversationsByIdNotes", OwnConversation: true},
		{ID: "patchCommunicationsConversationsById", OwnConversation: true},
		{ID: "putCommunicationsConversationsByIdTagsByTagId", OwnConversation: true},
		{ID: "deleteCommunicationsConversationsByIdTagsByTagId", OwnConversation: true},
		{ID: "getCommunicationsTags"},
		{ID: "getCustomer", CustomerData: true},
		{ID: "getCustomersByIdContacts", CustomerData: true},
		{ID: "listSalesOrders", CustomerData: true},
		{ID: "getSalesOrder", CustomerData: true},
		{ID: "getInvoices", CustomerData: true},
		{ID: "getInvoicesById", CustomerData: true},
		{ID: "getProducts"},
		{ID: "getProduct"},
		{ID: "getProductsByIdVariants"},
		{ID: "getProductsByIdVariantsByVariantIdPrices"},
		{ID: "searchKnowledge"},
		{ID: "getKnowledgeArticleText"},
	},
	// The assistant's gate turns a confirm-mode operation into never when any
	// permission it names is only read, so the permissions the writes name are
	// change, and so is conversations-view, which they all name beside theirs.
	Needs: []need{
		{"communications:conversations-view", levelChange},
		{"communications:conversations-reply", levelChange},
		{"communications:conversations-manage", levelChange},
		{"customers:view", levelRead},
		{"customers:contacts-view", levelRead},
		{"customers:associations-view", levelRead},
		{"orders:access", levelRead},
		{"invoices:access", levelRead},
		{"products:products-view", levelRead},
		{"products:variants-view", levelRead},
		{"products:pricing-view", levelRead},
		{"products:categories-view", levelRead},
		{"products:tax-categories-view", levelRead},
		{"knowledge:articles", levelRead},
	},
	HardRules: []hardRule{
		{"S1", true}, {"S2", true}, {"S3", false}, {"S4", false}, {"S5", false},
		{"S6", true}, {"S7", true}, {"C1", true}, {"C2", false},
	},
}

// templates are the templates of this build, by key.
var templates = map[string]*template{templateFrontDesk: &frontDesk}

// agentNever are the permissions no level can raise for an agent (V9): Staff's
// health data, the sealed national identity number and bank account, and the
// personal data Customers and Staff reveal or erase; identity's and every
// Owner-tier permission; this module's own; Invoices' issue and payments; and
// every approval in Time, Expenses and Staff. The Assistant's API is not here:
// it names no permission, and the dispatcher refuses it by its path. A new
// permission that matches one of these families must be added (the test in
// internal/integration fails until it is), and a level for a listed key is
// refused 409 level_locked.
var agentNever = []string{
	"agents:activity", "agents:manage", "agents:review", "agents:view",
	"customers:personal-data",
	"expenses:approve",
	"identity:manage",
	"invoices:issue", "invoices:payments",
	"staff:approve", "staff:manage", "staff:personal-data",
	"time:approve",
}

// neverFamilies are the key patterns the contract test holds agentNever to: a
// permission whose key matches one and is not listed is a gap in the lock.
var neverFamilies = []string{
	`^identity:`,
	`^agents:`,
	`:approve$`,
	`:personal-data$`,
	`^invoices:(issue|payments)$`,
	`^staff:manage$`,
}

// isNever says no level can raise key for an agent.
func isNever(key string) bool { return slices.Contains(agentNever, key) }

// NeverPermissions is agentNever and the patterns it must cover, for the test
// in internal/integration that holds them to the composed catalog.
func NeverPermissions() (keys, families []string) {
	return slices.Clone(agentNever), slices.Clone(neverFamilies)
}

// templateView is the template as the contract answers it.
func templateView(t *template) gen.AgentTemplate {
	out := gen.AgentTemplate{
		Key:                 gen.AgentTemplateKey(t.Key),
		DefaultName:         t.DefaultName,
		DefaultInstructions: t.DefaultInstructions,
		Allowlist:           make([]string, 0, len(t.Allowlist)),
		Needs:               make([]gen.AgentTemplateNeed, 0, len(t.Needs)),
		HardRules:           make([]gen.AgentTemplateRule, 0, len(t.HardRules)),
	}
	for _, a := range t.Allowlist {
		out.Allowlist = append(out.Allowlist, a.ID)
	}
	for _, n := range t.Needs {
		out.Needs = append(out.Needs, gen.AgentTemplateNeed{PermissionKey: n.Permission, Level: gen.AgentTemplateNeedLevel(n.Level)})
	}
	for _, r := range t.HardRules {
		out.HardRules = append(out.HardRules, gen.AgentTemplateRule{Code: r.Code, Locked: r.Locked})
	}
	return out
}

// ListAgentTemplates (GET /api/v1/agents/templates)
func (s *server) ListAgentTemplates(_ context.Context, _ gen.ListAgentTemplatesRequestObject) (gen.ListAgentTemplatesResponseObject, error) {
	return gen.ListAgentTemplates200JSONResponse(gen.AgentTemplateList{Templates: []gen.AgentTemplate{templateView(&frontDesk)}}), nil
}
