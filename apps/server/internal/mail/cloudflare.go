package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/netguard"
)

// cloudflareAPIBase is Cloudflare's API origin. Tests replace it, and the
// transport, through export_test.go; no configuration field reaches either.
const cloudflareAPIBase = "https://api.cloudflare.com/client/v4"

var (
	testCloudflareBase      string
	testCloudflareTransport http.RoundTripper
)

const (
	// cloudflareSendTimeout bounds one send when the caller's own deadline
	// is later or absent: a stalled API must not hold a password reset open.
	cloudflareSendTimeout = 30 * time.Second
	// cloudflareResponseMaxBytes bounds the response read; the API answers
	// a send with a few hundred bytes of JSON.
	cloudflareResponseMaxBytes = 64 << 10
	// cloudflareMaxRecipients is the API's limit across to, cc and bcc,
	// checked here so the refusal names the cause.
	cloudflareMaxRecipients = 50
)

// cloudflareSender delivers mail through Cloudflare Email Service's REST
// API (POST /accounts/{account_id}/email/sending/send). It carries the
// system mail — identity's invitations and password resets, Staff's notices
// — and nothing else: communications' channels always speak SMTP.
type cloudflareSender struct {
	endpoint string
	token    string
	from     string
	client   *http.Client
}

// NewCloudflare builds a Sender over cfg's Cloudflare account, API token and
// from address. The from address's domain must be onboarded to Email Sending
// in that account; the API refuses the send otherwise.
func NewCloudflare(cfg config.MailConfig) (Sender, error) {
	return newCloudflare(cfg)
}

func newCloudflare(cfg config.MailConfig) (*cloudflareSender, error) {
	if cfg.CloudflareAccountID == "" {
		return nil, fmt.Errorf("mail: cloudflare account ID is required")
	}
	if cfg.CloudflareAPIToken == "" {
		return nil, fmt.Errorf("mail: cloudflare API token is required")
	}
	if cfg.From == "" {
		return nil, fmt.Errorf("mail: cloudflare from address is required")
	}
	base := cloudflareAPIBase
	if testCloudflareBase != "" {
		base = testCloudflareBase
	}
	transport := testCloudflareTransport
	if transport == nil {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		transport = &http.Transport{
			Proxy:                 nil,
			DialContext:           netguard.DialContext(&net.Resolver{}, dialer.DialContext),
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: cloudflareSendTimeout,
		}
	}
	return &cloudflareSender{
		endpoint: base + "/accounts/" + url.PathEscape(cfg.CloudflareAccountID) + "/email/sending/send",
		token:    cfg.CloudflareAPIToken,
		from:     cfg.From,
		client: &http.Client{
			Transport: transport,
			// A redirect would re-send the request, token included, to a
			// host no operator named.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func (s *cloudflareSender) Send(ctx context.Context, m Message) error {
	return s.send(ctx, Outbound{To: []string{m.To}, Subject: m.Subject, TextBody: m.TextBody})
}

// cloudflareAddress is the API's named-address object; a bare address is
// sent as a plain string instead.
type cloudflareAddress struct {
	Address string `json:"address"`
	Name    string `json:"name"`
}

type cloudflareAttachment struct {
	Content     string `json:"content"`
	FileName    string `json:"filename"`
	Type        string `json:"type"`
	Disposition string `json:"disposition"`
}

type cloudflareRequest struct {
	From        any                    `json:"from"`
	To          []string               `json:"to,omitempty"`
	Cc          []string               `json:"cc,omitempty"`
	Bcc         []string               `json:"bcc,omitempty"`
	ReplyTo     string                 `json:"reply_to,omitempty"`
	Subject     string                 `json:"subject"`
	Text        string                 `json:"text,omitempty"`
	HTML        string                 `json:"html,omitempty"`
	Headers     map[string]string      `json:"headers,omitempty"`
	Attachments []cloudflareAttachment `json:"attachments,omitempty"`
}

type cloudflareResponse struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result *struct {
		Delivered        []string `json:"delivered"`
		PermanentBounces []string `json:"permanent_bounces"`
		Queued           []string `json:"queued"`
	} `json:"result"`
}

// request is out as the API's JSON body. Message-ID is platform-controlled
// there — the API refuses the header — so out.MessageID is not carried; the
// threading headers are. A calendar part or an inline image has no field to
// travel in, so either is refused rather than silently dropped.
func (s *cloudflareSender) request(out Outbound) (cloudflareRequest, error) {
	n := len(out.To) + len(out.Cc) + len(out.Bcc)
	if n == 0 {
		return cloudflareRequest{}, fmt.Errorf("mail: an outbound message needs at least one recipient")
	}
	if n > cloudflareMaxRecipients {
		return cloudflareRequest{}, fmt.Errorf("mail: cloudflare takes at most %d recipients per message, not %d", cloudflareMaxRecipients, n)
	}
	if out.Calendar != nil {
		return cloudflareRequest{}, fmt.Errorf("mail: the cloudflare driver cannot carry a calendar part")
	}
	req := cloudflareRequest{
		From:    s.from,
		To:      out.To,
		Cc:      out.Cc,
		Bcc:     out.Bcc,
		ReplyTo: out.ReplyTo,
		Subject: out.Subject,
		Text:    out.TextBody,
		HTML:    out.HTMLBody,
	}
	if out.DisplayName != "" {
		req.From = cloudflareAddress{Address: s.from, Name: out.DisplayName}
	}
	if out.InReplyTo != "" || len(out.References) > 0 {
		req.Headers = map[string]string{}
		if out.InReplyTo != "" {
			req.Headers["In-Reply-To"] = bracketedContentID(out.InReplyTo)
		}
		if len(out.References) > 0 {
			refs := make([]string, len(out.References))
			for i, r := range out.References {
				refs[i] = bracketedContentID(r)
			}
			req.Headers["References"] = strings.Join(refs, " ")
		}
	}
	for _, a := range out.Attachments {
		if a.Inline || a.ContentID != "" {
			return cloudflareRequest{}, fmt.Errorf("mail: the cloudflare driver cannot carry an inline attachment")
		}
		req.Attachments = append(req.Attachments, cloudflareAttachment{
			Content:     base64.StdEncoding.EncodeToString(a.Content),
			FileName:    a.FileName,
			Type:        a.ContentType,
			Disposition: "attachment",
		})
	}
	return req, nil
}

func (s *cloudflareSender) send(ctx context.Context, out Outbound) error {
	body, err := s.request(out)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("mail: encoding cloudflare request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, cloudflareSendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("mail: building cloudflare request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("mail: sending through cloudflare: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, cloudflareResponseMaxBytes))
	if err != nil {
		return fmt.Errorf("mail: reading cloudflare response: %w", err)
	}

	var parsed cloudflareResponse
	decodeErr := json.Unmarshal(raw, &parsed)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || decodeErr != nil || !parsed.Success {
		return cloudflareError(resp.StatusCode, parsed)
	}
	// A permanent bounce is reported inside a successful response; the mail
	// did not arrive, so the caller must hear it as a failure.
	if parsed.Result != nil && len(parsed.Result.PermanentBounces) > 0 {
		return fmt.Errorf("mail: cloudflare reported %d of %d recipients as permanently bounced", len(parsed.Result.PermanentBounces), len(out.To)+len(out.Cc)+len(out.Bcc))
	}
	return nil
}

// cloudflareError names the HTTP status and the API's own error codes, which
// are fixed identifiers (email.sending.error.throttled, say) and never echo
// the message or the token.
func cloudflareError(status int, parsed cloudflareResponse) error {
	codes := make([]string, 0, len(parsed.Errors))
	for _, e := range parsed.Errors {
		codes = append(codes, fmt.Sprintf("%d %s", e.Code, e.Message))
	}
	if len(codes) == 0 {
		return fmt.Errorf("mail: cloudflare refused the send: HTTP %d", status)
	}
	return fmt.Errorf("mail: cloudflare refused the send: HTTP %d: %s", status, strings.Join(codes, "; "))
}
