// Package netguard is the one table of addresses the server will not dial:
// private, loopback, link-local (including the 169.254.169.254 cloud
// metadata address), carrier-grade-NAT, unique-local, multicast, and
// otherwise non-routable or reserved ranges. internal/mail's SMTP
// destination guard delegates its address classification here, so there is
// exactly one list of forbidden ranges rather than several that could drift
// apart as new outbound clients (an SMP HTTP client among them) are added.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
)

// ErrDisallowed wraps every address DialContext refuses to dial, so a
// caller can distinguish "this destination is not allowed" from any other
// dial failure.
var ErrDisallowed = errors.New("netguard: destination not allowed")

// Resolver resolves a hostname to its IP addresses. It is the subset of
// *net.Resolver DialContext needs, so callers and tests can inject a canned
// answer and never touch real DNS.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Disallowed reports whether addr is a private (RFC1918), unique-local
// (RFC4193), link-local (RFC3927, including the 169.254.169.254 cloud
// metadata address), loopback, multicast, or otherwise non-routable or
// reserved address. It unwraps an IPv4-mapped IPv6 address first, so
// ::ffff:10.0.0.1 is classified exactly like 10.0.0.1.
//
// It mirrors the ranges rejected by the .NET SmtpDestinationGuard
// (apps/communications/backend/Communications.Module/Services/SmtpDestinationGuard.cs).
// Unlike that guard, Disallowed is pure and has no allowlist: a caller that
// needs one — internal/mail's SMTP driver tests dial an in-process loopback
// listener despite the guard — keeps its own test-only hook and
// special-cases loopback before delegating here.
func Disallowed(addr netip.Addr) bool {
	candidate := addr.Unmap()
	if candidate.IsLoopback() || candidate.IsUnspecified() || candidate.IsMulticast() {
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

// DialContext wraps dial with the address guard: it splits address into
// host and port, resolves host (a literal IP is checked without a lookup),
// and refuses the destination — without ever calling dial — when
// resolution fails, resolves to nothing, or any resolved address is
// disallowed. Otherwise it dials net.JoinHostPort of the first resolved
// address, never the hostname again, so the connection goes to the exact
// address that was checked. That is what defeats a DNS-rebinding attack: a
// DNS answer that changes between the check and the connect cannot smuggle
// a disallowed destination past the guard.
func DialContext(resolver Resolver, dial func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, address string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("netguard: invalid address %q: %w", address, err)
		}

		addrs, err := resolve(ctx, resolver, host)
		if err != nil {
			return nil, err
		}
		if len(addrs) == 0 {
			return nil, fmt.Errorf("%w: %q did not resolve to any address", ErrDisallowed, host)
		}

		for _, addr := range addrs {
			if Disallowed(addr) {
				return nil, fmt.Errorf(
					"%w: %q resolves to %s, a private, link-local, loopback, or reserved address",
					ErrDisallowed, host, addr)
			}
		}

		return dial(ctx, network, net.JoinHostPort(addrs[0].String(), port))
	}
}

// resolve returns host's addresses: a literal IP as a single-element slice
// with no lookup, otherwise resolver's answer.
func resolve(ctx context.Context, resolver Resolver, host string) ([]netip.Addr, error) {
	if literal, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{literal}, nil
	}
	addrs, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("netguard: host %q could not be resolved: %w", host, err)
	}
	return addrs, nil
}
