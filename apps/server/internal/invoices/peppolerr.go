package invoices

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/netguard"
)

// peppolErrorKind and peppolDNSServers are copied from the customers module
// (customers/peppol_lookup.go, customers/server.go): depguard forbids
// importing another business module, and both are small enough that a copy
// is cheaper than a shared package for two callers (EHF and KID design D6).

// peppolErrorKind classifies a failed Peppol lookup into a small, fixed set of
// kinds for a warning log, rather than logging err.Error() itself: the
// message can carry the participant identifier — an organisation number —
// which does not belong in a log line.
func peppolErrorKind(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, netguard.ErrDisallowed):
		return "disallowed_destination"
	default:
		return "network"
	}
}

// peppolDNSServers is peppol.Options.DNSServers from Config.PeppolDNSServer:
// nil (meaning the server's own name servers) when it is unset, else the one
// configured resolver.
func peppolDNSServers(server string) []string {
	if server == "" {
		return nil
	}
	return []string{server}
}
