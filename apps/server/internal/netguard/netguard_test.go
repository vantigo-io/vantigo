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

// fakeResolver lets a test hand DialContext a canned DNS answer instead of
// touching real DNS, and records whether it was called at all — proving a
// literal IP host never reaches it.
type fakeResolver struct {
	addrs  []netip.Addr
	err    error
	called bool
}

func (r *fakeResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	r.called = true
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
