package mail

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
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
		return nil, fmt.Errorf("%w: smtp host %q could not be resolved: %v", ErrDestinationRejected, host, err)
	}
	return addrs, nil
}

// disallowedDestination reports whether addr is a private (RFC1918),
// unique-local (RFC4193), link-local (RFC3927, including the
// 169.254.169.254 cloud metadata address), loopback, multicast, or
// otherwise non-routable/reserved address.
func disallowedDestination(addr netip.Addr) bool {
	candidate := addr.Unmap()
	if candidate.IsLoopback() {
		return !allowLoopback
	}
	// Multicast is refused for IPv4 too, stricter than .NET's
	// SmtpDestinationGuard, which let 224.0.0.0/4 through; no SMTP server
	// lives there.
	if candidate.IsUnspecified() || candidate.IsMulticast() {
		return true
	}
	if candidate.Is4() {
		return disallowedIPv4(candidate.As4())
	}
	return disallowedIPv6(candidate.As16())
}

func disallowedIPv4(b [4]byte) bool {
	switch {
	case b[0] == 0: // 0.0.0.0/8 - "this network"
		return true
	case b[0] == 10: // 10.0.0.0/8 - RFC1918
		return true
	case b[0] == 100 && b[1] >= 64 && b[1] <= 127: // 100.64.0.0/10 - carrier-grade NAT
		return true
	case b[0] == 169 && b[1] == 254: // 169.254.0.0/16 - RFC3927 link-local, incl. the 169.254.169.254 cloud metadata address
		return true
	case b[0] == 172 && b[1] >= 16 && b[1] <= 31: // 172.16.0.0/12 - RFC1918
		return true
	case b[0] == 192 && b[1] == 0 && b[2] == 0: // 192.0.0.0/24 - IETF protocol assignments
		return true
	case b[0] == 192 && b[1] == 168: // 192.168.0.0/16 - RFC1918
		return true
	case b[0] >= 240: // 240.0.0.0/4 - reserved, plus 255.255.255.255
		return true
	default:
		return false
	}
}

func disallowedIPv6(b [16]byte) bool {
	addr := netip.AddrFrom16(b)
	if addr.IsLinkLocalUnicast() {
		return true
	}
	if b[0] == 0xfe && b[1]&0xc0 == 0xc0 { // fec0::/10 - deprecated site-local
		return true
	}
	return b[0]&0xfe == 0xfc // fc00::/7 - RFC4193 unique local addresses
}
