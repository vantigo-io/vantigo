package live

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

const appOrigin = "https://vantigo.example.com"

// served is a started hub behind an httptest server.
type served struct {
	hub    *Hub
	access *fakeAccess
	url    string // ws://…/api/v1/live
	stop   context.CancelFunc
}

func serve(t *testing.T, tune ...func(*Hub)) *served {
	t.Helper()
	access := newFakeAccess()
	h := New(Options{DatabaseURL: testdb.URL(t), Registry: testRegistry(t, "1"), Access: access, AppOrigin: appOrigin})
	for _, f := range tune {
		f(h)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.Start(ctx)
	srv := httptest.NewServer(h)
	t.Cleanup(func() {
		cancel()
		wctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := h.Wait(wctx); err != nil {
			t.Errorf("the hub did not stop: %v", err)
		}
		srv.Close()
	})
	return &served{hub: h, access: access, url: "ws" + strings.TrimPrefix(srv.URL, "http") + Path + "?v=1", stop: cancel}
}

// dial opens a socket as the browser would, signed in as user's session.
func (s *served) dial(t *testing.T, user, session uuid.UUID, header http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	if header == nil {
		header = http.Header{"Origin": {appOrigin}}
	}
	if user != uuid.Nil {
		header.Set("Cookie", "session="+user.String()+":"+session.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, s.url, &websocket.DialOptions{HTTPHeader: header})
	if c != nil {
		t.Cleanup(func() { _ = c.CloseNow() })
	}
	return c, resp, err
}

func read(t *testing.T, c *websocket.Conn) (map[string]any, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("frame %s: %v", b, err)
	}
	return m, nil
}

// closedWith reads until the socket closes and returns its close code.
func closedWith(t *testing.T, c *websocket.Conn) websocket.StatusCode {
	t.Helper()
	for {
		if _, err := read(t, c); err != nil {
			return websocket.CloseStatus(err)
		}
	}
}

func write(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := c.Write(context.Background(), websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

// The handshake (L2): the version, the origin rule — Sec-Fetch-Site
// same-origin, or the Origin equal to APP_URL's; a missing Origin is refused
// — and the session, checked before upgrading.
func TestHandler_Handshake(t *testing.T) {
	s := serve(t)
	user, session := s.access.person("alpha:view")
	for name, tc := range map[string]struct {
		header http.Header
		signed bool
		path   string
		status int
	}{
		"same origin":                {http.Header{"Origin": {appOrigin}}, true, "", http.StatusSwitchingProtocols},
		"Sec-Fetch-Site same-origin": {http.Header{"Origin": {"http://localhost:5173"}, "Sec-Fetch-Site": {"same-origin"}}, true, "", http.StatusSwitchingProtocols},
		"another origin":             {http.Header{"Origin": {"https://evil.example"}}, true, "", http.StatusForbidden},
		"cross-site":                 {http.Header{"Origin": {"https://evil.example"}, "Sec-Fetch-Site": {"cross-site"}}, true, "", http.StatusForbidden},
		"no Origin":                  {http.Header{}, true, "", http.StatusForbidden},
		"signed out":                 {http.Header{"Origin": {appOrigin}}, false, "", http.StatusUnauthorized},
		"another version":            {http.Header{"Origin": {appOrigin}}, true, "?v=2", http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			url := s.url
			if tc.path != "" {
				url = strings.TrimSuffix(url, "?v=1") + tc.path
			}
			if tc.signed {
				tc.header.Set("Cookie", "session="+user.String()+":"+session.String())
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, resp, _ := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: tc.header})
			if c != nil {
				_ = c.CloseNow()
			}
			if resp == nil || resp.StatusCode != tc.status {
				got := 0
				if resp != nil {
					got = resp.StatusCode
				}
				t.Errorf("status %d, want %d", got, tc.status)
			}
		})
	}
}

// Over the wire: a subscription, an event, and the frames' shapes (L4).
func TestHandler_SubscribeAndHear(t *testing.T) {
	s := serve(t)
	user, session := s.access.person("alpha:view")
	c, _, err := s.dial(t, user, session, nil)
	if err != nil {
		t.Fatal(err)
	}
	write(t, c, map[string]any{"type": "sub", "topic": "alpha.list"})
	if f, err := read(t, c); err != nil || f["type"] != "ok" {
		t.Fatalf("%v %v, want ok", f, err)
	}
	s.hub.dispatch(event("alpha.list", "row.changed", "9"))
	if f, err := read(t, c); err != nil || f["type"] != "event" || f["id"] != "9" {
		t.Errorf("%v %v, want the event", f, err)
	}
}

// The close codes: 1009 for a client frame over 4 KiB, 1007 for a malformed binary
// frame, 4429 for the 21st socket, 4401 when the session ends, 1001 at
// shutdown (L4, L11, L12).
func TestHandler_CloseCodes(t *testing.T) {
	t.Run("a frame over 4 KiB", func(t *testing.T) {
		s := serve(t)
		user, session := s.access.person()
		c, _, err := s.dial(t, user, session, nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = c.Write(context.Background(), websocket.MessageText, []byte(`{"type":"ping","pad":"`+strings.Repeat("x", 5000)+`"}`))
		if code := closedWith(t, c); code != websocket.StatusMessageTooBig {
			t.Errorf("closed with %d, want 1009", code)
		}
	})
	t.Run("a malformed binary frame", func(t *testing.T) {
		s := serve(t)
		user, session := s.access.person()
		c, _, err := s.dial(t, user, session, nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = c.Write(context.Background(), websocket.MessageBinary, []byte{1, 2, 3})
		if code := closedWith(t, c); code != websocket.StatusInvalidFramePayloadData {
			t.Errorf("closed with %d, want 1007", code)
		}
	})
	t.Run("too many sockets", func(t *testing.T) {
		s := serve(t, func(h *Hub) { h.limits.connsPerUser = 1 })
		user, session := s.access.person()
		if _, _, err := s.dial(t, user, session, nil); err != nil {
			t.Fatal(err)
		}
		c, _, err := s.dial(t, user, session, nil)
		if err != nil {
			t.Fatal(err)
		}
		if code := closedWith(t, c); code != CloseTooManyConnections {
			t.Errorf("closed with %d, want 4429", code)
		}
	})
	t.Run("the session ends", func(t *testing.T) {
		s := serve(t, func(h *Hub) { h.limits.recheck = 50 * time.Millisecond })
		user, session := s.access.person()
		c, _, err := s.dial(t, user, session, nil)
		if err != nil {
			t.Fatal(err)
		}
		s.access.endSession(session)
		if code := closedWith(t, c); code != CloseSessionEnded {
			t.Errorf("closed with %d, want 4401", code)
		}
	})
	t.Run("shutdown", func(t *testing.T) {
		s := serve(t)
		user, session := s.access.person()
		c, _, err := s.dial(t, user, session, nil)
		if err != nil {
			t.Fatal(err)
		}
		s.stop()
		if code := closedWith(t, c); code != websocket.StatusGoingAway {
			t.Errorf("closed with %d, want 1001", code)
		}
	})
}
