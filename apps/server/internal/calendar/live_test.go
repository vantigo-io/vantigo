package calendar_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/calendar"
	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// liveEvent is one notification on live.Channel.
type liveEvent struct {
	T  string `json:"t"`
	K  string `json:"k"`
	ID string `json:"id"`
}

// liveListener LISTENs on the harness's database, as a hub does.
type liveListener struct{ events chan liveEvent }

func listenLive(t *testing.T, h *modtest.Harness) *liveListener {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	conn, err := pgx.ConnectConfig(ctx, h.Pool().Config().ConnConfig.Copy())
	if err != nil {
		t.Fatalf("connect the listener: %v", err)
	}
	if _, err := conn.Exec(ctx, "LISTEN "+live.Channel); err != nil {
		t.Fatalf("listen: %v", err)
	}
	l := &liveListener{events: make(chan liveEvent, 256)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			n, err := conn.WaitForNotification(ctx)
			if err != nil {
				return
			}
			var e liveEvent
			if json.Unmarshal([]byte(n.Payload), &e) == nil && e.T != "" {
				l.events <- e
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = conn.Close(context.Background())
	})
	return l
}

// expect waits for every one of want, in any order, among what arrives.
func (l *liveListener) expect(t *testing.T, want ...liveEvent) {
	t.Helper()
	missing := map[liveEvent]bool{}
	for _, w := range want {
		missing[w] = true
	}
	deadline := time.After(10 * time.Second)
	var seen []liveEvent
	for len(missing) > 0 {
		select {
		case e := <-l.events:
			seen = append(seen, e)
			delete(missing, e)
		case <-deadline:
			t.Fatalf("live events missing %v; heard %v", missing, seen)
		}
	}
}

// settle drops what is still arriving, so the next expect sees only what the
// next step sends.
func (l *liveListener) settle() {
	for {
		select {
		case <-l.events:
		case <-time.After(200 * time.Millisecond):
			return
		}
	}
}

func syncEvent(id uuid.UUID) liveEvent {
	return liveEvent{T: "calendar.mailbox-sync:" + id.String(), K: "sync.changed", ID: id.String()}
}

// Saving the setting, asking for a sync now and the worker's result are heard
// on the person's own topic with their id (K41).
func TestLive_MailboxSyncIsHeardOnTheCallersOwnTopic(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, _ := r.person(t)
	other, otherID, _ := r.person(t)
	l := listenLive(t, r.Harness)

	turnOn(t, c, "default", "full", "calendar.appointment")
	l.expect(t, syncEvent(me))
	l.settle()

	do(t, c, http.MethodPost, mailboxSyncRunPath, nil, http.StatusOK, nil)
	l.expect(t, syncEvent(me))
	l.settle()

	if n := mailboxCycle(t, r); n != 1 {
		t.Fatalf("synced %d people, want 1", n)
	}
	l.expect(t, syncEvent(me))
	if got := getSync(t, c); got.State != gen.MailboxSyncStateActive {
		t.Errorf("after the sync the state is %s, want active", got.State)
	}

	// Another person's card is another topic.
	l.settle()
	turnOn(t, other, "default", "full", "calendar.appointment")
	l.expect(t, syncEvent(otherID))
	for _, e := range l.collect() {
		if e == syncEvent(me) {
			t.Errorf("heard the first person's topic for the second person's save: %v", e)
		}
	}
}

// collect drains what arrives until it goes quiet.
func (l *liveListener) collect() []liveEvent {
	var out []liveEvent
	for {
		select {
		case e := <-l.events:
			out = append(out, e)
		case <-time.After(300 * time.Millisecond):
			return out
		}
	}
}

// An appointment's write nudges its people's syncs without a card event: the
// card shows no due time (K41).
func TestLive_AnAppointmentWriteNudgesWithoutAnEvent(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, _, _ := r.person(t)
	turnOn(t, c, "default", "full", "calendar.appointment")
	if n := mailboxCycle(t, r); n != 1 {
		t.Fatalf("synced %d people, want 1", n)
	}
	l := listenLive(t, r.Harness)
	create(t, c, timed(t, "Standup", oslo, "2026-09-14T09:00", "2026-09-14T09:30"))
	for _, e := range l.collect() {
		if e.T == "calendar.mailbox-sync:"+e.ID {
			t.Errorf("heard %v for an appointment's write", e)
		}
	}
}

// The topic's Authorize (L9, K41): calendar:access and the caller's own id as
// the key; somebody else's, an unknown or a malformed key, and a missing key
// are all forbidden.
func TestLive_TopicAuthorizesOnlyTheCallersOwnKey(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, me := signIn(t, h)
	_, other := signIn(t, h)
	_, noAccess := h.SignInUser(t, "projects:access")
	topics := calendar.Module().LiveTopics(h.Deps())
	if len(topics) != 1 || topics[0].Kind != "calendar.mailbox-sync" {
		t.Fatalf("topics = %v, want calendar.mailbox-sync only", topics)
	}
	access := h.Deps().Access
	for name, tc := range map[string]struct {
		user uuid.UUID
		key  string
		ok   bool
	}{
		"my own key":            {me, me.String(), true},
		"somebody else's":       {me, other.String(), false},
		"nobody's":              {me, uuid.NewString(), false},
		"not an id":             {me, "x", false},
		"uppercase spelling":    {me, strings.ToUpper(me.String()), false},
		"without a key":         {me, "", false},
		"no calendar:access":    {noAccess, noAccess.String(), false},
		"the other's own key":   {other, other.String(), true},
		"the other with my key": {other, me.String(), false},
	} {
		err := live.Authorize(context.Background(), access, tc.user, topics[0], tc.key)
		if tc.ok && err != nil {
			t.Errorf("%s: %v, want allowed", name, err)
		}
		if !tc.ok && !errors.Is(err, live.ErrForbidden) {
			t.Errorf("%s: %v, want live.ErrForbidden", name, err)
		}
	}
}
