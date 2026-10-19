package calendar_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The subscriptions (calendar feed design D6, D7, D8) through the harness.
// netguard refuses loopback, so the harness's transport stands in for the
// guarded one (modtest.WithTransport): every request a fetch makes, to any
// host, reaches one httptest server, which answers by path and records what
// it was asked. The guard itself is subscriptions_fetch_internal_test.go's.

const subscriptionsPath = "/api/v1/calendar/subscriptions"

func subscriptionPath(id int64) string { return subscriptionsPath + "/" + strconv.FormatInt(id, 10) }

func refreshPath(id int64) string { return subscriptionPath(id) + "/refresh" }

// feedHost is the host every subscription in these tests names: not an IP
// literal, so the address rules pass it, and never resolved, since the
// harness's transport answers for it.
const feedHost = "calendar.example.com"

func feedURL(path string) string { return "https://" + feedHost + path }

// feedServer is the calendars elsewhere: an httptest server answering by
// path, and a RoundTripper sending every request to it.
type feedServer struct {
	srv *httptest.Server

	mu       sync.Mutex
	handlers map[string]http.HandlerFunc
	asked    []*http.Request
}

func newFeedServer(t *testing.T) *feedServer {
	t.Helper()
	f := &feedServer{handlers: map[string]http.HandlerFunc{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.asked = append(f.asked, r)
		h := f.handlers[r.URL.Path]
		f.mu.Unlock()
		if h == nil {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// RoundTrip sends r to the test server, whatever host it names.
func (f *feedServer) RoundTrip(r *http.Request) (*http.Response, error) {
	out := r.Clone(r.Context())
	out.URL.Scheme = "http"
	out.URL.Host = f.srv.Listener.Addr().String()
	return f.srv.Client().Transport.RoundTrip(out)
}

// serve answers path with h from now on.
func (f *feedServer) serve(path string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[path] = h
}

// serveCalendar answers path with body as a calendar, with etag when it is
// not "", and 304 to a request naming that etag.
func (f *feedServer) serveCalendar(path, body, etag string) {
	f.serve(path, func(w http.ResponseWriter, r *http.Request) {
		if etag != "" {
			if r.Header.Get("If-None-Match") == etag {
				w.Header().Set("ETag", etag)
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", etag)
		}
		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		_, _ = w.Write([]byte(body))
	})
}

// requests is every request so far, in order.
func (f *feedServer) requests() []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked)
}

// paths is every path asked so far, in order.
func (f *feedServer) paths() []string {
	var out []string
	for _, r := range f.requests() {
		out = append(out, r.URL.Path)
	}
	return out
}

// icsCalendar is a calendar of the given VEVENT lines, each event's lines
// between its BEGIN and END, and of the calendar's own properties first.
func icsCalendar(props []string, events ...[]string) string {
	lines := append([]string{"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Elsewhere//EN"}, props...)
	for _, e := range events {
		lines = append(lines, "BEGIN:VEVENT", "DTSTAMP:20260901T000000Z")
		lines = append(lines, e...)
		lines = append(lines, "END:VEVENT")
	}
	lines = append(lines, "END:VCALENDAR")
	return strings.Join(lines, "\r\n") + "\r\n"
}

// matchCalendar is a calendar of one match on 15 September 2026, three days
// after the harness's start.
var matchCalendar = icsCalendar(nil, []string{"UID:match-1", "SUMMARY:Match", "DTSTART:20260915T170000Z", "DTEND:20260915T190000Z"})

// newSubscribing is a harness whose fetches reach f.
func newSubscribing(t *testing.T, opts ...modtest.Option) (*harness, *feedServer) {
	t.Helper()
	f := newFeedServer(t)
	return newHarness(t, append([]modtest.Option{modtest.WithTransport(f)}, opts...)...), f
}

// subscriptionBody is a CalendarSubscriptionWrite.
func subscriptionBody(name, url, colour string, busyOnly bool) map[string]any {
	return map[string]any{"name": name, "url": url, "colour": colour, "busyOnly": busyOnly}
}

// subscribe posts a subscription as c, failing unless 201.
func subscribe(t *testing.T, c *modtest.Client, body map[string]any) gen.CalendarSubscription {
	t.Helper()
	var s gen.CalendarSubscription
	do(t, c, http.MethodPost, subscriptionsPath, body, http.StatusCreated, &s)
	return s
}

// storedBody is the subscription's body column, "" when null.
func storedBody(t *testing.T, h *harness, id int64) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `SELECT coalesce(body, '') FROM calendar.subscriptions WHERE id = $1`, id)
}

func TestSubscriptions_APostFetchesOnceAndTheItemsAreOnTheCalendarInTheSubscriptionsColour(t *testing.T) {
	t.Parallel()
	h, f := newSubscribing(t)
	c, _ := signIn(t, h)
	f.serveCalendar("/team.ics", matchCalendar, "")

	var meta gen.CalendarMeta
	do(t, c, http.MethodGet, metaPath, nil, http.StatusOK, &meta)
	for _, k := range meta.Sources {
		if k.Kind == "calendar.subscription" {
			t.Errorf("meta lists the subscriptions' kind before there is one: %+v", meta.Sources)
		}
	}

	s := subscribe(t, c, subscriptionBody("  Football  ", "webcal://"+feedHost+"/team.ics", "teal", false))
	if s.Url != feedURL("/team.ics") || s.Name != "Football" || s.Colour != gen.Teal || s.BusyOnly ||
		s.FetchedAt == nil || !s.FetchedAt.Equal(h.Now()) || s.AttemptedAt == nil || s.Error != nil {
		t.Errorf("the subscription = %+v", s)
	}
	asked := f.requests()
	if len(asked) != 1 || asked[0].Header.Get("User-Agent") != "Vantigo-Calendar" || asked[0].Header.Get("Accept") != "text/calendar" ||
		asked[0].Header.Get("If-None-Match") != "" || asked[0].Method != http.MethodGet {
		t.Errorf("the fetch asked %d times: %+v", len(asked), asked)
	}

	got := ofKind(items(t, c, windowPath("2026-09-14", "2026-09-20", "timeZone=Europe/Oslo")), "calendar.subscription")
	if len(got) != 1 {
		t.Fatalf("subscription items = %+v", got)
	}
	it := got[0]
	if it.Id != strconv.FormatInt(s.Id, 10)+":match-1" || it.Title != "Match" || it.Subtitle == nil || *it.Subtitle != "Football" ||
		it.Path != nil || it.Subscription == nil || it.Subscription.Colour != gen.Teal || it.Subscription.Name != "Football" ||
		it.Subscription.Id != s.Id || it.StartsAt == nil || !it.StartsAt.Equal(time.Date(2026, 9, 15, 17, 0, 0, 0, time.UTC)) {
		t.Errorf("the item = %+v (subscription %+v)", it, it.Subscription)
	}
	if only := items(t, c, windowPath("2026-09-14", "2026-09-20", "kinds=calendar.appointment")); len(ofKind(only, "calendar.subscription")) != 0 {
		t.Errorf("kinds=calendar.appointment answered the subscription's items")
	}
	if named := items(t, c, windowPath("2026-09-14", "2026-09-20", "kinds=calendar.subscription")); len(named.Items) != 1 {
		t.Errorf("kinds=calendar.subscription answered %+v", named.Items)
	}

	do(t, c, http.MethodGet, metaPath, nil, http.StatusOK, &meta)
	if last := meta.Sources[len(meta.Sources)-1]; last != (gen.CalendarSourceKind{Kind: "calendar.subscription", Module: "calendar"}) {
		t.Errorf("meta's sources end with %+v", last)
	}
	var list []gen.CalendarSubscription
	do(t, c, http.MethodGet, subscriptionsPath, nil, http.StatusOK, &list)
	if len(list) != 1 || list[0].Id != s.Id {
		t.Errorf("the list = %+v", list)
	}
}

func TestSubscriptions_ABusyOnlySubscriptionAnswersBlankTitles(t *testing.T) {
	t.Parallel()
	h, f := newSubscribing(t)
	c, _ := signIn(t, h)
	f.serveCalendar("/private.ics", matchCalendar, "")
	subscribe(t, c, subscriptionBody("Partner", feedURL("/private.ics"), "grape", true))
	got := ofKind(items(t, c, windowPath("2026-09-14", "2026-09-20")), "calendar.subscription")
	if len(got) != 1 || got[0].Title != "" || !got[0].Subscription.BusyOnly {
		t.Errorf("a busy-only item = %+v", got)
	}
}

func TestSubscriptions_RefusesEachAddressRuleTheNameAndTheColourAsFieldErrors(t *testing.T) {
	t.Parallel()
	h, f := newSubscribing(t)
	c, _ := signIn(t, h)
	for _, tc := range []struct {
		name  string
		body  map[string]any
		field string
		opts  []modtest.RequestOption
	}{
		{name: "a private address", body: subscriptionBody("A", "http://10.0.0.1/x", "blue", false), field: "url"},
		{name: "a user and a password", body: subscriptionBody("A", "https://user:pw@host.example.com/", "blue", false), field: "url"},
		{name: "a relative address", body: subscriptionBody("A", "calendars/team.ics", "blue", false), field: "url"},
		{name: "an ftp address", body: subscriptionBody("A", "ftp://example.com/team.ics", "blue", false), field: "url"},
		{name: "no host", body: subscriptionBody("A", "https:///team.ics", "blue", false), field: "url"},
		{name: "too long an address", body: subscriptionBody("A", feedURL("/"+strings.Repeat("a", 2048)), "blue", false), field: "url"},
		{name: "a 101-character name", body: subscriptionBody(strings.Repeat("n", 101), feedURL("/x.ics"), "blue", false), field: "name"},
		{name: "a blank name", body: subscriptionBody("   ", feedURL("/x.ics"), "blue", false), field: "name"},
		{name: "a colour off the palette", body: subscriptionBody("A", feedURL("/x.ics"), "beige", false), field: "colour",
			opts: []modtest.RequestOption{modtest.SkipContract("beige is not in the request enum: the server's own refusal is the subject")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fieldRefused(t, c.Do(http.MethodPost, subscriptionsPath, tc.body, tc.opts...), tc.field)
		})
	}
	if n := h.Count(t, `SELECT count(*) FROM calendar.subscriptions`); n != 0 || len(f.requests()) != 0 {
		t.Errorf("a refused write stored %d rows and fetched %d times", n, len(f.requests()))
	}
}

func TestSubscriptions_TheEleventhIsRefusedWithSubscriptionLimit(t *testing.T) {
	t.Parallel()
	h, f := newSubscribing(t)
	c, userID := signIn(t, h)
	for i := range 10 {
		h.Exec(t, `INSERT INTO calendar.subscriptions (user_id, name, url, colour, busy_only, created_at, updated_at)
		           VALUES ($1, $2, $3, 'blue', false, $4, $4)`, userID, "Calendar "+strconv.Itoa(i), feedURL("/c.ics"), h.Now())
	}
	refused(t, c.Do(http.MethodPost, subscriptionsPath, subscriptionBody("Eleventh", feedURL("/x.ics"), "blue", false)), http.StatusConflict, "subscription_limit")
	if len(f.requests()) != 0 {
		t.Errorf("the refused eleventh fetched %v", f.paths())
	}
	other, _ := signIn(t, h)
	f.serveCalendar("/x.ics", matchCalendar, "")
	subscribe(t, other, subscriptionBody("Someone else's first", feedURL("/x.ics"), "blue", false))
}

func TestSubscriptions_AnotherPersonsSubscriptionIsTheNotFoundOfEveryOperation(t *testing.T) {
	t.Parallel()
	h, f := newSubscribing(t)
	owner, _ := signIn(t, h)
	stranger, _ := signIn(t, h)
	f.serveCalendar("/mine.ics", matchCalendar, "")
	s := subscribe(t, owner, subscriptionBody("Mine", feedURL("/mine.ics"), "red", false))

	write := subscriptionBody("Taken", feedURL("/theirs.ics"), "red", false)
	do(t, stranger, http.MethodPut, subscriptionPath(s.Id), write, http.StatusNotFound, nil)
	do(t, stranger, http.MethodPost, refreshPath(s.Id), nil, http.StatusNotFound, nil)
	do(t, stranger, http.MethodDelete, subscriptionPath(s.Id), nil, http.StatusNotFound, nil)
	var list []gen.CalendarSubscription
	do(t, stranger, http.MethodGet, subscriptionsPath, nil, http.StatusOK, &list)
	if len(list) != 0 {
		t.Errorf("the stranger lists %+v", list)
	}
	if got := ofKind(items(t, stranger, windowPath("2026-09-14", "2026-09-20")), "calendar.subscription"); len(got) != 0 {
		t.Errorf("the stranger's calendar carries %+v", got)
	}
	do(t, owner, http.MethodPut, subscriptionPath(999999), write, http.StatusNotFound, nil)
	do(t, owner, http.MethodPost, refreshPath(999999), nil, http.StatusNotFound, nil)
	if len(f.requests()) != 1 {
		t.Errorf("the refused operations fetched %v", f.paths())
	}

	do(t, owner, http.MethodDelete, subscriptionPath(s.Id), nil, http.StatusNoContent, nil)
	do(t, owner, http.MethodDelete, subscriptionPath(s.Id), nil, http.StatusNotFound, nil)
	if got := ofKind(items(t, owner, windowPath("2026-09-14", "2026-09-20")), "calendar.subscription"); len(got) != 0 {
		t.Errorf("a removed subscription's items are still on the calendar: %+v", got)
	}
}

func TestSubscriptions_ARedirectIsTheRedirectErrorAndNothingIsFollowed(t *testing.T) {
	t.Parallel()
	h, f := newSubscribing(t)
	c, _ := signIn(t, h)
	f.serve("/old.ics", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, feedURL("/new.ics"), http.StatusMovedPermanently)
	})
	f.serveCalendar("/new.ics", matchCalendar, "")
	s := subscribe(t, c, subscriptionBody("Moved", feedURL("/old.ics"), "blue", false))
	if s.Error == nil || *s.Error != "the address redirects; subscribe to the address it points at" || s.FetchedAt != nil || s.AttemptedAt == nil {
		t.Errorf("the subscription = %+v", s)
	}
	if got := f.paths(); !slices.Equal(got, []string{"/old.ics"}) {
		t.Errorf("the fetch asked for %v", got)
	}
}

func TestSubscriptions_ABodyOverOneMebibyteIsTheSizeErrorAndTheLastGoodBodyIsKept(t *testing.T) {
	t.Parallel()
	h, f := newSubscribing(t)
	c, _ := signIn(t, h)
	f.serveCalendar("/big.ics", matchCalendar, "")
	s := subscribe(t, c, subscriptionBody("Big", feedURL("/big.ics"), "blue", false))
	firstFetch := *s.FetchedAt

	f.serve("/big.ics", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(icsCalendar([]string{"X-PADDING:" + strings.Repeat("x", 2<<20)})))
	})
	h.Advance(time.Hour)
	var got gen.CalendarSubscription
	do(t, c, http.MethodPost, refreshPath(s.Id), nil, http.StatusOK, &got)
	if got.Error == nil || *got.Error != "the calendar is larger than 1 MiB" || got.FetchedAt == nil || !got.FetchedAt.Equal(firstFetch) ||
		got.AttemptedAt == nil || !got.AttemptedAt.Equal(h.Now()) {
		t.Errorf("after a 2 MiB answer the subscription = %+v", got)
	}
	if body := storedBody(t, h, s.Id); body != matchCalendar {
		t.Errorf("the body was replaced: %d bytes", len(body))
	}
	if items := ofKind(items(t, c, windowPath("2026-09-14", "2026-09-20")), "calendar.subscription"); len(items) != 1 {
		t.Errorf("the last good read's items = %+v", items)
	}
}

func TestSubscriptions_AnswersThatAreNotACalendarAreEachTheirSentence(t *testing.T) {
	t.Parallel()
	h, f := newSubscribing(t)
	c, _ := signIn(t, h)
	f.serve("/page.ics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><title>Sign in</title>"))
	})
	f.serve("/broken.ics", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("\r\n\r\nBEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nnot a property\r\n"))
	})
	f.serve("/down.ics", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	for path, want := range map[string]string{
		"/page.ics":    "the address did not answer a calendar",
		"/broken.ics":  "the address did not answer a calendar",
		"/down.ics":    "the address answered 503 Service Unavailable",
		"/missing.ics": "the address answered 404 Not Found",
	} {
		s := subscribe(t, c, subscriptionBody("Odd "+path, feedURL(path), "blue", false))
		if s.Error == nil || *s.Error != want || s.FetchedAt != nil {
			t.Errorf("%s: the subscription = %+v, want the error %q", path, s, want)
		}
		if body := storedBody(t, h, s.Id); body != "" {
			t.Errorf("%s: a body was stored: %q", path, body)
		}
	}
	if items := ofKind(items(t, c, windowPath("2026-09-14", "2026-09-20")), "calendar.subscription"); len(items) != 0 {
		t.Errorf("failed subscriptions answered items %+v", items)
	}
}

func TestSubscriptions_ANotModifiedAnswerWritesFetchedAtAlone(t *testing.T) {
	t.Parallel()
	h, f := newSubscribing(t)
	c, _ := signIn(t, h)
	f.serveCalendar("/etag.ics", matchCalendar, `"v1"`)
	s := subscribe(t, c, subscriptionBody("Tagged", feedURL("/etag.ics"), "blue", false))

	h.Advance(time.Hour)
	var got gen.CalendarSubscription
	do(t, c, http.MethodPost, refreshPath(s.Id), nil, http.StatusOK, &got)
	asked := f.requests()
	if len(asked) != 2 || asked[1].Header.Get("If-None-Match") != `"v1"` {
		t.Fatalf("the refresh asked %d times, the last with If-None-Match %q", len(asked), asked[len(asked)-1].Header.Get("If-None-Match"))
	}
	if got.FetchedAt == nil || !got.FetchedAt.Equal(h.Now()) || got.Error != nil {
		t.Errorf("after a 304 the subscription = %+v", got)
	}
	if body := storedBody(t, h, s.Id); body != matchCalendar {
		t.Errorf("a 304 changed the body")
	}
	if etag := modtest.One[string](t, h.Harness, `SELECT etag FROM calendar.subscriptions WHERE id = $1`, s.Id); etag != `"v1"` {
		t.Errorf("etag = %q", etag)
	}
}

func TestSubscriptions_APutWithANewAddressForgetsTheOldFetchAndFetchesTheNew(t *testing.T) {
	t.Parallel()
	h, f := newSubscribing(t)
	c, _ := signIn(t, h)
	f.serveCalendar("/a.ics", matchCalendar, `"a"`)
	other := icsCalendar(nil, []string{"UID:practice", "SUMMARY:Practice", "DTSTART;VALUE=DATE:20260916"})
	f.serveCalendar("/b.ics", other, "")
	s := subscribe(t, c, subscriptionBody("Club", feedURL("/a.ics"), "blue", false))

	var renamed gen.CalendarSubscription
	do(t, c, http.MethodPut, subscriptionPath(s.Id), subscriptionBody("The club", feedURL("/a.ics"), "orange", true), http.StatusOK, &renamed)
	if renamed.Name != "The club" || renamed.Colour != gen.Orange || !renamed.BusyOnly || len(f.requests()) != 1 ||
		renamed.FetchedAt == nil || !renamed.FetchedAt.Equal(*s.FetchedAt) {
		t.Errorf("a PUT that kept the address = %+v after %d fetches", renamed, len(f.requests()))
	}

	h.Advance(time.Hour)
	var moved gen.CalendarSubscription
	do(t, c, http.MethodPut, subscriptionPath(s.Id), subscriptionBody("The club", "webcals://"+feedHost+"/b.ics", "orange", false), http.StatusOK, &moved)
	asked := f.requests()
	if len(asked) != 2 || asked[1].URL.Path != "/b.ics" || asked[1].Header.Get("If-None-Match") != "" {
		t.Fatalf("the new address was asked as %+v", asked)
	}
	if moved.Url != feedURL("/b.ics") || moved.FetchedAt == nil || !moved.FetchedAt.Equal(h.Now()) || moved.Error != nil {
		t.Errorf("the moved subscription = %+v", moved)
	}
	if body := storedBody(t, h, s.Id); body != other {
		t.Errorf("the body is not the new address's")
	}
	if n := h.Count(t, `SELECT count(*) FROM calendar.subscriptions WHERE id = $1 AND etag IS NULL`, s.Id); n != 1 {
		t.Errorf("the old address's ETag was kept")
	}
	got := ofKind(items(t, c, windowPath("2026-09-14", "2026-09-20")), "calendar.subscription")
	if len(got) != 1 || got[0].Title != "Practice" || !got[0].AllDay {
		t.Errorf("after the move the items = %+v", got)
	}
}
