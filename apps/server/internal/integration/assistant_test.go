package integration_test

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/accounting"
	"github.com/vantigo-io/vantigo/server/internal/agents"
	"github.com/vantigo-io/vantigo/server/internal/assistant"
	"github.com/vantigo-io/vantigo/server/internal/calendar"
	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/energy"
	"github.com/vantigo-io/vantigo/server/internal/entra"
	"github.com/vantigo-io/vantigo/server/internal/expenses"
	"github.com/vantigo-io/vantigo/server/internal/google"
	"github.com/vantigo-io/vantigo/server/internal/inventory"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/knowledge"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing"
	"github.com/vantigo-io/vantigo/server/internal/meta"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
	"github.com/vantigo-io/vantigo/server/internal/operations"
	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/planning"
	"github.com/vantigo-io/vantigo/server/internal/products"
	"github.com/vantigo-io/vantigo/server/internal/projects"
	"github.com/vantigo-io/vantigo/server/internal/staff"
	timetracking "github.com/vantigo-io/vantigo/server/internal/time"
)

// The Assistant's dispatcher over the whole real contract: every business
// module composed beside identity, the merged contract the server serves, and
// the mode of each of its operations (the Assistant design S4). The dispatcher's
// own tests use a small synthetic contract; this one proves the rules hold for
// the contract as it ships, and pins the operations whose mode is stated by
// hand, so a change to one is a decision someone makes.

// everyModule is every business module the server mounts.
func everyModule() []module.Module {
	return []module.Module{
		customers.Module(), products.Module(), energy.Module(), communications.Module(),
		projects.Module(), timetracking.Module(), expenses.Module(), invoices.Module(),
		staff.Module(), planning.Module(), inventory.Module(), orders.Module(), manufacturing.Module(), accounting.Module(), knowledge.Module(), calendar.Module(), entra.Module(), google.Module(), meta.Module(), assistant.Module(), agents.Module(),
	}
}

// markedByHand is every operation whose x-vantigo-assistant is written in
// openapi/*.yaml, with the reason. The set is pinned: marking one more is a
// product decision, not a side effect.
var markedByHand = map[string]string{
	// A POST that only reads: it works out days from a trip's times and writes nothing.
	"postExpensesClaimsByIdPerDiemSuggestion": "auto",

	// A secret or a credential would pass through the model and the provider.
	"getIdentityAccountMfaSetup":                  "never", // the authenticator's shared secret
	"getIdentityOwnerMfaSetup":                    "never",
	"postIdentityAccountMfaSetup":                 "never",
	"postIdentityAccountMfaEnable":                "never", // answers the recovery codes
	"postIdentityAccountMfaDisable":               "never",
	"postIdentityAccountMfaRecoveryCodes":         "never",
	"postIdentityOwnerMfaSetup":                   "never",
	"postIdentityOwnerMfaEnable":                  "never",
	"postIdentityOwnerMfaDisable":                 "never",
	"postIdentityOwnerMfaRecoveryCodes":           "never",
	"postIdentityAccountPassword":                 "never", // a password in the body
	"postIdentityAccountPasskeysBegin":            "never", // a WebAuthn ceremony needs the person's browser
	"postIdentityAccountPasskeysComplete":         "never",
	"deleteIdentityAccountPasskeysByCredentialId": "never",
	"postIdentityAccountSessionsRevoke":           "never", // ends the session the assistant stands on
	"getCalendarFeed":                             "never", // the address holds a bearer token
	"postCalendarFeedByVariant":                   "never",
	"postCommunicationsChannels":                  "never", // the SMTP password in the body
	"putCommunicationsChannelsById":               "never",
	"postInventoryLotAttachment":                  "never", // a lot's file is uploaded by a person (manufacturing phase 2 M54)

	// The assistant never calls itself (S4). Every operation of its own API says
	// so, though its access rules (session, OwnerManagement) already close them.
	"getAssistantAvailability":      "never",
	"getAssistantMe":                "never",
	"putAssistantMe":                "never",
	"listAssistantConversations":    "never",
	"createAssistantConversation":   "never",
	"getAssistantConversation":      "never",
	"updateAssistantConversation":   "never",
	"deleteAssistantConversation":   "never",
	"sendAssistantMessage":          "never",
	"stopAssistantRun":              "never",
	"confirmAssistantPendingAction": "never",
	"cancelAssistantPendingAction":  "never",
	"getAssistantBulkJob":           "never",
	"continueAssistantBulkJob":      "never",
	"stopAssistantBulkJob":          "never",
	"getAssistantBulkJobFailures":   "never",
	"getAssistantUsage":             "never",
	"getAssistantSettings":          "never",
	"putAssistantSettings":          "never",
	"getAssistantPermissions":       "never",
	"putAssistantPermission":        "never",
	"getAssistantPerson":            "never",
	"putAssistantPerson":            "never",

	// Knowledge reaches the Assistant only through searchKnowledge and
	// getKnowledgeArticleText, the two reads that apply ai_hidden (knowledge
	// design K14, K16); every other operation, reads included, says never.
	"listKnowledgeTree":              "never",
	"createKnowledgeArticle":         "never",
	"getKnowledgeArticle":            "never",
	"updateKnowledgeArticle":         "never",
	"updateKnowledgeArticleContent":  "never",
	"resetKnowledgeArticleLog":       "never",
	"moveKnowledgeArticle":           "never",
	"getKnowledgeArticleAccess":      "never",
	"updateKnowledgeArticleAccess":   "never",
	"listKnowledgeArticleVersions":   "never",
	"getKnowledgeArticleVersion":     "never",
	"restoreKnowledgeArticleVersion": "never",
	"trashKnowledgeArticle":          "never",
	"listKnowledgeTrash":             "never",
	"restoreKnowledgeArticle":        "never",
	"emptyKnowledgeTrash":            "never",
	"listKnowledgeFavourites":        "never",
	"addKnowledgeFavourite":          "never",
	"removeKnowledgeFavourite":       "never",
	"moveKnowledgeFavourite":         "never",
	"listKnowledgeAssignableUsers":   "never",
	"uploadKnowledgeFile":            "never",
	"getKnowledgeFile":               "never",
	"listKnowledgeBacklinks":         "never",
	"exportKnowledgeArticle":         "never",
	"importKnowledgeMarkdown":        "never",
	"resolveKnowledgeLinks":          "never",
	"copyKnowledgeFile":              "never",

	// Steering a virtual employee, raising its levels and binding its channels are
	// the Owner's own decisions (virtual employees design V12), never made on a
	// model's word: every operation of the agents API says never.
	"listAgents":             "never",
	"createAgent":            "never",
	"deleteAgent":            "never",
	"getAgent":               "never",
	"updateAgent":            "never",
	"getAgentAvatar":         "never",
	"putAgentAvatar":         "never",
	"pauseAgent":             "never",
	"putAgentPermission":     "never",
	"resumeAgent":            "never",
	"createAgentRole":        "never",
	"deleteAgentRole":        "never",
	"updateAgentRole":        "never",
	"setAgentRoleChannels":   "never",
	"getAgentSetup":          "never",
	"getAgentsSettings":      "never",
	"putAgentsSettings":      "never",
	"listAgentTemplates":     "never",
	"giveBackConversation":   "never",
	"getConversationHandOff": "never",
	// The review queue and what it shows (V20): deciding what an agent proposes is a
	// person's, and the queue, a run and the audit show what the agent read.
	"listReviewItems":      "never",
	"getReviewCount":       "never",
	"getReviewItem":        "never",
	"approveReviewItem":    "never",
	"sendEditedReviewItem": "never",
	"discardReviewItem":    "never",
	"getRoleCalibration":   "never",
	"getAgentUsage":        "never",
	"listAgentRuns":        "never",
	"getAgentRun":          "never",
	"listAgentsAudit":      "never",
	// The agents a person may talk to in the panel (V31): the Assistant's own endpoint.
	"listAssistantAgents": "never",
	// What an agent remembers and what the weekly check proposes (V27, V28): the notes are
	// what a stranger's mail made a model write down, and an instruction edit is the Owner's.
	"getAgentMemory":               "never",
	"deleteAgentMemory":            "never",
	"getCustomersByIdAgentMemory":  "never",
	"listRoleSuggestions":          "never",
	"applyInstructionSuggestion":   "never",
	"dismissInstructionSuggestion": "never",
	// Registering a production posts a stock movement, and its route names only
	// manufacturing:access: the handler asks for shop-floor or orders-manage
	// itself (manufacturing design M33), which the Assistant's gate cannot see.
	"postManufacturingProduction": "never",
	// The shop-floor actions are never the Assistant's: an agent does not clock
	// in (manufacturing design §10).
	"startManufacturingWorkOrder":       "never",
	"pauseManufacturingWorkOrder":       "never",
	"finishManufacturingWorkOrder":      "never",
	"reportManufacturingWorkOrderScrap": "never",
	"blockManufacturingWorkCenter":      "never",
	"unblockManufacturingWorkCenter":    "never",
	// A check's result and its photo are the station's (manufacturing phase 2
	// M97): an agent neither measures nor photographs, and a photo is a file
	// it never fetches.
	"recordManufacturingCheckResult": "never",
	// Register quantity is the station's too (manufacturing phase 2 M116).
	"registerManufacturingWorkOrderQuantity": "never",
	"uploadManufacturingCheckPhoto":          "never",
	"getManufacturingCheckPhoto":             "never",
	// Shared shop-floor stations (manufacturing phase 2 M129–M143): a station
	// is a device and its operators people at it; a pairing code, a PIN and a
	// badge are credentials, and which stations exist and what they reach is
	// an administrator's.
	"getIdentityStations":                             "never",
	"postIdentityStations":                            "never",
	"putIdentityStationsById":                         "never",
	"postIdentityStationsByIdPairing":                 "never",
	"postIdentityStationsByIdRevoke":                  "never",
	"postIdentityStationsByIdOperatorsBySessionIdEnd": "never",
	"deleteIdentityStationsByIdBackoffByUserId":       "never",
	"getIdentityStation":                              "never",
	"postIdentityStationPair":                         "never",
	"getIdentityStationOperators":                     "never",
	"postIdentityStationOperators":                    "never",
	"postIdentityStationOperatorsBySessionIdEnd":      "never",
	"getIdentityAccountPin":                           "never",
	"putIdentityAccountPin":                           "never",
	"deleteIdentityAccountPin":                        "never",
	"getIdentityUsersByIdOperatorCredentials":         "never",
	"deleteIdentityUsersByIdPin":                      "never",
	"putIdentityUsersByIdBadge":                       "never",
	"deleteIdentityUsersByIdBadge":                    "never",
	"getManufacturingStationWorkCenters":              "never",
	"putManufacturingStationWorkCenters":              "never",
	// A voucher's attachment is a file a person uploads and downloads: an
	// agent neither sends nor fetches one (accounting design A4).
	"uploadAccountingAttachment": "never",
	"getAccountingAttachment":    "never",
	// A report's CSV is a file for a person's spreadsheet, and it carries
	// customers' names (accounting design A1, A11).
	"exportAccountingReport": "never",
	// The preview only reads: it computes a journal's tax lines and writes
	// nothing (A6).
	"previewAccountingVoucher": "auto",
}

func TestOperations_TheRealContract(t *testing.T) {
	mods := everyModule()
	opts := []modtest.Option{modtest.WithRecorder(contracttest.NewForModule("customers"))}
	for _, m := range mods {
		opts = append(opts, modtest.WithModule(m))
	}
	h := modtest.New(t, opts...)
	ops := h.Operations(t)

	catalog := map[string]contracts.Permission{}
	for _, m := range append([]module.Module{identityPermissions()}, mods...) {
		for _, p := range m.Permissions {
			catalog[p.Key] = p
		}
	}

	type declared struct {
		method, access, mark string
	}
	contract := map[string]declared{}
	for _, name := range openapi.Modules {
		doc, err := openapi.Load(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range doc.Paths.Map() {
			for method, op := range item.Operations() {
				access, _ := op.Extensions["x-vantigo-access"].(string)
				mark, _ := op.Extensions["x-vantigo-assistant"].(string)
				contract[op.OperationID] = declared{method: method, access: access, mark: mark}
			}
		}
	}

	all := ops.All()
	if len(all) != len(contract) {
		t.Fatalf("the dispatcher knows %d operations, the contracts declare %d", len(all), len(contract))
	}

	// The contract's mode is S4's without its Sensitive clause (S16), derived
	// here again from the contract alone; the default gate's mode, which is what
	// a dispatcher with no gate allows, is the contract's with every operation
	// that names a Sensitive permission closed (delivery 0's rule), derived
	// separately.
	counts := map[operations.Mode]int{}
	gated := map[operations.Mode]int{}
	for _, info := range all {
		counts[info.Mode]++
		d, ok := contract[info.ID]
		if !ok {
			t.Errorf("%s is not in the contracts", info.ID)
			continue
		}
		if strings.TrimSpace(info.Summary) == "" {
			t.Errorf("%s has no summary", info.ID)
		}
		want := derivedMode(t, info, d.method, d.access, d.mark, catalog)
		if info.Mode != want {
			t.Errorf("%s (%s %s, %s): contract mode %q, want %q", info.ID, d.method, info.Path, d.access, info.Mode, want)
		}
		wantSensitive := namesSensitive(d.access, catalog)
		if info.Sensitive != wantSensitive {
			t.Errorf("%s (%s): Sensitive = %v, want %v", info.ID, d.access, info.Sensitive, wantSensitive)
		}
		wantGated := want
		if wantSensitive {
			wantGated = operations.ModeNever
		}
		got, _ := ops.Effective(context.Background(), info.ID)
		if got != wantGated {
			t.Errorf("%s (%s): the default gate's mode %q, want %q", info.ID, d.access, got, wantGated)
		}
		gated[got]++
	}
	t.Logf("%d operations by contract: %d auto, %d confirm, %d never; behind the default gate: %d auto, %d confirm, %d never",
		len(all), counts[operations.ModeAuto], counts[operations.ModeConfirm], counts[operations.ModeNever],
		gated[operations.ModeAuto], gated[operations.ModeConfirm], gated[operations.ModeNever])
	if gated[operations.ModeNever] <= counts[operations.ModeNever] {
		t.Errorf("the default gate closed nothing: %d never beside the contract's %d", gated[operations.ModeNever], counts[operations.ModeNever])
	}
	if counts[operations.ModeAuto] == 0 || counts[operations.ModeConfirm] == 0 || counts[operations.ModeNever] == 0 {
		t.Errorf("modes = %v, want all three in use", counts)
	}

	// The hand-written marks are exactly the pinned set.
	marked := map[string]string{}
	for id, d := range contract {
		if d.mark != "" {
			marked[id] = d.mark
		}
	}
	// Of Knowledge's operations exactly two are callable, both auto (K14):
	// the contract leaves them unmarked GETs, and this pins that no other
	// becomes callable by losing its mark.
	knowledgeCallable := map[string]string{}
	for _, info := range all {
		if strings.HasPrefix(info.Path, "/api/v1/knowledge/") && info.Mode != operations.ModeNever {
			knowledgeCallable[info.ID] = string(info.Mode)
		}
	}
	if want := map[string]string{"searchKnowledge": "auto", "getKnowledgeArticleText": "auto"}; !equalMaps(knowledgeCallable, want) {
		t.Errorf("Knowledge's callable operations = %v, want exactly %v", sortedPairs(knowledgeCallable), sortedPairs(want))
	}

	if !equalMaps(marked, markedByHand) {
		t.Errorf("x-vantigo-assistant is written on %v\nwant exactly %v\n(add an operation here only when its mode is meant to differ from S4's default)", sortedPairs(marked), sortedPairs(markedByHand))
	}

	// Accounting's writes of consequence are asked before they are made: a
	// manual journal, a reversal, the opening and the lock (the last two under
	// accounting:close, which no agent level can raise, reference/agents.md).
	for _, id := range []string{"postAccountingVoucher", "reverseAccountingVoucher", "postAccountingOpening", "putAccountingLock",
		"settleAccountingOpenItem"} {
		if got, _ := ops.Effective(context.Background(), id); got != operations.ModeConfirm {
			t.Errorf("%s: the default gate's mode %q, want confirm", id, got)
		}
	}

	// What a model is shown fits a small context window: every operation that
	// can be called describes in at most 8 KB of JSON.
	for _, info := range all {
		if info.Mode == operations.ModeNever {
			continue
		}
		raw, err := ops.Describe(info.ID)
		if err != nil || len(raw) > 8<<10 || !json.Valid(raw) {
			t.Errorf("Describe(%s): %d bytes, valid %v, %v", info.ID, len(raw), json.Valid(raw), err)
		}
	}
}

// identityPermissions is identity's contribution to the catalog, which is
// all the harness composes beside the modules given.
func identityPermissions() module.Module {
	return module.Module{Permissions: []contracts.Permission{{Key: "identity:manage", Sensitive: true}}}
}

// namesSensitive reports whether an access rule names a Sensitive permission.
func namesSensitive(access string, catalog map[string]contracts.Permission) bool {
	kind, list, _ := strings.Cut(access, ":")
	if kind != "permission" {
		return false
	}
	for _, key := range strings.Split(list, "+") {
		if catalog[key].Sensitive {
			return true
		}
	}
	return false
}

// derivedMode is S4 restated from the contract alone, without its Sensitive
// clause (S16): the explicit mark, else never for a rule or a file or a
// permission the catalog lacks, else GET → auto and the rest → confirm.
func derivedMode(t *testing.T, info operations.Info, method, access, mark string, catalog map[string]contracts.Permission) operations.Mode {
	t.Helper()
	if mark != "" {
		return operations.Mode(mark)
	}
	kind, list, _ := strings.Cut(access, ":")
	switch kind {
	case "anonymous", "scim", "session":
		return operations.ModeNever
	case "policy":
		for _, name := range strings.Split(list, "+") {
			if slices.Contains([]string{"Owner", "OwnerManagement", "AuthorizationManagement", "SystemAdmin"}, name) {
				return operations.ModeNever
			}
		}
	case "permission":
		for _, key := range strings.Split(list, "+") {
			if _, ok := catalog[key]; !ok {
				return operations.ModeNever
			}
		}
	}
	// A file or a redirect: the summary words are no guide, so the operations
	// the dispatcher classified by media type are checked by their paths.
	if isFile(info.ID) {
		return operations.ModeNever
	}
	if method == "GET" {
		return operations.ModeAuto
	}
	return operations.ModeConfirm
}

// isFile is the operations of the shipped contract that are uploads or
// downloads, found by their summary: a change here is a change to the contract.
func isFile(id string) bool {
	return slices.Contains(filesOrRedirects, id)
}

// filesOrRedirects are the unmarked, non-sensitive operations whose request or
// success response is not JSON, which S4 makes never.
var filesOrRedirects = []string{
	"getCalendarFeedByFile",
	"getCommunicationsAttachmentsByIdDownload",
	"getCommunicationsMessagesByIdRaw",
	"getCommunicationsPublicMediaByToken",
	"headCommunicationsPublicMediaByToken",
	"getCustomersByIdAttachmentsByAttachmentId",
	"getCustomersExport",
	"getCustomersImportTemplate",
	"getCustomersImportsByImportIdFailedRows",
	"getEnergyImportsByImportIdFailedRows",
	"getExpensesAttachmentsById",
	"getExpensesReimbursementsExportCsv",
	"getIdentityAccountAvatar",
	"getIdentityOwnerUsersByIdAvatar",
	"getIdentityOidcCallback",
	"getIdentityOidcChallenge",
	"getIdentityOidcComplete",
	"getInvoicesCollectionExportCsv",
	"getInvoicesExportCsv",
	"getInvoicesReminderPrintBatchesByIdPdf",
	"getInvoicesRemindersByIdPdf",
	"getInvoicesByIdPdf",
	"getInvoicesByIdPreviewPdf",
	"getInvoicesByIdTransmissionsByTransmissionIdUbl",
	"getInvoicesVippsPaymentsCsv",
	"getProductsByIdImagesByImageIdContent",
	"postProductsByIdImages",
	"getStaffDocumentsById",
	"getStaffPayrollExport",
	"getStaffPeopleExportCsv",
	"getStaffPeopleImportTemplateCsv",
	"getStaffPeopleByIdPhoto",
	"getStaffPersonCompetencesByIdEvidence",
	"importInventoryOpeningBalances",
	"postCustomersImports",
	"postEnergyImports",
	"postEnergyMeteringPointsByIdConsumptionImport",
	"postEnergySpotPricesImport",
	"postExpensesEntriesByIdAttachments",
	"postIdentityAccountAvatar",
	"putIdentityAccountAvatar",
	"postCommunicationsConversationsByIdAttachments",
	"postCustomersByIdAttachments",
	"postStaffPeopleByIdDocuments",
	"postStaffPeopleImport",
	"postStaffPersonCompetencesByIdEvidence",
	"putStaffPeopleByIdPhoto",
	"postInvoicesBankFiles",
	"printInventoryLabels",
	"getInventoryTransferNote",
	"exportInventoryLowStock",
	"exportInventoryExpiringLots",
	"printInventoryLotLabels",
	"exportInventoryTrace",
	"printInventoryTrace",
	"importInventoryPlacements",
	"getInventoryLotAttachment",
	"getManufacturingCheckPhoto",
	"getQuotePdf",
	"getQuoteProforma",
	"getSalesOrderPdf",
	"getSalesOrderProforma",
	"getDeliveryPickList",
	"getDeliveryPackingSlip",
	"getShipmentLabel",
	"getShipmentWaybill",
	"uploadKnowledgeFile",
	"getKnowledgeFile",
	"exportKnowledgeArticle",
	"importKnowledgeMarkdown",
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func sortedPairs(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
