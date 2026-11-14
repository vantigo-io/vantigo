package agents

import (
	"context"
	"slices"

	"github.com/vantigo-io/vantigo/server/internal/agents/gen"
)

// The role templates (V12, V30). The front desk answers the conversations that
// come in on its channels; reminder replies is the front desk on the invoice
// mailbox; the sales follow-up writes to the contact of a quote that was sent and
// not accepted; the timesheet reminders tell a person their week is missing
// hours; the project digest tells a project manager how the projects stand.
const (
	templateFrontDesk          = "front_desk"
	templateSalesFollowup      = "sales_followup"
	templateReminderReplies    = "reminder_replies"
	templateTimesheetReminders = "timesheet_reminders"
	templateProjectDigest      = "project_digest"
)

// The module's own tools a template can offer its role (V15, V27, V29).
var (
	conversationTools = []string{toolReply, toolNote, toolTag, toolRemember, toolFollowUp, toolHandOff}
)

// The fields of a role a template uses, so that the role page shows only these.
const (
	fieldChannels   = "channels"
	fieldReplyMode  = "replyMode"
	fieldStartMode  = "startMode"
	fieldChangeMode = "changeMode"
	fieldComplaints = "complaintTags"
	fieldWaitDays   = "waitDays"
	fieldSchedule   = "schedule"
	fieldRecipient  = "recipient"
)

// The subjects a run can be about (V29): the conversation it answers, or what a
// wakeup or an event named.
const (
	subjectQuote      = "quote"
	subjectSchedule   = "schedule"
	subjectPersonWeek = "person_week"
)

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
// Safe says the template judges a change by this operation safe to automate
// (V29): only such a change follows the role's change mode, and every other
// change is reviewed.
type allowedOperation struct {
	ID              string
	CustomerData    bool
	OwnConversation bool
	Safe            bool
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
	// Tools are the module's own tools the role's agent is offered besides the
	// three that reach the contract's operations.
	Tools []string
	// Fields are the role fields the template uses.
	Fields []string
	// Subject is what the role's runs are about when it is not a conversation
	// (subjectQuote, subjectSchedule, subjectPersonWeek); "" for a template that
	// answers the conversations on its channels.
	Subject string
	// Recipient says the template's notify tool writes to the role's chosen
	// person; otherwise it writes to the person the run is about.
	Recipient bool
}

// answersConversations says the template answers the conversations that come in
// on its role's channels (V13).
func (t *template) answersConversations() bool { return t.Subject == "" }

// offers says the template gives its role's agent a tool.
func (t *template) offers(tool string) bool { return slices.Contains(t.Tools, tool) }

// uses says the template uses a role field.
func (t *template) uses(field string) bool { return slices.Contains(t.Fields, field) }

// frontDesk answers customers on the channels it is bound to. Its allowlist is
// short (V9b): the conversation's own reads and writes, the customer's orders,
// invoices, shipments and contacts, Products, and Knowledge. A shipment names
// its order's customer, and the lists of shipments and deliveries filter by it,
// so "where is my parcel?" is read for the run's customer only.
//
// patchCommunicationsConversationsById carries customerId and assignedUserId
// besides the status, so a run's tools set only the status (call_operation) and,
// through hand_off, the assignee; the dispatcher's scope cannot tell a write to
// another customer. The conversation's own detail carries no customer scope: a
// run with no customer (V9b) must still read the conversation it answers, and
// the run's tools pin it to that conversation alone.
var frontDesk = template{
	Key:         templateFrontDesk,
	DefaultName: "Front desk",
	DefaultInstructions: `You answer customers who write to the company. Be polite, brief and concrete.

Answer from what you have read in this conversation's customer record, the product catalog and the articles in Knowledge, and say which. If you do not know, or the customer asks for something you may not decide, hand the conversation to a colleague with a note that says what the customer wants.

Never promise a date, a discount, a refund or a price you have not read. Answer truthfully if you are asked whether you are a person.`,
	Allowlist: []allowedOperation{
		{ID: "getCommunicationsConversations", CustomerData: true},
		{ID: "getCommunicationsConversationsById", OwnConversation: true},
		{ID: "postCommunicationsConversationsByIdReply", OwnConversation: true},
		{ID: "postCommunicationsConversationsByIdNotes", OwnConversation: true},
		{ID: "patchCommunicationsConversationsById", OwnConversation: true, Safe: true},
		{ID: "putCommunicationsConversationsByIdTagsByTagId", OwnConversation: true},
		{ID: "deleteCommunicationsConversationsByIdTagsByTagId", OwnConversation: true},
		{ID: "getCommunicationsTags"},
		{ID: "getCustomer", CustomerData: true},
		{ID: "getCustomersByIdContacts", CustomerData: true},
		{ID: "listSalesOrders", CustomerData: true},
		{ID: "getSalesOrder", CustomerData: true},
		{ID: "listOrderShipments", CustomerData: true},
		{ID: "getShipment", CustomerData: true},
		{ID: "listDeliveries", CustomerData: true},
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
	Tools:  conversationTools,
	Fields: []string{fieldChannels, fieldReplyMode, fieldChangeMode, fieldComplaints},
}

// reminderReplies is the front desk on the invoice mailbox (V30): the same
// allowlist, which holds Invoices' reads, and instructions about reminders.
var reminderReplies = template{
	Key:         templateReminderReplies,
	DefaultName: "Reminder replies",
	DefaultInstructions: `You answer customers who write back about an invoice or a payment reminder. Be polite, brief and concrete.

Read the invoice the customer means (its status, its due date, what is paid) before you answer, and say which invoice you read. Never promise a payment plan, a postponed due date, a waived fee or a credit note: hand the conversation to a colleague with a note that says what the customer asks for. Answer truthfully if you are asked whether you are a person.`,
	Allowlist: frontDesk.Allowlist,
	Needs:     frontDesk.Needs,
	HardRules: frontDesk.HardRules,
	Tools:     conversationTools,
	Fields:    frontDesk.Fields,
}

// salesFollowup writes to the contact of a quote that was sent and is still not
// accepted (V30). It reads the quote and its customer and writes one new
// conversation, on a channel of its own, to a contact of that customer.
var salesFollowup = template{
	Key:         templateSalesFollowup,
	DefaultName: "Sales follow-up",
	DefaultInstructions: `A quote was sent to a customer some days ago. Read the quote. If it is still sent and waiting (not accepted, declined, expired or cancelled), write a short, friendly follow-up to the customer's contact: ask whether they have questions about the quote, and offer to help. Do not offer a discount or a changed price, and do not promise a date.

Find the contact with the customer's contacts and write to their e-mail address. If the quote is no longer waiting, or the customer has no contact with an e-mail address, do nothing more: end your work.`,
	Allowlist: []allowedOperation{
		{ID: "getQuote", CustomerData: true},
		{ID: "getCustomer", CustomerData: true},
		{ID: "getCustomersByIdContacts", CustomerData: true},
		{ID: "getCommunicationsConversations", CustomerData: true},
		{ID: opStart},
		{ID: "searchKnowledge"},
		{ID: "getKnowledgeArticleText"},
	},
	Needs: []need{
		{"orders:access", levelRead},
		{"customers:view", levelRead},
		{"customers:contacts-view", levelRead},
		{"customers:associations-view", levelRead},
		{"communications:conversations-view", levelRead},
		{"communications:conversations-reply", levelChange},
		{"knowledge:articles", levelRead},
	},
	HardRules: []hardRule{{"S1", true}, {"S2", true}, {"S3", false}, {"C1", true}, {"C2", false}},
	Tools:     []string{toolStartConversation, toolRemember, toolFollowUp, toolHandOff},
	Fields:    []string{fieldChannels, fieldStartMode, fieldWaitDays},
	Subject:   subjectQuote,
}

// timesheetReminders tells a person their week is missing hours, through the
// host's notifications (V30). It reads nothing: the event names the person and
// the week, and the notice goes to that person alone.
var timesheetReminders = template{
	Key:                 templateTimesheetReminders,
	DefaultName:         "Timesheet reminders",
	DefaultInstructions: `A colleague has not submitted their timesheet for a week that has ended. Write a short, friendly reminder to them: say which week, say how many hours they have registered, and ask them to register what is missing and submit the week. Do not scold, and do not mention anyone else.`,
	Allowlist:           nil,
	Needs:               nil,
	HardRules:           []hardRule{{"C1", true}},
	Tools:               []string{toolNotify, toolHandOff},
	Fields:              nil,
	Subject:             subjectPersonWeek,
}

// projectDigest writes a weekly note to a project manager on how the projects
// stand (V30), on the role's schedule, through the host's notifications.
var projectDigest = template{
	Key:                 templateProjectDigest,
	DefaultName:         "Project digest",
	DefaultInstructions: `Once a week, write a short digest for the project manager: which projects are active, which milestones are late or due in the next two weeks, and anything that looks stuck. Read the projects and their milestones first, and write only what you have read. Keep it to what the manager should act on.`,
	Allowlist: []allowedOperation{
		{ID: "getProjects"},
		{ID: "getProjectsById"},
		{ID: "getProjectsByIdMilestones"},
	},
	Needs:     []need{{"projects:access", levelRead}},
	HardRules: []hardRule{{"C1", true}},
	Tools:     []string{toolNotify, toolHandOff},
	Fields:    []string{fieldSchedule, fieldRecipient},
	Subject:   subjectSchedule,
	Recipient: true,
}

// templates are the templates of this build, by key.
var templates = map[string]*template{
	templateFrontDesk:          &frontDesk,
	templateReminderReplies:    &reminderReplies,
	templateSalesFollowup:      &salesFollowup,
	templateTimesheetReminders: &timesheetReminders,
	templateProjectDigest:      &projectDigest,
}

// templateOrder is the order the templates are listed in.
var templateOrder = []string{templateFrontDesk, templateReminderReplies, templateSalesFollowup, templateTimesheetReminders, templateProjectDigest}

// agentNever are the permissions no level can raise for an agent (V9): Staff's
// health data, the sealed national identity number and bank account, and the
// personal data Customers and Staff reveal or erase; identity's and every
// Owner-tier permission; this module's own; Invoices' issue and payments; and
// every approval in Time, Expenses and Staff; and every other permission that
// cannot be delegated (Delegable false), which is Owner-tier: Entra's and
// Google's administration, Knowledge's, Orders' carriers and settings, and
// Manufacturing's costs (manufacturing design §8), and Accounting's close —
// the chart, the opening balances and the lock (accounting design A1). The
// Assistant's API is not here: it names no permission, and the dispatcher refuses it by its path. A new
// permission that matches one of these families must be added (the test in
// internal/integration fails until it is), and a level for a listed key is
// refused 409 level_locked.
var agentNever = []string{
	"accounting:close",
	"agents:activity", "agents:manage", "agents:review", "agents:view",
	"customers:personal-data",
	"entra:manage",
	"expenses:approve",
	"google:manage",
	"identity:manage",
	"invoices:issue", "invoices:payments",
	"knowledge:manage",
	"manufacturing:costs-view",
	"orders:carriers-manage", "orders:settings",
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
		SafeOperations:      []string{},
		Tools:               slices.Clone(t.Tools),
		Fields:              make([]gen.AgentTemplateFields, 0, len(t.Fields)),
		Trigger:             gen.AgentTemplateTrigger(triggerOf(t)),
	}
	for _, f := range t.Fields {
		out.Fields = append(out.Fields, gen.AgentTemplateFields(f))
	}
	for _, a := range t.Allowlist {
		out.Allowlist = append(out.Allowlist, a.ID)
		if a.Safe {
			out.SafeOperations = append(out.SafeOperations, a.ID)
		}
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
	out := make([]gen.AgentTemplate, 0, len(templateOrder))
	for _, key := range templateOrder {
		out = append(out, templateView(templates[key]))
	}
	return gen.ListAgentTemplates200JSONResponse(gen.AgentTemplateList{Templates: out}), nil
}

// triggerOf is what wakes a template's roles, as the contract names it.
func triggerOf(t *template) string {
	switch t.Subject {
	case subjectQuote:
		return "quote_sent"
	case subjectPersonWeek:
		return "week_unsubmitted"
	case subjectSchedule:
		return "schedule"
	}
	return "conversation"
}
