package netguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("netip.ParseAddr(%q): %v", s, err)
	}
	return addr
}

// TestDisallowed_RejectsForbiddenRanges mirrors the ranges rejected by
// apps/communications/backend/Communications.Module/Services/SmtpDestinationGuard.cs's
// IsDisallowedDestination: loopback, private (RFC1918 and fc00::/7),
// link-local (including the 169.254.169.254 cloud metadata address), CGNAT
// 100.64/10, 0.0.0.0/8 and unspecified addresses. These cases moved here
// verbatim from internal/mail's own guard test, plus two IPv4-mapped IPv6
// cases proving Disallowed unwraps before classifying.
func TestDisallowed_RejectsForbiddenRanges(t *testing.T) {
	t.Parallel()
	cases := []string{
		"127.0.0.1",              // loopback
		"::1",                    // loopback (v6)
		"10.1.2.3",               // RFC1918
		"172.16.0.1",             // RFC1918
		"172.31.255.255",         // RFC1918
		"192.168.1.1",            // RFC1918
		"fd00::1",                // RFC4193 unique local (fc00::/7)
		"fc00::1",                // RFC4193 unique local (fc00::/7)
		"169.254.1.1",            // link-local
		"169.254.169.254",        // cloud metadata address
		"100.64.0.1",             // CGNAT
		"100.127.255.255",        // CGNAT
		"0.0.0.0",                // this network / unspecified
		"0.1.2.3",                // 0.0.0.0/8
		"::",                     // unspecified (v6)
		"192.0.0.1",              // IETF protocol assignments
		"240.0.0.1",              // reserved
		"255.255.255.255",        // reserved/broadcast
		"fe80::1",                // link-local (v6)
		"ff02::1",                // multicast (v6)
		"::ffff:10.0.0.1",        // IPv4-mapped IPv6 of a private address
		"::ffff:169.254.169.254", // IPv4-mapped IPv6 of the cloud metadata address

		// IPv6 forms that embed (or obfuscate) an IPv4 address, so a
		// translation gateway cannot be talked into reaching a forbidden
		// IPv4 destination. See TestDisallowed_AcceptsPublicAddressEmbeddedInIPv6
		// for the mirror-image cases where the embedded address is public.
		"64:ff9b::a9fe:a9fe", // NAT64 well-known prefix, embedding the cloud metadata address
		"64:ff9b::a00:1",     // NAT64 well-known prefix, embedding 10.0.0.1
		"::a00:1",            // deprecated IPv4-compatible ::/96, embedding 10.0.0.1
		"2002:a00:1::",       // 6to4, embedding 10.0.0.1
		"64:ff9b:1::1",       // RFC 8215 local-use NAT64 /48 - refused outright, embedding position is operator-defined
		"2001::1",            // Teredo 2001::/32 - refused outright, embedding is obfuscated
		"64:ff9b::7f00:1",    // NAT64, embedding loopback 127.0.0.1
		"64:ff9b::e000:1",    // NAT64, embedding multicast 224.0.0.1
	}
	for _, host := range cases {
		host := host
		t.Run(host, func(t *testing.T) {
			t.Parallel()
			if !Disallowed(mustAddr(t, host)) {
				t.Fatalf("Disallowed(%q) = false, want true", host)
			}
		})
	}
}

// TestDisallowed_AcceptsPublicAddress is the guard's positive case: an
// ordinary public address is never rejected.
func TestDisallowed_AcceptsPublicAddress(t *testing.T) {
	t.Parallel()
	if Disallowed(mustAddr(t, "93.184.216.34")) {
		t.Fatalf("Disallowed(93.184.216.34) = true, want false")
	}
}

// TestDisallowed_AcceptsPublicAddressEmbeddedInIPv6 proves the embedded-IPv4
// classification lets a public address through: a NAT64/6to4-only host must
// keep working, so only the embedded address's own forbidden-ness matters,
// not the mere fact of embedding.
func TestDisallowed_AcceptsPublicAddressEmbeddedInIPv6(t *testing.T) {
	t.Parallel()
	cases := []string{
		"64:ff9b::808:808",     // NAT64 well-known prefix, embedding 8.8.8.8
		"::808:808",            // deprecated IPv4-compatible ::/96, embedding 8.8.8.8
		"2002:808:808::",       // 6to4, embedding 8.8.8.8
		"2a00:1450:4001::200e", // an ordinary global IPv6 address, not any embedding form
	}
	for _, host := range cases {
		host := host
		t.Run(host, func(t *testing.T) {
			t.Parallel()
			if Disallowed(mustAddr(t, host)) {
				t.Fatalf("Disallowed(%q) = true, want false", host)
			}
		})
	}
}

// TestDisallowed_RejectsInvalidAddr proves the zero-value/invalid
// netip.Addr is disallowed — there is nothing valid to dial.
func TestDisallowed_RejectsInvalidAddr(t *testing.T) {
	t.Parallel()
	if !Disallowed(netip.Addr{}) {
		t.Fatalf("Disallowed(netip.Addr{}) = false, want true")
	}
}

// fakeResolver lets a test hand DialContext a canned DNS answer instead of
// touching real DNS, and records whether it was called at all — proving a
// literal IP host never reaches it.
type fakeResolver struct {
	addrs  []netip.Addr
	err    error
	called bool
	// ctx records the context.Context LookupNetIP was called with, so a
	// test can prove the caller's context (including its cancellation)
	// reaches the resolver rather than a fresh one.
	ctx context.Context
}

func (r *fakeResolver) LookupNetIP(ctx context.Context, _ string, _ string) ([]netip.Addr, error) {
	r.called = true
	r.ctx = ctx
	if r.err != nil {
		return nil, r.err
	}
	return r.addrs, nil
}

// fakeConn is the net.Conn a fake dial func returns; DialContext never
// inspects it beyond returning it, so a zero-value stand-in is enough.
type fakeConn struct{ net.Conn }

func TestDialContext_DialsPublicAddress(t *testing.T) {
	t.Parallel()
	resolver := &fakeResolver{addrs: []netip.Addr{mustAddr(t, "203.0.113.5")}}

	var dialedNetwork, dialedAddr string
	dial := func(_ context.Context, network, addr string) (net.Conn, error) {
		dialedNetwork, dialedAddr = network, addr
		return fakeConn{}, nil
	}

	conn, err := DialContext(resolver, dial)(context.Background(), "tcp", "mail.example.test:25")
	if err != nil {
		t.Fatalf("DialContext() = %v, want nil", err)
	}
	if conn == nil {
		t.Fatalf("DialContext() conn = nil, want the dial func's connection")
	}
	if dialedNetwork != "tcp" {
		t.Fatalf("dialed network = %q, want tcp", dialedNetwork)
	}
	if dialedAddr != "203.0.113.5:25" {
		t.Fatalf("dialed addr = %q, want 203.0.113.5:25", dialedAddr)
	}
}

func TestDialContext_RefusesWhenAnyResolvedAddressIsDisallowed(t *testing.T) {
	t.Parallel()
	resolver := &fakeResolver{addrs: []netip.Addr{
		mustAddr(t, "203.0.113.5"),
		mustAddr(t, "10.0.0.5"),
	}}

	dialed := false
	dial := func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return fakeConn{}, nil
	}

	_, err := DialContext(resolver, dial)(context.Background(), "tcp", "mail.example.test:25")
	if !errors.Is(err, ErrDisallowed) {
		t.Fatalf("DialContext() = %v, want ErrDisallowed", err)
	}
	if dialed {
		t.Fatalf("dial func was called, want it never called when a resolved address is disallowed")
	}
}

func TestDialContext_LiteralIPHostSkipsLookup(t *testing.T) {
	t.Parallel()
	resolver := &fakeResolver{}

	var dialedAddr string
	dial := func(_ context.Context, _ string, addr string) (net.Conn, error) {
		dialedAddr = addr
		return fakeConn{}, nil
	}

	_, err := DialContext(resolver, dial)(context.Background(), "tcp", "203.0.113.5:25")
	if err != nil {
		t.Fatalf("DialContext() = %v, want nil", err)
	}
	if resolver.called {
		t.Fatalf("resolver.LookupNetIP was called for a literal IP host, want no lookup")
	}
	if dialedAddr != "203.0.113.5:25" {
		t.Fatalf("dialed addr = %q, want 203.0.113.5:25", dialedAddr)
	}
}

func TestDialContext_PropagatesResolutionError(t *testing.T) {
	t.Parallel()
	resolveErr := errors.New("no such host")
	resolver := &fakeResolver{err: resolveErr}

	dial := func(context.Context, string, string) (net.Conn, error) {
		t.Fatalf("dial func was called, want it never called when resolution fails")
		return nil, nil
	}

	_, err := DialContext(resolver, dial)(context.Background(), "tcp", "mail.example.test:25")
	if err == nil {
		t.Fatalf("DialContext() = nil, want the resolution error propagated")
	}
	if !errors.Is(err, resolveErr) {
		t.Fatalf("DialContext() = %v, want it to wrap %v", err, resolveErr)
	}
}

func TestDialContext_RefusesEmptyAnswer(t *testing.T) {
	t.Parallel()
	resolver := &fakeResolver{addrs: nil}

	dial := func(context.Context, string, string) (net.Conn, error) {
		t.Fatalf("dial func was called, want it never called for an empty answer")
		return nil, nil
	}

	_, err := DialContext(resolver, dial)(context.Background(), "tcp", "mail.example.test:25")
	if !errors.Is(err, ErrDisallowed) {
		t.Fatalf("DialContext() = %v, want ErrDisallowed", err)
	}
}

func TestDialContext_RefusesInvalidAddress(t *testing.T) {
	t.Parallel()
	resolver := &fakeResolver{}
	dial := func(context.Context, string, string) (net.Conn, error) {
		t.Fatalf("dial func was called, want it never called for an invalid address")
		return nil, nil
	}

	_, err := DialContext(resolver, dial)(context.Background(), "tcp", "not-a-host-port")
	if err == nil {
		t.Fatalf("DialContext() = nil, want an error for an address with no port")
	}
}

// TestDialContext_RefusesEmptyHost proves DialContext refuses an empty host
// itself — it does not depend on the resolver (real or fake) happening to
// reject an empty string.
func TestDialContext_RefusesEmptyHost(t *testing.T) {
	t.Parallel()
	resolver := &fakeResolver{}
	dial := func(context.Context, string, string) (net.Conn, error) {
		t.Fatalf("dial func was called, want it never called for an empty host")
		return nil, nil
	}

	_, err := DialContext(resolver, dial)(context.Background(), "tcp", ":25")
	if !errors.Is(err, ErrDisallowed) {
		t.Fatalf("DialContext() = %v, want ErrDisallowed", err)
	}
	if resolver.called {
		t.Fatalf("resolver.LookupNetIP was called for an empty host")
	}
}

// TestDialContext_RefusesDisallowedLiteral proves the literal-IP path (no
// resolver lookup) is checked against the guard exactly like a resolved
// address.
func TestDialContext_RefusesDisallowedLiteral(t *testing.T) {
	t.Parallel()
	resolver := &fakeResolver{}
	dialed := false
	dial := func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return fakeConn{}, nil
	}

	_, err := DialContext(resolver, dial)(context.Background(), "tcp", "10.0.0.5:25")
	if !errors.Is(err, ErrDisallowed) {
		t.Fatalf("DialContext() = %v, want ErrDisallowed", err)
	}
	if dialed {
		t.Fatalf("dial func was called, want it never called for a disallowed literal")
	}
	if resolver.called {
		t.Fatalf("resolver.LookupNetIP was called for a literal IP host")
	}
}

// TestDialContext_RefusesIPv4MappedLiteral proves a literal IPv4-mapped
// IPv6 host is unwrapped and classified like the plain IPv4 address it
// carries.
func TestDialContext_RefusesIPv4MappedLiteral(t *testing.T) {
	t.Parallel()
	resolver := &fakeResolver{}
	dial := func(context.Context, string, string) (net.Conn, error) {
		t.Fatalf("dial func was called, want it never called for a disallowed literal")
		return nil, nil
	}

	_, err := DialContext(resolver, dial)(context.Background(), "tcp", "[::ffff:10.0.0.1]:443")
	if !errors.Is(err, ErrDisallowed) {
		t.Fatalf("DialContext() = %v, want ErrDisallowed", err)
	}
}

// TestDialContext_RefusesZonedLinkLocalLiteral proves a literal link-local
// host carrying a zone identifier (as a network interface would hand back)
// is still classified as link-local.
func TestDialContext_RefusesZonedLinkLocalLiteral(t *testing.T) {
	t.Parallel()
	resolver := &fakeResolver{}
	dial := func(context.Context, string, string) (net.Conn, error) {
		t.Fatalf("dial func was called, want it never called for a disallowed literal")
		return nil, nil
	}

	_, err := DialContext(resolver, dial)(context.Background(), "tcp", "[fe80::1%eth0]:443")
	if !errors.Is(err, ErrDisallowed) {
		t.Fatalf("DialContext() = %v, want ErrDisallowed", err)
	}
}

// TestDialContext_PropagatesCancellationToResolverAndDial proves the
// caller's context — including its cancellation — reaches both the
// resolver and the dial func, rather than DialContext substituting a fresh
// one at either step.
func TestDialContext_PropagatesCancellationToResolverAndDial(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resolver := &fakeResolver{addrs: []netip.Addr{mustAddr(t, "203.0.113.5")}}

	var dialCtx context.Context
	dial := func(ctx context.Context, _ string, _ string) (net.Conn, error) {
		dialCtx = ctx
		return fakeConn{}, nil
	}

	_, _ = DialContext(resolver, dial)(ctx, "tcp", "mail.example.test:25")

	if resolver.ctx == nil || resolver.ctx.Err() != context.Canceled {
		t.Fatalf("resolver context = %v, want a context already Canceled", resolver.ctx)
	}
	if dialCtx == nil || dialCtx.Err() != context.Canceled {
		t.Fatalf("dial context = %v, want a context already Canceled", dialCtx)
	}
}
