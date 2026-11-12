package products

import (
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
