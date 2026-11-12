package inventory

import (
	"os"
	"strings"
	"testing"
)

// TestRefusalCodesAreInTheContract proves every refusal code this module
// answers is one its contract names, so a client reading the contract knows
// every code it can be refused with.
func TestRefusalCodesAreInTheContract(t *testing.T) {
	t.Parallel()
	contract, err := os.ReadFile("../openapi/specs/inventory.yaml")
	if err != nil {
		t.Fatalf("read the contract: %v", err)
	}
	for _, code := range []string{
		codeSiteCodeTaken, codeLocationCodeTaken, codeFullCodeTaken, codeCodeInvalid, codeDepthExceeded,
		codeParentInvalid, codeBulkTooLarge, codeLocationHasStock, codeLocationHasHistory, codeRootLocation,
		codeLocationArchived, codeLocationNotFound, codeVariantNotFound, codeVariantNotStockable,
		codeVariantDiscontinued, codeMoveCrossesSites, codeSameLocation, codeInsufficientStock,
		codeQuantityInvalid, codeNothingToPost, codeTooManyLines, codeClientRequestReused,
		codeRevisionConflict, codeDependencyUnavail,
		codeEnteredInvalid, codePackagingNotFound, codeCountScopeOverlaps, codeCountNotOpen,
		codeCountLinesMoved, codeLocationHasOpenCount, codeLabelsTooMany,
		codeTransferSameSite, codeSiteArchived, codeTransferNotDraft, codeTransferNotReceivable,
		codeTransferNotCancellable, codeTransferOverReceived, codeFromLocationRequired,
		codeTransferLineUnknown, codeNothingToClose, codeReceiveIncomplete, codeSiteHasOpenTransfers,
		codeLocationInTransit, codeSiteHasTransfers, codeMaximumBelowReorderPoint, codePlacementEmpty,
		codePlacementLocationInvalid, codeHoldNotInventory, codeHoldExists, codeLotDatesInvalid,
	} {
		if !strings.Contains(string(contract), code) {
			t.Errorf("the contract does not name the refusal code %s", code)
		}
	}
}
