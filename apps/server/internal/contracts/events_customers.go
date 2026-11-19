package contracts

import (
	"strconv"
	"time"
)

// The events the customers module publishes onto the platform outbox
// (invoices phase 6 design I38). Each is a versioned wire form; a payload
// carries ids and amounts and never personal data.

// CustomerCreditLimitChanged is customers.credit_limit.changed v1: a
// customer's credit limit was set, changed or removed. CreditLimit is the new
// limit in NOK as exact decimal text, nil when it was removed.
type CustomerCreditLimitChanged struct {
	CustomerID  int32     `json:"customerId"`
	CreditLimit *string   `json:"creditLimit"`
	ChangedAt   time.Time `json:"changedAt"`
}

func (CustomerCreditLimitChanged) EventName() string { return "customers.credit_limit.changed" }
func (CustomerCreditLimitChanged) EventVersion() int { return 1 }
func (e CustomerCreditLimitChanged) AggregateID() string {
	return strconv.FormatInt(int64(e.CustomerID), 10)
}
