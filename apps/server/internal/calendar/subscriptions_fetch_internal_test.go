package calendar

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The fetch's guard and its sentences, below the harness: the harness's
// transport stands in for the guarded one, so the guard is tested here on the
// production transport with the resolver injected.

// cannedResolver answers every host with addrs, and counts nothing.
type cannedResolver []netip.Addr

func (r cannedResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return r, nil
}

func fetchingServer(rt http.RoundTripper) *server {
	return newServer(module.Deps{HTTPTransport: rt, Logger: slog.New(slog.DiscardHandler), Clock: time.Now})
}

func TestFetchSubscription_TheGuardRefusesAHostThatResolvesToAPrivateAddress(t *testing.T) {
	dialled := false
	dial := func(context.Context, string, string) (net.Conn, error) {
		dialled = true
		return nil, net.ErrClosed
	}
	for _, addr := range []string{"10.0.0.5", "127.0.0.1", "169.254.169.254", "::ffff:192.168.1.1"} {
		transport := guardedTransport(cannedResolver{netip.MustParseAddr(addr)}, dial)
		o := fetchingServer(transport).fetchSubscription(context.Background(), fetchTarget{id: 1, url: "https://calendar.example.com/team.ics"})
		if o.failure != fetchDisallowed {
			t.Errorf("a host resolving to %s answered %+v, want %q", addr, o, fetchDisallowed)
		}
	}
	if dialled {
		t.Error("the guard dialled a refused address")
	}
	if transport := subscriptionTransport(); transport.Proxy != nil || transport.DialContext == nil {
		t.Error("the production transport has a proxy or no guarded dialler")
	}
}

func TestFetchSubscription_ASlowAddressDidNotAnswerInTime(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	o := fetchingServer(srv.Client().Transport).fetchSubscription(ctx, fetchTarget{id: 1, url: srv.URL + "/slow.ics"})
	if o.failure != fetchTimedOut {
		t.Errorf("a slow address answered %+v, want %q", o, fetchTimedOut)
	}
}

func TestFetchSubscription_AnAddressThatRefusesTheConnectionCouldNotBeReached(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close()
	o := fetchingServer(http.DefaultTransport).fetchSubscription(context.Background(), fetchTarget{id: 1, url: addr + "/x.ics"})
	if o.failure != fetchUnreachable {
		t.Errorf("a closed port answered %+v, want %q", o, fetchUnreachable)
	}
}

func TestFetchSubscription_KeepsTheValidatorsAndTheRefreshInterval(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Last-Modified", "Tue, 15 Sep 2026 10:00:00 GMT")
		_, _ = w.Write([]byte("\r\n  BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//EN\r\nREFRESH-INTERVAL;VALUE=DURATION:P1D\r\nX-PUBLISHED-TTL:PT1H\r\nEND:VCALENDAR\r\n"))
	}))
	defer srv.Close()
	o := fetchingServer(srv.Client().Transport).fetchSubscription(context.Background(), fetchTarget{id: 1, url: srv.URL})
	if o.failure != "" || o.etag == nil || *o.etag != `"abc"` || o.lastModified == nil || *o.lastModified != "Tue, 15 Sep 2026 10:00:00 GMT" || o.refresh != 24*time.Hour {
		t.Errorf("the outcome = %+v", o)
	}
}
