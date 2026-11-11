package contracts

import (
	"strconv"
	"time"
)

// The events the manufacturing module publishes onto the platform outbox
// (manufacturing design M48, M48a). A payload carries ids and quantities and
// never personal data (orders design O3).

// ManufacturingOrderDone is manufacturing.order.done v1: a manufacturing
// order whose origin is a sales order line was closed. Produced is its good
// output as exact decimal text; SiteID the site it was made at.
type ManufacturingOrderDone struct {
	ManufacturingOrderID int64     `json:"manufacturingOrderId"`
	SalesOrderID         int64     `json:"salesOrderId"`
	SalesOrderLineID     int64     `json:"salesOrderLineId"`
	SiteID               int64     `json:"siteId"`
	Produced             string    `json:"produced"`
	ClosedAt             time.Time `json:"closedAt"`
}

func (ManufacturingOrderDone) EventName() string { return "manufacturing.order.done" }
func (ManufacturingOrderDone) EventVersion() int { return 1 }
func (e ManufacturingOrderDone) AggregateID() string {
	return strconv.FormatInt(e.ManufacturingOrderID, 10)
}
