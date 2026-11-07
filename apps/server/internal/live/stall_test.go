package live

import (
	"context"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// stallProxy forwards TCP to target until stall, after which the connections
// it already holds go silent — bytes are swallowed both ways, nothing is
// closed — as a connection does behind a NAT that forgot it. Connections made
// after the stall are forwarded normally.
type stallProxy struct {
	ln     net.Listener
	target string
	mu     sync.Mutex
	held   []*atomic.Bool
}

func newStallProxy(t *testing.T, target string) *stallProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &stallProxy{ln: ln, target: target}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			in, err := ln.Accept()
			if err != nil {
				return
			}
			out, err := net.Dial("tcp", target)
			if err != nil {
				_ = in.Close()
				continue
			}
			stalled := &atomic.Bool{}
			p.mu.Lock()
			p.held = append(p.held, stalled)
			p.mu.Unlock()
			pump := func(dst, src net.Conn) {
				buf := make([]byte, 32*1024)
				for {
					n, err := src.Read(buf)
					if n > 0 && !stalled.Load() {
						if _, werr := dst.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						_ = dst.Close()
						return
					}
				}
			}
			go pump(out, in)
			go pump(in, out)
			t.Cleanup(func() { _ = in.Close(); _ = out.Close() })
		}
	}()
	return p
}

func (p *stallProxy) stall() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.held {
		s.Store(true)
	}
}

// A listener whose connection stalls — no error, no FIN, just silence — is
// found out by its idle probe, degrades its sockets, reconnects and resyncs
// them with reason reconnect; events flow again after. A quiet but healthy
// connection passes the probe and stays up.
func TestHub_AStalledListenerIsProbedAndReplaced(t *testing.T) {
	direct := testdb.URL(t)
	u, err := url.Parse(direct)
	if err != nil {
		t.Fatal(err)
	}
	proxy := newStallProxy(t, u.Host)
	u.Host = proxy.ln.Addr().String()

	access := newFakeAccess()
	h := startHub(t, u.String(), access, testRegistry(t), func(h *Hub) {
		h.limits.listenIdle, h.limits.probe = 100*time.Millisecond, 200*time.Millisecond
	})
	user, session := access.person("alpha:view")
	c := attach(t, h, user, session)
	send(h, c, map[string]any{"type": "sub", "topic": "alpha.list"})
	frame(t, c)

	time.Sleep(350 * time.Millisecond) // several idle probes on a healthy connection
	noFrame(t, c)
	notifier := connect(t, direct)
	if err := Notify(context.Background(), notifier, "alpha.list", "row.changed", "before"); err != nil {
		t.Fatal(err)
	}
	if f := nextEvent(t, c); f["id"] != "before" {
		t.Fatalf("%v, want the event on a probed, healthy listener", f)
	}

	proxy.stall()
	if f := frame(t, c); f["type"] != "degraded" {
		t.Fatalf("%v, want degraded once the probe goes unanswered", f)
	}
	if f := frame(t, c); f["type"] != "resync" || f["reason"] != "reconnect" {
		t.Fatalf("%v, want resync reconnect on a new connection", f)
	}
	if err := Notify(context.Background(), notifier, "alpha.list", "row.changed", "after"); err != nil {
		t.Fatal(err)
	}
	if f := nextEvent(t, c); f["id"] != "after" {
		t.Errorf("%v, want the event after the reconnect", f)
	}
}
