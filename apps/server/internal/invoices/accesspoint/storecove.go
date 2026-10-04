package accesspoint

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// This file is the Storecove adapter, by Storecove's API v2 OpenAPI document
// as the plan's spike read it (EHF and KID design D7).
//
// The client is the customers module's Brreg client's shape: the base URL is
// the operator's (INVOICES_STORECOVE_BASE_URL), so it dials unguarded — the
// SSRF guard refuses loopback and private hosts and would make a mock
// impossible — through the transport the caller hands it (Deps.HTTPTransport,
// http.DefaultTransport when nil), never follows a redirect, and bounds every
// call by one thirty-second deadline. Unlike Brreg's it never retries: the
// worker retries under the idempotency key, one call per claim (D9), so a
// timeout after Storecove accepted cannot become a second document.

const (
	// callTimeout bounds one method call, every request it makes included.
	callTimeout = 30 * time.Second
	// maxDocumentBytes bounds a delivered document, and any answer read.
	maxDocumentBytes = 20 << 20
)

// schemes maps a Peppol participant scheme to Storecove's own name for it.
// Only what Vantigo sends to is mapped: a Norwegian organisation number.
var schemes = map[string]string{
	"0192": "NO:ORG",
}

type storecove struct {
	baseURL       string
	apiKey        string
	legalEntityID int
	client        *http.Client
	timeout       time.Duration
	now           func() time.Time
}

// NewStorecove is the adapter for one Storecove API key and the legal entity
// it sends as. baseURL has no trailing slash (config trims it). now is the
// clock a Retry-After date is judged against — the module's, so a test's fixed
// clock holds here too; nil is the wall clock.
func NewStorecove(baseURL, apiKey string, legalEntityID int, transport http.RoundTripper, now func() time.Time) AccessPoint {
	return newStorecove(baseURL, apiKey, legalEntityID, transport, now, callTimeout)
}

func newStorecove(baseURL, apiKey string, legalEntityID int, transport http.RoundTripper, now func() time.Time, timeout time.Duration) AccessPoint {
	if transport == nil {
		transport = http.DefaultTransport
	}
	if now == nil {
		now = time.Now
	}
	return &storecove{
		baseURL:       strings.TrimSuffix(baseURL, "/"),
		apiKey:        apiKey,
		legalEntityID: legalEntityID,
		// A 3xx is answered as the non-2xx it is: following one would send the
		// API key to a host no operator named.
		client:  &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		timeout: timeout,
		now:     now,
	}
}

// submissionRequest is the DocumentSubmission body.
type submissionRequest struct {
	LegalEntityID   int    `json:"legalEntityId"`
	IdempotencyGUID string `json:"idempotencyGuid"`
	Routing         struct {
		EIdentifiers []eIdentifier `json:"eIdentifiers"`
	} `json:"routing"`
	Document struct {
		DocumentType    string `json:"documentType"`
		RawDocumentData struct {
			Document      string `json:"document"`
			ParseStrategy string `json:"parseStrategy"`
		} `json:"rawDocumentData"`
	} `json:"document"`
}

type eIdentifier struct {
	Scheme string `json:"scheme"`
	ID     string `json:"id"`
}

// Submit is POST document_submissions. The UBL goes as raw document data for
// Storecove to parse — it regenerates what it transmits — under
// documentType "invoice", the one type its raw path takes.
func (c *storecove) Submit(ctx context.Context, s Submission) (SubmissionRef, error) {
	scheme, id, ok := strings.Cut(s.Receiver, ":")
	storecoveScheme, mapped := schemes[scheme]
	if !ok || !mapped || id == "" {
		return "", ErrUnmappedScheme
	}
	var body submissionRequest
	body.LegalEntityID = c.legalEntityID
	body.IdempotencyGUID = s.IdempotencyKey.String()
	body.Routing.EIdentifiers = []eIdentifier{{Scheme: storecoveScheme, ID: id}}
	body.Document.DocumentType = "invoice"
	body.Document.RawDocumentData.Document = base64.StdEncoding.EncodeToString(s.UBL)
	body.Document.RawDocumentData.ParseStrategy = "ubl"
	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("accesspoint: encode the submission: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	status, answer, err := c.call(ctx, http.MethodPost, "document_submissions", raw)
	if err != nil {
		return "", err
	}
	if err := c.classify(http.MethodPost, "document_submissions", status, answer); err != nil {
		return "", err
	}
	var result struct {
		GUID string `json:"guid"`
	}
	if err := json.Unmarshal(answer.body, &result); err != nil || result.GUID == "" {
		return "", errors.New("accesspoint: storecove accepted the submission but answered no guid")
	}
	return SubmissionRef(result.GUID), nil
}

// webhookBody is the part of a document-submission webhook the port reads.
type webhookBody struct {
	EventType       string `json:"event_type"`
	Event           string `json:"event"`
	Details         string `json:"details"`
	GUID            string `json:"guid"`
	IdempotencyGUID string `json:"idempotencyGuid"`
}

// NextEvent is GET webhook_instances/: one WebhookInstance, or 204 for an
// empty queue. The instance's body is a JSON string holding the webhook's
// own JSON, so it is decoded twice. An instance whose body is not a
// document-submission webhook — another event type, or a body that does not
// decode — is answered bare (no submission, submitted), so the drain
// acknowledges it rather than reading it forever.
func (c *storecove) NextEvent(ctx context.Context) (Event, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	const path = "webhook_instances/"
	status, answer, err := c.call(ctx, http.MethodGet, path, nil)
	if err != nil {
		return Event{}, false, err
	}
	if status == http.StatusNoContent {
		return Event{}, false, nil
	}
	if err := c.classify(http.MethodGet, path, status, answer); err != nil {
		return Event{}, false, err
	}
	var instance struct {
		GUID string `json:"guid"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal(answer.body, &instance); err != nil || instance.GUID == "" {
		return Event{}, false, errors.New("accesspoint: storecove answered a webhook instance that does not decode")
	}
	e := Event{ID: instance.GUID, State: StateSubmitted}
	var body webhookBody
	if err := json.Unmarshal([]byte(instance.Body), &body); err != nil || body.EventType != "document_submission" {
		return e, true, nil
	}
	e.SubmissionRef = SubmissionRef(body.GUID)
	if key, err := uuid.Parse(body.IdempotencyGUID); err == nil {
		e.IdempotencyKey = key
	}
	e.State = stateOf(body.Event)
	e.Reason = body.Details
	return e, true, nil
}

// stateOf folds Storecove's states onto the port's: succeeded is the
// receiving access point's AS4 receipt; failed is final and no_action_taken
// is no routable receiver; everything else — the states gated on an Invoice
// Response or tax clearance — is short of an outcome.
func stateOf(event string) SubmissionState {
	switch event {
	case "succeeded":
		return StateDelivered
	case "failed", "no_action_taken":
		return StateFailed
	default:
		return StateSubmitted
	}
}

// AckEvent is DELETE webhook_instances/{guid}.
func (c *storecove) AckEvent(ctx context.Context, eventID string) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	path := "webhook_instances/" + url.PathEscape(eventID)
	status, answer, err := c.call(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	return c.classify(http.MethodDelete, "webhook_instances/{guid}", status, answer)
}

// evidenceBody is the part of DocumentSubmissionEvidence the port reads.
type evidenceBody struct {
	Documents []struct {
		Document string `json:"document"`
		MIMEType string `json:"mime_type"`
	} `json:"documents"`
	Evidence struct {
		MessageID            string `json:"message_id"`
		ReceivingAccesspoint string `json:"receiving_accesspoint"`
	} `json:"evidence"`
}

// Evidence is GET document_submissions/{guid}/evidence/sending: 404 until
// the submission succeeded. Its documents are expiring URLs, so every one is
// fetched at once, under the same deadline as the evidence itself — over
// https only, every URL judged before any is fetched, without the API key
// (they are presigned, and the key must not travel to another host), and at
// most maxDocumentBytes each. The delivered copy is the first XML document,
// else the first: Storecove's documentation says a Peppol evidence carries
// exactly one.
func (c *storecove) Evidence(ctx context.Context, ref SubmissionRef) (Evidence, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	path := "document_submissions/" + url.PathEscape(string(ref)) + "/evidence/sending"
	status, answer, err := c.call(ctx, http.MethodGet, path, nil)
	if err != nil {
		return Evidence{}, err
	}
	if status == http.StatusNotFound {
		return Evidence{}, ErrNotYetAvailable
	}
	if err := c.classify(http.MethodGet, "document_submissions/{guid}/evidence/sending", status, answer); err != nil {
		return Evidence{}, err
	}
	var body evidenceBody
	if err := json.Unmarshal(answer.body, &body); err != nil {
		return Evidence{}, errors.New("accesspoint: storecove answered evidence that does not decode")
	}
	if len(body.Documents) == 0 {
		return Evidence{}, errors.New("accesspoint: storecove's evidence lists no document")
	}
	for i, d := range body.Documents {
		u, err := url.Parse(d.Document)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return Evidence{}, fmt.Errorf("accesspoint: evidence document %d is not an https URL", i)
		}
	}
	delivered := 0
	for i, d := range body.Documents {
		if strings.Contains(d.MIMEType, "xml") {
			delivered = i
			break
		}
	}
	ev := Evidence{ReceiptJSON: answer.body, MessageID: body.Evidence.MessageID, ReceivingAP: body.Evidence.ReceivingAccesspoint}
	for i, d := range body.Documents {
		doc, err := c.fetchDocument(ctx, d.Document)
		if err != nil {
			return Evidence{}, fmt.Errorf("accesspoint: fetch evidence document %d: %w", i, err)
		}
		if i == delivered {
			ev.Delivered, ev.DeliveredMIME = doc, d.MIMEType
		}
	}
	return ev, nil
}

// fetchDocument downloads one presigned document URL. Its errors never carry
// the URL: its query is the signature that grants the download, and an error
// goes into last_error and the logs.
func (c *storecove) fetchDocument(ctx context.Context, raw string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, errors.New("the URL does not parse")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, transportFailure(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("answered %d", resp.StatusCode)
	}
	return readLimited(resp.Body)
}

// Verify is GET legal_entities/{id}: the key, and that it reaches the legal
// entity it sends as.
func (c *storecove) Verify(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	path := "legal_entities/" + strconv.Itoa(c.legalEntityID)
	status, answer, err := c.call(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	return c.classify(http.MethodGet, "legal_entities/{id}", status, answer)
}

// answer is a response as read: its body and its Retry-After.
type answer struct {
	body       []byte
	retryAfter string
}

// call makes one authenticated request against the API and reads its answer
// whole. A transport failure or the deadline is its error.
func (c *storecove) call(ctx context.Context, method, path string, body []byte) (int, answer, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/"+path, reader)
	if err != nil {
		return 0, answer{}, fmt.Errorf("accesspoint: build the request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, answer{}, fmt.Errorf("accesspoint: storecove %s %s: %w", method, operationOf(path), withoutURL(err))
	}
	defer func() { _ = resp.Body.Close() }()
	read, err := readLimited(resp.Body)
	if err != nil {
		return 0, answer{}, fmt.Errorf("accesspoint: read storecove's answer: %w", err)
	}
	return resp.StatusCode, answer{body: read, retryAfter: resp.Header.Get("Retry-After")}, nil
}

// withoutURL is err less the *url.Error around it, which names the URL the
// request went to.
func withoutURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// transportFailure is a document download's transport error reduced to its
// kind. Unlike the API's base URL, which is the operator's, a document URL's
// host and path are the provider's storage, and a dial, DNS or TLS error
// names them below the *url.Error too.
func transportFailure(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return withoutURL(err)
	default:
		return errors.New("the download failed in transport")
	}
}

// operationOf names a request path without the ids in it.
func operationOf(path string) string {
	head, _, _ := strings.Cut(path, "/")
	return head
}

// readLimited reads r whole, refusing more than maxDocumentBytes.
func readLimited(r io.Reader) ([]byte, error) {
	read, err := io.ReadAll(io.LimitReader(r, maxDocumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(read) > maxDocumentBytes {
		return nil, fmt.Errorf("larger than %d bytes", maxDocumentBytes)
	}
	return read, nil
}

// classify is a status as the port's error classes: nil for a 2xx. The
// operation names the call in a plain error; Storecove's body never goes
// into one — only a 422's messages are carried, for the worker to redact.
func (c *storecove) classify(method, operation string, status int, a answer) error {
	switch {
	case status >= 200 && status < 300:
		return nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ErrUnauthorized
	case status == http.StatusUnprocessableEntity:
		return &ErrUnprocessable{Messages: messagesOf(a.body)}
	case status == http.StatusTooManyRequests:
		return &ErrThrottled{RetryAfter: retryAfter(a.retryAfter, c.now())}
	default:
		return fmt.Errorf("accesspoint: storecove %s %s answered %d", method, operation, status)
	}
}

// messagesOf reads a 422's [{source, details}] as "source: details" each.
func messagesOf(body []byte) []string {
	var models []struct {
		Source  string `json:"source"`
		Details string `json:"details"`
	}
	if err := json.Unmarshal(body, &models); err != nil {
		return nil
	}
	messages := make([]string, 0, len(models))
	for _, m := range models {
		switch {
		case m.Source != "" && m.Details != "":
			messages = append(messages, m.Source+": "+m.Details)
		case m.Details != "":
			messages = append(messages, m.Details)
		case m.Source != "":
			messages = append(messages, m.Source)
		}
	}
	return messages
}

// retryAfter reads a Retry-After header, seconds or an HTTP date judged
// against now; zero when it is absent, unreadable or already past.
func retryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(v); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil {
		if d := at.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}
