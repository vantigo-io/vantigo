package live

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// stationAccess is fakeAccess that also answers stations (contracts.
// StationAccess): a request's station is its "station" cookie, a station's
// id; a "session" cookie beside it makes the handshake a person's.
type stationAccess struct {
	*fakeAccess
	smu      sync.Mutex
	stations map[uuid.UUID]bool   // live
	allowed  map[uuid.UUID]string // the one address a station is reached from, "" for any
	seen     map[uuid.UUID]string // the address the last re-check was given
}

func (a *stationAccess) CheckStation(r *http.Request) (uuid.UUID, bool, error) {
	c, err := r.Cookie("station")
	if err != nil {
		return uuid.Nil, false, nil
	}
	if _, err := r.Cookie("session"); err == nil {
		return uuid.Nil, false, nil
	}
	id, err := uuid.Parse(c.Value)
	a.smu.Lock()
	defer a.smu.Unlock()
	if err != nil || !a.stations[id] {
		return uuid.Nil, true, contracts.ErrUnauthenticated
	}
	return id, true, nil
}

func (a *stationAccess) StationKnown(_ context.Context, id uuid.UUID) (bool, error) {
	a.smu.Lock()
	defer a.smu.Unlock()
	_, ok := a.stations[id]
	return ok, nil
}

func (a *stationAccess) StationLive(_ context.Context, id uuid.UUID, ip string) (bool, error) {
	a.smu.Lock()
	defer a.smu.Unlock()
	a.seen[id] = ip
	return a.stations[id] && (a.allowed[id] == "" || a.allowed[id] == ip), nil
}

// stationRegistry serves alpha.list (people only) and alpha.center, which a
// station may hear for the centers in centers[station] and a person holding
// alpha:view for any.
func stationRegistry(t testing.TB, centers map[uuid.UUID][]string, mu *sync.Mutex) *Registry {
	t.Helper()
	reg := NewRegistry()
	err := reg.Register("alpha",
		Topic{Kind: "alpha.list", Authorize: func(ctx context.Context, key string) error {
			if key != "" || !HasPermission(ctx, "alpha:view") {
				return ErrForbidden
			}
			return nil
		}},
		Topic{Kind: "alpha.center", Presence: true, Station: true, Authorize: func(ctx context.Context, key string) error {
			if station, ok := StationID(ctx); ok {
				mu.Lock()
				defer mu.Unlock()
				for _, c := range centers[station] {
					if c == key {
						return nil
					}
				}
				return ErrForbidden
			}
			if !HasPermission(ctx, "alpha:view") {
				return ErrForbidden
			}
			return nil
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// The station path (manufacturing phase 2 §17): the station cookie alone
// opens a socket; it hears only the topics marked for stations, for its own
// keys; it announces no presence; a revoked station's socket closes with 4401
// at the re-check, and a subscription the station no longer reaches is
// dropped.
func TestHandler_StationPath(t *testing.T) {
	access := &stationAccess{fakeAccess: newFakeAccess(), stations: map[uuid.UUID]bool{}, allowed: map[uuid.UUID]string{}, seen: map[uuid.UUID]string{}}
	station, other := uuid.New(), uuid.New()
	access.stations[station], access.stations[other] = true, true
	var mu sync.Mutex
	centers := map[uuid.UUID][]string{station: {"1", "2"}, other: {"3"}}
	h := New(Options{DatabaseURL: testdb.URL(t), Registry: stationRegistry(t, centers, &mu), Access: access, AppOrigin: appOrigin})
	h.limits.recheck = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	h.Start(ctx)
	srv := httptest.NewServer(h)
	t.Cleanup(func() {
		cancel()
		wctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = h.Wait(wctx)
		srv.Close()
	})
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + Path + "?v=1"
	dial := func(cookie string) (*websocket.Conn, *http.Response, error) {
		dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer dcancel()
		c, resp, err := websocket.Dial(dctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {appOrigin}, "Cookie": {cookie}}})
		if c != nil {
			t.Cleanup(func() { _ = c.CloseNow() })
		}
		return c, resp, err
	}

	// An unknown station is refused before upgrading.
	if _, resp, err := dial("station=" + uuid.NewString()); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an unknown station: %v %v", resp, err)
	}

	c, _, err := dial("station=" + station.String())
	if err != nil {
		t.Fatal(err)
	}
	// next is the next frame that is not a presence set.
	next := func() (map[string]any, error) {
		for {
			f, err := read(t, c)
			if err != nil || f["type"] != "presence" {
				return f, err
			}
		}
	}
	expect := func(topic, typ string) {
		t.Helper()
		write(t, c, map[string]any{"type": "sub", "topic": topic})
		f, err := next()
		if err != nil || f["type"] != typ {
			t.Fatalf("sub %s: %v %v, want %s", topic, f, err, typ)
		}
	}
	expect("alpha.center:1", "ok")
	expect("alpha.center:3", "denied") // another station's center
	expect("alpha.list", "denied")     // a topic not marked for stations

	// A station's socket announces no presence.
	write(t, c, map[string]any{"type": "presence", "topic": "alpha.center:1", "state": "viewing"})
	h.mu.Lock()
	members := len(h.presence.members("alpha.center:1"))
	h.mu.Unlock()
	if members != 0 {
		t.Fatalf("%d presence members from a station, want none", members)
	}

	// The station loses center 1: the re-check drops the subscription.
	mu.Lock()
	centers[station] = []string{"2"}
	mu.Unlock()
	f, err := next()
	if err != nil || f["type"] != "denied" || f["topic"] != "alpha.center:1" {
		t.Fatalf("after losing the center: %v %v, want denied", f, err)
	}

	// Revoked, the station's socket closes with 4401.
	access.smu.Lock()
	access.stations[station] = false
	access.smu.Unlock()
	if code := closedWith(t, c); code != CloseSessionEnded {
		t.Fatalf("closed with %d, want 4401", code)
	}

	// The re-check is given the address of the socket's handshake, so a
	// station whose allowlist no longer admits it closes with 4401 too.
	fenced := uuid.New()
	access.smu.Lock()
	access.stations[fenced] = true
	access.smu.Unlock()
	fc, _, err := dial("station=" + fenced.String())
	if err != nil {
		t.Fatal(err)
	}
	access.smu.Lock()
	access.allowed[fenced] = "10.0.0.1"
	access.smu.Unlock()
	if code := closedWith(t, fc); code != CloseSessionEnded {
		t.Fatalf("outside the allowlist the socket closed with %d, want 4401", code)
	}
	access.smu.Lock()
	seen := access.seen[fenced]
	access.smu.Unlock()
	if seen != "127.0.0.1" {
		t.Fatalf("the re-check was given %q, want the handshake's address", seen)
	}

	// A session cookie beside the station's makes the handshake a person's.
	user, session := access.person("alpha:view")
	p, _, err := dial("station=" + other.String() + "; session=" + user.String() + ":" + session.String())
	if err != nil {
		t.Fatal(err)
	}
	write(t, p, map[string]any{"type": "sub", "topic": "alpha.list"})
	if f, err := read(t, p); err != nil || f["type"] != "ok" {
		t.Fatalf("a person's socket: %v %v", f, err)
	}
}
