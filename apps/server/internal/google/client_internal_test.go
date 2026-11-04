package google

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// The guarded client (client.go): its hosts, its retries and its caps, and
// Google's words never logged.

// staticSource is a tokenSource answering one token.
type staticSource struct{ drops int }

func (s *staticSource) token(context.Context) (string, error) { return "a-token", nil }
func (s *staticSource) dropped()                              { s.drops++ }

// scripted answers each request with the next of its answers.
type scripted struct {
	mu      sync.Mutex
	answers []*http.Response
	seen    []*url.URL
}

func (s *scripted) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, req.URL)
	if len(s.answers) == 0 {
		return nil, errors.New("no answer left")
	}
	a := s.answers[0]
	s.answers = s.answers[1:]
	a.Request = req
	return a, nil
}

func answerOf(status int, body string, header ...string) *http.Response {
	h := http.Header{}
	for i := 0; i+1 < len(header); i += 2 {
		h.Set(header[i], header[i+1])
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

// testClient is a client over rt logging into log, its waits recorded.
func testClient(rt http.RoundTripper, log *bytes.Buffer, waits *[]time.Duration) *googleClient {
	c := &googleClient{
		http:   &http.Client{Transport: hostGuard{next: rt}},
		tokens: &tokenCache{},
		now:    time.Now,
		logger: slog.New(slog.NewTextHandler(log, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	c.sleep = func(_ context.Context, d time.Duration) error { *waits = append(*waits, d); return nil }
	return c
}

func TestClient_OnlyTheFixedHosts(t *testing.T) {
	t.Parallel()
	rt := &scripted{}
	var log bytes.Buffer
	var waits []time.Duration
	c := testClient(rt, &log, &waits)
	for _, target := range []string{
		"https://evil.example/admin/directory/v1/customers/my_customer",
		"http://admin.googleapis.com/admin/directory/v1/customers/my_customer",
		"https://admin.googleapis.com:8443/x",
		"https://user@admin.googleapis.com/x",
		"https://admin.googleapis.com.evil.example/x",
		"https://admin.googleapis.com/admin/../../x",
	} {
		if err := c.get(context.Background(), &staticSource{}, target, nil); err == nil {
			t.Errorf("%s: answered, want refused", target)
		}
	}
	// The guard is on the transport too, so the token exchange — made by
	// golang.org/x/oauth2 with the client from the context — is held to it.
	req, _ := http.NewRequest(http.MethodPost, "https://oauth2.evil.example/token", nil)
	if _, err := c.http.Do(req); err == nil {
		t.Errorf("a token request elsewhere went through")
	}
	if len(rt.seen) != 0 {
		t.Errorf("%d requests reached the transport", len(rt.seen))
	}
}

func TestClient_RetriesAndCaps(t *testing.T) {
	t.Parallel()
	words := "Google's words about the refusal"
	refusal := `{"error":{"code":403,"message":"` + words + `","errors":[{"reason":"forbidden","message":"` + words + `"}],"status":"PERMISSION_DENIED"}}`
	target := directoryBase + "/customers/my_customer"

	t.Run("a 5xx once, then the answer", func(t *testing.T) {
		rt := &scripted{answers: []*http.Response{answerOf(503, ""), answerOf(200, `{"id":"C1"}`)}}
		var log bytes.Buffer
		var waits []time.Duration
		var into struct{ ID string }
		if err := testClient(rt, &log, &waits).get(context.Background(), &staticSource{}, target, &into); err != nil || into.ID != "C1" {
			t.Errorf("get: %v %+v", err, into)
		}
		if len(waits) != 1 || waits[0] != serverErrorDelay {
			t.Errorf("waits = %v", waits)
		}
	})
	t.Run("a 5xx twice is unreachable", func(t *testing.T) {
		rt := &scripted{answers: []*http.Response{answerOf(503, ""), answerOf(502, "")}}
		var log bytes.Buffer
		var waits []time.Duration
		if err := testClient(rt, &log, &waits).get(context.Background(), &staticSource{}, target, nil); !errors.Is(err, errGoogleUnreachable) {
			t.Errorf("get: %v, want errGoogleUnreachable", err)
		}
	})
	t.Run("a 429 waits Retry-After once", func(t *testing.T) {
		rt := &scripted{answers: []*http.Response{answerOf(429, "", "Retry-After", "3"), answerOf(429, "", "Retry-After", "3")}}
		var log bytes.Buffer
		var waits []time.Duration
		if err := testClient(rt, &log, &waits).get(context.Background(), &staticSource{}, target, nil); !errors.Is(err, errThrottled) {
			t.Errorf("get: %v, want errThrottled", err)
		}
		if len(waits) != 1 || waits[0] != 3*time.Second {
			t.Errorf("waits = %v", waits)
		}
	})
	t.Run("a Retry-After over ten seconds is not waited", func(t *testing.T) {
		rt := &scripted{answers: []*http.Response{answerOf(429, "", "Retry-After", "11")}}
		var log bytes.Buffer
		var waits []time.Duration
		if err := testClient(rt, &log, &waits).get(context.Background(), &staticSource{}, target, nil); !errors.Is(err, errThrottled) || len(waits) != 0 {
			t.Errorf("get: %v, waits %v", err, waits)
		}
	})
	t.Run("a 401 drops the token once", func(t *testing.T) {
		rt := &scripted{answers: []*http.Response{answerOf(401, ""), answerOf(200, `{}`)}}
		var log bytes.Buffer
		var waits []time.Duration
		src := &staticSource{}
		if err := testClient(rt, &log, &waits).get(context.Background(), src, target, nil); err != nil || src.drops != 1 {
			t.Errorf("get: %v, drops %d", err, src.drops)
		}
	})
	t.Run("an answer over 1 MiB", func(t *testing.T) {
		rt := &scripted{answers: []*http.Response{answerOf(200, `"`+strings.Repeat("x", maxBody)+`"`)}}
		var log bytes.Buffer
		var waits []time.Duration
		if err := testClient(rt, &log, &waits).get(context.Background(), &staticSource{}, target, nil); !errors.Is(err, errResponseTooLarge) {
			t.Errorf("get: %v, want errResponseTooLarge", err)
		}
	})
	t.Run("a refusal is its status and reason, never its words", func(t *testing.T) {
		rt := &scripted{answers: []*http.Response{answerOf(403, refusal)}}
		var log bytes.Buffer
		var waits []time.Duration
		err := testClient(rt, &log, &waits).get(context.Background(), &staticSource{}, target+"?quotaUser=123", nil)
		var g *googleError
		if !errors.As(err, &g) || g.Status != 403 || g.Reason != "forbidden" || !errors.Is(err, errAdminRefused) {
			t.Errorf("get: %v, want a 403 forbidden", err)
		}
		if strings.Contains(err.Error(), words) || strings.Contains(log.String(), words) {
			t.Errorf("Google's words escaped: %v / %s", err, log.String())
		}
		if strings.Contains(log.String(), "quotaUser") {
			t.Errorf("the log carries the query: %s", log.String())
		}
	})
}

func TestTokenCache_KeepsUntilFiveMinutesBeforeExpiry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	c := &tokenCache{}
	key := tokenKey{id: 1, subject: "admin@nordlys.no", scopes: "a b"}
	c.put(key, cachedToken{value: "t", until: now.Add(time.Hour - tokenEarlyExpiry)}, now)
	if _, ok := c.get(key, now.Add(54*time.Minute)); !ok {
		t.Errorf("a token gone before its time")
	}
	if _, ok := c.get(key, now.Add(55*time.Minute)); ok {
		t.Errorf("a token kept within five minutes of its expiry")
	}
	other := tokenKey{id: 1, subject: "admin@nordlys.no", scopes: "a"}
	if _, ok := c.get(other, now); ok {
		t.Errorf("another scope set shares a token")
	}
}
