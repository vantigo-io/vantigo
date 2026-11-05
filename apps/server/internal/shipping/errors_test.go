package shipping_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/shipping"
)

func TestIsDefinitiveRefusal(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		err  error
		want bool
	}{
		"refused keys":   {fmt.Errorf("x: %w", shipping.ErrUnauthorized), true},
		"not sent":       {shipping.ErrInvalidRequest, true},
		"carrier refuse": {fmt.Errorf("x: %w", &shipping.CarrierRefusal{Code: "X", Message: "No"}), true},
		"timeout":        {errors.New("context deadline exceeded"), false},
		"too late":       {shipping.ErrTooLate, false},
	} {
		if got := shipping.IsDefinitiveRefusal(c.err); got != c.want {
			t.Errorf("%s: IsDefinitiveRefusal = %v, want %v", name, got, c.want)
		}
	}
	if got := (&shipping.CarrierRefusal{Code: "C", Message: "M"}).Error(); got != "shipping: the carrier refused the request (C): M" {
		t.Errorf("Error() = %q", got)
	}
}
