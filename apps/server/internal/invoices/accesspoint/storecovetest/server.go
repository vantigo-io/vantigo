// Package storecovetest is an httptest Storecove for tests only: the shapes
// of Storecove's API v2 that the access-point adapter speaks (EHF and KID
// design D7, the plan's spike findings), served from one TLS listener so
// every test package — the adapter's, the module's, the integration test —
// drives the real adapter over real HTTP without touching the network.
//
// It is an ordinary package, not a _test file, so other packages' tests can
// import it; nothing outside a test ever should.
//
// Evidence documents are served by the same server over its TLS listener:
// the adapter fetches a document URL only over https (D7), and this server
// keeps that rule true in every test rather than loosening it for loopback.
// A client reaches it through Transport, which trusts the server's
// certificate — a harness passes it as Deps.HTTPTransport.
package storecovetest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The credentials the server accepts.
const (
	// APIKey is the only key the server accepts as a Bearer token.
	APIKey = "storecovetest-api-key"
	// LegalEntityID is the only legal entity the key reaches.
	LegalEntityID = 4711
	// RetryAfterSeconds is the Retry-After the Throttled mode answers.
	RetryAfterSeconds = 17
)

// RetryAfterDate is the Retry-After the ThrottledUntilDate mode answers, as
// an HTTP date: a test judges it against a fixed clock.
var RetryAfterDate = time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC)

// basePath is the path Storecove's API lives under; URL includes it, as the
// production base URL does.
const basePath = "/api/v2"

// Mode is how the server answers every API request until the next Fail.
// Document downloads are never failed.
type Mode string

const (
	// Normal answers as Storecove would.
	Normal Mode = ""
	// Unauthorized answers 401 with no body.
	Unauthorized Mode = "401"
	// Forbidden answers 403 with no body.
	Forbidden Mode = "403"
	// Unprocessable answers 422 with Rejection as its one message.
	Unprocessable Mode = "422"
	// Throttled answers 429 with Retry-After: RetryAfterSeconds.
	Throttled Mode = "429"
	// ThrottledUntilDate answers 429 with Retry-After: RetryAfterDate.
	ThrottledUntilDate Mode = "429-date"
	// ThrottledWithoutRetryAfter answers 429 with no Retry-After.
	ThrottledWithoutRetryAfter Mode = "429-bare"
	// ServerError answers 503.
	ServerError Mode = "5xx"
	// Timeout never answers: the request is held until the client gives up.
	Timeout Mode = "timeout"
)

// Rejection is the 422 body's one entry in the Unprocessable mode.
var Rejection = ErrorModel{Source: "document", Details: "The document did not pass validation"}

// ErrorModel is one entry of Storecove's 422 body.
type ErrorModel struct {
	Source  string `json:"source"`
	Details string `json:"details"`
}

// Submission is one document submission the server accepted.
type Submission struct {
	// GUID is the guid the server answered.
	GUID string
	// Authorization is the request's Authorization header.
	Authorization string
	// Body is the request body exactly as sent.
	Body []byte
	// LegalEntityID, IdempotencyGUID, Scheme and ID are the body's fields;
	// UBL is rawDocumentData.document decoded.
	LegalEntityID   int
	IdempotencyGUID string
	Scheme, ID      string
	UBL             []byte
}

// Event is one webhook to put on the pull queue.
type Event struct {
	// GUID is the WebhookInstance's own guid, the one a DELETE names; a
	// fresh one when empty.
	GUID string
	// EventType is the body's event_type, "document_submission" when empty.
	EventType string
	// Event is succeeded, failed, no_action_taken or any other state.
	Event string
	// SubmissionGUID and IdempotencyGUID are the body's guid and
	// idempotencyGuid.
	SubmissionGUID, IdempotencyGUID string
	// Details is the body's textual detail.
	Details string
	// RawBody, when set, is served as the body string instead of one built
	// from the fields above.
	RawBody string
}

// Document is one document an evidence lists.
type Document struct {
	// Body is served at a URL on this server; MIME is its mime_type,
	// application/xml when empty.
	Body []byte
	MIME string
	// URL, when set, is listed instead of a URL on this server, and Body is
	// not served.
	URL string
}

// Server is the fake. Its methods are safe for concurrent use.
type Server struct {
	srv  *httptest.Server
	stop chan struct{}

	mu          sync.Mutex
	mode        Mode
	submissions []Submission
	seenKeys    map[string]string
	queue       []queued
	acked       []string
	evidence    map[string][]byte
	documents   map[string]Document
	fetches     []DocumentFetch
	requests    int
}

type queued struct {
	guid, body string
}

// DocumentFetch is one download of a planted document.
type DocumentFetch struct {
	Path string
	// Authorization is the header the download carried: an expiring URL is
	// a presigned one, and the API key must never travel to it.
	Authorization string
}

// New starts a server for t and closes it when t ends.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{
		stop:      make(chan struct{}),
		seenKeys:  map[string]string{},
		evidence:  map[string][]byte{},
		documents: map[string]Document{},
	}
	s.srv = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(func() {
		close(s.stop)
		s.srv.Close()
	})
	return s
}

// URL is the base URL to point the adapter (INVOICES_STORECOVE_BASE_URL) at:
// https, with Storecove's /api/v2 path and no trailing slash.
func (s *Server) URL() string { return s.srv.URL + basePath }

// Transport is a RoundTripper that trusts the server's certificate.
func (s *Server) Transport() http.RoundTripper { return s.srv.Client().Transport }

// Fail sets how every following API request is answered; Fail(Normal) ends
// it.
func (s *Server) Fail(m Mode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = m
}

// Submissions is every submission accepted, in order.
func (s *Server) Submissions() []Submission {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Submission(nil), s.submissions...)
}

// Requests is how many API requests the server has answered or held,
// document downloads not counted.
func (s *Server) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

// Enqueue puts one event at the back of the pull queue and answers its
// WebhookInstance guid.
func (s *Server) Enqueue(e Event) string {
	if e.GUID == "" {
		e.GUID = uuid.NewString()
	}
	body := e.RawBody
	if body == "" {
		eventType := e.EventType
		if eventType == "" {
			eventType = "document_submission"
		}
		raw, _ := json.Marshal(map[string]any{
			"event_type": eventType, "event_group": "invoice", "event": e.Event,
			"v_delivery": e.Event == "succeeded", "details": e.Details, "response_document": nil,
			"guid": e.SubmissionGUID, "idempotencyGuid": e.IdempotencyGUID, "tenant_id": "storecovetest",
		})
		body = string(raw)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = append(s.queue, queued{guid: e.GUID, body: body})
	return e.GUID
}

// Acked is every WebhookInstance guid deleted, in order.
func (s *Server) Acked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.acked...)
}

// Queued is how many events are still on the queue.
func (s *Server) Queued() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// Evidence plants the sending evidence of submission guid, listing docs, and
// answers the JSON the server will serve for it.
func (s *Server) Evidence(guid string, docs ...Document) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	listed := make([]map[string]string, 0, len(docs))
	for i, d := range docs {
		mime := d.MIME
		if mime == "" {
			mime = "application/xml"
		}
		url := d.URL
		if url == "" {
			path := fmt.Sprintf("/documents/%s/%d", guid, i)
			s.documents[path] = d
			url = s.srv.URL + path
		}
		listed = append(listed, map[string]string{"document": url, "expires_at": "2026-09-12 12:00:00", "mime_type": mime})
	}
	body, _ := json.Marshal(map[string]any{
		"guid": guid, "sender": "974760673", "receiver": "923609016", "network": "peppol",
		"documents": listed,
		"evidence": map[string]any{
			"transmission_id": "tx-" + guid, "message_id": "msg-" + guid,
			"receiving_accesspoint": "CN=PNO000999,O=Storecovetest AP,C=NO",
		},
	})
	s.evidence[guid] = body
	return body
}

// DocumentFetches is every download of a planted document, in order.
func (s *Server) DocumentFetches() []DocumentFetch {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]DocumentFetch(nil), s.fetches...)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/documents/") {
		s.serveDocument(w, r)
		return
	}
	s.mu.Lock()
	s.requests++
	mode := s.mode
	s.mu.Unlock()
	if s.failed(w, r, mode) {
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+APIKey {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	path, ok := strings.CutPrefix(r.URL.Path, basePath+"/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch {
	case r.Method == http.MethodPost && path == "document_submissions":
		s.submit(w, r)
	case r.Method == http.MethodGet && path == "webhook_instances/":
		s.nextEvent(w)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "webhook_instances/"):
		s.ack(w, strings.TrimPrefix(path, "webhook_instances/"))
	case r.Method == http.MethodGet && strings.HasPrefix(path, "document_submissions/") && strings.HasSuffix(path, "/evidence/sending"):
		s.serveEvidence(w, strings.TrimSuffix(strings.TrimPrefix(path, "document_submissions/"), "/evidence/sending"))
	case r.Method == http.MethodGet && strings.HasPrefix(path, "legal_entities/"):
		if strings.TrimPrefix(path, "legal_entities/") != strconv.Itoa(LegalEntityID) {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": LegalEntityID, "party_name": "Kraft-Verket AS", "country": "NO"})
	default:
		http.NotFound(w, r)
	}
}

// failed answers the request as mode says, reporting whether it did.
func (s *Server) failed(w http.ResponseWriter, r *http.Request, mode Mode) bool {
	switch mode {
	case Normal:
		return false
	case Unauthorized:
		w.WriteHeader(http.StatusUnauthorized)
	case Forbidden:
		w.WriteHeader(http.StatusForbidden)
	case Unprocessable:
		writeJSON(w, http.StatusUnprocessableEntity, []ErrorModel{Rejection})
	case Throttled:
		w.Header().Set("Retry-After", strconv.Itoa(RetryAfterSeconds))
		w.WriteHeader(http.StatusTooManyRequests)
	case ThrottledUntilDate:
		w.Header().Set("Retry-After", RetryAfterDate.Format(http.TimeFormat))
		w.WriteHeader(http.StatusTooManyRequests)
	case ThrottledWithoutRetryAfter:
		w.WriteHeader(http.StatusTooManyRequests)
	case ServerError:
		w.WriteHeader(http.StatusServiceUnavailable)
	case Timeout:
		select {
		case <-r.Context().Done():
		case <-s.stop:
		}
	}
	return true
}

// submissionBody is the part of a DocumentSubmission the server checks.
type submissionBody struct {
	LegalEntityID   int    `json:"legalEntityId"`
	IdempotencyGUID string `json:"idempotencyGuid"`
	Routing         struct {
		EIdentifiers []struct {
			Scheme string `json:"scheme"`
			ID     string `json:"id"`
		} `json:"eIdentifiers"`
	} `json:"routing"`
	Document struct {
		DocumentType    string `json:"documentType"`
		RawDocumentData struct {
			Document      string `json:"document"`
			ParseStrategy string `json:"parseStrategy"`
		} `json:"rawDocumentData"`
	} `json:"document"`
}

func (s *Server) submit(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var body submissionBody
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, []ErrorModel{{Source: "body", Details: "The body is not JSON"}})
		return
	}
	var refusals []ErrorModel
	refuse := func(source, details string) {
		refusals = append(refusals, ErrorModel{Source: source, Details: details})
	}
	if body.LegalEntityID != LegalEntityID {
		refuse("legalEntityId", "Unknown legal entity")
	}
	if len(body.IdempotencyGUID) != 36 {
		refuse("idempotencyGuid", "Must be 36 characters")
	}
	if len(body.Routing.EIdentifiers) == 0 {
		refuse("routing", "No eIdentifiers")
	}
	if body.Document.DocumentType != "invoice" {
		refuse("document.documentType", "Must be invoice")
	}
	if body.Document.RawDocumentData.ParseStrategy != "ubl" {
		refuse("document.rawDocumentData.parseStrategy", "Must be ubl")
	}
	ubl, err := base64.StdEncoding.DecodeString(body.Document.RawDocumentData.Document)
	if err != nil || len(ubl) == 0 {
		refuse("document.rawDocumentData.document", "Not base64")
	}
	if len(refusals) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, refusals)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, seen := s.seenKeys[body.IdempotencyGUID]; seen {
		writeJSON(w, http.StatusUnprocessableEntity, []ErrorModel{{Source: "idempotencyGuid", Details: "This idempotencyGuid has already been used"}})
		return
	}
	guid := uuid.NewString()
	s.seenKeys[body.IdempotencyGUID] = guid
	s.submissions = append(s.submissions, Submission{
		GUID: guid, Authorization: r.Header.Get("Authorization"), Body: raw,
		LegalEntityID: body.LegalEntityID, IdempotencyGUID: body.IdempotencyGUID,
		Scheme: body.Routing.EIdentifiers[0].Scheme, ID: body.Routing.EIdentifiers[0].ID, UBL: ubl,
	})
	writeJSON(w, http.StatusOK, map[string]string{"guid": guid})
}

func (s *Server) nextEvent(w http.ResponseWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	head := s.queue[0]
	writeJSON(w, http.StatusOK, map[string]string{"guid": head.guid, "body": head.body})
}

func (s *Server) ack(w http.ResponseWriter, guid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, q := range s.queue {
		if q.guid == guid {
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			s.acked = append(s.acked, guid)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

func (s *Server) serveEvidence(w http.ResponseWriter, guid string) {
	s.mu.Lock()
	body, ok := s.evidence[guid]
	s.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *Server) serveDocument(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	d, ok := s.documents[r.URL.Path]
	s.fetches = append(s.fetches, DocumentFetch{Path: r.URL.Path, Authorization: r.Header.Get("Authorization")})
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	mime := d.MIME
	if mime == "" {
		mime = "application/xml"
	}
	w.Header().Set("Content-Type", mime)
	_, _ = w.Write(d.Body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
