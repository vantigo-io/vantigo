package meta_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/meta"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// The splitting worker and what Communications reads (Messenger design N11):
// each stored webhook event becomes rows of meta.inbound, attachments fetched
// from Meta's CDN at once, read from a cursor and acknowledged.

const (
	inbPage  = "111222333"
	inbOther = "999888777"
	inbUser  = "5550001"
	// inbMs is the events' timestamp, ms since the epoch.
	inbMs = int64(1_700_000_000_000)
)

// inbMemStore is an in-memory storage.ObjectStore.
type inbMemStore struct {
	mu    sync.Mutex
	files map[string][]byte
	types map[string]string
}

func newInbMemStore() *inbMemStore {
	return &inbMemStore{files: map[string][]byte{}, types: map[string]string{}}
}

func (s *inbMemStore) Put(_ context.Context, key string, r io.Reader, contentType string) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[key], s.types[key] = b, contentType
	return nil
}

func (s *inbMemStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.files[key]
	if !ok {
		return nil, fmt.Errorf("get %q: %w", key, storage.ErrNotExist)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (s *inbMemStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.files[key]
	return ok, nil
}

func (s *inbMemStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files, key)
	return nil
}

func (s *inbMemStore) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.files))
	for k := range s.files {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// inbCDN is a fake Meta CDN: a RoundTripper that answers every file request
// with body, counts the hosts it was asked, and fails on demand.
type inbCDN struct {
	mu    sync.Mutex
	body  []byte
	ctype string
	fail  bool
	calls []string
}

func (c *inbCDN) RoundTrip(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, req.URL.Host+req.URL.Path)
	if c.fail {
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader("no")), Header: http.Header{}, Request: req}, nil
	}
	h := http.Header{}
	if c.ctype != "" {
		h.Set("Content-Type", c.ctype)
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(c.body)), ContentLength: int64(len(c.body)), Header: h, Request: req}, nil
}

func (c *inbCDN) set(fn func(*inbCDN)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(c)
}

func (c *inbCDN) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

const inbImageURL = "https://scontent.xx.fbcdn.net/v/t1.15752-9/photo_123.png?oh=abc&oe=def"

// inbFixture is a configured meta installation with one connected Page, a fake
// CDN and an in-memory object store.
type inbFixture struct {
	h     *harness
	cdn   *inbCDN
	files *inbMemStore
	split *meta.SplitWorker
	ret   *meta.RetentionWorker
	m     contracts.ChannelMessenger
}

func newInbFixture(t *testing.T) *inbFixture {
	t.Helper()
	cdn := &inbCDN{body: []byte("PNGDATA"), ctype: "image/png"}
	files := newInbMemStore()
	h := newHarness(t, modtest.WithTransport(cdn), modtest.WithObjectStore(files))
	o, _ := owner(t, h)
	configure(t, o)
	for _, id := range []string{inbPage, inbOther} {
		h.Exec(t, `INSERT INTO meta.pages (page_id, name, token_sealed, added_at) VALUES ($1, 'Nordlys', '\x00', now())`, id)
	}
	d := h.Deps()
	return &inbFixture{h: h, cdn: cdn, files: files, split: meta.NewSplitWorker(d), ret: meta.NewRetentionWorker(d), m: meta.Module().ChannelMessenger(d)}
}

// post stores payload as Meta posts it, signed.
func (f *inbFixture) post(t *testing.T, payload any) {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if r := whPost(t, f.h, b, appSecret); r.Status != http.StatusOK {
		t.Fatalf("post: status %d", r.Status)
	}
}

// cycle runs one splitting cycle.
func (f *inbFixture) cycle(t *testing.T) {
	t.Helper()
	if err := f.split.RunOnce(context.Background()); err != nil {
		t.Fatalf("split: %v", err)
	}
}

// pending is the Page's events after the cursor.
func (f *inbFixture) pending(t *testing.T, page string, after int64) []contracts.MessengerEvent {
	t.Helper()
	ev, err := f.m.Pending(context.Background(), page, after, 200)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	return ev
}

// inbEnvelope is a webhook body for page with the messaging items.
func inbEnvelope(page string, items ...map[string]any) map[string]any {
	return map[string]any{"object": "page", "entry": []any{map[string]any{"id": page, "time": inbMs, "messaging": items}}}
}

// inbText is a message of a person's.
func inbText(mid, text string, extra map[string]any) map[string]any {
	msg := map[string]any{"mid": mid, "text": text}
	for k, v := range extra {
		msg[k] = v
	}
	return map[string]any{"sender": map[string]any{"id": inbUser}, "recipient": map[string]any{"id": inbPage}, "timestamp": inbMs, "message": msg}
}

func inbImage(url string) []any {
	return []any{map[string]any{"type": "image", "payload": map[string]any{"url": url}}}
}

func inbWatermark(kind string, ms int64) map[string]any {
	return map[string]any{"sender": map[string]any{"id": inbUser}, "recipient": map[string]any{"id": inbPage}, "timestamp": ms, kind: map[string]any{"watermark": ms}}
}

func TestSplit_EveryKindOfEvent(t *testing.T) {
	f := newInbFixture(t)
	postback := map[string]any{
		"sender": map[string]any{"id": inbUser}, "recipient": map[string]any{"id": inbPage}, "timestamp": inbMs + 5,
		"postback": map[string]any{"mid": "pb.1", "title": "Buy", "payload": contracts.MessengerPayload("msg-7", "buy")},
	}
	echo := map[string]any{
		"sender": map[string]any{"id": inbPage}, "recipient": map[string]any{"id": inbUser}, "timestamp": inbMs + 6,
		"message": map[string]any{"mid": "echo.1", "is_echo": true, "text": "From the inbox"},
	}
	f.post(t, inbEnvelope(inbPage,
		inbText("m.1", "Hello", map[string]any{"attachments": inbImage(inbImageURL)}),
		inbText("m.2", "Yes", map[string]any{"quick_reply": map[string]any{"payload": contracts.MessengerPayload("msg-1", "yes")}}),
		inbText("m.3", "kari@nordlys.no", map[string]any{"quick_reply": map[string]any{"payload": "kari@nordlys.no"}}),
		inbText("m.4", "plain", map[string]any{"quick_reply": map[string]any{"payload": "OTHER_APP_PAYLOAD"}}),
		inbText("m.5", "", map[string]any{"attachments": []any{
			map[string]any{"type": "template", "payload": map[string]any{"url": "https://example.test/x"}},
			map[string]any{"type": "fallback", "payload": map[string]any{}},
			map[string]any{"type": "location", "payload": map[string]any{"coordinates": map[string]any{"lat": 1, "long": 2}}},
		}}),
		postback, echo,
		inbWatermark("delivery", inbMs+7), inbWatermark("read", inbMs+8),
	))
	f.cycle(t)

	events := f.pending(t, inbPage, 0)
	if len(events) != 8 {
		t.Fatalf("%d events, want 8 (the template-only message dropped): %+v", len(events), events)
	}
	for i := 1; i < len(events); i++ {
		if events[i].Cursor <= events[i-1].Cursor {
			t.Errorf("cursors not ascending: %d then %d", events[i-1].Cursor, events[i].Cursor)
		}
	}

	hello := events[0]
	if hello.Kind != contracts.MessengerEventMessage || hello.Participant != inbUser || hello.PlatformMessageID != "m.1" ||
		hello.Text != "Hello" || hello.Echo || hello.Answer != nil || !hello.At.Equal(time.UnixMilli(inbMs)) {
		t.Errorf("the text message: %+v", hello)
	}
	if len(hello.Attachments) != 1 || hello.Attachments[0].FileName != "photo_123.png" || hello.Attachments[0].ContentType != "image/png" ||
		hello.Attachments[0].Ref != fmt.Sprintf("%d:0", hello.Cursor) {
		t.Errorf("the attachment: %+v", hello.Attachments)
	}

	if a := events[1].Answer; a == nil || a.MessageID != "msg-1" || a.ChoiceID != "yes" || events[1].Text != "Yes" {
		t.Errorf("the quick reply: %+v", events[1])
	}
	if a := events[2].Answer; a == nil || a.MessageID != "" || a.ChoiceID != contracts.MessengerShareEmail || events[2].Text != "kari@nordlys.no" {
		t.Errorf("the e-mail quick reply: %+v", events[2])
	}
	if events[3].Answer != nil || events[3].Text != "plain" {
		t.Errorf("a quick reply that is not ours: %+v", events[3])
	}
	pb := events[4]
	if pb.Kind != contracts.MessengerEventMessage || pb.PlatformMessageID != "pb.1" || pb.Text != "Buy" || pb.Answer == nil || pb.Answer.MessageID != "msg-7" || pb.Answer.ChoiceID != "buy" {
		t.Errorf("the postback: %+v", pb)
	}
	if e := events[5]; !e.Echo || e.Participant != inbUser || e.PlatformMessageID != "echo.1" || e.Text != "From the inbox" || e.Answer != nil {
		t.Errorf("the echo: %+v", e)
	}
	if e := events[6]; e.Kind != contracts.MessengerEventDelivered || e.Participant != inbUser || !e.Watermark.Equal(time.UnixMilli(inbMs+7)) {
		t.Errorf("delivered: %+v", e)
	}
	if e := events[7]; e.Kind != contracts.MessengerEventRead || !e.Watermark.Equal(time.UnixMilli(inbMs+8)) {
		t.Errorf("read: %+v", e)
	}

	if n := f.h.Count(t, `SELECT count(*) FROM meta.webhook_events WHERE state = 'split' AND split_at IS NOT NULL`); n != 1 {
		t.Errorf("the event is not marked split")
	}
	keys := f.files.keys()
	if len(keys) != 1 || !strings.HasPrefix(keys[0], inbPage+"/") || !strings.HasSuffix(keys[0], "/0") || len(strings.Split(keys[0], "/")[1]) != 32 {
		t.Errorf("stored files: %v", keys)
	}
	if got := f.files.types[keys[0]]; got != "image/png" {
		t.Errorf("stored content type %q", got)
	}
	if len(f.pending(t, inbOther, 0)) != 0 {
		t.Error("another Page sees the events")
	}
}

func TestSplit_DropsWhatIsNotForAConnectedPage(t *testing.T) {
	f := newInbFixture(t)
	f.post(t, inbEnvelope("424242", inbText("m.x", "stranger", nil)))
	f.post(t, map[string]any{"object": "instagram", "entry": []any{map[string]any{"id": inbPage, "messaging": []any{inbText("m.y", "other object", nil)}}}})
	f.cycle(t)
	if n := f.h.Count(t, `SELECT count(*) FROM meta.inbound`); n != 0 {
		t.Fatalf("%d rows from events that are not for a Page", n)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM meta.webhook_events WHERE state = 'split'`); n != 2 {
		t.Errorf("%d events settled, want 2", n)
	}
}

func TestSplit_DedupsAMessageAndAWatermark(t *testing.T) {
	f := newInbFixture(t)
	f.post(t, inbEnvelope(inbPage, inbText("m.dup", "Once", nil), inbWatermark("delivery", inbMs+1)))
	f.post(t, inbEnvelope(inbPage, inbText("m.dup", "Once", nil), inbWatermark("delivery", inbMs+1), inbWatermark("delivery", inbMs+2), inbWatermark("read", inbMs+1)))
	f.cycle(t)

	events := f.pending(t, inbPage, 0)
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	if want := []string{"message", "delivered", "delivered", "read"}; !slices.Equal(kinds, want) {
		t.Fatalf("kinds %v, want %v", kinds, want)
	}
}

func TestSplit_KeepsTheOrderOfSeveralEvents(t *testing.T) {
	f := newInbFixture(t)
	for i := 1; i <= 3; i++ {
		f.post(t, inbEnvelope(inbPage, inbText(fmt.Sprintf("m.%d", i), fmt.Sprintf("message %d", i), nil)))
	}
	f.cycle(t)
	events := f.pending(t, inbPage, 0)
	if len(events) != 3 {
		t.Fatalf("%d events", len(events))
	}
	for i, e := range events {
		if e.Text != fmt.Sprintf("message %d", i+1) {
			t.Errorf("event %d is %q", i, e.Text)
		}
	}
	after := f.pending(t, inbPage, events[0].Cursor)
	if len(after) != 2 || after[0].Text != "message 2" {
		t.Errorf("after the first: %+v", after)
	}
	if got, _ := f.m.Pending(context.Background(), inbPage, 0, 0); len(got) != 1 {
		t.Errorf("limit 0 is clamped to 1, got %d", len(got))
	}
	if got, _ := f.m.Pending(context.Background(), inbPage, 0, 100000); len(got) != 3 {
		t.Errorf("a huge limit is clamped, got %d", len(got))
	}
}

func TestSplit_FailsAPayloadItCannotRead(t *testing.T) {
	f := newInbFixture(t)
	b := []byte(`[1, 2]`)
	if r := whPost(t, f.h, b, appSecret); r.Status != http.StatusOK {
		t.Fatalf("post: %d", r.Status)
	}
	f.cycle(t)
	if n := f.h.Count(t, `SELECT count(*) FROM meta.webhook_events WHERE state = 'failed' AND last_error IS NOT NULL`); n != 1 {
		t.Errorf("the unreadable event is not marked failed")
	}
	f.cycle(t)
	if n := f.h.Count(t, `SELECT attempts FROM meta.webhook_events`); n != 1 {
		t.Errorf("a failed event was claimed again (%d attempts)", n)
	}
}

func TestSplit_DefersACDNFailureAndDropsTheFileAfterFiveAttempts(t *testing.T) {
	f := newInbFixture(t)
	f.cdn.set(func(c *inbCDN) { c.fail = true })
	f.post(t, inbEnvelope(inbPage, inbText("m.cdn", "See photo", map[string]any{"attachments": inbImage(inbImageURL)})))

	for attempt := 1; attempt <= 4; attempt++ {
		f.cycle(t)
		if n := f.h.Count(t, `SELECT count(*) FROM meta.webhook_events WHERE state = 'pending' AND attempts = $1 AND last_error IS NOT NULL AND next_attempt_at > $2`, attempt, f.h.Now()); n != 1 {
			t.Fatalf("attempt %d: the event is not deferred", attempt)
		}
		if n := f.h.Count(t, `SELECT count(*) FROM meta.inbound`); n != 0 {
			t.Fatalf("attempt %d: a row exists", attempt)
		}
		// Not due yet: a cycle at once claims nothing.
		calls := f.cdn.count()
		f.cycle(t)
		if f.cdn.count() != calls {
			t.Fatalf("attempt %d: a deferred event was tried before its time", attempt)
		}
		f.h.Advance(31 * time.Minute)
	}
	f.cycle(t)
	events := f.pending(t, inbPage, 0)
	if len(events) != 1 || events[0].Text != "See photo" || len(events[0].Attachments) != 0 {
		t.Fatalf("after the fifth attempt: %+v", events)
	}
	if len(f.files.keys()) != 0 {
		t.Errorf("files stored: %v", f.files.keys())
	}
	if strings.Contains(f.h.Logs(), "fbcdn") {
		t.Error("the log carries the attachment's address")
	}
}

func TestSplit_DropsAMessageOfOnlyAFileThatCannotBeKept(t *testing.T) {
	f := newInbFixture(t)
	f.cdn.set(func(c *inbCDN) { c.fail = true })
	f.post(t, inbEnvelope(inbPage, inbText("m.only", "", map[string]any{"attachments": inbImage(inbImageURL)}), inbText("m.next", "after", nil)))
	for range 5 {
		f.cycle(t)
		f.h.Advance(31 * time.Minute)
	}
	events := f.pending(t, inbPage, 0)
	if len(events) != 1 || events[0].Text != "after" {
		t.Fatalf("events %+v", events)
	}
}

func TestSplit_RefusesAFileOverTheLimit(t *testing.T) {
	f := newInbFixture(t)
	f.cdn.set(func(c *inbCDN) { c.body = make([]byte, 25<<20+1) })
	f.post(t, inbEnvelope(inbPage, inbText("m.big", "big", map[string]any{"attachments": inbImage(inbImageURL)})))
	f.cycle(t)
	if n := f.h.Count(t, `SELECT count(*) FROM meta.inbound`); n != 0 || len(f.files.keys()) != 0 {
		t.Fatalf("a file over 25 MiB was kept (%d rows, %v)", n, f.files.keys())
	}
	f.cdn.set(func(c *inbCDN) { c.body = make([]byte, 25<<20) })
	f.h.Advance(time.Minute)
	f.cycle(t)
	if n := f.h.Count(t, `SELECT count(*) FROM meta.inbound`); n != 1 {
		t.Fatalf("a file of exactly 25 MiB was refused")
	}
}

func TestSplit_RefusesAHostOutsideMetasCDN(t *testing.T) {
	f := newInbFixture(t)
	f.post(t, inbEnvelope(inbPage, inbText("m.evil", "x", map[string]any{"attachments": inbImage("https://evil.example.test/a.png")})))
	f.cycle(t)
	if f.cdn.count() != 0 {
		t.Fatal("the CDN fake was asked for a foreign host")
	}
	if n := f.h.Count(t, `SELECT count(*) FROM meta.inbound`); n != 0 {
		t.Fatal("a row exists for a foreign file")
	}
}

func TestSplit_FileNamesAndTypes(t *testing.T) {
	f := newInbFixture(t)
	f.cdn.set(func(c *inbCDN) { c.ctype = "" })
	f.post(t, inbEnvelope(inbPage, inbText("m.files", "files", map[string]any{"attachments": []any{
		map[string]any{"type": "audio", "payload": map[string]any{"url": "https://cdn.fbsbx.com/v/audio"}},
		map[string]any{"type": "file", "payload": map[string]any{"url": "https://cdn.fbsbx.com/v/Report%20Q3.pdf?x=1"}},
		map[string]any{"type": "video", "payload": map[string]any{"url": "https://video.xx.fbcdn.net/"}},
	}})))
	f.cycle(t)
	events := f.pending(t, inbPage, 0)
	if len(events) != 1 || len(events[0].Attachments) != 3 {
		t.Fatalf("events %+v", events)
	}
	a := events[0].Attachments
	if a[0].FileName != "audio.mp3" || a[0].ContentType != "audio/mpeg" {
		t.Errorf("audio %+v", a[0])
	}
	if a[1].FileName != "Report Q3.pdf" || a[1].ContentType != "application/octet-stream" {
		t.Errorf("file %+v", a[1])
	}
	if a[2].ContentType != "video/mp4" || !strings.HasPrefix(a[2].FileName, "attachment-2") || !strings.HasSuffix(a[2].FileName, ".mp4") {
		t.Errorf("video %+v", a[2])
	}
}

func TestInbound_PendingNeedsAConnectedPage(t *testing.T) {
	f := newInbFixture(t)
	if _, err := f.m.Pending(context.Background(), "000", 0, 10); !errors.Is(err, contracts.ErrMessengerEndpointUnknown) {
		t.Fatalf("an unknown Page: %v", err)
	}
}

func TestInbound_AckIsIdempotentAndKeepsTheRows(t *testing.T) {
	f := newInbFixture(t)
	ctx := context.Background()
	f.post(t, inbEnvelope(inbPage, inbText("m.a", "a", map[string]any{"attachments": inbImage(inbImageURL)}), inbText("m.b", "b", nil), inbText("m.c", "c", nil)))
	f.cycle(t)
	events := f.pending(t, inbPage, 0)

	if err := f.m.Ack(ctx, inbPage, events[1].Cursor); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if err := f.m.Ack(ctx, inbPage, events[1].Cursor); err != nil {
		t.Fatalf("Ack again: %v", err)
	}
	if err := f.m.Ack(ctx, inbOther, events[2].Cursor); err != nil {
		t.Fatalf("Ack of another Page: %v", err)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM meta.inbound WHERE acked_at IS NOT NULL`); n != 2 {
		t.Errorf("%d rows acknowledged, want 2 (not the third, not by another Page)", n)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM meta.inbound`); n != 3 || len(f.files.keys()) != 1 {
		t.Error("Ack deleted rows or files")
	}
	// The rows stay readable after the acknowledgement, for 7 days.
	if got := f.pending(t, inbPage, 0); len(got) != 3 {
		t.Errorf("%d events after Ack, want 3", len(got))
	}
}

func TestInbound_NotifiedAnswersPagesWithUnacknowledgedNews(t *testing.T) {
	f := newInbFixture(t)
	ctx := context.Background()
	f.h.Advance(time.Hour)
	f.post(t, inbEnvelope(inbPage, inbText("m.n", "news", nil)))
	f.cycle(t)
	created := f.h.Now()

	pages, err := f.m.Notified(ctx, created.Add(-time.Second))
	if err != nil || !slices.Equal(pages, []string{inbPage}) {
		t.Fatalf("Notified before: %v %v", pages, err)
	}
	// since is exclusive: news that arrived at since is not after it.
	if pages, err := f.m.Notified(ctx, created); err != nil || len(pages) != 0 {
		t.Fatalf("Notified at the arrival: %v %v", pages, err)
	}
	f.h.Advance(time.Minute)
	if pages, _ := f.m.Notified(ctx, created.Add(-time.Second)); len(pages) != 1 {
		t.Fatalf("Notified later: %v", pages)
	}
	events := f.pending(t, inbPage, 0)
	if err := f.m.Ack(ctx, inbPage, events[0].Cursor); err != nil {
		t.Fatal(err)
	}
	if pages, _ := f.m.Notified(ctx, created.Add(-time.Second)); len(pages) != 0 {
		t.Fatalf("Notified after the Ack: %v", pages)
	}
}

func TestInbound_OpenAttachment(t *testing.T) {
	f := newInbFixture(t)
	ctx := context.Background()
	f.post(t, inbEnvelope(inbPage, inbText("m.f", "with file", map[string]any{"attachments": inbImage(inbImageURL)}), inbText("m.g", "without", nil)))
	f.cycle(t)
	events := f.pending(t, inbPage, 0)
	ref := events[0].Attachments[0].Ref

	rc, file, err := f.m.OpenAttachment(ctx, inbPage, ref)
	if err != nil {
		t.Fatalf("OpenAttachment: %v", err)
	}
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(data) != "PNGDATA" || file.FileName != "photo_123.png" || file.ContentType != "image/png" || file.Size != 7 {
		t.Errorf("file %+v data %q", file, data)
	}

	for name, tc := range map[string]struct{ page, ref string }{
		"another Page's":      {inbOther, ref},
		"an index past":       {inbPage, fmt.Sprintf("%d:1", events[0].Cursor)},
		"a row without files": {inbPage, fmt.Sprintf("%d:0", events[1].Cursor)},
		"a missing row":       {inbPage, "999999:0"},
		"no colon":            {inbPage, "12"},
		"not numbers":         {inbPage, "a:b"},
		"a negative index":    {inbPage, fmt.Sprintf("%d:-1", events[0].Cursor)},
		"empty":               {inbPage, ""},
	} {
		if _, _, err := f.m.OpenAttachment(ctx, tc.page, tc.ref); err == nil {
			t.Errorf("%s ref opened", name)
		}
	}
}
