package products

import (
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The tracking mode (manufacturing phase 2 M49–M51): a variant's stock is
// kept in lots, in serials, or not at all. Only a goods product's variant may
// be tracked; a change of the mode is held by stock, reservations and stock in
// transit (variants.go's PUT, the holders' third purpose).

// trackingRequiresGoods records the field error of a tracked variant of a
// product that is not goods: 400, tracking_requires_goods, as a validation
// problem under prefix+"tracking".
func trackingRequiresGoods(prefix, tracking, productType string, errs map[string][]string) {
	if tracking != contracts.TrackingNone && productType != "Goods" {
		errs[prefix+"tracking"] = []string{"Only a goods product's variant may be tracked."}
	}
}

// trackingUnavailable is the interim guard of manufacturing phase 2 §1: until
// deliveries and productions capture lots (layers O and M), a tracked variant
// would be refused lot_required by every document that takes it, so no write
// may make one — 409 *Tracking unavailable* (tracking_unavailable) for any
// mode but none. Layer M deletes this function, its calls and its test.
func trackingUnavailable(tracking string) *apicommon.ProblemDetails {
	if tracking == contracts.TrackingNone {
		return nil
	}
	p := apicommon.ProblemStatus("Tracking unavailable",
		"Lot and serial tracking is not available yet: deliveries and productions cannot record lots. Keep the tracking at none.",
		http.StatusConflict)
	return &p
}
