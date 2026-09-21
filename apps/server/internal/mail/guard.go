package mail

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/vantigo-io/vantigo/server/internal/netguard"
)

// ErrDestinationRejected wraps every rejection the destination guard makes,
// so a caller can distinguish "this destination is not allowed" from other
// send failures.
var ErrDestinationRejected = errors.New("mail: smtp destination rejected")

// hostResolver resolves a hostname to its IP addresses. It is the subset of
// *net.Resolver the guard needs, so tests can inject a canned answer and
// never touch real DNS.
type hostResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// destinationGuard rejects SMTP destinations that resolve to a private,
// loopback, link-local, carrier-grade-NAT, or otherwise non-routable
// address — including the 169.254.169.254 cloud metadata address. It
// mirrors the ranges rejected by the .NET SmtpDestinationGuard
// (apps/communications/backend/Communications.Module/Services/SmtpDestinationGuard.cs),
// less that guard's allowlist: this port has no allowlist, only the
// test-only loopback hook below.
//
// The smtp driver runs Check from inside its DialContextFunc, so the check
// happens immediately before the socket is opened and the connection is
// made to the exact address that was checked — never a fresh hostname
// lookup — which is what defeats a DNS-rebinding attack.
type destinationGuard struct {
	resolver hostResolver
}

func newDestinationGuard(resolver hostResolver) *destinationGuard {
	return &destinationGuard{resolver: resolver}
}

// allowLoopback lets the SMTP driver's own tests point it at an in-process
// loopback listener despite the guard. It defaults to false and stays false
// in production: there is no configuration field that reaches it, and the
// only code that sets it lives in export_test.go, which is compiled only
// by `go test`.
var allowLoopback = false

// Check resolves host and returns its first address if every resolved
// address is a permitted SMTP destination, or an error naming the host if
// resolution failed, it resolved to nothing, or any resolved address is
// disallowed.
func (g *destinationGuard) Check(ctx context.Context, host string) (netip.Addr, error) {
	if host == "" {
		return netip.Addr{}, fmt.Errorf("%w: an SMTP host is required", ErrDestinationRejected)
	}

	addrs, err := g.resolve(ctx, host)
	if err != nil {
		return netip.Addr{}, err
	}
	if len(addrs) == 0 {
		return netip.Addr{}, fmt.Errorf("%w: smtp host %q did not resolve to any address", ErrDestinationRejected, host)
	}

	for _, addr := range addrs {
		if disallowedDestination(addr) {
			return netip.Addr{}, fmt.Errorf(
				"%w: smtp host %q resolves to %s, a private, link-local, loopback, or reserved address",
				ErrDestinationRejected, host, addr)
		}
	}
	return addrs[0], nil
}

func (g *destinationGuard) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if literal, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{literal}, nil
	}
	addrs, err := g.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("%w: smtp host %q could not be resolved: %w", ErrDestinationRejected, host, err)
	}
	return addrs, nil
}

// disallowedDestination reports whether addr is a private (RFC1918),
// unique-local (RFC4193), link-local (RFC3927, including the
// 169.254.169.254 cloud metadata address), loopback, multicast, or
// otherwise non-routable/reserved address. The range table itself lives in
// internal/netguard, shared with every other outbound client this server
// guards; only the loopback test hook stays here, since netguard.Disallowed
// is pure and has no allowlist of its own.
func disallowedDestination(addr netip.Addr) bool {
	candidate := addr.Unmap()
	if candidate.IsLoopback() {
		return !allowLoopback
	}
	return netguard.Disallowed(candidate)
}
