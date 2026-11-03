package mail_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/mail"
)

const (
	cfAccount = "0123456789abcdef0123456789abcdef"
	cfToken   = "cf-test-token"
)

func cloudflareConfig() config.MailConfig {
	return config.MailConfig{
		Driver:              "cloudflare",
		From:                "noreply@vantigo.example",
		CloudflareAccountID: cfAccount,
		CloudflareAPIToken:  cfToken,
	}
}

// cloudflareAPI is an in-process stand-in for the send endpoint: it records
// each request and answers status with body.
type cloudflareAPI struct {
	t        *testing.T
	status   int
	body     string
	requests []recordedCloudflareRequest
}

type recordedCloudflareRequest struct {
	method, path, auth string
	payload            map[string]any
}

func (a *cloudflareAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		a.t.Errorf("request body is not JSON: %v: %s", err, raw)
	}
	a.requests = append(a.requests, recordedCloudflareRequest{r.Method, r.URL.Path, r.Header.Get("Authorization"), payload})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(a.status)
	_, _ = io.WriteString(w, a.body)
}

const cfDelivered = `{"success":true,"errors":[],"messages":[],"result":{"delivered":["person@example.test"],"permanent_bounces":[],"queued":[]}}`

func newCloudflareAPI(t *testing.T, status int, body string) *cloudflareAPI {
	t.Helper()
	api := &cloudflareAPI{t: t, status: status, body: body}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	t.Cleanup(mail.UseCloudflareForTests(srv.URL, srv.Client().Transport))
	return api
}

func TestNew_SelectsCloudflareDriver(t *testing.T) {
	api := newCloudflareAPI(t, http.StatusOK, cfDelivered)
	sender, err := mail.New(&config.Config{Env: config.Production, Mail: cloudflareConfig()}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	if err := sender.Send(context.Background(), mail.Message{To: "person@example.test", Subject: "Reset your password", TextBody: "https://app.example/reset?token=x"}); err != nil {
		t.Fatalf("Send() = %v", err)
	}
	if len(api.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(api.requests))
	}
	got := api.requests[0]
	if got.method != http.MethodPost || got.path != "/accounts/"+cfAccount+"/email/sending/send" {
		t.Errorf("request = %s %s", got.method, got.path)
	}
	if got.auth != "Bearer "+cfToken {
		t.Errorf("Authorization = %q", got.auth)
	}
	want := map[string]any{
		"from":    "noreply@vantigo.example",
		"to":      []any{"person@example.test"},
		"subject": "Reset your password",
		"text":    "https://app.example/reset?token=x",
	}
	gotJSON, _ := json.Marshal(got.payload)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("payload = %s, want %s", gotJSON, wantJSON)
	}
}

func TestSendOutbound_Cloudflare_FullEnvelope(t *testing.T) {
	api := newCloudflareAPI(t, http.StatusOK, cfDelivered)
	err := mail.SendOutbound(context.Background(), cloudflareConfig(), mail.Outbound{
		DisplayName: "Vantigo",
		To:          []string{"a@example.test"},
		Cc:          []string{"b@example.test"},
		Bcc:         []string{"c@example.test"},
		ReplyTo:     "hr@example.test",
		Subject:     "Your certificate expires",
		TextBody:    "text",
		HTMLBody:    "<p>html</p>",
		MessageID:   "ignored@vantigo.invalid",
		InReplyTo:   "parent@vantigo.invalid",
		References:  []string{"root@vantigo.invalid", "<parent@vantigo.invalid>"},
		Attachments: []mail.Attachment{{FileName: "cert.pdf", ContentType: "application/pdf", Content: []byte("%PDF")}},
	})
	if err != nil {
		t.Fatalf("SendOutbound() = %v", err)
	}
	p := api.requests[0].payload
	if from, _ := p["from"].(map[string]any); from["address"] != "noreply@vantigo.example" || from["name"] != "Vantigo" {
		t.Errorf("from = %v", p["from"])
	}
	if p["reply_to"] != "hr@example.test" || p["html"] != "<p>html</p>" {
		t.Errorf("payload = %v", p)
	}
	for _, field := range []string{"to", "cc", "bcc"} {
		if list, _ := p[field].([]any); len(list) != 1 {
			t.Errorf("%s = %v", field, p[field])
		}
	}
	headers, _ := p["headers"].(map[string]any)
	if headers["In-Reply-To"] != "<parent@vantigo.invalid>" || headers["References"] != "<root@vantigo.invalid> <parent@vantigo.invalid>" {
		t.Errorf("headers = %v", headers)
	}
	if _, ok := headers["Message-ID"]; ok {
		t.Error("Message-ID is platform-controlled and must not be sent")
	}
	atts, _ := p["attachments"].([]any)
	if len(atts) != 1 {
		t.Fatalf("attachments = %v", p["attachments"])
	}
	att := atts[0].(map[string]any)
	if att["content"] != base64.StdEncoding.EncodeToString([]byte("%PDF")) || att["filename"] != "cert.pdf" || att["type"] != "application/pdf" || att["disposition"] != "attachment" {
		t.Errorf("attachment = %v", att)
	}
}

func TestSendOutbound_Cloudflare_ReportsAPIError(t *testing.T) {
	newCloudflareAPI(t, http.StatusTooManyRequests, `{"success":false,"errors":[{"code":10004,"message":"email.sending.error.throttled"}],"messages":[],"result":null}`)
	err := mail.SendOutbound(context.Background(), cloudflareConfig(), mail.Outbound{To: []string{"a@example.test"}, Subject: "s", TextBody: "secret-body"})
	if err == nil {
		t.Fatal("SendOutbound() = nil, want the API's refusal")
	}
	for _, want := range []string{"HTTP 429", "10004", "email.sending.error.throttled"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	for _, leak := range []string{cfToken, "secret-body"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q leaks %q", err, leak)
		}
	}
}

func TestSendOutbound_Cloudflare_PermanentBounceIsAFailure(t *testing.T) {
	newCloudflareAPI(t, http.StatusOK, `{"success":true,"errors":[],"messages":[],"result":{"delivered":[],"permanent_bounces":["a@example.test"],"queued":[]}}`)
	if err := mail.SendOutbound(context.Background(), cloudflareConfig(), mail.Outbound{To: []string{"a@example.test"}, Subject: "s", TextBody: "t"}); err == nil || !strings.Contains(err.Error(), "permanently bounced") {
		t.Fatalf("SendOutbound() = %v, want a bounce error", err)
	}
}

func TestSendOutbound_Cloudflare_QueuedIsSuccess(t *testing.T) {
	newCloudflareAPI(t, http.StatusOK, `{"success":true,"errors":[],"messages":[],"result":{"delivered":[],"permanent_bounces":[],"queued":["a@example.test"]}}`)
	if err := mail.SendOutbound(context.Background(), cloudflareConfig(), mail.Outbound{To: []string{"a@example.test"}, Subject: "s", TextBody: "t"}); err != nil {
		t.Fatalf("SendOutbound() = %v, want nil for a queued send", err)
	}
}

func TestSendOutbound_Cloudflare_RefusesWhatItCannotCarry(t *testing.T) {
	api := newCloudflareAPI(t, http.StatusOK, cfDelivered)
	many := make([]string, 51)
	for i := range many {
		many[i] = "r@example.test"
	}
	for name, out := range map[string]mail.Outbound{
		"no recipient":      {Subject: "s"},
		"over 50":           {To: many, Subject: "s"},
		"calendar part":     {To: []string{"a@example.test"}, Calendar: &mail.CalendarPart{Method: "REQUEST", Body: "BEGIN:VCALENDAR"}},
		"inline attachment": {To: []string{"a@example.test"}, Attachments: []mail.Attachment{{FileName: "logo.png", ContentID: "logo", Inline: true}}},
	} {
		if err := mail.SendOutbound(context.Background(), cloudflareConfig(), out); err == nil {
			t.Errorf("%s: SendOutbound() = nil, want a refusal", name)
		}
	}
	if len(api.requests) != 0 {
		t.Errorf("a refused message reached the API %d times", len(api.requests))
	}
}

func TestSendOutbound_Cloudflare_DoesNotFollowRedirects(t *testing.T) {
	newCloudflareAPI(t, http.StatusTemporaryRedirect, "")
	if err := mail.SendOutbound(context.Background(), cloudflareConfig(), mail.Outbound{To: []string{"a@example.test"}, Subject: "s"}); err == nil || !strings.Contains(err.Error(), "HTTP 307") {
		t.Fatalf("SendOutbound() = %v, want the redirect reported as a refusal", err)
	}
}

func TestNewCloudflare_RequiresItsSettings(t *testing.T) {
	for _, mutate := range []func(*config.MailConfig){
		func(c *config.MailConfig) { c.CloudflareAccountID = "" },
		func(c *config.MailConfig) { c.CloudflareAPIToken = "" },
		func(c *config.MailConfig) { c.From = "" },
	} {
		cfg := cloudflareConfig()
		mutate(&cfg)
		if _, err := mail.NewCloudflare(cfg); err == nil {
			t.Errorf("NewCloudflare(%+v) = nil error", cfg)
		}
	}
}
