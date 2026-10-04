package invoices_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// transmissionPath is POST …/transmissions/{transmissionId}/<action>, or the
// UBL's GET for "ubl".
func transmissionPath(id, tid int64, action string) string {
	return fmt.Sprintf("%s/%d/transmissions/%d/%s", invoicesPath, id, tid, action)
}

// sameInstant is whether a wire timestamp is the instant want.
func sameInstant(t *testing.T, wire string, want time.Time) bool {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, wire)
	if err != nil {
		t.Fatalf("parse %q: %v", wire, err)
	}
	return at.Equal(want.Truncate(time.Microsecond))
}

// transmissionStatus is a transmission row's status.
func transmissionStatus(t *testing.T, h *harness, tid int64) string {
	t.Helper()
	var status string
	if err := h.Pool().QueryRow(context.Background(), `SELECT status FROM invoices.transmissions WHERE id = $1`, tid).Scan(&status); err != nil {
		t.Fatalf("read transmission %d: %v", tid, err)
	}
	return status
}

// Only a queued transmission that was never attempted and is not leased is
// cancelled (D9): once the worker has stamped the crash marker — the claim
// won — or holds a live lease, the submission may have reached the provider
// and the cancel is transmission_not_cancellable. A cancelled transmission
// lets the document be sent again; a transmission is cancelled only under its
// own document.
func TestTransmissions_CancelOnlyNeverAttempted(t *testing.T) {
	t.Parallel()
	h, _ := ehfReady(t)
	inv := issuedAcme(t, h)
	sentAsEhf(t, h, inv.ID)
	tid, _, _, _ := latestTransmission(t, h, inv.ID)
	c := issuer(t, h)

	other := issuedAcme(t, h)
	if res := c.Do(http.MethodPost, transmissionPath(other.ID, tid, "cancel"), nil); res.Status != http.StatusNotFound {
		t.Errorf("cancel under another document = %d %s, want 404", res.Status, res.Body)
	}
	if res := c.Do(http.MethodPost, transmissionPath(999999, tid, "cancel"), nil); res.Status != http.StatusNotFound {
		t.Errorf("cancel under an unknown document = %d %s, want 404", res.Status, res.Body)
	}
	if res := c.Do(http.MethodPost, transmissionPath(inv.ID, 999999, "cancel"), nil); res.Status != http.StatusNotFound {
		t.Errorf("cancel an unknown transmission = %d %s, want 404", res.Status, res.Body)
	}

	res := c.Do(http.MethodPost, transmissionPath(inv.ID, tid, "cancel"), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("cancel = %d %s, want 200", res.Status, res.Body)
	}
	var got invoiceJSON
	res.JSON(&got)
	if got.Ehf == nil || got.Ehf.Status != "cancelled" || got.Ehf.Transmissions[0].CancelledAt == nil || !got.Ehf.CanSend {
		t.Fatalf("after the cancel ehf = %+v, want cancelled, with cancelledAt, sendable again", got.Ehf)
	}
	if at := *got.Ehf.Transmissions[0].CancelledAt; !sameInstant(t, at, h.Now()) {
		t.Errorf("cancelledAt = %s, want the module clock's %s — never SQL now()", at, h.Now().Format(time.RFC3339Nano))
	}
	sendRefused(t, "a second cancel", c.Do(http.MethodPost, transmissionPath(inv.ID, tid, "cancel"), nil),
		http.StatusConflict, "transmission_not_cancellable")

	// The worker claimed it and stamped the marker before the cancel.
	sentAsEhf(t, h, inv.ID)
	claimed, _, _, _ := latestTransmission(t, h, inv.ID)
	h.Exec(t, `UPDATE invoices.transmissions SET submit_attempted_at = $2 WHERE id = $1`, claimed, h.Now())
	sendRefused(t, "a cancel after the marker", c.Do(http.MethodPost, transmissionPath(inv.ID, claimed, "cancel"), nil),
		http.StatusConflict, "transmission_not_cancellable")

	// A live lease, the marker not yet stamped.
	leased := issuedAcme(t, h)
	sentAsEhf(t, h, leased.ID)
	ltid, _, _, _ := latestTransmission(t, h, leased.ID)
	h.Exec(t, `UPDATE invoices.transmissions SET lease_id = 'worker-1', lease_until = $2 WHERE id = $1`, ltid, h.Now().Add(time.Minute))
	sendRefused(t, "a cancel under a live lease", c.Do(http.MethodPost, transmissionPath(leased.ID, ltid, "cancel"), nil),
		http.StatusConflict, "transmission_not_cancellable")
	h.Exec(t, `UPDATE invoices.transmissions SET lease_until = $2 WHERE id = $1`, ltid, h.Now().Add(-time.Second))
	if res := c.Do(http.MethodPost, transmissionPath(leased.ID, ltid, "cancel"), nil); res.Status != http.StatusOK {
		t.Errorf("a cancel after the lease ran out = %d %s, want 200", res.Status, res.Body)
	}
	if transmissionStatus(t, h, claimed) != "queued" || transmissionStatus(t, h, tid) != "cancelled" {
		t.Errorf("statuses = %s, %s; want the claimed one queued and the first cancelled", transmissionStatus(t, h, claimed), transmissionStatus(t, h, tid))
	}
}

// A transmission carries its crash marker — when the worker last stamped it,
// right before calling the provider — for an issuer only (reading 19): absent
// while never attempted, which is when a queued one can still be cancelled.
func TestTransmissions_CarryTheirAttemptMarker(t *testing.T) {
	t.Parallel()
	h, _ := ehfReady(t)
	inv := issuedAcme(t, h)
	sent := sentAsEhf(t, h, inv.ID)
	if m := sent.Ehf.Transmissions[0].SubmitAttemptedAt; m != nil {
		t.Errorf("a never-attempted transmission's submitAttemptedAt = %s, want absent", *m)
	}
	tid := sent.Ehf.Transmissions[0].ID
	at := h.Now().Add(-time.Minute)
	h.Exec(t, `UPDATE invoices.transmissions SET submit_attempted_at = $2 WHERE id = $1`, tid, at)
	got := readAs(t, issuer(t, h), inv.ID).Ehf.Transmissions[0]
	if got.SubmitAttemptedAt == nil || !sameInstant(t, *got.SubmitAttemptedAt, at) {
		t.Errorf("an issuer's submitAttemptedAt = %v, want %s", got.SubmitAttemptedAt, at.Format(time.RFC3339Nano))
	}
	if m := readAs(t, h.SignIn(t, "invoices:access"), inv.ID).Ehf.Transmissions[0].SubmitAttemptedAt; m != nil {
		t.Errorf("a reader's submitAttemptedAt = %s, want absent", *m)
	}
}

// Only an unconfirmed transmission is resolved, by a person with a note (D9):
// failed allows a new send, delivered closes it with its delivery time. The
// outcome is delivered or failed — anything else is a 400 from the
// contract's enum, and the query itself refuses a non-terminal outcome — and
// a resolution of anything but an unconfirmed transmission is
// transmission_not_resolvable.
func TestTransmissions_ResolveOnlyUnconfirmed(t *testing.T) {
	t.Parallel()
	h, _ := ehfReady(t)
	inv := issuedAcme(t, h)
	sentAsEhf(t, h, inv.ID)
	tid, _, _, _ := latestTransmission(t, h, inv.ID)
	c, userID := h.SignInUser(t, "invoices:access", "invoices:issue")
	resolve := func(id, tid int64, body map[string]any) (int, []byte, invoiceJSON) {
		t.Helper()
		res := c.Do(http.MethodPost, transmissionPath(id, tid, "resolve"), body)
		var got invoiceJSON
		if res.Status == http.StatusOK {
			res.JSON(&got)
		}
		return res.Status, res.Body, got
	}

	sendRefused(t, "resolve a queued transmission", c.Do(http.MethodPost, transmissionPath(inv.ID, tid, "resolve"),
		map[string]any{"outcome": "failed", "note": "Not there"}), http.StatusConflict, "transmission_not_resolvable")

	h.Exec(t, `UPDATE invoices.transmissions SET status = 'unconfirmed', submit_attempted_at = $2, submitted_at = $2 WHERE id = $1`, tid, h.Now())
	for name, body := range map[string]map[string]any{
		"a queued outcome":    {"outcome": "queued", "note": "x"},
		"a submitted outcome": {"outcome": "submitted", "note": "x"},
		"no note":             {"outcome": "failed", "note": "  "},
		"a note over 500":     {"outcome": "failed", "note": strings.Repeat("n", 501)},
	} {
		if status, body, _ := resolve(inv.ID, tid, body); status != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, status, body)
		}
	}
	if status, _, _ := resolve(issuedAcme(t, h).ID, tid, map[string]any{"outcome": "failed", "note": "x"}); status != http.StatusNotFound {
		t.Errorf("resolve under another document = %d, want 404", status)
	}

	// The query's own guard: a non-terminal outcome matches nothing.
	for _, outcome := range []string{"queued", "submitted", "unconfirmed", "cancelled"} {
		n, err := store.New(h.Pool()).ResolveTransmission(context.Background(), store.ResolveTransmissionParams{
			Outcome: outcome, Now: h.Now(), ResolvedByUserID: &userID, ResolutionNote: ptr("x"), ID: tid, InvoiceID: inv.ID,
		})
		if err != nil || n != 0 {
			t.Errorf("ResolveTransmission(%s) = %d, %v; want 0 rows", outcome, n, err)
		}
	}

	status, body, got := resolve(inv.ID, tid, map[string]any{"outcome": "failed", "note": "  Storecove has no record of it  "})
	if status != http.StatusOK {
		t.Fatalf("resolve as failed = %d %s, want 200", status, body)
	}
	tr := got.Ehf.Transmissions[0]
	if tr.Status != "failed" || tr.FailedAt == nil || tr.ResolvedByUserID == nil || *tr.ResolvedByUserID != userID.String() ||
		tr.ResolutionNote == nil || *tr.ResolutionNote != "Storecove has no record of it" || !got.Ehf.CanSend {
		t.Errorf("resolved as failed = %+v (canSend %v), want failed by the caller with the trimmed note, sendable again", tr, got.Ehf.CanSend)
	}
	if status, _, _ := resolve(inv.ID, tid, map[string]any{"outcome": "delivered", "note": "again"}); status != http.StatusConflict {
		t.Errorf("resolve a failed transmission = %d, want 409", status)
	}

	other := issuedAcme(t, h)
	otherID := plantTransmissionOn(t, h, other.ID, "unconfirmed")
	status, body, got = resolve(other.ID, otherID, map[string]any{"outcome": "delivered", "note": "The receiver confirmed it"})
	if status != http.StatusOK {
		t.Fatalf("resolve as delivered = %d %s, want 200", status, body)
	}
	tr = got.Ehf.Transmissions[0]
	if tr.Status != "delivered" || tr.DeliveredAt == nil || got.Ehf.DeliveredAt == nil || got.Ehf.CanSend {
		t.Fatalf("resolved as delivered = %+v, ehf %+v; want delivered with deliveredAt, not sendable", tr, got.Ehf)
	}
	if !sameInstant(t, *tr.DeliveredAt, h.Now()) {
		t.Errorf("deliveredAt = %s, want the module clock's %s — never SQL now()", *tr.DeliveredAt, h.Now().Format(time.RFC3339Nano))
	}
}

// The UBL a transmission carries is downloaded as stored, verified against
// its hash, by anyone who may read invoices (D10): an altered or missing
// object is a 500 and never rendered again, a store that cannot be read a
// 503, and a transmission only under its own document.
func TestTransmissions_UblDownload(t *testing.T) {
	t.Parallel()
	h, _ := ehfReady(t)
	inv := issuedAcme(t, h)
	sentAsEhf(t, h, inv.ID)
	tid, key, hash, _ := latestTransmission(t, h, inv.ID)
	reader := h.SignIn(t, "invoices:access")

	res := reader.Do(http.MethodGet, transmissionPath(inv.ID, tid, "ubl"), nil)
	if res.Status != http.StatusOK || res.Header("Content-Type") != "application/xml" || sha(res.Body) != hash ||
		!strings.Contains(res.Header("Content-Disposition"), fmt.Sprintf(`faktura-%d-%d.xml`, *inv.Number, tid)) ||
		res.Header("Cache-Control") != "private, no-store" {
		t.Fatalf("GET ubl = %d %s %q %q, want the stored bytes as application/xml, named, never cached",
			res.Status, res.Header("Content-Type"), res.Header("Content-Disposition"), res.Header("Cache-Control"))
	}
	if !strings.Contains(string(res.Body), "<Invoice") {
		t.Errorf("the UBL = %.200s, want an Invoice", res.Body)
	}

	other := issuedAcme(t, h)
	for what, path := range map[string]string{
		"under another document":  transmissionPath(other.ID, tid, "ubl"),
		"an unknown transmission": transmissionPath(inv.ID, 999999, "ubl"),
		"an unknown document":     transmissionPath(999999, tid, "ubl"),
	} {
		if res := reader.Do(http.MethodGet, path, nil); res.Status != http.StatusNotFound {
			t.Errorf("GET ubl %s = %d, want 404", what, res.Status)
		}
	}

	h.objects.failGets(errors.New("the store is down"))
	sendRefused(t, "GET ubl with the store down", reader.Do(http.MethodGet, transmissionPath(inv.ID, tid, "ubl"), nil),
		http.StatusServiceUnavailable, "storage_unavailable")
	h.objects.failGets(nil)

	original := h.objects.object(key)
	h.objects.replace(key, append([]byte("<!-- altered -->"), original...))
	if res := reader.Do(http.MethodGet, transmissionPath(inv.ID, tid, "ubl"), nil); res.Status != http.StatusInternalServerError {
		t.Errorf("GET an altered ubl = %d, want 500", res.Status)
	}
	h.objects.lose(key)
	if res := reader.Do(http.MethodGet, transmissionPath(inv.ID, tid, "ubl"), nil); res.Status != http.StatusInternalServerError {
		t.Errorf("GET a missing ubl = %d, want 500", res.Status)
	}
	if _, puts, _ := h.objects.stored(); h.objects.object(key) != nil {
		t.Errorf("the missing UBL was stored again (%d puts)", puts)
	}
	logged := 0
	for _, l := range strings.Split(h.Logs(), "\n") {
		if strings.Contains(l, `"level":"ERROR"`) && strings.Contains(l, fmt.Sprintf(`"transmission_id":%d`, tid)) {
			logged++
		}
	}
	if logged < 2 {
		t.Errorf("logs =\n%s\nwant the altered and the missing UBL each logged at error", h.Logs())
	}
}
