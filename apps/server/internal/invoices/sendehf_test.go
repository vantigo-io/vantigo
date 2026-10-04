package invoices_test

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/peppol"
)

func sendEhfPath(id int64) string { return fmt.Sprintf("%s/%d/send-ehf", invoicesPath, id) }

// ehfJSON is a document's EHF state as a client reads it.
type ehfJSON struct {
	Status        string             `json:"status"`
	QueuedAt      *string            `json:"queuedAt"`
	SubmittedAt   *string            `json:"submittedAt"`
	DeliveredAt   *string            `json:"deliveredAt"`
	FailedAt      *string            `json:"failedAt"`
	ProviderRef   *string            `json:"providerRef"`
	Reason        *string            `json:"reason"`
	CanSend       bool               `json:"canSend"`
	BlockedBy     *string            `json:"blockedBy"`
	Preference    *string            `json:"preference"`
	BuyerPeppolID *string            `json:"buyerPeppolId"`
	Transmissions []transmissionJSON `json:"transmissions"`
}

// transmissionJSON is one transmission as a client reads it.
type transmissionJSON struct {
	ID                  int64   `json:"id"`
	Status              string  `json:"status"`
	Provider            string  `json:"provider"`
	IdempotencyKey      string  `json:"idempotencyKey"`
	ReceiverParticipant string  `json:"receiverParticipant"`
	UblSha256           string  `json:"ublSha256"`
	QueuedAt            string  `json:"queuedAt"`
	SubmittedAt         *string `json:"submittedAt"`
	DeliveredAt         *string `json:"deliveredAt"`
	FailedAt            *string `json:"failedAt"`
	CancelledAt         *string `json:"cancelledAt"`
	ProviderRef         *string `json:"providerRef"`
	Reason              *string `json:"reason"`
	ResolvedByUserID    *string `json:"resolvedByUserId"`
	ResolutionNote      *string `json:"resolutionNote"`
	UblURL              string  `json:"ublUrl"`
}

// fakeLookup stands in for the Peppol network: it answers result or err,
// records every participant it was asked about, and runs onCall first — the
// moment between the send's reads and its lock.
type fakeLookup struct {
	mu     sync.Mutex
	calls  []string
	result peppol.Result
	err    error
	onCall func(participant string)
}

func (f *fakeLookup) lookup(_ context.Context, participant string) (peppol.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, participant)
	res, err, onCall := f.result, f.err, f.onCall
	f.mu.Unlock()
	if onCall != nil {
		onCall(participant)
	}
	return res, err
}

// answer sets what every lookup answers from now on.
func (f *fakeLookup) answer(res peppol.Result, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.result, f.err = res, err
}

// whenCalled sets onCall.
func (f *fakeLookup) whenCalled(fn func(participant string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onCall = fn
}

// asked is every participant looked up so far.
func (f *fakeLookup) asked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// receivesBoth is a participant registered for both document types.
var receivesBoth = peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}

// ehfReady is an installation that can send as EHF (EHF and KID design D1):
// the Peppol lookup faked and answering that the receiver takes both document
// types, a complete seller (whose Peppol id defaults to 0192:974760673) and
// access-point credentials stored. Acme carries the Peppol id 0192:923609016
// and the buyer reference PO-77 into every draft made for it.
func ehfReady(t *testing.T, opts ...modtest.Option) (*harness, *fakeLookup) {
	t.Helper()
	fake := &fakeLookup{result: receivesBoth}
	h := newHarness(t, append([]modtest.Option{modtest.WithPeppolLookup(fake.lookup)}, opts...)...)
	saveSeller(t, h, completeSeller(1))
	plantAccessPointCredentials(t, h)
	return h, fake
}

// sendEhfAs posts a send as EHF of id as c.
func sendEhfAs(c *modtest.Client, id int64) *modtest.Response {
	return c.Do(http.MethodPost, sendEhfPath(id), nil)
}

// sentAsEhf sends id as EHF as an issuer and answers the document, failing
// unless the transmission was queued.
func sentAsEhf(t *testing.T, h *harness, id int64) invoiceJSON {
	t.Helper()
	res := sendEhfAs(issuer(t, h), id)
	if res.Status != http.StatusOK {
		t.Fatalf("send %d as EHF = %d %s, want 200", id, res.Status, res.Body)
	}
	var inv invoiceJSON
	res.JSON(&inv)
	if inv.Ehf == nil || inv.Ehf.Status != "queued" || len(inv.Ehf.Transmissions) == 0 || inv.Ehf.Transmissions[0].Status != "queued" {
		t.Fatalf("send %d as EHF answered ehf %+v, want a queued transmission", id, inv.Ehf)
	}
	return inv
}

// transmissionRows is how many transmissions document id has.
func transmissionRows(t *testing.T, h *harness, id int64) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM invoices.transmissions WHERE invoice_id = $1`, id)
}

// ublKeys is every UBL object in the store.
func ublKeys(h *harness) []string {
	keys, _, _ := h.objects.stored()
	var out []string
	for _, k := range keys {
		if strings.HasSuffix(k, ".xml") {
			out = append(out, k)
		}
	}
	return out
}

// latestTransmission is document id's newest transmission row: its id, UBL
// key and hash, and sender.
func latestTransmission(t *testing.T, h *harness, id int64) (tid int64, key, hash, sender string) {
	t.Helper()
	row := h.Pool().QueryRow(context.Background(), `SELECT id, ubl_object_key, ubl_sha256, sender_participant
		FROM invoices.transmissions WHERE invoice_id = $1 ORDER BY id DESC LIMIT 1`, id)
	if err := row.Scan(&tid, &key, &hash, &sender); err != nil {
		t.Fatalf("read document %d's latest transmission: %v", id, err)
	}
	return tid, key, hash, sender
}

// plantTransmissionOn inserts a transmission of document id at status, through
// the send's own query and an UPDATE the trigger allows.
func plantTransmissionOn(t *testing.T, h *harness, id int64, status string) int64 {
	t.Helper()
	tid := insertTransmission(t, h, id)
	if status != "queued" {
		h.Exec(t, `UPDATE invoices.transmissions SET status = $2::varchar,
			submitted_at = CASE WHEN $2::varchar IN ('submitted', 'delivered', 'unconfirmed') THEN $3::timestamptz END,
			delivered_at = CASE WHEN $2::varchar = 'delivered' THEN $3::timestamptz END,
			failed_at = CASE WHEN $2::varchar = 'failed' THEN $3::timestamptz END,
			cancelled_at = CASE WHEN $2::varchar = 'cancelled' THEN $3::timestamptz END,
			submit_attempted_at = CASE WHEN $2::varchar <> 'cancelled' THEN $3::timestamptz END
			WHERE id = $1`, tid, status, h.Now())
	}
	return tid
}

// kCode creates a VAT code in category K (intra-EEA) and answers its id.
func kCode(t *testing.T, h *harness) int32 {
	t.Helper()
	res := manager(t, h).Do(http.MethodPost, vatCodesPath, map[string]any{
		"code": "K1", "name": "Salg til EØS", "safTCode": "52", "ehfCategory": "K",
		"exemptionReason": "Intra-EEA supply", "ratePercent": 0, "validFrom": "2026-01-01",
	})
	if res.Status != http.StatusCreated {
		t.Fatalf("POST /vat-codes = %d %s, want 201", res.Status, res.Body)
	}
	var created vatCodeJSON
	res.JSON(&created)
	return created.ID
}

// An installation that cannot send as EHF says so before anything else (D8
// step 1): the switch off, the Peppol lookup disabled, no access-point
// credentials or no seller Peppol id answer ehf_unavailable for an issued
// document, a draft and an id that does not exist alike, and nothing is read,
// looked up or queued.
func TestSendEhf_UnavailableIsJudgedFirst(t *testing.T) {
	t.Parallel()
	for name, setup := range map[string]func(t *testing.T) (*harness, *fakeLookup){
		"INVOICES_EHF_ENABLED=0": func(t *testing.T) (*harness, *fakeLookup) {
			return ehfReady(t, modtest.WithEnv("INVOICES_EHF_ENABLED", "0"))
		},
		"PEPPOL_LOOKUP_ENABLED=0": func(t *testing.T) (*harness, *fakeLookup) {
			return ehfReady(t, modtest.WithEnv("PEPPOL_LOOKUP_ENABLED", "0"))
		},
		"no credentials": func(t *testing.T) (*harness, *fakeLookup) {
			h, fake := ehfReady(t)
			h.Exec(t, `DELETE FROM invoices.access_point_credentials`)
			return h, fake
		},
		"no seller Peppol id": func(t *testing.T) (*harness, *fakeLookup) {
			h, fake := ehfReady(t)
			h.Exec(t, `UPDATE invoices.settings SET peppol_id = NULL`)
			return h, fake
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, fake := setup(t)
			inv := issuedAcme(t, h)
			draft := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 1, 100, vat25)))
			c, userID := h.SignInUser(t, "invoices:access", "invoices:issue")
			for _, id := range []int64{inv.ID, draft.ID, 999999} {
				sendRefused(t, fmt.Sprintf("send %d as EHF", id), sendEhfAs(c, id), http.StatusServiceUnavailable, "ehf_unavailable")
			}
			if calls := contractCalls.by(userID); len(calls) != 0 {
				t.Errorf("calls out of the module = %+v, want none", calls)
			}
			if n := len(fake.asked()); n != 0 || transmissionRows(t, h, inv.ID) != 0 {
				t.Errorf("%d lookups, %d transmissions; want none", n, transmissionRows(t, h, inv.ID))
			}
		})
	}
}

// A draft is no document to send (invoice_draft), an unknown id is a bare
// 404, and a customer this module has anonymised is never sent to again
// (customer_anonymised, D8 step 2) — judged before the network. And an
// erasure that commits after the send's reads is caught under the lock: one
// committed while the receiver is looked up is judged again once the
// document is locked, and one whose marker commits after that judgment is
// refused by the insert's own trigger — a 409 either way, never a 500, and
// nothing queued. Not parallel: the seam is the package's.
func TestSendEhf_RefusesADraftAndAnAnonymisedCustomer(t *testing.T) {
	h, fake := ehfReady(t)
	c := issuer(t, h)
	draft := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 1, 100, vat25)))
	sendRefused(t, "send a draft as EHF", sendEhfAs(c, draft.ID), http.StatusConflict, "invoice_draft")
	if res := sendEhfAs(c, 999999); res.Status != http.StatusNotFound {
		t.Errorf("send an unknown id as EHF = %d %s, want 404", res.Status, res.Body)
	}
	inv := issuedAcme(t, h)
	h.Exec(t, `INSERT INTO invoices.erased_customers (customer_id, erased_at) VALUES ($1, now())`, customerAcme)
	sendRefused(t, "send to an anonymised customer as EHF", sendEhfAs(c, inv.ID), http.StatusConflict, "customer_anonymised")
	if n := len(fake.asked()); n != 0 {
		t.Errorf("%d lookups before the refusals, want none", n)
	}

	// The erasure commits while the receiver is looked up — after the
	// send's reads, before its lock.
	during, duringFake := ehfReady(t)
	first := issuedAcme(t, during)
	duringFake.whenCalled(func(string) {
		if _, err := during.Pool().Exec(context.Background(),
			`INSERT INTO invoices.erased_customers (customer_id, erased_at) VALUES ($1, now())`, customerAcme); err != nil {
			t.Errorf("erase during the lookup: %v", err)
		}
	})
	sendRefused(t, "an erasure committed during the lookup", sendEhfAs(issuer(t, during), first.ID), http.StatusConflict, "customer_anonymised")
	if n := transmissionRows(t, during, first.ID); n != 0 {
		t.Errorf("%d transmissions after an erasure committed during the lookup, want none", n)
	}

	// The marker commits inside the send's transaction, after the
	// judgment under the lock: the insert trigger's re-check refuses it.
	late, _ := ehfReady(t)
	second := issuedAcme(t, late)
	restore := invoices.SetBeforeTransmissionInsert(func(ctx context.Context, id int64) {
		if id != second.ID {
			return
		}
		if _, err := late.Pool().Exec(context.WithoutCancel(ctx),
			`INSERT INTO invoices.erased_customers (customer_id, erased_at) VALUES ($1, now())`, customerAcme); err != nil {
			t.Errorf("erase on the seam: %v", err)
		}
	})
	defer restore()
	sendRefused(t, "an erasure committed after the judgment under the lock", sendEhfAs(issuer(t, late), second.ID), http.StatusConflict, "customer_anonymised")
	if n := transmissionRows(t, late, second.ID); n != 0 {
		t.Errorf("%d transmissions after an erasure committed on the seam, want none", n)
	}
}

// A document whose buyer snapshot has no Peppol id — or one that is not
// <scheme>:<value>, which the render could not place — cannot be sent as EHF
// (no_peppol_id, D8 step 3), and the network is not asked.
func TestSendEhf_NoPeppolId(t *testing.T) {
	t.Parallel()
	h, fake := ehfReady(t)
	for name, id := range map[string]string{"none": "", "no scheme": "923609016", "no value": "0192:"} {
		h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.PeppolID = id })
		inv := issuedAcme(t, h)
		sendRefused(t, "a buyer Peppol id "+name, sendEhfAs(issuer(t, h), inv.ID), http.StatusConflict, "no_peppol_id")
	}
	if n := len(fake.asked()); n != 0 {
		t.Errorf("%d lookups, want none", n)
	}
}

// Peppol needs a buyer reference or an order reference (PEPPOL-EN16931-R003):
// a document with neither is buyer_reference_missing (D8 step 4) — the
// references are frozen at issue — and either one is enough.
func TestSendEhf_BuyerReferenceMissing(t *testing.T) {
	t.Parallel()
	h, fake := ehfReady(t)
	body := draftBody(customerAcme, line("Konsulenttime", 1, 100, vat25))
	body["yourReference"] = ""
	bare := issued(t, h, createDraft(t, h, body).ID)
	sendRefused(t, "a document with neither reference", sendEhfAs(issuer(t, h), bare.ID), http.StatusConflict, "buyer_reference_missing")
	if n := len(fake.asked()); n != 0 {
		t.Errorf("%d lookups, want none", n)
	}
	body["orderReference"] = "ORDRE-9"
	sentAsEhf(t, h, issued(t, h, createDraft(t, h, body).ID).ID)
}

// While a transmission is queued, submitted, delivered or unconfirmed a
// document is not sent again (ehf_already_sent, D8 step 5): judged before
// the render and the network.
func TestSendEhf_AlreadySent(t *testing.T) {
	t.Parallel()
	h, fake := ehfReady(t)
	inv := issuedAcme(t, h)
	sentAsEhf(t, h, inv.ID)
	asked := len(fake.asked())
	sendRefused(t, "a second send", sendEhfAs(issuer(t, h), inv.ID), http.StatusConflict, "ehf_already_sent")
	for _, status := range []string{"submitted", "delivered", "unconfirmed"} {
		doc := issuedAcme(t, h)
		plantTransmissionOn(t, h, doc.ID, status)
		sendRefused(t, "a send beside a "+status+" transmission", sendEhfAs(issuer(t, h), doc.ID), http.StatusConflict, "ehf_already_sent")
	}
	if n := len(fake.asked()) - asked; n != 0 {
		t.Errorf("%d lookups for the refused sends, want none", n)
	}
	if n := transmissionRows(t, h, inv.ID); n != 1 {
		t.Errorf("%d transmissions, want the first one only", n)
	}
}

// The pre-check runs on the rendered bytes before the network (D8 step 6): a
// line in VAT category K is ehf_invalid naming vat_category_k_unsupported, the
// lookup is never asked and no UBL is stored.
func TestSendEhf_PrecheckBeforeTheLookup(t *testing.T) {
	t.Parallel()
	h, fake := ehfReady(t)
	k := kCode(t, h)
	inv := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("Levering til EØS", 1, 1000, k))).ID)
	res := sendEhfAs(issuer(t, h), inv.ID)
	sendRefused(t, "a K line", res, http.StatusConflict, "ehf_invalid")
	named := false
	for _, r := range problemOf(t, res).Rules {
		named = named || (r.ID == "vat_category_k_unsupported" && r.Message != "")
	}
	if !named {
		t.Errorf("rules = %s, want vat_category_k_unsupported with its message", res.Body)
	}
	if n := len(fake.asked()); n != 0 {
		t.Errorf("%d lookups on an ehf_invalid, want none", n)
	}
	if keys := ublKeys(h); len(keys) != 0 || transmissionRows(t, h, inv.ID) != 0 {
		t.Errorf("UBL objects %v, %d transmissions; want none", keys, transmissionRows(t, h, inv.ID))
	}
}

// The receiver is re-checked on the Peppol network at the send, outside any
// lock (D8 step 7): not registered, or registered without this document's
// type, is peppol_not_receivable with both facts; a network that cannot
// answer is 502 peppol_lookup_failed, logged by its kind and never with the
// participant. Nothing is stored or queued on a refusal; a send that goes
// asks once, with the snapshot's Peppol id, and the call is on the record.
func TestSendEhf_TheReceiverRecheck(t *testing.T) {
	t.Parallel()
	h, fake := ehfReady(t)
	inv := issuedAcme(t, h)
	c, userID := h.SignInUser(t, "invoices:access", "invoices:issue")

	notReceivable := func(what string, id int64, registered, canReceive bool) {
		t.Helper()
		res := sendEhfAs(c, id)
		sendRefused(t, what, res, http.StatusConflict, "peppol_not_receivable")
		p := problemOf(t, res)
		if p.PeppolRegistered == nil || *p.PeppolRegistered != registered || p.PeppolCanReceive == nil || *p.PeppolCanReceive != canReceive {
			t.Errorf("%s: peppolRegistered %v, peppolCanReceive %v; want %v, %v", what, p.PeppolRegistered, p.PeppolCanReceive, registered, canReceive)
		}
	}
	fake.answer(peppol.Result{}, nil)
	notReceivable("a receiver not registered", inv.ID, false, false)
	fake.answer(peppol.Result{Registered: true, CanReceiveCreditNote: true}, nil)
	notReceivable("a receiver that takes no invoice", inv.ID, true, false)

	credit := issued(t, h, creditDraft(t, h, inv.ID).ID)
	fake.answer(peppol.Result{Registered: true, CanReceiveInvoice: true}, nil)
	notReceivable("a receiver that takes no credit note", credit.ID, true, false)

	fake.answer(peppol.Result{}, context.DeadlineExceeded)
	sendRefused(t, "a lookup that times out", sendEhfAs(c, inv.ID), http.StatusBadGateway, "peppol_lookup_failed")
	logged := false
	for _, l := range strings.Split(h.Logs(), "\n") {
		if strings.Contains(l, "923609016") {
			t.Errorf("a log line carries the participant: %s", l)
		}
		logged = logged || (strings.Contains(l, "peppol lookup failed") && strings.Contains(l, `"errorKind":"timeout"`))
	}
	if !logged {
		t.Errorf("logs =\n%s\nwant the failed lookup logged by its kind", h.Logs())
	}
	if keys := ublKeys(h); len(keys) != 0 || transmissionRows(t, h, inv.ID) != 0 {
		t.Errorf("UBL objects %v, %d transmissions after the refusals; want none", keys, transmissionRows(t, h, inv.ID))
	}

	fake.answer(receivesBoth, nil)
	before := len(fake.asked())
	if res := sendEhfAs(c, inv.ID); res.Status != http.StatusOK {
		t.Fatalf("a send to a receiver that takes invoices = %d %s, want 200", res.Status, res.Body)
	}
	if asked := fake.asked()[before:]; !slices.Equal(asked, []string{"0192:923609016"}) {
		t.Errorf("the send looked up %v, want the snapshot's 0192:923609016 once", asked)
	}
	lookups := 0
	for _, call := range contractCalls.by(userID) {
		if call.method == "Peppol.Lookup" {
			lookups++
			if call.locked {
				t.Errorf("a Peppol lookup was made inside a locked transaction")
			}
		}
	}
	if lookups == 0 {
		t.Errorf("calls out of the module = %+v, want the Peppol lookups on the record", contractCalls.by(userID))
	}
	if calls := lockedContractCalls.since(0); len(calls) != 0 {
		t.Errorf("calls made under a lock: %v", calls)
	}
}

// The UBL is stored once per bytes, under documents/<id>/<number>-<sha256>.xml
// and before the transmission is queued (D8 step 8); the row carries its key
// and hash. A send after a cancel renders the same bytes, finds the object
// there and stores nothing again.
func TestSendEhf_StoresTheUblOnce(t *testing.T) {
	t.Parallel()
	h, _ := ehfReady(t)
	inv := issuedAcme(t, h)
	sent := sentAsEhf(t, h, inv.ID)
	keys := ublKeys(h)
	if len(keys) != 1 || !regexp.MustCompile(fmt.Sprintf(`^documents/%d/%d-[0-9a-f]{64}\.xml$`, inv.ID, *inv.Number)).MatchString(keys[0]) {
		t.Fatalf("UBL objects = %v, want one documents/<id>/<number>-<sha256>.xml", keys)
	}
	tid, key, hash, sender := latestTransmission(t, h, inv.ID)
	if key != keys[0] || !strings.HasSuffix(key, "-"+hash+".xml") || sha(h.objects.object(key)) != hash {
		t.Errorf("row = key %q hash %q, want the stored object's key and the SHA-256 of its bytes", key, hash)
	}
	if sender != "0192:974760673" || sent.Ehf.Transmissions[0].UblSha256 != hash || sent.Ehf.Transmissions[0].ID != tid {
		t.Errorf("sender %q, answered %+v; want the seller's Peppol id and the row", sender, sent.Ehf.Transmissions[0])
	}
	row := modtest.One[string](t, h.Harness, `SELECT provider || ' ' || receiver_participant || ' ' || status || ' ' || pdf_sha256 || ' ' ||
		lookup_registered::text || ' ' || lookup_can_receive::text || ' ' || (queued_at = next_attempt_at)::text || ' ' || (submit_attempted_at IS NULL)::text
		FROM invoices.transmissions WHERE id = $1`, tid)
	pdfHash := modtest.One[string](t, h.Harness, `SELECT pdf_sha256 FROM invoices.invoices WHERE id = $1`, inv.ID)
	if want := "storecove 0192:923609016 queued " + pdfHash + " true true true true"; row != want {
		t.Errorf("row = %q, want %q", row, want)
	}
	_, puts, _ := h.objects.stored()

	if res := issuer(t, h).Do(http.MethodPost, transmissionPath(inv.ID, tid, "cancel"), nil); res.Status != http.StatusOK {
		t.Fatalf("cancel = %d %s", res.Status, res.Body)
	}
	sentAsEhf(t, h, inv.ID)
	_, again, _, _ := latestTransmission(t, h, inv.ID)
	if after := ublKeys(h); !slices.Equal(after, keys) || again != keys[0] {
		t.Errorf("after a cancel and a second send: objects %v, the new row's key %q; want the one object", after, again)
	}
	if _, n, _ := h.objects.stored(); n != puts {
		t.Errorf("%d puts after the second send, want %d — the object was there", n, puts)
	}
}

// A transmission that ended unconfirmed and that a person resolved as failed
// may have reached the receiver, so a new send carries the same bytes (D4,
// reading 13): its key, its hash and its sender, though a fresh render would
// now differ.
func TestSendEhf_ReusesTheBytesAfterAResolvedUnconfirmed(t *testing.T) {
	t.Parallel()
	h, _ := ehfReady(t)
	inv := issuedAcme(t, h)
	sentAsEhf(t, h, inv.ID)
	tid, key, hash, sender := latestTransmission(t, h, inv.ID)
	h.Exec(t, `UPDATE invoices.transmissions SET status = 'unconfirmed', submit_attempted_at = $2, submitted_at = $2 WHERE id = $1`, tid, h.Now())
	if res := issuer(t, h).Do(http.MethodPost, transmissionPath(inv.ID, tid, "resolve"), map[string]any{
		"outcome": "failed", "note": "Storecove has no record of it",
	}); res.Status != http.StatusOK {
		t.Fatalf("resolve = %d %s", res.Status, res.Body)
	}
	h.Exec(t, `UPDATE invoices.settings SET peppol_id = '0192:910000000'`)

	sentAsEhf(t, h, inv.ID)
	newID, newKey, newHash, newSender := latestTransmission(t, h, inv.ID)
	if newID == tid || newKey != key || newHash != hash || newSender != sender {
		t.Errorf("the new transmission = %d %q %q %q, want a new row carrying %q %q %q", newID, newKey, newHash, newSender, key, hash, sender)
	}
	if keys := ublKeys(h); len(keys) != 1 {
		t.Errorf("UBL objects = %v, want the one reused", keys)
	}
}

// After any other failure, and after a cancel, the send renders fresh (D4):
// a corrected seller id is not locked out, and the new bytes are a new
// object beside the old.
func TestSendEhf_RendersFreshAfterAFailure(t *testing.T) {
	t.Parallel()
	for _, end := range []string{"failed", "cancelled"} {
		t.Run(end, func(t *testing.T) {
			t.Parallel()
			h, _ := ehfReady(t)
			inv := issuedAcme(t, h)
			sentAsEhf(t, h, inv.ID)
			tid, key, hash, _ := latestTransmission(t, h, inv.ID)
			if end == "failed" {
				h.Exec(t, `UPDATE invoices.transmissions SET status = 'failed', failed_at = $2, last_error = 'refused' WHERE id = $1`, tid, h.Now())
			} else if res := issuer(t, h).Do(http.MethodPost, transmissionPath(inv.ID, tid, "cancel"), nil); res.Status != http.StatusOK {
				t.Fatalf("cancel = %d %s", res.Status, res.Body)
			}
			h.Exec(t, `UPDATE invoices.settings SET peppol_id = '0192:910000000'`)

			sentAsEhf(t, h, inv.ID)
			_, newKey, newHash, newSender := latestTransmission(t, h, inv.ID)
			if newKey == key || newHash == hash || newSender != "0192:910000000" {
				t.Errorf("after a %s transmission: %q %q %q, want a fresh render under the new seller id", end, newKey, newHash, newSender)
			}
			if keys := ublKeys(h); len(keys) != 2 || sha(h.objects.object(newKey)) != newHash {
				t.Errorf("UBL objects = %v, want the old and the new", keys)
			}
		})
	}
}

// Two sends of one document: the first is held on the seam inside its
// transaction, after its judgment under the lock; the second waits on the
// document's lock, then sees the first's row and is refused. With the lock
// bypassed both pass the judgment, and the partial unique index refuses the
// second with the same code. One transmission either way. Not parallel: the
// seam is the package's.
func TestSendEhf_TwoRacingSendsOneQueued(t *testing.T) {
	h, _ := ehfReady(t)
	inv := issuedAcme(t, h)
	second := issuer(t, h)
	var secondRes *modtest.Response
	done := make(chan struct{})
	var fired atomic.Bool
	restore := invoices.SetBeforeTransmissionInsert(func(_ context.Context, id int64) {
		if id != inv.ID || fired.Swap(true) {
			return
		}
		go func() {
			defer close(done)
			secondRes = sendEhfAs(second, inv.ID)
		}()
		if err := awaitLockWaiter(h); err != nil {
			t.Errorf("the second send: %v", err)
		}
	})
	first := sendEhfAs(issuer(t, h), inv.ID)
	restore()
	if !fired.Load() {
		t.Fatalf("the first send never reached the seam: %d %s", first.Status, first.Body)
	}
	<-done
	if first.Status != http.StatusOK {
		t.Errorf("the first send = %d %s, want 200", first.Status, first.Body)
	}
	sendRefused(t, "the second send", secondRes, http.StatusConflict, "ehf_already_sent")
	if n := transmissionRows(t, h, inv.ID); n != 1 {
		t.Errorf("%d transmissions, want one", n)
	}

	// Without the lock: both reach the seam, the floor decides.
	bare := issuedAcme(t, h)
	unlock := invoices.SetSendEhfWithoutLock()
	defer unlock()
	var arrived atomic.Int32
	both := make(chan struct{})
	restore = invoices.SetBeforeTransmissionInsert(func(_ context.Context, id int64) {
		if id != bare.ID {
			return
		}
		if arrived.Add(1) == 2 {
			close(both)
		}
		select {
		case <-both:
		case <-time.After(10 * time.Second):
			t.Errorf("the other send never reached the seam")
		}
	})
	defer restore()
	results := make([]*modtest.Response, 2)
	var wg sync.WaitGroup
	for i, c := range []*modtest.Client{issuer(t, h), issuer(t, h)} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = sendEhfAs(c, bare.ID)
		}()
	}
	wg.Wait()
	statuses := []int{results[0].Status, results[1].Status}
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{http.StatusOK, http.StatusConflict}) {
		t.Fatalf("two sends without the lock = %d %s and %d %s, want one 200 and one 409", results[0].Status, results[0].Body, results[1].Status, results[1].Body)
	}
	for _, res := range results {
		if res.Status == http.StatusConflict {
			sendRefused(t, "the send the index refused", res, http.StatusConflict, "ehf_already_sent")
		}
	}
	if n := transmissionRows(t, h, bare.ID); n != 1 {
		t.Errorf("%d transmissions without the lock, want one", n)
	}
}

// The credentials are read FOR SHARE under the send's lock, so a DELETE of
// them never leaves a queued transmission without credentials: a DELETE that
// starts while the send holds them waits, then sees the row and is refused
// (transmissions_active); a DELETE that holds them first commits, and the
// send finds none (ehf_unavailable). Not parallel: the seam is the package's.
func TestSendEhf_NeedsCredentialsUnderTheLock(t *testing.T) {
	h, _ := ehfReady(t)
	inv := issuedAcme(t, h)
	admin := h.SignIn(t, "invoices:access", "invoices:manage")
	var deleteRes *modtest.Response
	done := make(chan struct{})
	var fired atomic.Bool
	restore := invoices.SetBeforeTransmissionInsert(func(_ context.Context, id int64) {
		if id != inv.ID || fired.Swap(true) {
			return
		}
		go func() {
			defer close(done)
			deleteRes = admin.Do(http.MethodDelete, accessPointPath, nil)
		}()
		if err := awaitLockWaiter(h); err != nil {
			t.Errorf("the DELETE: %v", err)
		}
	})
	res := sendEhfAs(issuer(t, h), inv.ID)
	restore()
	if !fired.Load() {
		t.Fatalf("the send never reached the seam: %d %s", res.Status, res.Body)
	}
	<-done
	if res.Status != http.StatusOK {
		t.Errorf("the send holding the credentials = %d %s, want 200", res.Status, res.Body)
	}
	sendRefused(t, "the DELETE behind the send", deleteRes, http.StatusConflict, "transmissions_active")
	if h.Count(t, `SELECT count(*) FROM invoices.access_point_credentials`) != 1 {
		t.Error("the credentials are gone while a transmission is queued")
	}

	// The DELETE's transaction holds the row first and commits its removal.
	other := issuedAcme(t, h)
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT * FROM invoices.access_point_credentials WHERE id = 1 FOR UPDATE`); err != nil {
		t.Fatalf("lock the credentials: %v", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM invoices.access_point_credentials WHERE id = 1`); err != nil {
		t.Fatalf("delete the credentials: %v", err)
	}
	sendDone := make(chan *modtest.Response, 1)
	c := issuer(t, h)
	go func() { sendDone <- sendEhfAs(c, other.ID) }()
	if err := awaitLockWaiter(h); err != nil {
		t.Fatalf("the send: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the removal: %v", err)
	}
	sendRefused(t, "the send behind the DELETE", <-sendDone, http.StatusServiceUnavailable, "ehf_unavailable")
	if n := transmissionRows(t, h, other.ID); n != 0 {
		t.Errorf("%d transmissions queued without credentials, want none", n)
	}
}

// The send as EHF is rate limited per client: 60 in ten minutes under its
// own policy, the 61st a 429 with a Retry-After, before access and before
// the document is looked up.
func TestSendEhf_TheRateLimit(t *testing.T) {
	t.Parallel()
	h, _ := ehfReady(t)
	c := issuer(t, h)
	for i := range 60 {
		if res := sendEhfAs(c, 999999); res.Status != http.StatusNotFound {
			t.Fatalf("send %d = %d %s, want 404", i+1, res.Status, res.Body)
		}
	}
	res := sendEhfAs(c, 999999)
	if res.Status != http.StatusTooManyRequests || res.Header("Retry-After") == "" {
		t.Fatalf("the 61st send = %d (Retry-After %q) %s, want 429 with a Retry-After", res.Status, res.Header("Retry-After"), res.Body)
	}
	// The e-mail send keeps a budget of its own.
	if res := sendAs(c, 999999, nil); res.Status == http.StatusTooManyRequests {
		t.Errorf("the e-mail send after 61 EHF sends = 429, want its own budget")
	}
	if res := sendEhfAs(issuer(t, h), 999999); res.Status != http.StatusNotFound {
		t.Errorf("another client's send = %d, want 404", res.Status)
	}
}

// Sending as EHF, cancelling and resolving are under invoices:issue (D1);
// downloading the UBL needs invoices:access alone.
func TestSendEhf_NeedsIssue(t *testing.T) {
	t.Parallel()
	h, fake := ehfReady(t)
	inv := issuedAcme(t, h)
	sentAsEhf(t, h, inv.ID)
	tid, _, _, _ := latestTransmission(t, h, inv.ID)
	asked := len(fake.asked())
	for _, perms := range [][]string{{"invoices:access"}, {"invoices:access", "invoices:create", "invoices:payments", "invoices:manage"}} {
		c := h.SignIn(t, perms...)
		for what, res := range map[string]*modtest.Response{
			"send":    sendEhfAs(c, inv.ID),
			"cancel":  c.Do(http.MethodPost, transmissionPath(inv.ID, tid, "cancel"), nil),
			"resolve": c.Do(http.MethodPost, transmissionPath(inv.ID, tid, "resolve"), map[string]any{"outcome": "failed", "note": "x"}),
		} {
			if res.Status != http.StatusForbidden {
				t.Errorf("%s as %v = %d %s, want 403", what, perms, res.Status, res.Body)
			}
		}
	}
	if n := len(fake.asked()) - asked; n != 0 || transmissionRows(t, h, inv.ID) != 1 {
		t.Errorf("%d lookups, %d transmissions; want none and the one", n, transmissionRows(t, h, inv.ID))
	}
	if res := h.SignIn(t, "invoices:access").Do(http.MethodGet, transmissionPath(inv.ID, tid, "ubl"), nil); res.Status != http.StatusOK {
		t.Errorf("the UBL as invoices:access = %d %s, want 200", res.Status, res.Body)
	}
}

// A draft warns early when it is headed for EHF — its customer's profile
// prefers EHF, or the customer has a Peppol id — and has neither reference
// (D8): on every read and save, an invoice draft's from the profile and a
// credit-note draft's from the snapshot it copied. A reference ends it, and
// a customer EHF is not for is never warned.
func TestDrafts_WarnsEhfBuyerReferenceMissing(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	const warning = "ehf_buyer_reference_missing"
	warns := func(inv invoiceJSON) bool { return slices.Contains(inv.Warnings, warning) }

	body := draftBody(customerAcme, line("Konsulenttime", 1, 100, vat25))
	body["yourReference"] = ""
	draft := createDraft(t, h, body)
	if !warns(draft) || !warns(getInvoice(t, h, draft.ID)) {
		t.Errorf("a draft for a customer with a Peppol id and no reference: created %v, read %v; want %s", draft.Warnings, getInvoice(t, h, draft.ID).Warnings, warning)
	}
	save := func(inv invoiceJSON, yours, order string) invoiceJSON {
		t.Helper()
		b := draftBody(inv.CustomerID, line("Konsulenttime", 1, 100, vat25))
		b["revision"], b["paymentTermsDays"], b["yourReference"], b["orderReference"] = inv.Revision, 30, yours, order
		res := creator(t, h).Do(http.MethodPut, invoicePath(inv.ID), b)
		if res.Status != http.StatusOK {
			t.Fatalf("PUT %d = %d %s", inv.ID, res.Status, res.Body)
		}
		var out invoiceJSON
		res.JSON(&out)
		return out
	}
	saved := save(draft, "", "")
	if !warns(saved) {
		t.Errorf("a save with neither reference: warnings %v, want %s", saved.Warnings, warning)
	}
	if saved = save(saved, "", "ORDRE-9"); warns(saved) {
		t.Errorf("a save with an order reference: warnings %v, want no %s", saved.Warnings, warning)
	}
	if saved = save(saved, "PO-1", ""); warns(saved) {
		t.Errorf("a save with a buyer reference: warnings %v, want no %s", saved.Warnings, warning)
	}

	h.customers.edit(customerPerson, func(p *contracts.CustomerBillingProfile) { p.InvoiceDelivery = "ehf" })
	if person := createDraft(t, h, draftBody(customerPerson, line("Consulting", 1, 100, vat25))); !warns(person) {
		t.Errorf("a draft for a customer preferring EHF with no reference: warnings %v, want %s", person.Warnings, warning)
	}
	if plain := createDraft(t, h, draftBody(customerNoTerms, line("Arbeid", 1, 100, vat25))); warns(plain) {
		t.Errorf("a draft for a customer EHF is not for: warnings %v, want no %s", plain.Warnings, warning)
	}

	original := issued(t, h, createDraft(t, h, body).ID)
	if credit := creditDraft(t, h, original.ID); !warns(credit) {
		t.Errorf("a credit-note draft whose snapshot has a Peppol id and no reference: warnings %v, want %s", credit.Warnings, warning)
	}
	if warns(original) {
		t.Errorf("an issued document carries the draft warning: %v", original.Warnings)
	}
}

// When the caller can send as EHF, the e-mail dialog's warning for a
// customer preferring EHF is ehf_preferred — send it as EHF instead — in
// place of delivery_preference_ehf (D10, reading 14); without EHF it stays.
func TestSend_EhfPreferredReplacesThePreferenceWarning(t *testing.T) {
	t.Parallel()
	h, _ := ehfReady(t, modtest.WithEnv("MAIL_DRIVER", "smtp"),
		modtest.WithEnv("SMTP_HOST", "smtp.example.invalid"), modtest.WithEnv("SMTP_FROM", "faktura@example.invalid"))
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) {
		p.InvoiceDelivery, p.InvoiceEmail = "ehf", "faktura@acme.example"
	})
	inv := issuedAcme(t, h)
	if d := readAs(t, issuer(t, h), inv.ID).SendDefaults; d == nil || !slices.Equal(d.Warnings, []string{"ehf_preferred", "buyer_norwegian_business"}) {
		t.Errorf("with EHF available: sendDefaults %+v, want ehf_preferred in place of delivery_preference_ehf", d)
	}
	h.Exec(t, `DELETE FROM invoices.access_point_credentials`)
	if d := readAs(t, issuer(t, h), inv.ID).SendDefaults; d == nil || !slices.Equal(d.Warnings, []string{"delivery_preference_ehf", "buyer_norwegian_business"}) {
		t.Errorf("without EHF: sendDefaults %+v, want delivery_preference_ehf", d)
	}
}

// An issued document answers its EHF state (D10): not_sent until a send,
// then the latest transmission's status and timestamps and every
// transmission; canSend with the first refusal a send would meet named in
// blockedBy, judged without the network; the provider's reference and the
// reason only for an issuer, the reason redacted; and, for an issuer's GET,
// the profile's preference and the customer's current Peppol id — on an
// installation with no SMTP too. A draft has none.
func TestDocument_TheEhfBlockAndBlockedBy(t *testing.T) {
	t.Parallel()
	h, fake := ehfReady(t)
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.InvoiceDelivery = "ehf" })
	inv := issuedAcme(t, h)
	issuerC, reader := issuer(t, h), h.SignIn(t, "invoices:access")

	got := readAs(t, issuerC, inv.ID).Ehf
	if got == nil || got.Status != "not_sent" || !got.CanSend || got.BlockedBy != nil || got.Transmissions == nil || len(got.Transmissions) != 0 {
		t.Fatalf("a sendable document's ehf = %+v, want not_sent, canSend and no transmissions", got)
	}
	if got.Preference == nil || *got.Preference != "ehf" || got.BuyerPeppolID == nil || *got.BuyerPeppolID != "0192:923609016" {
		t.Errorf("an issuer's GET with no SMTP: preference %v, buyerPeppolId %v; want ehf and 0192:923609016", got.Preference, got.BuyerPeppolID)
	}
	if r := readAs(t, reader, inv.ID).Ehf; r == nil || r.Preference != nil || r.BuyerPeppolID != nil {
		t.Errorf("a reader's ehf = %+v, want no preference and no buyerPeppolId", r)
	}
	if d := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))); d.Ehf != nil {
		t.Errorf("a draft's ehf = %+v, want none", d.Ehf)
	}
	if len(fake.asked()) != 0 {
		t.Errorf("reads made %d lookups, want none", len(fake.asked()))
	}

	sent := sentAsEhf(t, h, inv.ID)
	if sent.SendDefaults != nil || sent.Ehf.Preference != nil || sent.Ehf.QueuedAt == nil || sent.Ehf.CanSend ||
		sent.Ehf.BlockedBy == nil || *sent.Ehf.BlockedBy != "ehf_already_sent" {
		t.Errorf("the send's answer = sendDefaults %+v, ehf %+v; want no defaults, queued and blocked by ehf_already_sent", sent.SendDefaults, sent.Ehf)
	}
	tr := sent.Ehf.Transmissions[0]
	if tr.Provider != "storecove" || tr.ReceiverParticipant != "0192:923609016" || tr.IdempotencyKey == "" ||
		tr.UblURL != transmissionPath(inv.ID, tr.ID, "ubl") || tr.QueuedAt == "" {
		t.Errorf("transmission = %+v", tr)
	}
	h.Exec(t, `UPDATE invoices.transmissions SET status = 'failed', failed_at = $2, provider_ref = 'guid-77',
		last_error = 'Receiver 0192:923609016 refused it; ask ops@acme.example' WHERE id = $1`, tr.ID, h.Now())
	got = readAs(t, issuerC, inv.ID).Ehf
	want := "Receiver <participant> refused it; ask <e-mail>"
	if got.Status != "failed" || got.FailedAt == nil || got.ProviderRef == nil || *got.ProviderRef != "guid-77" ||
		got.Reason == nil || *got.Reason != want || got.Transmissions[0].Reason == nil || *got.Transmissions[0].Reason != want ||
		got.Transmissions[0].ProviderRef == nil || !got.CanSend {
		t.Errorf("an issuer's ehf after a failure = %+v, want failed with the reference and the redacted reason, sendable again", got)
	}
	if r := readAs(t, reader, inv.ID).Ehf; r.ProviderRef != nil || r.Reason != nil || r.Transmissions[0].ProviderRef != nil || r.Transmissions[0].Reason != nil {
		t.Errorf("a reader's ehf after a failure = %+v, want neither the reference nor the reason", r)
	}

	blocked := func(what string, h *harness, id int64, code string) {
		t.Helper()
		e := readAs(t, issuer(t, h), id).Ehf
		if e == nil || e.CanSend || e.BlockedBy == nil || *e.BlockedBy != code {
			t.Errorf("%s: ehf %+v, want blocked by %s", what, e, code)
		}
	}
	unavailable := newHarness(t)
	saveSeller(t, unavailable, completeSeller(1))
	blocked("an installation without EHF", unavailable, issuedAcme(t, unavailable).ID, "ehf_unavailable")

	noRef := draftBody(customerAcme, line("Konsulenttime", 1, 100, vat25))
	noRef["yourReference"] = ""
	blocked("no reference", h, issued(t, h, createDraft(t, h, noRef).ID).ID, "buyer_reference_missing")
	blocked("a K line", h, issued(t, h, createDraft(t, h, draftBody(customerAcme, line("EØS", 1, 100, kCode(t, h)))).ID).ID, "ehf_invalid")
	queued := issuedAcme(t, h)
	plantTransmissionOn(t, h, queued.ID, "unconfirmed")
	blocked("an unconfirmed transmission", h, queued.ID, "ehf_already_sent")
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.PeppolID = "" })
	blocked("no buyer Peppol id", h, issuedAcme(t, h).ID, "no_peppol_id")
	h.Exec(t, `INSERT INTO invoices.erased_customers (customer_id, erased_at) VALUES ($1, now())`, customerAcme)
	blocked("an anonymised customer", h, inv.ID, "customer_anonymised")
}

// The list answers each issued document's latest EHF status, not_sent
// before any, in one query; a draft has none (D10).
func TestList_EhfStatus(t *testing.T) {
	t.Parallel()
	h, _ := ehfReady(t)
	plain := issuedAcme(t, h)
	sent := issuedAcme(t, h)
	sentAsEhf(t, h, sent.ID)
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	status := map[int64]string{}
	for _, d := range list(t, h, "").Data {
		if d.EhfStatus == nil {
			status[d.ID] = "<absent>"
		} else {
			status[d.ID] = *d.EhfStatus
		}
	}
	want := map[int64]string{plain.ID: "not_sent", sent.ID: "queued", draft.ID: "<absent>"}
	for id, s := range want {
		if status[id] != s {
			t.Errorf("ehfStatus of %d = %q, want %q", id, status[id], s)
		}
	}
}

// insertTransmission queues a transmission of document id through the
// send's own query, outside the handler.
func insertTransmission(t *testing.T, h *harness, id int64) int64 {
	t.Helper()
	now := h.Now()
	var tid int64
	if err := h.Pool().QueryRow(context.Background(), `INSERT INTO invoices.transmissions (
		invoice_id, provider, idempotency_key, sender_participant, receiver_participant, document_type, process_id,
		ubl_object_key, ubl_sha256, pdf_sha256, next_attempt_at, lookup_registered, lookup_can_receive, lookup_at,
		queued_at, created_by_user_id) VALUES ($1, 'storecove', $2, '0192:974760673', '0192:923609016', 'invoice', 'billing',
		$3, $4, $4, $5, true, true, $5, $5, $6) RETURNING id`,
		id, uuid.New(), fmt.Sprintf("documents/%d/planted.xml", id), sha([]byte("planted")), now, uuid.New()).Scan(&tid); err != nil {
		t.Fatalf("insert a transmission of %d: %v", id, err)
	}
	return tid
}
