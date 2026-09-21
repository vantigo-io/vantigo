// Package netguard is the one table of addresses the server will not dial:
// private, loopback, link-local (including the 169.254.169.254 cloud
// metadata address), carrier-grade-NAT, unique-local, multicast, and
// otherwise non-routable or reserved ranges — including the IPv6 forms that
// merely carry an IPv4 address inside them (IPv4-mapped ::ffff:/96, NAT64,
// 6to4, the deprecated IPv4-compatible ::/96), classified by the embedded
// address, and Teredo, whose embedding is obfuscated rather than plain and
// so is refused outright. internal/mail's SMTP destination guard delegates
// its address classification here, so there is exactly one list of
// forbidden ranges rather than several that could drift apart as new
// outbound clients (an SMP HTTP client among them) are added.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
)

// ErrDisallowed wraps the destination DialContext refuses to dial: every
// resolved address was disallowed, an empty host, or resolution returned no
// addresses at all. It does NOT wrap a malformed address (net.SplitHostPort
// failed) or a resolution failure (the resolver itself returned an error)
// — those are ordinary errors, not a disallowed destination, though a
// resolution failure is still %w-wrapped around the resolver's own error so
// errors.Is/As still reaches it.
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
// reserved address. An invalid/zero netip.Addr is disallowed too — there is
// nothing valid to dial.
//
// It unwraps an IPv4-mapped IPv6 address first, so ::ffff:10.0.0.1 is
// classified exactly like 10.0.0.1, and it likewise sees through the other
// IPv6 forms that embed an IPv4 address (NAT64, 6to4, the deprecated
// IPv4-compatible ::/96): the embedded address is extracted and classified
// by the same table, so a translation gateway cannot be used to reach a
// forbidden IPv4 destination that a plain IPv6 route would never reach.
// Multicast is refused for IPv4 too (224.0.0.0/4), deliberately stricter
// than the .NET SmtpDestinationGuard this table otherwise mirrors
// (apps/communications/backend/Communications.Module/Services/SmtpDestinationGuard.cs),
// which let that range through — no SMTP or SMP server lives there.
//
// Unlike that guard, Disallowed is pure and has no allowlist: a caller that
// needs one — internal/mail's SMTP driver tests dial an in-process loopback
// listener despite the guard — keeps its own test-only hook and
// special-cases loopback before delegating here.
func Disallowed(addr netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	candidate := addr.Unmap()
	// Multicast is refused for IPv4 too, stricter than .NET's
	// SmtpDestinationGuard, which let 224.0.0.0/4 through; no SMTP or SMP
	// server lives there.
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
	if b[0]&0xfe == 0xfc { // fc00::/7 - RFC4193 unique local addresses
		return true
	}
	if b[0] == 0x20 && b[1] == 0x01 && b[2] == 0x00 && b[3] == 0x00 {
		// 2001::/32 - Teredo: the embedded address is obfuscated (XORed
		// with the Teredo server's own address), not a plain embedding, and
		// no SMTP or SMP server lives there — refuse the whole prefix
		// outright rather than try to decode it.
		return true
	}
	if b[0] == 0x00 && b[1] == 0x64 && b[2] == 0xff && b[3] == 0x9b && b[4] == 0x00 && b[5] == 0x01 {
		// 64:ff9b:1::/48 - RFC 8215 local-use NAT64: per RFC 6052 the
		// embedding position within the /48 depends on the operator's own
		// prefix length, so there is no single fixed offset to extract —
		// refuse the whole /48 rather than guess.
		return true
	}
	if embedded, ok := embeddedIPv4(b); ok {
		return disallowedEmbeddedIPv4(embedded)
	}
	return false
}

// embeddedIPv4 extracts the IPv4 address embedded in an IPv6 address that
// plainly embeds one at a fixed offset, so it can be classified by the same
// table an ordinary IPv4 destination gets rather than only checking
// IPv6-shaped ranges. It covers the two RFC 6052 embeddings — the NAT64
// well-known prefix 64:ff9b::/96 and the deprecated IPv4-compatible
// ::/96 (both: last 32 bits; note :: and ::1 are already refused earlier,
// by Disallowed's own unspecified/loopback check, before disallowedIPv6
// ever runs) — and 6to4 (RFC 3056, 2002::/16, bits 16-47). The local-use
// NAT64 /48 and Teredo are handled by the caller before this is reached,
// since neither has a single fixed offset to extract (NAT64 /48) or embeds
// the address plainly at all (Teredo).
func embeddedIPv4(b [16]byte) (embedded [4]byte, ok bool) {
	switch {
	case b[0] == 0x00 && b[1] == 0x64 && b[2] == 0xff && b[3] == 0x9b &&
		b[4] == 0 && b[5] == 0 && b[6] == 0 && b[7] == 0 &&
		b[8] == 0 && b[9] == 0 && b[10] == 0 && b[11] == 0:
		// 64:ff9b::/96 - NAT64 well-known prefix
		return [4]byte{b[12], b[13], b[14], b[15]}, true
	case b[0] == 0 && b[1] == 0 && b[2] == 0 && b[3] == 0 &&
		b[4] == 0 && b[5] == 0 && b[6] == 0 && b[7] == 0 &&
		b[8] == 0 && b[9] == 0 && b[10] == 0 && b[11] == 0:
		// ::/96 - deprecated IPv4-compatible
		return [4]byte{b[12], b[13], b[14], b[15]}, true
	case b[0] == 0x20 && b[1] == 0x02:
		// 2002::/16 - 6to4
		return [4]byte{b[2], b[3], b[4], b[5]}, true
	default:
		return [4]byte{}, false
	}
}

// disallowedEmbeddedIPv4 classifies an IPv4 address embedded in one of the
// plain-embedding IPv6 forms by the same table disallowedIPv4 uses, plus
// the two IPv4 ranges that a native (or IPv4-mapped ::ffff:/96) address
// only trips via netip's IsLoopback/IsMulticast before disallowedIPv4 is
// ever reached, and so would otherwise slip through an embedded form:
// loopback 127.0.0.0/8 and multicast 224.0.0.0/4.
func disallowedEmbeddedIPv4(b [4]byte) bool {
	if b[0] == 127 { // 127.0.0.0/8 - loopback
		return true
	}
	if b[0] >= 224 && b[0] <= 239 { // 224.0.0.0/4 - multicast
		return true
	}
	return disallowedIPv4(b)
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
		if host == "" {
			return nil, fmt.Errorf("%w: address %q has no host", ErrDisallowed, address)
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
