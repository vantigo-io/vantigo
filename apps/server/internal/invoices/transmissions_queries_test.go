package invoices_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// The transmissions queries the workers will complete their claims with
// (EHF and KID design D9), held to the rule the trigger cannot enforce for
// them: every completion names the status its claim saw, so a row the events
// worker moved meanwhile — it takes no lease — answers 0 rows, never the
// trigger's refusal; the machine's resolution of an unconfirmed row writes
// its note without a user; and an event with an empty reference never stores
// it.
func TestTransmissions_CompletionsAreStatusGuarded(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	ctx := context.Background()
	q := store.New(h.Pool())
	now := h.Now()
	insert := func(invoiceID int64) store.InvoicesTransmission {
		t.Helper()
		row, err := q.InsertTransmission(ctx, store.InsertTransmissionParams{
			InvoiceID: invoiceID, Provider: "storecove", IdempotencyKey: uuid.New(),
			SenderParticipant: "0192:974760673", ReceiverParticipant: "0192:923609016",
			DocumentType: "urn:oasis:names:specification:ubl:schema:xsd:Invoice-2::Invoice", ProcessID: "urn:fdc:peppol.eu:2017:poacc:billing:01:1.0",
			UblObjectKey: "documents/1/1-1001.xml", UblSha256: strings.Repeat("a", 64), PdfSha256: strings.Repeat("b", 64),
			Now: now, LookupRegistered: true, LookupCanReceive: true, LookupAt: now, CreatedByUserID: uuid.New(),
		})
		if err != nil {
			t.Fatalf("insert a transmission: %v", err)
		}
		return row
	}
	claim := func(lease string) store.InvoicesTransmission {
		t.Helper()
		row, err := q.ClaimTransmission(ctx, store.ClaimTransmissionParams{LeaseID: &lease, LeaseUntil: now.Add(time.Minute), Now: now})
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		return row
	}

	// A queued row claimed, then delivered by an event while the claim runs.
	moved := insert(issuedAcme(t, h).ID)
	claimed := claim("w1")
	if claimed.ID != moved.ID {
		t.Fatalf("claimed %d, want %d", claimed.ID, moved.ID)
	}
	if n, err := q.ApplyEventDelivered(ctx, store.ApplyEventDeliveredParams{Now: now, ProviderRef: ptr("guid-1"), IdempotencyKey: moved.IdempotencyKey}); err != nil || n != 1 {
		t.Fatalf("the event = %d rows, %v; want 1", n, err)
	}
	if n, err := q.RescheduleLeased(ctx, store.RescheduleLeasedParams{
		NextAttemptAt: now.Add(time.Minute), SubmitAttemptsDelta: 1, ID: moved.ID, LeaseID: ptr("w1"), Status: "queued",
	}); err != nil || n != 0 {
		t.Errorf("a reschedule after the event = %d rows, %v; want a 0-row no-op", n, err)
	}
	if n, err := q.MarkFailedLeased(ctx, store.MarkFailedLeasedParams{Now: now, LastError: ptr("timeout"), ID: moved.ID, LeaseID: ptr("w1"), Status: "queued"}); err != nil || n != 0 {
		t.Errorf("a failure after the event = %d rows, %v; want a 0-row no-op", n, err)
	}

	// The machine resolves an unconfirmed row: a note, no user.
	resolved := insert(issuedAcme(t, h).ID)
	claim("w2")
	if n, err := q.MarkUnconfirmedLeased(ctx, store.MarkUnconfirmedLeasedParams{NextAttemptAt: now, ID: resolved.ID, LeaseID: ptr("w2"), Status: "queued"}); err != nil || n != 1 {
		t.Fatalf("unconfirmed = %d rows, %v; want 1", n, err)
	}
	claim("w3")
	if n, err := q.MarkDeliveredLeased(ctx, store.MarkDeliveredLeasedParams{
		Now: now, MachineNote: ptr("Delivered, the provider answered after the row was unconfirmed"), ID: resolved.ID, LeaseID: ptr("w3"), Status: "unconfirmed",
	}); err != nil || n != 1 {
		t.Fatalf("delivered by the machine = %d rows, %v; want 1", n, err)
	}
	row, err := q.GetTransmission(ctx, resolved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "delivered" || row.ResolvedByUserID != nil || row.ResolutionNote == nil {
		t.Errorf("resolved = %s, user %v, note %v; want delivered with the machine's note and no user", row.Status, row.ResolvedByUserID, row.ResolutionNote)
	}

	// An event matched by its key with an empty reference stores no
	// reference: NULLIF keeps the row's NULL, so a later event by its real
	// reference still matches and takes it. Without NULLIF the row would
	// carry '' here.
	for _, apply := range []func(store.InvoicesTransmission) (int64, error){
		func(r store.InvoicesTransmission) (int64, error) {
			return q.ApplyEventFailed(ctx, store.ApplyEventFailedParams{Now: now, LastError: ptr("refused"), ProviderRef: ptr(""), IdempotencyKey: r.IdempotencyKey})
		},
		func(r store.InvoicesTransmission) (int64, error) {
			return q.ApplyEventDelivered(ctx, store.ApplyEventDeliveredParams{Now: now, ProviderRef: ptr(""), IdempotencyKey: r.IdempotencyKey})
		},
	} {
		plain := insert(issuedAcme(t, h).ID)
		if n, err := apply(plain); err != nil || n != 1 {
			t.Fatalf("an event by its key = %d rows, %v; want 1", n, err)
		}
		if row, err = q.GetTransmission(ctx, plain.ID); err != nil || row.ProviderRef != nil || row.ResolutionNote != nil {
			t.Errorf("after the event: reference %v note %v (%v), want neither", row.ProviderRef, row.ResolutionNote, err)
		}
	}
}
