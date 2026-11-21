package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// composedPOSTs is every POST of the composed contract, by operationId, with
// the composed permission catalog.
func composedPOSTs(t *testing.T) (map[string]*openapi3.Operation, []contracts.Permission) {
	t.Helper()
	var catalog []contracts.Permission
	for _, m := range append([]module.Module{identity.Module(nil)}, everyModule()...) {
		catalog = append(catalog, m.Permissions...)
	}
	posts := map[string]*openapi3.Operation{}
	for _, name := range openapi.Modules {
		doc, err := openapi.Load(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range doc.Paths.Map() {
			if op := item.Post; op != nil {
				posts[op.OperationID] = op
			}
		}
	}
	return posts, catalog
}

// exclusionReason says why module.Idempotent leaves a POST out, in the
// predicate's order (idempotency design I19).
func exclusionReason(op *openapi3.Operation, catalog []contracts.Permission) string {
	if reason, ok := module.IdempotencyNever[op.OperationID]; ok {
		return "never: " + reason
	}
	access, _ := op.Extensions["x-vantigo-access"].(string)
	if !strings.HasPrefix(access, "permission:") {
		kind, _, _ := strings.Cut(access, ":")
		return "rule " + kind
	}
	switch op.Extensions[contracts.APIExtension] {
	case contracts.APIExtensionNever:
		return "x-vantigo-api: never"
	case contracts.APIExtensionRead:
		return "x-vantigo-api: read"
	}
	never := contracts.APINever(catalog)
	for _, key := range strings.Split(strings.TrimPrefix(access, "permission:"), "+") {
		if slices.Contains(never, key) {
			return "APINever permission " + key
		}
	}
	for _, p := range op.Parameters {
		if p.Value != nil && strings.EqualFold(p.Value.Name, module.IdempotencyHeader) {
			return "own Idempotency-Key"
		}
	}
	if !operations.JSONRequest(op) {
		return "request not JSON"
	}
	if !operations.JSONResponse(op) {
		return "response not JSON"
	}
	return "success is no-store"
}

// TestIdempotency_TheScopeOverTheComposedContract is the predicate the router,
// the integrator document and MCP share (idempotency design I19), over every
// POST of the composed contract: each one it excludes is listed here with why,
// and every POST not listed is in scope. A new POST that is excluded, or a
// listed one that is now in scope, fails until this list says so.
func TestIdempotency_TheScopeOverTheComposedContract(t *testing.T) {
	posts, catalog := composedPOSTs(t)
	got := map[string]string{}
	inScope := 0
	for id, op := range posts {
		if module.Idempotent(http.MethodPost, op, catalog) {
			inScope++
			continue
		}
		got[id] = exclusionReason(op, catalog)
	}
	var lines []string
	for id, reason := range got {
		if want, ok := idempotencyExcluded[id]; !ok || want != reason {
			lines = append(lines, fmt.Sprintf("%q: %q,", id, reason))
		}
	}
	for id := range idempotencyExcluded {
		if _, ok := got[id]; !ok {
			lines = append(lines, fmt.Sprintf("%s is listed as excluded but is in scope (or gone)", id))
		}
	}
	sort.Strings(lines)
	if len(lines) > 0 {
		t.Errorf("the exclusions differ from idempotencyExcluded:\n%s", strings.Join(lines, "\n"))
	}
	t.Logf("%d POSTs: %d in scope, %d excluded", len(posts), inScope, len(got))
	if inScope < 300 {
		t.Errorf("%d POSTs in scope; the predicate is broken", inScope)
	}
	for _, id := range []string{
		// One-time secrets (I19, I20).
		"postStaffPeopleByIdBankAccountReveal", "postStaffPeopleByIdNationalIdReveal", "postStaffPeopleByIdAccountPass",
		"postIdentityStationPair",
		// Anonymous with a cookie's session (I19).
		"postIdentityLogin", "postIdentityPasskeysLoginBegin",
		// Policy rules: keys and secrets.
		"postIdentityIntegrationsByIdKeys", "postIdentityIntegrationsByIdWebhooksByWebhookIdRollSecret",
		// Their own key.
		"postCommunicationsConversations", "postInvoicesByIdApplyCredit",
	} {
		if _, ok := posts[id]; !ok {
			t.Errorf("%s is not in the contract", id)
		} else if _, excluded := got[id]; !excluded {
			t.Errorf("%s is in scope", id)
		}
	}
	for id := range module.IdempotencyNever {
		if _, ok := posts[id]; !ok {
			t.Errorf("IdempotencyNever names %s, which is not in the contract", id)
		}
	}
}

// TestIdempotency_NoOperationInScopeStreams is I32: the middleware's recorder
// holds an answer back and its Flush does nothing, which is right only because
// every operation in scope answers JSON or nothing on success: none streams an
// event stream or a file.
func TestIdempotency_NoOperationInScopeStreams(t *testing.T) {
	posts, catalog := composedPOSTs(t)
	for id, op := range posts {
		if !module.Idempotent(http.MethodPost, op, catalog) || op.Responses == nil {
			continue
		}
		for code, ref := range op.Responses.Map() {
			if !strings.HasPrefix(code, "2") || ref.Value == nil {
				continue
			}
			for contentType := range ref.Value.Content {
				if !operations.IsJSON(contentType) {
					t.Errorf("%s answers %s %s, which the recorder would hold back", id, code, contentType)
				}
			}
		}
	}
}

// idempotencyExcluded is every POST of the composed contract the middleware
// does not cover, with why (exclusionReason).
var idempotencyExcluded = map[string]string{
	"addAccountingTaxCodeRate":                              "x-vantigo-api: never",
	"applyAccountingSaftImport":                             "x-vantigo-api: never",
	"applyInstructionSuggestion":                            "x-vantigo-api: never",
	"approveReviewItem":                                     "x-vantigo-api: never",
	"cancelAssistantPendingAction":                          "rule session",
	"closeAccountingVatTerm":                                "x-vantigo-api: never",
	"confirmAssistantPendingAction":                         "rule session",
	"continueAssistantBulkJob":                              "rule session",
	"createAccountingAccount":                               "x-vantigo-api: never",
	"createAccountingBankAccount":                           "x-vantigo-api: never",
	"createAccountingBankRule":                              "x-vantigo-api: never",
	"createAccountingPostingRule":                           "x-vantigo-api: never",
	"createAccountingSupplier":                              "rule session",
	"createAgent":                                           "x-vantigo-api: never",
	"createAgentRole":                                       "x-vantigo-api: never",
	"createAssistantConversation":                           "rule session",
	"createCarrierAccount":                                  "x-vantigo-api: never",
	"createQuoteTemplate":                                   "x-vantigo-api: never",
	"discardReviewItem":                                     "x-vantigo-api: never",
	"dismissInstructionSuggestion":                          "x-vantigo-api: never",
	"estimateShipping":                                      "x-vantigo-api: read",
	"importAccountingBankStatement":                         "request not JSON",
	"importInventoryOpeningBalances":                        "request not JSON",
	"importInventoryPlacements":                             "request not JSON",
	"importKnowledgeMarkdown":                               "request not JSON",
	"importPurchasingSupplierItems":                         "request not JSON",
	"matchAccountingSaftImport":                             "x-vantigo-api: never",
	"pauseAgent":                                            "x-vantigo-api: never",
	"postAccountingOpening":                                 "x-vantigo-api: never",
	"postCalendarFeedByVariant":                             "x-vantigo-api: never",
	"postCalendarMailboxSyncRun":                            "x-vantigo-api: never",
	"postCalendarRsvp":                                      "rule anonymous",
	"postCalendarRsvpRead":                                  "rule anonymous",
	"postCalendarSubscriptions":                             "x-vantigo-api: never",
	"postCalendarSubscriptionsByIdRefresh":                  "x-vantigo-api: never",
	"postCommunicationsConversations":                       "own Idempotency-Key",
	"postCommunicationsConversationsByIdAttachments":        "own Idempotency-Key",
	"postCommunicationsConversationsByIdReply":              "own Idempotency-Key",
	"postCustomersByIdAttachments":                          "request not JSON",
	"postCustomersImports":                                  "request not JSON",
	"postEnergyImports":                                     "request not JSON",
	"postEnergyMeteringPointsByIdConsumptionImport":         "request not JSON",
	"postEnergySpotPricesImport":                            "request not JSON",
	"postEntraAccountsByObjectIdConfirm":                    "x-vantigo-api: never",
	"postEntraConnectionCredentials":                        "rule policy",
	"postEntraConnectionTest":                               "rule policy",
	"postEntraGroupsByObjectIdConfirm":                      "x-vantigo-api: never",
	"postEntraMailboxNotifications":                         "rule anonymous",
	"postEntraOutlookComplete":                              "rule session",
	"postEntraOutlookConnect":                               "rule session",
	"postExpensesClaimsByIdPerDiemSuggestion":               "x-vantigo-api: read",
	"postExpensesEntriesByIdAttachments":                    "request not JSON",
	"postGoogleAccountsByUserIdConfirm":                     "x-vantigo-api: never",
	"postGoogleCalendarNotifications":                       "rule anonymous",
	"postGoogleComplete":                                    "rule session",
	"postGoogleConnect":                                     "rule session",
	"postGoogleConnectionCredentials":                       "rule policy",
	"postGoogleConnectionTest":                              "rule policy",
	"postGoogleGroupsByGroupIdConfirm":                      "x-vantigo-api: never",
	"postIdentityAccessDelegations":                         "rule policy",
	"postIdentityAccessDelegationsByIdRevoke":               "rule policy",
	"postIdentityAccessGroups":                              "rule policy",
	"postIdentityAccessGroupsByGroupIdMembersByUserId":      "rule policy",
	"postIdentityAccessGroupsByGroupIdRoleMappingsByRoleId": "rule policy",
	"postIdentityAccessRoles":                               "rule policy",
	"postIdentityAccountAvatar":                             "rule policy",
	"postIdentityAccountMfaDisable":                         "rule policy",
	"postIdentityAccountMfaEnable":                          "rule policy",
	"postIdentityAccountMfaRecoveryCodes":                   "rule policy",
	"postIdentityAccountMfaSetup":                           "rule policy",
	"postIdentityAccountPasskeysBegin":                      "rule policy",
	"postIdentityAccountPasskeysComplete":                   "rule policy",
	"postIdentityAccountPassword":                           "rule policy",
	"postIdentityAccountSessionsRevoke":                     "rule policy",
	"postIdentityBootstrap":                                 "rule anonymous",
	"postIdentityBrands":                                    "x-vantigo-api: never",
	"postIdentityBrandsByIdCampaigns":                       "x-vantigo-api: never",
	"postIdentityBrandsByIdArchive":                         "x-vantigo-api: never",
	"postIdentityBrandsByIdDiscardDraft":                    "x-vantigo-api: never",
	"postIdentityBrandsByIdDuplicate":                       "x-vantigo-api: never",
	"postIdentityBrandsByIdMakeDefault":                     "x-vantigo-api: never",
	"postIdentityBrandsByIdPublish":                         "x-vantigo-api: never",
	"postIdentityBrandsByIdRestore":                         "x-vantigo-api: never",
	"postIdentityIntegrations":                              "rule policy",
	"postIdentityIntegrationsByIdArchive":                   "rule policy",
	"postIdentityOauthAuthorizations":                       "rule policy",
	"postIdentityOauthAuthorizationsPreview":                "rule policy",
	"postIdentityIntegrationsByIdDisable":                   "rule policy",
	"postIdentityIntegrationsByIdEnable":                    "rule policy",
	"postIdentityIntegrationsByIdKeys":                      "rule policy",
	"postIdentityIntegrationsByIdKeysByKeyIdRevoke":         "rule policy",
	"postIdentityIntegrationsByIdKeysByKeyIdRoll":           "rule policy",
	"postIdentityIntegrationsByIdWebhooks":                  "rule policy",
	"postIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesByDeliveryIdResend": "rule policy",
	"postIdentityIntegrationsByIdWebhooksByWebhookIdDisable":                      "rule policy",
	"postIdentityIntegrationsByIdWebhooksByWebhookIdEnable":                       "rule policy",
	"postIdentityIntegrationsByIdWebhooksByWebhookIdRollSecret":                   "rule policy",
	"postIdentityIntegrationsByIdWebhooksByWebhookIdTest":                         "rule policy",
	"postIdentityInvitationsAccept":                                               "rule anonymous",
	"postIdentityLogin":                                                           "rule anonymous",
	"postIdentityLogin2fa":                                                        "rule anonymous",
	"postIdentityLogout":                                                          "rule session",
	"postIdentityOidcCallback":                                                    "rule anonymous",
	"postIdentityOwnerInvitations":                                                "rule policy",
	"postIdentityOwnerInvitationsByIdResend":                                      "rule policy",
	"postIdentityOwnerInvitationsByIdRevoke":                                      "rule policy",
	"postIdentityOwnerMfaDisable":                                                 "rule policy",
	"postIdentityOwnerMfaEnable":                                                  "rule policy",
	"postIdentityOwnerMfaRecoveryCodes":                                           "rule policy",
	"postIdentityOwnerMfaResetByUserId":                                           "rule policy",
	"postIdentityOwnerMfaSetup":                                                   "rule policy",
	"postIdentityOwnerSsoScimToken":                                               "rule policy",
	"postIdentityOwnerUsers":                                                      "rule policy",
	"postIdentityOwnerUsersByIdDisable":                                           "rule policy",
	"postIdentityOwnerUsersByIdEnable":                                            "rule policy",
	"postIdentityOwnerUsersByIdManage":                                            "rule policy",
	"postIdentityOwnerUsersByIdPassword":                                          "rule policy",
	"postIdentityOwnerUsersByIdPasswordReset":                                     "rule policy",
	"postIdentityOwnerUsersByIdSsoLink":                                           "rule policy",
	"postIdentityOwnerUsersByIdUnmanage":                                          "rule policy",
	"postIdentityPasskeysLoginBegin":                                              "rule anonymous",
	"postIdentityPasskeysLoginComplete":                                           "rule anonymous",
	"postIdentityPasswordRecoveryRequest":                                         "rule anonymous",
	"postIdentityPasswordRecoveryReset":                                           "rule anonymous",
	"postIdentityScimV2Groups":                                                    "rule scim",
	"postIdentityScimV2Users":                                                     "rule scim",
	"postIdentityStationOperators":                                                "rule anonymous",
	"postIdentityStationOperatorsBySessionIdEnd":                                  "rule anonymous",
	"postIdentityStationPair":                                                     "rule anonymous",
	"postIdentityStations":                                                        "x-vantigo-api: never",
	"postIdentityStationsByIdOperatorsBySessionIdEnd":                             "x-vantigo-api: never",
	"postIdentityStationsByIdPairing":                                             "x-vantigo-api: never",
	"postIdentityStationsByIdRevoke":                                              "x-vantigo-api: never",
	"postIdentitySystemUsersByUserIdSessionsRevoke":                               "rule policy",
	"postInventoryLotAttachment":                                                  "request not JSON",
	"postInvoicesBankFiles":                                                       "request not JSON",
	"postInvoicesByIdApplyCredit":                                                 "own Idempotency-Key",
	"postInvoicesExchangeRatesFetch":                                              "x-vantigo-api: never",
	"postInvoicesPayAttempts":                                                     "rule anonymous",
	"postInvoicesPayRead":                                                         "rule anonymous",
	"postMetaPages":                                                               "rule policy",
	"postMetaPagesByPageIdResubscribe":                                            "rule policy",
	"postMetaSettingsVerifyToken":                                                 "rule policy",
	"postMetaWebhook":                                                             "rule anonymous",
	"postProductsByIdImages":                                                      "request not JSON",
	"postStaffAccountsRun":                                                        "x-vantigo-api: never",
	"postStaffPeopleByIdAccountDisable":                                           "x-vantigo-api: never",
	"postStaffPeopleByIdAccountPass":                                              "x-vantigo-api: never",
	"postStaffPeopleByIdAccountRun":                                               "x-vantigo-api: never",
	"postStaffPeopleByIdBankAccountReveal":                                        "x-vantigo-api: never",
	"postStaffPeopleByIdDocuments":                                                "request not JSON",
	"postStaffPeopleByIdNationalIdReveal":                                         "x-vantigo-api: never",
	"postStaffPeopleImport":                                                       "request not JSON",
	"postStaffPeopleSimilarErased":                                                "x-vantigo-api: read",
	"postStaffPersonCompetencesByIdEvidence":                                      "request not JSON",
	"postStaffRetentionRun":                                                       "never: the retention run erases up to fifty people and walks the deletion ledger in the request itself, and can outlast the lease",
	"postTimeEntries":                                                             "x-vantigo-api: never",
	"postTimeWeeksByWeekStartSubmit":                                              "x-vantigo-api: never",
	"previewAccountingBankCsv":                                                    "x-vantigo-api: read",
	"previewAccountingVoucher":                                                    "x-vantigo-api: read",
	"printInventoryLabels":                                                        "x-vantigo-api: read",
	"reopenAccountingVatTerm":                                                     "x-vantigo-api: never",
	"resolveKnowledgeLinks":                                                       "x-vantigo-api: read",
	"resumeAgent":                                                                 "x-vantigo-api: never",
	"sendAssistantMessage":                                                        "rule session",
	"sendEditedReviewItem":                                                        "x-vantigo-api: never",
	"setUpAccountingBooks":                                                        "x-vantigo-api: never",
	"stopAssistantBulkJob":                                                        "rule session",
	"stopAssistantRun":                                                            "rule session",
	"unfileAccountingVatTerm":                                                     "x-vantigo-api: never",
	"uploadAccountingAttachment":                                                  "request not JSON",
	"uploadAccountingSaftImport":                                                  "x-vantigo-api: never",
	"uploadAccountingSupplierInvoiceFile":                                         "request not JSON",
	"uploadKnowledgeFile":                                                         "request not JSON",
	"uploadManufacturingCheckPhoto":                                               "request not JSON",
	"verifyCarrierAccount":                                                        "x-vantigo-api: never",
}
