package payments

import (
	"fmt"
	"strconv"
)

// Money is an amount in a currency's minor units — øre for NOK. It is the
// only amount that crosses the port: a consumer converts its exact decimal
// at the call, and an adapter sends Minor as the provider's integer value.
type Money struct {
	Currency string
	Minor    int64
}

// NOK is minor øre.
func NOK(minor int64) Money { return Money{Currency: "NOK", Minor: minor} }

// String writes the amount in major units with two decimals and its
// currency, "1234.56 NOK", by integer arithmetic only.
func (m Money) String() string {
	sign := ""
	// The magnitude as uint64, so the most negative int64 has one too.
	magnitude := uint64(m.Minor)
	if m.Minor < 0 {
		sign = "-"
		magnitude = -magnitude
	}
	return fmt.Sprintf("%s%s.%02d %s", sign, strconv.FormatUint(magnitude/100, 10), magnitude%100, m.Currency)
}
