package google_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google"
	"github.com/vantigo-io/vantigo/server/internal/google/googletest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The calendar's change notifications (Google Workspace design G32): Watch's
// channel and its renewal, Unwatch, Notified, the anonymous endpoint and what
// it takes, and google-sweep keeping a closing connection while a channel
// needs its token.

const (
	publicBase        = "https://public.example.com"
	notificationsPath = "/api/v1/google/calendar-notifications"
)

// newNoticesFixture is a calendar fixture with PUBLIC_BASE_URL set.
func newNoticesFixture(t *testing.T, opts ...modtest.Option) *calendarFixture {
	t.Helper()
	return newCalendarFixture(t, append([]modtest.Option{modtest.WithEnv("PUBLIC_BASE_URL", publicBase)}, opts...)...)
}

// notify posts a message as Google does: headers alone, no body.
func notify(t *testing.T, c *modtest.Client, channel, token, resource, state string) {
	t.Helper()
	opts := []modtest.RequestOption{}
	for k, v := range map[string]string{"X-Goog-Channel-ID": channel, "X-Goog-Channel-Token": token, "X-Goog-Resource-ID": resource, "X-Goog-Resource-State": state, "X-Goog-Message-Number": "2"} {
		if v != "" {
			opts = append(opts, modtest.Header(k, v))
		}
	}
	if r := c.Do(http.MethodPost, notificationsPath, nil, opts...); r.Status != http.StatusNoContent || len(r.Body) != 0 {
		t.Fatalf("the endpoint answered %d %q, want 204 and nothing", r.Status, r.Body)
	}
}

// watchCalls counts events.watch calls since from.
func (f *calendarFixture) watchCalls(from int) int {
	n := 0
	for _, c := range f.calendarCalls(from) {
		if strings.HasSuffix(c.Path, "/events/watch") {
			n++
		}
	}
	return n
}

func TestMailboxNotifications_WatchOpensAndRenewsAChannel(t *testing.T) {
	t.Parallel()
	f := newNoticesFixture(t)
	_, gid := f.connected(t, "kari@nordlys.no")
	ctx := context.Background()
	f.g.SetChannelLife(7 * 24 * time.Hour)

	if err := f.p.Watch(ctx, gid); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	channels := f.g.Channels()
	if len(channels) != 1 || channels[0].Address != publicBase+notificationsPath || channels[0].Owner != gid || len(channels[0].Token) != 43 {
		t.Fatalf("channels %+v", channels)
	}
	first := channels[0]
	var stored []byte
	if err := f.h.Pool().QueryRow(ctx, `SELECT token_hash FROM google.calendar_channels WHERE google_user_id = $1 AND channel_id = $2`, gid, first.ID).Scan(&stored); err != nil {
		t.Fatalf("the row: %v", err)
	}
	if strings.Contains(string(stored), first.Token) {
		t.Error("the token is stored as it is")
	}

	// Held, more than three days left: nothing.
	n := len(f.g.Calls())
	f.h.Advance(3 * 24 * time.Hour)
	if err := f.p.Watch(ctx, gid); err != nil || f.watchCalls(n) != 0 {
		t.Errorf("a live channel: %v, %d watches, want none", err, f.watchCalls(n))
	}
	// Less than three days left: a new channel, the old one stopped.
	f.h.Advance(25 * time.Hour)
	if err := f.p.Watch(ctx, gid); err != nil || f.watchCalls(n) != 1 {
		t.Fatalf("the renewal: %v, %d watches", err, f.watchCalls(n))
	}
	if stopped := f.g.Stopped(); len(stopped) != 1 || stopped[0] != first.ID {
		t.Errorf("stopped %v, want the old channel", stopped)
	}
	if got := f.g.Channels(); len(got) != 1 || got[0].ID == first.ID {
		t.Errorf("channels %+v, want the new one alone", got)
	}
	// A channel granted a short life is renewed at half of it (U5).
	f.g.SetChannelLife(24 * time.Hour)
	f.h.Advance(8 * 24 * time.Hour)
	if err := f.p.Watch(ctx, gid); err != nil {
		t.Fatal(err)
	}
	n = len(f.g.Calls())
	f.h.Advance(11 * time.Hour)
	_ = f.p.Watch(ctx, gid)
	f.h.Advance(2 * time.Hour)
	_ = f.p.Watch(ctx, gid)
	if w := f.watchCalls(n); w != 1 {
		t.Errorf("a day's channel: %d watches over 13 hours, want one at half its life", w)
	}

	// Unwatch stops it and forgets the row; again, nothing.
	if err := f.p.Unwatch(ctx, gid); err != nil {
		t.Fatalf("Unwatch: %v", err)
	}
	if len(f.g.Channels()) != 0 || f.h.Count(t, `SELECT count(*) FROM google.calendar_channels`) != 0 {
		t.Error("the channel or its row is left")
	}
	if err := f.p.Unwatch(ctx, gid); err != nil {
		t.Errorf("Unwatch again: %v", err)
	}
	if strings.Contains(f.h.Logs(), first.Token) || strings.Contains(f.h.Logs(), first.ID) {
		t.Error("the log holds a channel's token or id")
	}
}

func TestMailboxNotifications_WithoutAPublicAddressNothingIsWatched(t *testing.T) {
	t.Parallel()
	f := newCalendarFixture(t)
	_, gid := f.connected(t, "kari@nordlys.no")
	n := len(f.g.Calls())
	if err := f.p.Watch(context.Background(), gid); err != nil || f.watchCalls(n) != 0 {
		t.Errorf("no PUBLIC_BASE_URL: %v, %d watches", err, f.watchCalls(n))
	}
	g := newNoticesFixture(t, modtest.WithEnv("CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED", "0"))
	_, gid = g.connected(t, "kari@nordlys.no")
	n = len(g.g.Calls())
	if err := g.p.Watch(context.Background(), gid); err != nil || g.watchCalls(n) != 0 {
		t.Errorf("notifications off: %v, %d watches", err, g.watchCalls(n))
	}
}

func TestMailboxNotifications_TheEndpointTakesAValidMessageAlone(t *testing.T) {
	t.Parallel()
	f := newNoticesFixture(t)
	_, gid := f.connected(t, "kari@nordlys.no")
	ctx := context.Background()
	if err := f.p.Watch(ctx, gid); err != nil {
		t.Fatal(err)
	}
	ch := f.g.Channels()[0]
	anon := f.h.Client(t)
	notified := func() []contracts.MailboxNotice {
		t.Helper()
		got, err := f.p.Notified(ctx, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	forged := strings.Repeat("A", 43)
	// The channel's first message, a forged token, another resource, an
	// unknown channel, nonsense and nothing at all: 204 and nothing marked.
	notify(t, anon, ch.ID, ch.Token, ch.ResourceID, "sync")
	notify(t, anon, ch.ID, forged, ch.ResourceID, "exists")
	notify(t, anon, ch.ID, ch.Token, "res-other", "exists")
	notify(t, anon, uuid.NewString(), ch.Token, ch.ResourceID, "exists")
	notify(t, anon, "not-a-channel", "short", ch.ResourceID, "exists")
	notify(t, anon, "", "", "", "")
	if got := notified(); len(got) != 0 {
		t.Fatalf("notified %+v, want nothing", got)
	}
	// A valid message marks the channel: Notified answers the mailbox.
	f.h.Advance(time.Minute)
	notify(t, anon, ch.ID, ch.Token, ch.ResourceID, "exists")
	got := notified()
	if len(got) != 1 || got[0].Mailbox != gid || !got[0].At.Equal(f.h.Now()) {
		t.Errorf("notified %+v, want %s now", got, gid)
	}
	if later, _ := f.p.Notified(ctx, f.h.Now()); len(later) != 0 {
		t.Errorf("notified after now: %+v", later)
	}
	// A signed-in caller is answered alike.
	signedIn, _ := f.h.SignInUser(t)
	notify(t, signedIn, ch.ID, forged, ch.ResourceID, "not_exists")
	if logs := f.h.Logs(); strings.Contains(logs, ch.Token) || strings.Contains(logs, ch.ID) || strings.Contains(logs, ch.ResourceID) {
		t.Error("the log holds a channel's token, id or resource")
	}
}

func TestMailboxNotifications_UnderCleanupAndWithNoToken(t *testing.T) {
	t.Parallel()
	f := newNoticesFixture(t)
	c, gid := f.connected(t, "kari@nordlys.no")
	ctx := context.Background()
	if err := f.p.Watch(ctx, gid); err != nil {
		t.Fatal(err)
	}
	// The switch off: Watch refused, Unwatch served by the closing token.
	setPersonal(t, f.o, false)
	if err := f.p.Watch(ctx, gid); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Watch under cleanup: %v, want unavailable", err)
	}
	if err := f.p.Unwatch(ctx, gid); err != nil || len(f.g.Channels()) != 0 {
		t.Errorf("Unwatch under cleanup: %v, %d channels", err, len(f.g.Channels()))
	}
	_ = c

	// A channel whose connection holds no token any more is forgotten.
	g := newNoticesFixture(t)
	_, ola := g.connected(t, "ola@nordlys.no")
	if err := g.p.Watch(ctx, ola); err != nil {
		t.Fatal(err)
	}
	g.h.Exec(t, `UPDATE google.connections SET state = 'closing', refresh_sealed = NULL WHERE google_user_id = $1`, ola)
	if err := g.p.Unwatch(ctx, ola); err != nil || g.h.Count(t, `SELECT count(*) FROM google.calendar_channels`) != 0 {
		t.Errorf("no token: %v, want the row forgotten", err)
	}
}

func TestSweep_KeepsAClosingConnectionWhileItHoldsAChannel(t *testing.T) {
	t.Parallel()
	f := newNoticesFixture(t)
	c, gid := f.connected(t, "kari@nordlys.no")
	ctx := context.Background()
	if err := f.p.Watch(ctx, gid); err != nil {
		t.Fatal(err)
	}
	if r := c.Do(http.MethodDelete, personalMePath, nil); r.Status != http.StatusNoContent {
		t.Fatal(r.Status)
	}
	sweep := google.NewSweepWorker(f.h.Deps())
	f.h.Advance(25 * time.Hour)
	if err := sweep.RunCycle(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM google.connections WHERE google_user_id = $1`, gid); n != 1 {
		t.Fatal("a closing connection holding a channel was deleted after a day")
	}
	// The channel stopped — a use of the token — and a day on: the sweep
	// deletes the row.
	if err := f.p.Unwatch(ctx, gid); err != nil {
		t.Fatal(err)
	}
	f.h.Advance(25 * time.Hour)
	if err := sweep.RunCycle(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM google.connections WHERE google_user_id = $1`, gid); n != 0 {
		t.Error("the spent connection was kept once its channel was stopped")
	}
	// Seven days delete a closing row whatever it holds.
	_, ola := f.connected(t, "ola@nordlys.no")
	if err := f.p.Watch(ctx, ola); err != nil {
		t.Fatal(err)
	}
	f.h.Exec(t, `UPDATE google.connections SET state = 'closing' WHERE google_user_id = $1`, ola)
	f.h.Advance(8 * 24 * time.Hour)
	if err := sweep.RunCycle(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM google.connections WHERE google_user_id = $1`, ola); n != 0 {
		t.Error("a closing row was kept past seven days")
	}
	_ = googletest.APIsHost
}
