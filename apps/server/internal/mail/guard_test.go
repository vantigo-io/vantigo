package mail

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

// fakeResolver lets a test hand the guard a canned DNS answer instead of
// touching real DNS.
type fakeResolver struct {
	addrs []netip.Addr
	err   error
}

func (r fakeResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.addrs, nil
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("netip.ParseAddr(%q): %v", s, err)
	}
	return addr
}

// TestDestinationGuard_RejectsDisallowedLiterals mirrors the ranges rejected
// by apps/communications/backend/Communications.Module/Services/SmtpDestinationGuard.cs's
// IsDisallowedDestination: loopback, private (RFC1918 and fc00::/7),
// link-local (including the 169.254.169.254 cloud metadata address), CGNAT
// 100.64/10, 0.0.0.0/8 and unspecified addresses.
func TestDestinationGuard_RejectsDisallowedLiterals(t *testing.T) {
	t.Parallel()
	cases := []string{
		"127.0.0.1",       // loopback
		"::1",             // loopback (v6)
		"10.1.2.3",        // RFC1918
		"172.16.0.1",      // RFC1918
		"172.31.255.255",  // RFC1918
		"192.168.1.1",     // RFC1918
		"fd00::1",         // RFC4193 unique local (fc00::/7)
		"fc00::1",         // RFC4193 unique local (fc00::/7)
		"169.254.1.1",     // link-local
		"169.254.169.254", // cloud metadata address
		"100.64.0.1",      // CGNAT
		"100.127.255.255", // CGNAT
		"0.0.0.0",         // this network / unspecified
		"0.1.2.3",         // 0.0.0.0/8
		"::",              // unspecified (v6)
		"192.0.0.1",       // IETF protocol assignments
		"240.0.0.1",       // reserved
		"255.255.255.255", // reserved/broadcast
		"fe80::1",         // link-local (v6)
		"ff02::1",         // multicast (v6)
	}
	for _, host := range cases {
		host := host
		t.Run(host, func(t *testing.T) {
			t.Parallel()
			g := newDestinationGuard(fakeResolver{addrs: []netip.Addr{mustAddr(t, host)}})
			_, err := g.Check(context.Background(), host)
			if !errors.Is(err, ErrDestinationRejected) {
				t.Fatalf("Check(%q) = %v, want ErrDestinationRejected", host, err)
			}
		})
	}
}

// TestDestinationGuard_AcceptsPublicLiteral is the guard's positive case:
// an ordinary public address is never rejected.
func TestDestinationGuard_AcceptsPublicLiteral(t *testing.T) {
	t.Parallel()
	g := newDestinationGuard(fakeResolver{})
	addr, err := g.Check(context.Background(), "93.184.216.34")
	if err != nil {
		t.Fatalf("Check() = %v, want nil", err)
	}
	if addr.String() != "93.184.216.34" {
		t.Fatalf("Check() addr = %v, want 93.184.216.34", addr)
	}
}

// TestDestinationGuard_UsesInjectedResolver proves resolution goes through
// the injected resolver — never real DNS — and that the guard checks every
// resolved address, not only the first.
func TestDestinationGuard_UsesInjectedResolver(t *testing.T) {
	t.Parallel()

	t.Run("rejects a resolved private address", func(t *testing.T) {
		t.Parallel()
		g := newDestinationGuard(fakeResolver{addrs: []netip.Addr{
			mustAddr(t, "203.0.113.5"),
			mustAddr(t, "10.0.0.5"),
		}})
		_, err := g.Check(context.Background(), "mail.example.test")
		if !errors.Is(err, ErrDestinationRejected) {
			t.Fatalf("Check() = %v, want ErrDestinationRejected", err)
		}
	})

	t.Run("accepts a resolved public address", func(t *testing.T) {
		t.Parallel()
		g := newDestinationGuard(fakeResolver{addrs: []netip.Addr{mustAddr(t, "203.0.113.5")}})
		addr, err := g.Check(context.Background(), "mail.example.test")
		if err != nil {
			t.Fatalf("Check() = %v, want nil", err)
		}
		if addr.String() != "203.0.113.5" {
			t.Fatalf("Check() addr = %v, want 203.0.113.5", addr)
		}
	})
}

func TestDestinationGuard_RejectsResolutionFailure(t *testing.T) {
	t.Parallel()
	g := newDestinationGuard(fakeResolver{err: errors.New("no such host")})
	_, err := g.Check(context.Background(), "mail.example.test")
	if !errors.Is(err, ErrDestinationRejected) {
		t.Fatalf("Check() = %v, want ErrDestinationRejected", err)
	}
}

func TestDestinationGuard_RejectsNoAddresses(t *testing.T) {
	t.Parallel()
	g := newDestinationGuard(fakeResolver{addrs: nil})
	_, err := g.Check(context.Background(), "mail.example.test")
	if !errors.Is(err, ErrDestinationRejected) {
		t.Fatalf("Check() = %v, want ErrDestinationRejected", err)
	}
}

func TestDestinationGuard_RejectsEmptyHost(t *testing.T) {
	t.Parallel()
	g := newDestinationGuard(fakeResolver{})
	_, err := g.Check(context.Background(), "")
	if !errors.Is(err, ErrDestinationRejected) {
		t.Fatalf("Check() = %v, want ErrDestinationRejected", err)
	}
}

// TestDestinationGuard_AllowLoopbackForTests proves the test-only hook —
// and only it — can make the guard accept loopback, and that turning it off
// again restores the default rejection.
func TestDestinationGuard_AllowLoopbackForTests(t *testing.T) {
	restore := AllowLoopbackForTests(nil)
	g := newDestinationGuard(fakeResolver{})
	if _, err := g.Check(context.Background(), "127.0.0.1"); err != nil {
		t.Fatalf("Check() = %v, want nil with the loopback hook enabled", err)
	}
	restore()

	if _, err := g.Check(context.Background(), "127.0.0.1"); !errors.Is(err, ErrDestinationRejected) {
		t.Fatalf("Check() = %v, want ErrDestinationRejected once restored", err)
	}
}
