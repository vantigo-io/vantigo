package accesspoint_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/invoices/accesspoint"
	"github.com/vantigo-io/vantigo/server/internal/invoices/accesspoint/storecovetest"
)

// adapter is the Storecove adapter pointed at sc with its own credentials.
func adapter(sc *storecovetest.Server) accesspoint.AccessPoint {
	return accesspoint.NewStorecove(sc.URL(), storecovetest.APIKey, storecovetest.LegalEntityID, sc.Transport())
}

const invoiceUBL = `<?xml version="1.0" encoding="UTF-8"?><Invoice xmlns="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"/>`

func submission() accesspoint.Submission {
	return accesspoint.Submission{
		IdempotencyKey: uuid.New(), Sender: "0192:974760673", Receiver: "0192:923609016",
		DocumentType: "urn:oasis:names:specification:ubl:schema:xsd:Invoice-2::Invoice##urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0::2.1",
		ProcessID:    "urn:fdc:peppol.eu:2017:poacc:billing:01:1.0",
		UBL:          []byte(invoiceUBL),
	}
}

// Submit posts exactly the body the spike read from Storecove's OpenAPI
// document — the legal entity, the idempotency guid, the receiver under
// Storecove's own scheme, and the UBL base64-encoded for Storecove to parse —
// under a Bearer key, and answers the guid Storecove gave it.
func TestStorecove_SubmitsTheDocumentShape(t *testing.T) {
	t.Parallel()
	sc := storecovetest.New(t)
	s := submission()

	ref, err := adapter(sc).Submit(context.Background(), s)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	got := sc.Submissions()
	if len(got) != 1 {
		t.Fatalf("%d submissions, want 1", len(got))
	}
	if string(ref) != got[0].GUID {
		t.Errorf("ref = %q, want the guid Storecove answered, %q", ref, got[0].GUID)
	}
	if got[0].Authorization != "Bearer "+storecovetest.APIKey {
		t.Errorf("Authorization = %q, want the Bearer key", got[0].Authorization)
	}
	var body any
	if err := json.Unmarshal(got[0].Body, &body); err != nil {
		t.Fatalf("the body is not JSON: %v", err)
	}
	want := map[string]any{
		"legalEntityId":   float64(storecovetest.LegalEntityID),
		"idempotencyGuid": s.IdempotencyKey.String(),
		"routing": map[string]any{
			"eIdentifiers": []any{map[string]any{"scheme": "NO:ORG", "id": "923609016"}},
		},
		"document": map[string]any{
			"documentType": "invoice",
			"rawDocumentData": map[string]any{
				"document":      base64.StdEncoding.EncodeToString([]byte(invoiceUBL)),
				"parseStrategy": "ubl",
			},
		},
	}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("body = %s\nwant %v", got[0].Body, want)
	}
	if !bytes.Equal(got[0].UBL, []byte(invoiceUBL)) {
		t.Errorf("the UBL Storecove decoded = %q, want the bytes submitted", got[0].UBL)
	}
}

// Storecove names a Norwegian organisation number NO:ORG, not by its Peppol
// scheme 0192. That is the one mapping so far; any other scheme, or a
// receiver that is no participant id at all, is ErrUnmappedScheme before
// anything is sent.
func TestStorecove_MapsTheSchemes(t *testing.T) {
	t.Parallel()
	sc := storecovetest.New(t)
	ap := adapter(sc)

	s := submission()
	s.Receiver = "0192:010101018"
	if _, err := ap.Submit(context.Background(), s); err != nil {
		t.Fatalf("Submit to 0192: %v", err)
	}
	if got := sc.Submissions(); len(got) != 1 || got[0].Scheme != "NO:ORG" || got[0].ID != "010101018" {
		t.Fatalf("submissions = %+v, want one to NO:ORG 010101018", got)
	}

	for _, receiver := range []string{"0088:7080000000001", "9908:923609016", "0208:0123456789", "0192:", "0192", ":923609016", ""} {
		s := submission()
		s.Receiver = receiver
		if _, err := ap.Submit(context.Background(), s); !errors.Is(err, accesspoint.ErrUnmappedScheme) {
			t.Errorf("Submit to %q = %v, want ErrUnmappedScheme", receiver, err)
		}
	}
	if n := sc.Requests(); n != 1 {
		t.Errorf("Storecove saw %d requests, want only the mapped one — an unmapped scheme is refused before any call", n)
	}
}

// Each failure Storecove can answer is told apart, the way the worker needs
// them (D9): a 422 carries Storecove's messages, 401 and 403 are the key,
// a 429 carries its Retry-After or none, and a 5xx or a timeout is a plain
// error — the outcome unknown. Nothing is retried inside the adapter.
func TestStorecove_ErrorClasses(t *testing.T) {
	t.Parallel()

	t.Run("422", func(t *testing.T) {
		t.Parallel()
		sc := storecovetest.New(t)
		sc.Fail(storecovetest.Unprocessable)
		_, err := adapter(sc).Submit(context.Background(), submission())
		var refused *accesspoint.ErrUnprocessable
		if !errors.As(err, &refused) {
			t.Fatalf("err = %v, want ErrUnprocessable", err)
		}
		want := storecovetest.Rejection.Source + ": " + storecovetest.Rejection.Details
		if !reflect.DeepEqual(refused.Messages, []string{want}) {
			t.Errorf("messages = %q, want [%q]", refused.Messages, want)
		}
	})

	t.Run("a duplicate key is the same 422", func(t *testing.T) {
		t.Parallel()
		sc := storecovetest.New(t)
		s := submission()
		if _, err := adapter(sc).Submit(context.Background(), s); err != nil {
			t.Fatal(err)
		}
		var refused *accesspoint.ErrUnprocessable
		if _, err := adapter(sc).Submit(context.Background(), s); !errors.As(err, &refused) || len(refused.Messages) != 1 {
			t.Fatalf("the second Submit = %v, want ErrUnprocessable with Storecove's message", err)
		}
		if n := len(sc.Submissions()); n != 1 {
			t.Errorf("%d submissions, want 1", n)
		}
	})

	for _, mode := range []storecovetest.Mode{storecovetest.Unauthorized, storecovetest.Forbidden} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			sc := storecovetest.New(t)
			sc.Fail(mode)
			ap := adapter(sc)
			ctx := context.Background()
			_, err := ap.Submit(ctx, submission())
			_, _, eventErr := ap.NextEvent(ctx)
			_, evidenceErr := ap.Evidence(ctx, "guid")
			for name, err := range map[string]error{
				"Submit": err, "NextEvent": eventErr, "AckEvent": ap.AckEvent(ctx, "guid"),
				"Evidence": evidenceErr, "Verify": ap.Verify(ctx),
			} {
				if !errors.Is(err, accesspoint.ErrUnauthorized) {
					t.Errorf("%s = %v, want ErrUnauthorized", name, err)
				}
			}
		})
	}

	t.Run("a wrong key is 401", func(t *testing.T) {
		t.Parallel()
		sc := storecovetest.New(t)
		ap := accesspoint.NewStorecove(sc.URL(), "not-the-key", storecovetest.LegalEntityID, sc.Transport())
		if err := ap.Verify(context.Background()); !errors.Is(err, accesspoint.ErrUnauthorized) {
			t.Fatalf("Verify = %v, want ErrUnauthorized", err)
		}
	})

	t.Run("429 with Retry-After", func(t *testing.T) {
		t.Parallel()
		sc := storecovetest.New(t)
		sc.Fail(storecovetest.Throttled)
		_, err := adapter(sc).Submit(context.Background(), submission())
		var throttled *accesspoint.ErrThrottled
		if !errors.As(err, &throttled) || throttled.RetryAfter != storecovetest.RetryAfterSeconds*time.Second {
			t.Fatalf("err = %v, want ErrThrottled after %ds", err, storecovetest.RetryAfterSeconds)
		}
	})

	t.Run("429 without Retry-After", func(t *testing.T) {
		t.Parallel()
		sc := storecovetest.New(t)
		sc.Fail(storecovetest.ThrottledWithoutRetryAfter)
		_, err := adapter(sc).Submit(context.Background(), submission())
		var throttled *accesspoint.ErrThrottled
		if !errors.As(err, &throttled) || throttled.RetryAfter != 0 {
			t.Fatalf("err = %v, want ErrThrottled with no retry-after", err)
		}
	})

	t.Run("5xx", func(t *testing.T) {
		t.Parallel()
		sc := storecovetest.New(t)
		sc.Fail(storecovetest.ServerError)
		_, err := adapter(sc).Submit(context.Background(), submission())
		assertPlain(t, err)
		if n := sc.Requests(); n != 1 {
			t.Errorf("Storecove saw %d requests, want 1 — no retry inside the adapter", n)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		t.Parallel()
		sc := storecovetest.New(t)
		sc.Fail(storecovetest.Timeout)
		ap := accesspoint.NewStorecoveWithTimeout(sc.URL(), storecovetest.APIKey, storecovetest.LegalEntityID, sc.Transport(), 200*time.Millisecond)
		start := time.Now()
		_, err := ap.Submit(context.Background(), submission())
		assertPlain(t, err)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want the call's own deadline", err)
		}
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("Submit took %s, want it bounded by its deadline", took)
		}
		if n := sc.Requests(); n != 1 {
			t.Errorf("Storecove saw %d requests, want 1 — no retry inside the adapter", n)
		}
	})
}

// assertPlain fails unless err is an error of none of the port's classes.
func assertPlain(t *testing.T, err error) {
	t.Helper()
	var refused *accesspoint.ErrUnprocessable
	var throttled *accesspoint.ErrThrottled
	switch {
	case err == nil:
		t.Fatal("err = nil, want a plain error")
	case errors.As(err, &refused), errors.As(err, &throttled),
		errors.Is(err, accesspoint.ErrUnauthorized), errors.Is(err, accesspoint.ErrUnmappedScheme),
		errors.Is(err, accesspoint.ErrNotYetAvailable):
		t.Fatalf("err = %v, want a plain error", err)
	}
}

// Status arrives on Storecove's pull queue: one WebhookInstance at a time,
// whose body is a JSON string holding the webhook's own JSON — decoded twice.
// Storecove's states fold onto the port's three; an empty queue (204) is
// ok=false; AckEvent deletes the instance by its own guid.
func TestStorecove_EventsAndAck(t *testing.T) {
	t.Parallel()
	sc := storecovetest.New(t)
	ap := adapter(sc)
	ctx := context.Background()

	if _, ok, err := ap.NextEvent(ctx); ok || err != nil {
		t.Fatalf("an empty queue = ok %v, %v; want ok=false and no error", ok, err)
	}

	key := uuid.New()
	cases := []struct {
		event string
		want  accesspoint.SubmissionState
	}{
		{"succeeded", accesspoint.StateDelivered},
		{"failed", accesspoint.StateFailed},
		{"no_action_taken", accesspoint.StateFailed},
		{"accepted", accesspoint.StateSubmitted},
		{"under_query", accesspoint.StateSubmitted},
	}
	for _, c := range cases {
		id := sc.Enqueue(storecovetest.Event{Event: c.event, SubmissionGUID: "sub-" + c.event, IdempotencyGUID: key.String(), Details: "detail " + c.event})
		e, ok, err := ap.NextEvent(ctx)
		if err != nil || !ok {
			t.Fatalf("%s: NextEvent = ok %v, %v", c.event, ok, err)
		}
		want := accesspoint.Event{ID: id, SubmissionRef: accesspoint.SubmissionRef("sub-" + c.event), IdempotencyKey: key, State: c.want, Reason: "detail " + c.event}
		if !reflect.DeepEqual(e, want) {
			t.Errorf("%s: event = %+v, want %+v", c.event, e, want)
		}
		if again, _, _ := ap.NextEvent(ctx); again.ID != id {
			t.Errorf("%s: before the ack the queue's head = %q, want the same instance %q", c.event, again.ID, id)
		}
		if err := ap.AckEvent(ctx, id); err != nil {
			t.Fatalf("%s: AckEvent: %v", c.event, err)
		}
	}
	if n := len(sc.Acked()); n != len(cases) || sc.Queued() != 0 {
		t.Errorf("acked %d, %d left; want %d and none", n, sc.Queued(), len(cases))
	}

	// An event of another type, or a body that is not the webhook's JSON, is
	// answered bare — no submission, submitted — so the drain acknowledges it
	// instead of reading it forever.
	for _, e := range []storecovetest.Event{
		{EventType: "received_document", Event: "received"},
		{RawBody: "not json"},
		{Event: "succeeded", SubmissionGUID: "sub-x", IdempotencyGUID: "not-a-uuid"},
	} {
		id := sc.Enqueue(e)
		got, ok, err := ap.NextEvent(ctx)
		if err != nil || !ok || got.ID != id {
			t.Fatalf("%+v: NextEvent = %+v, ok %v, %v", e, got, ok, err)
		}
		if e.EventType != "" || e.RawBody != "" {
			if got.SubmissionRef != "" || got.IdempotencyKey != uuid.Nil || got.State != accesspoint.StateSubmitted {
				t.Errorf("%+v: event = %+v, want a bare submitted event", e, got)
			}
		} else if got.SubmissionRef != "sub-x" || got.IdempotencyKey != uuid.Nil || got.State != accesspoint.StateDelivered {
			t.Errorf("an unreadable idempotency guid: event = %+v, want the reference kept and the key nil", got)
		}
		if err := ap.AckEvent(ctx, id); err != nil {
			t.Fatal(err)
		}
	}

	if err := ap.AckEvent(ctx, "no-such-instance"); err == nil {
		t.Error("acknowledging an instance Storecove does not have = nil, want an error")
	}
}

// Evidence is Storecove's sending evidence once the receiving access point
// accepted the message: the JSON kept as the receipt, the AS4 message id
// and access point read from it, and the delivered document fetched at once
// from its expiring URL — over https only, at most 20 MiB, and without the
// API key, which must never travel to a presigned URL. Before Storecove has
// it, ErrNotYetAvailable.
func TestStorecove_EvidenceFetchesTheDocuments(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("404 is not yet", func(t *testing.T) {
		t.Parallel()
		sc := storecovetest.New(t)
		if _, err := adapter(sc).Evidence(ctx, "sub-1"); !errors.Is(err, accesspoint.ErrNotYetAvailable) {
			t.Fatalf("Evidence = %v, want ErrNotYetAvailable", err)
		}
	})

	t.Run("the receipt and the delivered copy", func(t *testing.T) {
		t.Parallel()
		sc := storecovetest.New(t)
		delivered := []byte("<Invoice>regenerated by Storecove</Invoice>")
		receipt := sc.Evidence("sub-1", storecovetest.Document{Body: delivered, MIME: "application/xml"})
		ev, err := adapter(sc).Evidence(ctx, "sub-1")
		if err != nil {
			t.Fatalf("Evidence: %v", err)
		}
		if !bytes.Equal(ev.ReceiptJSON, receipt) {
			t.Errorf("receipt = %s, want the evidence as served, %s", ev.ReceiptJSON, receipt)
		}
		if !bytes.Equal(ev.Delivered, delivered) || ev.DeliveredMIME != "application/xml" {
			t.Errorf("delivered = %q (%s), want %q (application/xml)", ev.Delivered, ev.DeliveredMIME, delivered)
		}
		if ev.MessageID != "msg-sub-1" || ev.ReceivingAP != "CN=PNO000999,O=Storecovetest AP,C=NO" {
			t.Errorf("message id %q, access point %q", ev.MessageID, ev.ReceivingAP)
		}
		fetches := sc.DocumentFetches()
		if len(fetches) != 1 || fetches[0].Authorization != "" {
			t.Errorf("document fetches = %+v, want one, carrying no Authorization", fetches)
		}
	})

	t.Run("every document is fetched; the XML one is the delivered copy", func(t *testing.T) {
		t.Parallel()
		sc := storecovetest.New(t)
		sc.Evidence("sub-1",
			storecovetest.Document{Body: []byte("%PDF-1.7"), MIME: "application/pdf"},
			storecovetest.Document{Body: []byte("<Invoice/>"), MIME: "application/xml"})
		ev, err := adapter(sc).Evidence(ctx, "sub-1")
		if err != nil {
			t.Fatalf("Evidence: %v", err)
		}
		if string(ev.Delivered) != "<Invoice/>" || ev.DeliveredMIME != "application/xml" {
			t.Errorf("delivered = %q (%s), want the XML document", ev.Delivered, ev.DeliveredMIME)
		}
		if n := len(sc.DocumentFetches()); n != 2 {
			t.Errorf("%d documents fetched, want both", n)
		}
	})

	t.Run("https only", func(t *testing.T) {
		t.Parallel()
		sc := storecovetest.New(t)
		sc.Evidence("sub-1",
			storecovetest.Document{Body: []byte("<Invoice/>")},
			storecovetest.Document{URL: strings.Replace(sc.URL(), "https://", "http://", 1) + "/documents/plain"})
		_, err := adapter(sc).Evidence(ctx, "sub-1")
		assertPlain(t, err)
		if !strings.Contains(err.Error(), "https") {
			t.Errorf("err = %v, want it to name the https rule", err)
		}
		if n := len(sc.DocumentFetches()); n != 0 {
			t.Errorf("%d documents fetched, want none — every URL is judged before any is fetched", n)
		}
	})

	t.Run("no document", func(t *testing.T) {
		t.Parallel()
		sc := storecovetest.New(t)
		sc.Evidence("sub-1")
		_, err := adapter(sc).Evidence(ctx, "sub-1")
		assertPlain(t, err)
	})

	t.Run("20 MiB", func(t *testing.T) {
		t.Parallel()
		sc := storecovetest.New(t)
		sc.Evidence("at-the-limit", storecovetest.Document{Body: bytes.Repeat([]byte("a"), accesspoint.MaxDocumentBytes)})
		sc.Evidence("over-the-limit", storecovetest.Document{Body: bytes.Repeat([]byte("a"), accesspoint.MaxDocumentBytes+1)})
		ev, err := adapter(sc).Evidence(ctx, "at-the-limit")
		if err != nil || len(ev.Delivered) != accesspoint.MaxDocumentBytes {
			t.Fatalf("a document of exactly 20 MiB = %d bytes, %v; want it whole", len(ev.Delivered), err)
		}
		_, err = adapter(sc).Evidence(ctx, "over-the-limit")
		assertPlain(t, err)
	})

	t.Run("a document that is gone", func(t *testing.T) {
		t.Parallel()
		sc := storecovetest.New(t)
		sc.Evidence("sub-1", storecovetest.Document{URL: strings.TrimSuffix(sc.URL(), "/api/v2") + "/documents/expired"})
		_, err := adapter(sc).Evidence(ctx, "sub-1")
		assertPlain(t, err)
	})
}

// Verify is the cheapest authenticated read: the legal entity the key is
// configured for. A legal entity the key does not reach is not ok.
func TestStorecove_Verify(t *testing.T) {
	t.Parallel()
	sc := storecovetest.New(t)
	if err := adapter(sc).Verify(context.Background()); err != nil {
		t.Fatalf("Verify = %v, want nil", err)
	}
	other := accesspoint.NewStorecove(sc.URL(), storecovetest.APIKey, storecovetest.LegalEntityID+1, sc.Transport())
	if err := other.Verify(context.Background()); err == nil {
		t.Error("Verify of another legal entity = nil, want an error")
	}
	sc.Fail(storecovetest.ServerError)
	assertPlain(t, adapter(sc).Verify(context.Background()))
}
