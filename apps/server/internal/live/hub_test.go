package live

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// fakeAccess is a contracts.Access over a table: who holds which keys, which
// sessions are live. A request's session is its "session" cookie,
// "<user>:<session>".
type fakeAccess struct {
	mu       sync.Mutex
	held     map[uuid.UUID]map[string]bool
	sessions map[uuid.UUID]uuid.UUID // session -> user
	holds    atomic.Int32            // UserHolds calls
}

func newFakeAccess() *fakeAccess {
	return &fakeAccess{held: map[uuid.UUID]map[string]bool{}, sessions: map[uuid.UUID]uuid.UUID{}}
}

// person signs a person in holding keys and returns their user and session.
func (a *fakeAccess) person(keys ...string) (uuid.UUID, uuid.UUID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	user, session := uuid.New(), uuid.New()
	a.held[user] = map[string]bool{}
	for _, k := range keys {
		a.held[user][k] = true
	}
	a.sessions[session] = user
	return user, session
}

func (a *fakeAccess) revoke(user uuid.UUID, key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.held[user], key)
}

func (a *fakeAccess) endSession(session uuid.UUID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, session)
}

func (a *fakeAccess) Check(r *http.Request, _ contracts.Rule) (contracts.Principal, error) {
	c, err := r.Cookie("session")
	if err != nil {
		return contracts.Principal{}, contracts.ErrUnauthenticated
	}
	userRaw, sessionRaw, _ := strings.Cut(c.Value, ":")
	user, err1 := uuid.Parse(userRaw)
	session, err2 := uuid.Parse(sessionRaw)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err1 != nil || err2 != nil || a.sessions[session] != user {
		return contracts.Principal{}, contracts.ErrUnauthenticated
	}
	return contracts.Principal{UserID: user, SessionID: session}, nil
}

func (a *fakeAccess) Reject(w http.ResponseWriter, _ *http.Request, _ contracts.Rule, _ error) {
	w.WriteHeader(http.StatusUnauthorized)
}

func (a *fakeAccess) UserHolds(_ context.Context, user uuid.UUID, keys ...string) (bool, error) {
	a.holds.Add(1)
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, k := range keys {
		if !a.held[user][k] {
			return false, nil
		}
	}
	return len(keys) > 0, nil
}

// UserSatisfies is not what this fake is about: false.
func (a *fakeAccess) UserSatisfies(context.Context, uuid.UUID, contracts.Rule) (bool, error) {
	return false, nil
}

func (a *fakeAccess) SessionLive(_ context.Context, session uuid.UUID) (uuid.UUID, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	user, ok := a.sessions[session]
	return user, ok, nil
}

// testRegistry serves alpha.list (alpha:view, no key, no presence) and
// alpha.item (alpha:view, a key in items, presence).
func testRegistry(t testing.TB, items ...string) *Registry {
	t.Helper()
	reg := NewRegistry()
	err := reg.Register("alpha",
		Topic{Kind: "alpha.list", Authorize: func(ctx context.Context, key string) error {
			if key != "" || !HasPermission(ctx, "alpha:view") {
				return ErrForbidden
			}
			return nil
		}},
		Topic{Kind: "alpha.item", Presence: true, Authorize: func(ctx context.Context, key string) error {
			for _, it := range items {
				if it == key && HasPermission(ctx, "alpha:view") {
					return nil
				}
			}
			return ErrForbidden
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// testHub is a hub that never Starts: its listener is marked up, so nothing
// degrades, and every test drives it through its own methods.
func testHub(t testing.TB, access contracts.Access, reg *Registry) *Hub {
	t.Helper()
	h := New(Options{Registry: reg, Access: access})
	h.listenerUp = true
	return h
}

// frame is a frame off c's queue, decoded; it fails the test after a second.
func frame(t testing.TB, c *client) map[string]any {
	t.Helper()
	select {
	case b := <-c.send:
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("frame %s: %v", b, err)
		}
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("no frame")
		return nil
	}
}

// noFrame fails the test when c's queue holds a frame.
func noFrame(t testing.TB, c *client) {
	t.Helper()
	select {
	case b := <-c.send:
		t.Fatalf("unexpected frame %s", b)
	case <-time.After(50 * time.Millisecond):
	}
}

func send(h *Hub, c *client, v map[string]any) {
	b, _ := json.Marshal(v)
	h.handleFrame(context.Background(), c, b)
}

func attach(t testing.TB, h *Hub, user, session uuid.UUID) *client {
	t.Helper()
	c, err := h.attach(user, session)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.detach(c) })
	return c
}

func event(topic, kind, id string) string {
	b, _ := json.Marshal(payload{T: topic, K: kind, ID: id})
	return string(b)
}

func TestTopicGrammar(t *testing.T) {
	for topic, ok := range map[string]bool{
		"communications.inbox": true,
		"communications.conversation:0b9e2c1e-5f1c-4c55-9f6c-1d6a0a2b3c4d": true,
		"knowledge.tree":        true,
		"a.b-c:K_9-x":           true,
		"Communications.inbox":  false,
		"communications":        false,
		"communications.inbox:": false,
		"communications.inbox:" + strings.Repeat("k", 65): false,
		"communications.inbox:a b":                        false,
		"communications.in_box":                           false,
	} {
		if ValidTopic(topic) != ok {
			t.Errorf("ValidTopic(%q) = %v, want %v", topic, !ok, ok)
		}
	}
}

// An event reaches exactly the sockets subscribed to its topic, as an event
// frame carrying only the identifier.
func TestHub_RoutesAnEventToItsTopicsSubscribers(t *testing.T) {
	access := newFakeAccess()
	h := testHub(t, access, testRegistry(t, "1"))
	user, session := access.person("alpha:view")
	listening := attach(t, h, user, session)
	other := attach(t, h, user, session)

	send(h, listening, map[string]any{"type": "sub", "topic": "alpha.list"})
	if f := frame(t, listening); f["type"] != "ok" || f["topic"] != "alpha.list" {
		t.Fatalf("sub answered %v", f)
	}
	send(h, other, map[string]any{"type": "sub", "topic": "alpha.item:1"})
	frame(t, other) // ok
	frame(t, other) // the presence set

	h.dispatch(event("alpha.list", "row.changed", "42"))
	if f := frame(t, listening); f["type"] != "event" || f["topic"] != "alpha.list" || f["kind"] != "row.changed" || f["id"] != "42" {
		t.Errorf("event frame = %v", f)
	}
	noFrame(t, other)

	send(h, listening, map[string]any{"type": "unsub", "topic": "alpha.list"})
	h.dispatch(event("alpha.list", "row.changed", "43"))
	noFrame(t, listening)

	send(h, listening, map[string]any{"type": "ping"})
	if f := frame(t, listening); f["type"] != "pong" {
		t.Errorf("ping answered %v", f)
	}
}

// Denials: an unknown kind and a malformed topic are unknown_topic; a key
// that names nothing and a topic the person may not see are both forbidden,
// so a subscription is no existence oracle. A denied subscription hears
// nothing.
func TestHub_DeniesWithoutRevealingExistence(t *testing.T) {
	access := newFakeAccess()
	h := testHub(t, access, testRegistry(t, "1"))
	viewer, vs := access.person("alpha:view")
	outsider, os := access.person()
	c := attach(t, h, viewer, vs)
	o := attach(t, h, outsider, os)

	for topic, reason := range map[string]string{
		"beta.list":    "unknown_topic",
		"alpha..x":     "unknown_topic",
		"alpha.item:2": "forbidden", // does not exist
		"alpha.list:x": "forbidden", // a key where none is taken
		"alpha.item":   "forbidden", // no key where one is needed
	} {
		send(h, c, map[string]any{"type": "sub", "topic": topic})
		if f := frame(t, c); f["type"] != "denied" || f["reason"] != reason {
			t.Errorf("%s: %v, want denied %s", topic, f, reason)
		}
	}
	send(h, o, map[string]any{"type": "sub", "topic": "alpha.item:1"}) // exists, not theirs
	if f := frame(t, o); f["type"] != "denied" || f["reason"] != "forbidden" {
		t.Errorf("outsider: %v, want denied forbidden", f)
	}
	h.dispatch(event("alpha.item:1", "row.changed", "1"))
	noFrame(t, o)
}

// A full queue is cleared and holds one resync; the third time in a minute
// the socket is closed with 1008 (L10). The fan-out never blocks.
func TestHub_SlowConsumerResyncsThenCloses(t *testing.T) {
	access := newFakeAccess()
	h := testHub(t, access, testRegistry(t))
	h.limits.queue = 4
	user, session := access.person("alpha:view")
	c := attach(t, h, user, session)
	send(h, c, map[string]any{"type": "sub", "topic": "alpha.list"})
	frame(t, c)

	for round := 1; round <= 3; round++ {
		for i := 0; i < 5; i++ { // one more than the queue holds
			h.dispatch(event("alpha.list", "row.changed", "x"))
		}
		if round < 3 {
			if f := frame(t, c); f["type"] != "resync" || f["reason"] != "slow_consumer" {
				t.Fatalf("round %d: %v, want the queue cleared to a resync", round, f)
			}
			noFrame(t, c)
			if c.isClosed() {
				t.Fatalf("round %d: closed early", round)
			}
		}
	}
	if !c.isClosed() || c.code != closePolicy {
		t.Errorf("closed %v with %d, want 1008 on the third overflow", c.isClosed(), c.code)
	}
}

// 200 subscriptions per socket, then limit; 20 sockets per user, then the
// 21st is refused.
func TestHub_Limits(t *testing.T) {
	access := newFakeAccess()
	keys := make([]string, 0, 201)
	for i := range 201 {
		keys = append(keys, uuid.NewString()[:8]+string(rune('a'+i%26)))
	}
	h := testHub(t, access, testRegistry(t, keys...))
	h.limits.queue = 512
	user, session := access.person("alpha:view")
	c := attach(t, h, user, session)
	for i, k := range keys {
		send(h, c, map[string]any{"type": "sub", "topic": "alpha.item:" + k})
		f := frame(t, c)
		if i < 200 {
			if f["type"] != "ok" {
				t.Fatalf("sub %d: %v", i, f)
			}
			frame(t, c) // presence
			continue
		}
		if f["type"] != "denied" || f["reason"] != "limit" {
			t.Errorf("the 201st: %v, want denied limit", f)
		}
	}

	for range 19 {
		attach(t, h, user, session)
	}
	if _, err := h.attach(user, session); !errors.Is(err, errTooManyClients) {
		t.Errorf("the 21st socket: %v, want refused", err)
	}
	other, os := access.person()
	attach(t, h, other, os) // another person is not counted against the first
}

// Presence: a frame needs a subscription to a topic that allows it; it is
// pushed at once to the topic's subscribers, collapsed by user to the
// strongest state, with the name from the directory; leaving empties it.
func TestHub_PresenceIsPushedLocally(t *testing.T) {
	access := newFakeAccess()
	reg := testRegistry(t, "1")
	dir := &fakeDirectory{names: map[uuid.UUID]string{}}
	reg.SetUsers(dir)
	h := testHub(t, access, reg)
	h.limits.pushThrottle = 0
	kari, ks := access.person("alpha:view")
	ola, os := access.person("alpha:view")
	dir.names[kari] = "Kari"
	k1 := attach(t, h, kari, ks)
	k2 := attach(t, h, kari, ks)
	o := attach(t, h, ola, os)

	send(h, k1, map[string]any{"type": "presence", "topic": "alpha.item:1", "state": "viewing"})
	if f := frame(t, k1); f["type"] != "denied" || f["reason"] != "forbidden" {
		t.Errorf("presence without a subscription: %v", f)
	}
	send(h, k1, map[string]any{"type": "sub", "topic": "alpha.list"})
	frame(t, k1)
	send(h, k1, map[string]any{"type": "presence", "topic": "alpha.list", "state": "viewing"})
	if f := frame(t, k1); f["type"] != "denied" {
		t.Errorf("presence on a topic without it: %v", f)
	}

	for _, c := range []*client{k1, k2, o} {
		send(h, c, map[string]any{"type": "sub", "topic": "alpha.item:1"})
		frame(t, c) // ok
		frame(t, c) // the set, empty
	}
	send(h, k1, map[string]any{"type": "presence", "topic": "alpha.item:1", "state": "viewing"})
	send(h, k2, map[string]any{"type": "presence", "topic": "alpha.item:1", "state": "replying"})
	// Two changes push concurrently, and a push older than the one a socket
	// already has is skipped, so one or two frames arrive: read until the
	// newest state is in.
	members := presenceUntil(t, o, func(m []map[string]any) bool {
		return len(m) == 1 && m[0]["state"] == "replying"
	})
	if len(members) != 1 || members[0]["userId"] != kari.String() || members[0]["state"] != "replying" || members[0]["name"] != "Kari" {
		t.Errorf("members = %v, want Kari once, replying", members)
	}

	send(h, k2, map[string]any{"type": "presence", "topic": "alpha.item:1", "state": nil})
	if members := presenceUntil(t, o, func(m []map[string]any) bool {
		return len(m) == 1 && m[0]["state"] == "viewing"
	}); len(members) != 1 || members[0]["state"] != "viewing" {
		t.Errorf("after one tab left: %v, want Kari viewing", members)
	}
	h.detach(k1)
	if members := presenceUntil(t, o, func(m []map[string]any) bool { return len(m) == 0 }); len(members) != 0 {
		t.Errorf("after Kari's sockets left: %v, want nobody", members)
	}
}

// presenceUntil reads presence frames off c until one satisfies ok, and
// returns its members. Pushes of one topic run concurrently and a push older
// than the newest a socket has is skipped, so how many frames a burst of
// changes yields is not fixed; the last state is.
func presenceUntil(t testing.TB, c *client, ok func([]map[string]any) bool) []map[string]any {
	t.Helper()
	for {
		members := lastPresence(t, c, 1)
		if ok(members) {
			return members
		}
	}
}

// lastPresence reads n presence frames off c and returns the last one's
// members.
func lastPresence(t testing.TB, c *client, n int) []map[string]any {
	t.Helper()
	var members []map[string]any
	for range n {
		f := frame(t, c)
		if f["type"] != "presence" {
			t.Fatalf("%v, want a presence frame", f)
		}
		members = nil
		for _, m := range f["members"].([]any) {
			members = append(members, m.(map[string]any))
		}
	}
	return members
}

// Another hub's members arrive in its batches and leave 45 s after they were
// last mentioned, so a hub that dies leaves no ghosts.
func TestHub_RemotePresenceExpires(t *testing.T) {
	access := newFakeAccess()
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	h := testHub(t, access, testRegistry(t, "1"))
	h.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	h.limits.pushThrottle = 0
	user, session := access.person("alpha:view")
	c := attach(t, h, user, session)
	send(h, c, map[string]any{"type": "sub", "topic": "alpha.item:1"})
	frame(t, c)
	frame(t, c)

	remote := uuid.NewString()
	h.applyRemote(remote, []presenceDelta{{T: "alpha.item:1", U: remote, S: "editing"}})
	if m := lastPresence(t, c, 1); len(m) != 1 || m[0]["userId"] != remote || m[0]["state"] != "editing" {
		t.Fatalf("remote member = %v", m)
	}
	mu.Lock()
	now = now.Add(30 * time.Second)
	mu.Unlock()
	h.expireRemote()
	noFrame(t, c)
	h.applyRemote("other-hub", nil) // another hub's batch refreshes nobody of ours
	mu.Lock()
	now = now.Add(20 * time.Second)
	mu.Unlock()
	h.expireRemote()
	if m := lastPresence(t, c, 1); len(m) != 0 {
		t.Errorf("after 50 s unmentioned: %v, want nobody", m)
	}
}

// The re-check (L11): a session that ended closes the socket with 4401; a
// subscription the person may no longer see is dropped with denied; and the
// permission is read once per cycle, however many subscriptions ask.
func TestHub_RecheckDropsWhatIsNoLongerAllowed(t *testing.T) {
	access := newFakeAccess()
	h := testHub(t, access, testRegistry(t, "1", "2", "3"))
	user, session := access.person("alpha:view")
	c := attach(t, h, user, session)
	for _, k := range []string{"1", "2", "3"} {
		send(h, c, map[string]any{"type": "sub", "topic": "alpha.item:" + k})
		frame(t, c)
		frame(t, c)
	}

	before := access.holds.Load()
	h.recheck(context.Background(), c)
	if got := access.holds.Load() - before; got != 1 {
		t.Errorf("UserHolds read %d times for three subscriptions, want once per cycle", got)
	}
	noFrame(t, c)

	access.revoke(user, "alpha:view")
	h.recheck(context.Background(), c)
	denied := map[string]bool{}
	for range 3 {
		f := frame(t, c)
		if f["type"] != "denied" || f["reason"] != "forbidden" {
			t.Fatalf("%v, want denied forbidden", f)
		}
		denied[f["topic"].(string)] = true
	}
	if len(denied) != 3 {
		t.Errorf("denied %v, want every subscription", denied)
	}
	h.dispatch(event("alpha.item:1", "row.changed", "1"))
	noFrame(t, c)

	access.endSession(session)
	h.recheck(context.Background(), c)
	if !c.isClosed() || c.code != CloseSessionEnded {
		t.Errorf("closed %v with %d, want 4401", c.isClosed(), c.code)
	}
}

// A socket arriving while the listener is down is told degraded; the
// listener's return resyncs every socket.
func TestHub_DegradedAndResync(t *testing.T) {
	access := newFakeAccess()
	h := testHub(t, access, testRegistry(t))
	h.setListener(false, 0)
	user, session := access.person()
	c := attach(t, h, user, session)
	if f := frame(t, c); f["type"] != "degraded" {
		t.Errorf("%v, want degraded", f)
	}
	h.setListener(true, 1)
	if f := frame(t, c); f["type"] != "resync" || f["reason"] != "reconnect" {
		t.Errorf("%v, want resync", f)
	}
}

type fakeDirectory struct {
	contracts.UserDirectory
	names map[uuid.UUID]string
}

func (d *fakeDirectory) Users(_ context.Context, ids []uuid.UUID) ([]contracts.UserEntry, error) {
	var out []contracts.UserEntry
	for _, id := range ids {
		if n, ok := d.names[id]; ok {
			out = append(out, contracts.UserEntry{ID: id, DisplayName: n, Active: true})
		}
	}
	return out, nil
}

func TestNotify_Validates(t *testing.T) {
	var q recordingExecer
	if err := Notify(context.Background(), &q, "Bad topic", "x.changed", ""); err == nil {
		t.Error("a topic outside the grammar was sent")
	}
	if err := Notify(context.Background(), &q, "alpha.list", "", ""); err == nil {
		t.Error("an empty kind was sent")
	}
	if err := Notify(context.Background(), &q, "alpha.list", "row.changed", strings.Repeat("x", 8000)); err == nil {
		t.Error("a payload over 7 KiB was sent")
	}
	if err := Notify(context.Background(), &q, "alpha.list", "row.changed", "7"); err != nil {
		t.Fatal(err)
	}
	if len(q.args) != 1 || q.args[0][0] != Channel || q.args[0][1] != `{"t":"alpha.list","k":"row.changed","id":"7"}` {
		t.Errorf("sent %v", q.args)
	}
}

type recordingExecer struct{ args [][]any }

func (r *recordingExecer) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	r.args = append(r.args, args)
	return pgconn.CommandTag{}, nil
}

// An Authorize that fails for a reason that is not the person's — a database
// error, a timeout — answers unavailable, which the client retries; only
// ErrForbidden is forbidden.
func TestHub_AnAuthorizeErrorIsUnavailable(t *testing.T) {
	access := newFakeAccess()
	reg := NewRegistry()
	if err := reg.Register("alpha", Topic{Kind: "alpha.list", Authorize: func(context.Context, string) error {
		return errors.New("the database is down")
	}}); err != nil {
		t.Fatal(err)
	}
	h := testHub(t, access, reg)
	user, session := access.person("alpha:view")
	c := attach(t, h, user, session)
	send(h, c, map[string]any{"type": "sub", "topic": "alpha.list"})
	if f := frame(t, c); f["type"] != "denied" || f["reason"] != "unavailable" {
		t.Errorf("%v, want denied unavailable", f)
	}
	h.dispatch(event("alpha.list", "row.changed", "1"))
	noFrame(t, c)
}

// A re-check racing the client's own unsub denies nothing: the topic was
// already left. And leaving presence on a topic one is not on is silent.
func TestHub_RecheckRacingUnsubDeniesNothing(t *testing.T) {
	access := newFakeAccess()
	reg := NewRegistry()
	inRecheck, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	if err := reg.Register("alpha", Topic{Kind: "alpha.list", Authorize: func(context.Context, string) error {
		if calls.Add(1) == 1 {
			return nil // the subscription
		}
		close(inRecheck)
		<-release
		return ErrForbidden
	}}); err != nil {
		t.Fatal(err)
	}
	h := testHub(t, access, reg)
	user, session := access.person()
	c := attach(t, h, user, session)
	send(h, c, map[string]any{"type": "sub", "topic": "alpha.list"})
	frame(t, c)

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.recheck(context.Background(), c)
	}()
	<-inRecheck
	send(h, c, map[string]any{"type": "unsub", "topic": "alpha.list"})
	close(release)
	<-done
	noFrame(t, c)

	send(h, c, map[string]any{"type": "presence", "topic": "alpha.list", "state": nil})
	send(h, c, map[string]any{"type": "presence", "topic": "beta.gone:1", "state": nil})
	noFrame(t, c)
}
