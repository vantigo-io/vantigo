package invoices

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/netguard"
	"github.com/vantigo-io/vantigo/server/internal/peppol"
)

// A failed lookup is logged by one of four kinds, never by its message: the
// message can carry the participant identifier, an organisation number
// (EHF and KID design D6, the customers precedent). The kinds see through
// wrapping.
func TestPeppolErrorKind(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("peppol: 0192:923609016: %w", context.DeadlineExceeded), "timeout"},
		{fmt.Errorf("peppol: %w", context.Canceled), "canceled"},
		{fmt.Errorf("peppol: dial: %w", netguard.ErrDisallowed), "disallowed_destination"},
		{errors.New("peppol: 0192:923609016: SERVFAIL"), "network"},
	} {
		if got := peppolErrorKind(c.err); got != c.want {
			t.Errorf("peppolErrorKind(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

// Unset, the server's own name servers (nil); set, the one resolver.
func TestPeppolDNSServers(t *testing.T) {
	t.Parallel()
	if got := peppolDNSServers(""); got != nil {
		t.Errorf("peppolDNSServers(\"\") = %v, want nil", got)
	}
	if got := peppolDNSServers("10.0.0.53:53"); !slices.Equal(got, []string{"10.0.0.53:53"}) {
		t.Errorf("peppolDNSServers = %v, want the one resolver", got)
	}
}

// lookupReceiver answers what the lookup answered, and logs a failure by its
// kind alone — the participant never reaches the log. With the lookup
// disabled it refuses without calling anything.
func TestLookupReceiver_LogsAFailureByKindOnly(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	failure := fmt.Errorf("peppol: 0192:923609016: %w", context.DeadlineExceeded)
	s := &server{
		deps: module.Deps{Logger: slog.New(slog.NewJSONHandler(&logs, nil))},
		peppolLookup: func(_ context.Context, participant string) (peppol.Result, error) {
			if participant == "0192:974760673" {
				return peppol.Result{Registered: true, CanReceiveInvoice: true}, nil
			}
			return peppol.Result{}, failure
		},
	}

	if res, err := s.lookupReceiver(context.Background(), "0192:974760673"); err != nil || !res.Registered || !res.CanReceiveInvoice {
		t.Errorf("a registered receiver = %+v, %v; want registered and able to receive an invoice", res, err)
	}
	if logs.Len() != 0 {
		t.Errorf("a successful lookup logged %s, want nothing", logs.String())
	}
	if _, err := s.lookupReceiver(context.Background(), "0192:923609016"); !errors.Is(err, failure) {
		t.Errorf("a failed lookup = %v, want the lookup's own error", err)
	}
	if got := logs.String(); !strings.Contains(got, `"errorKind":"timeout"`) || strings.Contains(got, "923609016") {
		t.Errorf("logs = %s, want the kind and never the participant", got)
	}

	disabled := &server{deps: s.deps}
	if _, err := disabled.lookupReceiver(context.Background(), "0192:923609016"); !errors.Is(err, errPeppolLookupDisabled) {
		t.Errorf("with the lookup disabled = %v, want errPeppolLookupDisabled", err)
	}
}
