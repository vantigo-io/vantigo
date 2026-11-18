package mps_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/consumerinvoice"
	"github.com/vantigo-io/vantigo/server/internal/consumerinvoice/mps"
	"github.com/vantigo-io/vantigo/server/internal/consumerinvoice/mps/mpstest"
)

const (
	issuer   = "974760673"
	creditor = "12345678903"
)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func adapter(t *testing.T) (*mps.Adapter, *mpstest.Server, *clock) {
	t.Helper()
	fake := mpstest.New()
	t.Cleanup(fake.Close)
	c := &clock{now: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)}
	fake.SetClock(c.Now)
	a, err := mps.New(mps.Config{
		EfakturaBaseURL: fake.EfakturaURL(), MandateBaseURL: fake.MandateURL(),
		Certificate: fake.ClientCertificate(), Username: "vantigo", Password: "secret",
		Issuer: issuer, CreditorAccount: creditor, Transport: fake.Transport(), Clock: c.Now,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return a, fake, c
}

func dob(s string) *time.Time {
	d, _ := time.Parse(time.DateOnly, s)
	return &d
}

// The lookup finds a person by two of phone, e-mail and date of birth, sends
// the issuer so a block is seen, answers in the keys' order, and never sends
// a national identity number.
func TestLookup_TheMatchingRule(t *testing.T) {
	t.Parallel()
	a, fake, _ := adapter(t)
	fake.AddPerson(mpstest.Person{Identifier: "123456789", Phone: "+4791234567", Email: "kari@example.no", DateOfBirth: "1980-05-17",
		FirstName: "Kari", LastName: "Nordmann", PostalCode: "0150", AutoAccept: true})
	fake.AddPerson(mpstest.Person{Identifier: "223456789", Phone: "+4799999999", Email: "ola@example.no",
		FirstName: "Ola", LastName: "Nordmann", PostalCode: "5003", Agreements: []string{issuer}})
	fake.AddPerson(mpstest.Person{Identifier: "323456789", Phone: "+4798888888", Email: "per@example.no",
		FirstName: "Per", LastName: "Hansen", BlockedIssuers: []string{issuer}, AutoAccept: true})

	got, err := a.Lookup(context.Background(), []consumerinvoice.Consumer{
		{Phone: "+4791234567", DateOfBirth: dob("1980-05-17")},
		{Email: "ola@example.no", FirstName: "Ola", LastName: "Nordmann"},
		{Phone: "+4798888888", Email: "per@example.no"},
		{Phone: "+4700000000", Email: "nobody@example.no"},
		{Email: "kari@example.no"},
	})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	want := []consumerinvoice.Address{
		{Status: consumerinvoice.LookupFound, Identifier: "123456789", AutoAccept: true},
		{Status: consumerinvoice.LookupFound, Identifier: "223456789", HasAgreement: true},
		{Status: consumerinvoice.LookupFound, Identifier: "323456789", AutoAccept: true, Blocked: true},
		{Status: consumerinvoice.LookupNotFound},
		{Status: consumerinvoice.LookupMissingData},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d answers, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("answer %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if !got[0].Sendable() || !got[1].Sendable() || got[2].Sendable() || got[3].Sendable() {
		t.Errorf("sendable = %v %v %v %v, want true true false false", got[0].Sendable(), got[1].Sendable(), got[2].Sendable(), got[3].Sendable())
	}
	for _, body := range fake.Requests() {
		if strings.Contains(body, "socialSecurityNumber") {
			t.Errorf("a request carried a national identity number field: %s", body)
		}
		if !strings.Contains(body, `xmlns="`+mps.ServiceNamespace+`"`) {
			t.Errorf("a lookup outside the service's namespace: %s", body)
		}
		if !strings.Contains(body, "<efakturaIssuer>"+issuer+"</efakturaIssuer>") {
			t.Errorf("a lookup without the issuer: %s", body)
		}
	}
}

// MatchableKeys is the matching rule the caller judges before a call.
func TestConsumer_MatchableKeys(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		c    consumerinvoice.Consumer
		want bool
	}{
		{"phone and e-mail", consumerinvoice.Consumer{Phone: "1", Email: "e"}, true},
		{"e-mail and a name", consumerinvoice.Consumer{Email: "e", FirstName: "a", LastName: "b"}, true},
		{"e-mail alone", consumerinvoice.Consumer{Email: "e"}, false},
		{"name, address and postal code", consumerinvoice.Consumer{FirstName: "a", LastName: "b", AddressLines: []string{"x"}, PostalCode: "0150"}, true},
		{"name alone", consumerinvoice.Consumer{FirstName: "a", LastName: "b"}, false},
	} {
		if got := c.c.MatchableKeys(); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// A shipment goes with its PDF, its states advance by the clock, a second
// send of the same id is ErrDuplicate, and the claim cancels until 14:00 the
// day before its due date.
func TestShipment_SendStatusCancel(t *testing.T) {
	t.Parallel()
	a, fake, c := adapter(t)
	ctx := context.Background()
	due := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	s := consumerinvoice.Shipment{ID: "6f1c0d7e-1f6e-4b8a-9d1e-0a1b2c3d4e5f", MessageID: "vantigo-1001", Transactions: []consumerinvoice.Transaction{{
		ID: "0e6a1d2b-3c4d-4e5f-8a9b-0c1d2e3f4a5b", PaymentType: consumerinvoice.PaymentBoth, KID: "900012340", Account: "12345678903",
		Amount: consumerinvoice.NOK(125000), DueDate: due, Identifier: "123456789", Reference: "1234", BrandName: "Selger AS",
		AbbreviatedName: "SELGER", ExternalReference: "10042", PDF: []byte("%PDF-1.7 test"),
	}}}
	ref, err := a.Send(ctx, s)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if string(ref) != s.ID {
		t.Errorf("ref %q, want the shipment id", ref)
	}
	got := fake.Shipments()
	if len(got) != 1 || got[0].Amount != "1250.00" || got[0].PaymentType != "EFAKTURA_AND_AVTALEGIRO" || string(got[0].PDF) != "%PDF-1.7 test" || got[0].DueDate != "2026-09-20" {
		t.Fatalf("the fake holds %+v", got)
	}
	if _, err := a.Send(ctx, s); !errors.Is(err, consumerinvoice.ErrDuplicate) {
		t.Errorf("a second send: %v, want ErrDuplicate", err)
	}

	st, err := a.Status(ctx, ref)
	if err != nil || len(st) != 1 || st[0].Efaktura != consumerinvoice.EfakturaSubmitted || st[0].AvtaleGiro != consumerinvoice.AvtaleGiroSubmitted {
		t.Fatalf("status before acceptance: %+v %v", st, err)
	}
	c.now = c.now.Add(10 * time.Minute)
	st, err = a.Status(ctx, ref)
	if err != nil || st[0].Efaktura != consumerinvoice.EfakturaDelivered || st[0].AvtaleGiro != consumerinvoice.AvtaleGiroAccepted || len(st[0].Raw) == 0 {
		t.Fatalf("status after acceptance: %+v %v", st, err)
	}

	outcome, err := a.Cancel(ctx, ref, s.Transactions[0].ID)
	if err != nil || outcome != consumerinvoice.CancelRequested {
		t.Fatalf("cancel: %v %v, want requested", outcome, err)
	}
	st, _ = a.Status(ctx, ref)
	if st[0].AvtaleGiro != consumerinvoice.AvtaleGiroCancelled {
		t.Errorf("after the cancel: %s, want cancelled", st[0].AvtaleGiro)
	}

	// Past 14:00 the day before, too late.
	s.ID, s.Transactions[0].ID = "7f1c0d7e-1f6e-4b8a-9d1e-0a1b2c3d4e5f", "1e6a1d2b-3c4d-4e5f-8a9b-0c1d2e3f4a5b"
	if _, err := a.Send(ctx, s); err != nil {
		t.Fatalf("second shipment: %v", err)
	}
	c.now = time.Date(2026, 9, 19, 12, 30, 0, 0, time.UTC) // 14:30 in Oslo
	if outcome, err := a.Cancel(ctx, consumerinvoice.Ref(s.ID), s.Transactions[0].ID); err != nil || outcome != consumerinvoice.CancelNotAllowed {
		t.Errorf("a late cancel: %v %v, want not allowed", outcome, err)
	}

	fake.SetTransactionState(s.Transactions[0].ID, "", "REJECTED", true, "ATG_REJECTED", "The payer refused the debit.")
	st, _ = a.Status(ctx, consumerinvoice.Ref(s.ID))
	if st[0].AvtaleGiro != consumerinvoice.AvtaleGiroRejected || !st[0].Attention || st[0].RejectionCode != "ATG_REJECTED" {
		t.Errorf("a rejected claim: %+v", st[0])
	}
}

// The faults fold onto the port's errors: refused credentials, a throttle, a
// refusal with its code, and a server fault that is a plain error.
func TestFaults_Fold(t *testing.T) {
	t.Parallel()
	a, fake, _ := adapter(t)
	ctx := context.Background()
	keys := []consumerinvoice.Consumer{{Phone: "1", Email: "e"}}

	fake.FailOn(mpstest.OpLookup, http.StatusUnauthorized)
	if _, err := a.Lookup(ctx, keys); !errors.Is(err, consumerinvoice.ErrUnauthorized) {
		t.Errorf("unauthorized: %v", err)
	}
	fake.FailOn(mpstest.OpLookup, http.StatusTooManyRequests)
	var throttled *consumerinvoice.ErrThrottled
	if _, err := a.Lookup(ctx, keys); !errors.As(err, &throttled) || throttled.RetryAfter != 30*time.Second {
		t.Errorf("throttled: %v", err)
	}
	fake.RefuseOn(mpstest.OpLookup, "1001", "Invalid key.")
	var refused *consumerinvoice.ErrRefused
	if _, err := a.Lookup(ctx, keys); !errors.As(err, &refused) || refused.Code != "1001" {
		t.Errorf("refused: %v", err)
	}
	fake.FailOn(mpstest.OpLookup, http.StatusInternalServerError)
	if _, err := a.Lookup(ctx, keys); err == nil || errors.As(err, &refused) || errors.Is(err, consumerinvoice.ErrUnauthorized) {
		t.Errorf("a server fault: %v, want a plain retryable error", err)
	}
	fake.FailOn(mpstest.OpLookup, 0)
	if _, err := a.Lookup(ctx, keys); err != nil {
		t.Errorf("cleared: %v", err)
	}
}

// The mandate queue pages, replays a page under the same request id, and
// answers nothing new as an empty page; coverage is the payer's limit.
func TestMandates_QueueAndCoverage(t *testing.T) {
	t.Parallel()
	a, fake, _ := adapter(t)
	ctx := context.Background()
	fake.SetPageSize(2)
	fake.QueueMandates(
		mpstest.Mandate{KID: "900012340", Status: "ACTV"},
		mpstest.Mandate{KID: "900012357", Status: "PEND", NotifyByMail: true},
		mpstest.Mandate{KID: "900012365", Status: "INAC", Deleted: true},
	)
	first, err := a.Mandates(ctx, "req-1")
	if err != nil || len(first.Mandates) != 2 || !first.More || first.Mandates[1].Status != consumerinvoice.MandatePending || !first.Mandates[1].NotifyByMail {
		t.Fatalf("first page: %+v %v", first, err)
	}
	replay, err := a.Mandates(ctx, "req-1")
	if err != nil || len(replay.Mandates) != 2 || replay.Mandates[0].KID != "900012340" {
		t.Errorf("replay: %+v %v", replay, err)
	}
	second, err := a.Mandates(ctx, "req-2")
	if err != nil || len(second.Mandates) != 1 || second.More || !second.Mandates[0].Deleted {
		t.Errorf("second page: %+v %v", second, err)
	}
	empty, err := a.Mandates(ctx, "req-3")
	if err != nil || len(empty.Mandates) != 0 || empty.More {
		t.Errorf("nothing new: %+v %v", empty, err)
	}

	fake.SetLimit("900012340", 100000)
	if ok, err := a.Covers(ctx, "900012340", consumerinvoice.NOK(99999)); err != nil || !ok {
		t.Errorf("within the limit: %v %v", ok, err)
	}
	if ok, err := a.Covers(ctx, "900012340", consumerinvoice.NOK(100001)); err != nil || ok {
		t.Errorf("above the limit: %v %v", ok, err)
	}
	fake.FailOn(mpstest.OpMandates, http.StatusForbidden)
	if _, err := a.Mandates(ctx, "req-4"); !errors.Is(err, consumerinvoice.ErrUnauthorized) {
		t.Errorf("a refused certificate: %v", err)
	}
}

// Verify is one lookup and, with a creditor account, one coverage check —
// never a page of the queue, which it would consume.
func TestVerify_LeavesTheQueue(t *testing.T) {
	t.Parallel()
	a, fake, _ := adapter(t)
	fake.QueueMandates(mpstest.Mandate{KID: "900012340", Status: "ACTV"})
	if err := a.Verify(context.Background()); err != nil {
		t.Fatalf("verify: %v", err)
	}
	page, err := a.Mandates(context.Background(), "after-verify")
	if err != nil || len(page.Mandates) != 1 {
		t.Errorf("the queue after verify: %+v %v, want the mandate still there", page, err)
	}
	fake.FailOn(mpstest.OpLookup, http.StatusUnauthorized)
	if err := a.Verify(context.Background()); !errors.Is(err, consumerinvoice.ErrUnauthorized) {
		t.Errorf("verify refused: %v", err)
	}
}

// The listener requires a client certificate: a client without one is
// turned away at the handshake.
func TestTLS_RequiresTheClientCertificate(t *testing.T) {
	t.Parallel()
	fake := mpstest.New()
	t.Cleanup(fake.Close)
	client := &http.Client{Transport: fake.Transport(), Timeout: 5 * time.Second}
	resp, err := client.Post(fake.EfakturaURL(), "text/xml", strings.NewReader("<x/>"))
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("a call without a client certificate answered %d, want the handshake refused", resp.StatusCode)
	}
}
