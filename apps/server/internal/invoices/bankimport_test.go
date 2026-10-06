package invoices_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile/bankfiletest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The bank import's tests (invoices payments and reminders design D3). The
// files are bankfiletest's, whose control totals are computed, and the
// parser's own OCR fixtures; no broken file is committed (plan reading 42).

const (
	bankFilesPath    = invoicesPath + "/bank-files"
	bankAccountsPath = invoicesPath + "/bank-accounts"

	// sellerAccount is completeSeller's bank account; olderAccount the one
	// the seller had before (bankHarness), which an issued invoice printed —
	// and the account of bankfile's OCR fixtures.
	sellerAccount = "86011117947"
	olderAccount  = "12345678903"
)

// bankDay is the day the bank tests import on: the clock moved from
// modtest.Start (2026-09-12) to 2026-10-07 at noon in Oslo, after every
// booking day of the fixtures.
var bankDay = time.Date(2026, time.October, 7, 10, 0, 0, 0, time.UTC)

// bankHarness is an installation whose seller banks with sellerAccount and
// once banked with olderAccount — an invoice issued then printed it, so a
// file on either is the seller's — on the clock of bankDay.
func bankHarness(t *testing.T, opts ...modtest.Option) *harness {
	t.Helper()
	h := newHarness(t, opts...)
	toBankDay(t, h)
	return h
}

// toBankDay gives h's seller its two accounts, as bankHarness does, and moves
// the clock to bankDay.
func toBankDay(t *testing.T, h *harness) {
	t.Helper()
	older := completeSeller(1)
	older["bankAccount"], older["iban"], older["bic"] = olderAccount, "", ""
	saved := saveSeller(t, h, older)
	thousand(t, h)
	saveSeller(t, h, completeSeller(saved.Revision))
	h.Advance(bankDay.Sub(h.Now()))
}

// importer is a caller who may import bank files.
func importer(t *testing.T, h *harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t, "invoices:access", "invoices:payments")
}

// fileBody is data as the one multipart part named field.
func fileBody(t *testing.T, field string, data ...[]byte) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, d := range data {
		part, err := w.CreateFormFile(field, "bank.dat")
		if err != nil {
			t.Fatalf("create the %s part: %v", field, err)
		}
		if _, err := part.Write(d); err != nil {
			t.Fatalf("write the %s part: %v", field, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close the multipart writer: %v", err)
	}
	return w.FormDataContentType(), buf.Bytes()
}

// upload POSTs data as the import's file part.
func upload(t *testing.T, c *modtest.Client, data []byte, opts ...modtest.RequestOption) *modtest.Response {
	t.Helper()
	contentType, body := fileBody(t, "file", data)
	return c.Do(http.MethodPost, bankFilesPath, nil, append([]modtest.RequestOption{modtest.RawBody(contentType, body)}, opts...)...)
}

// bankFileJSON is an imported file as a client reads it.
type bankFileJSON struct {
	ID            int64          `json:"id"`
	Format        string         `json:"format"`
	Sha256        string         `json:"sha256"`
	Accounts      []string       `json:"accounts"`
	FirstBookedOn *string        `json:"firstBookedOn"`
	LastBookedOn  *string        `json:"lastBookedOn"`
	Transactions  int            `json:"transactions"`
	Duplicates    int            `json:"duplicates"`
	Ignored       int            `json:"ignored"`
	IgnoredKinds  map[string]int `json:"ignoredKinds"`
	UploadedBy    uuid.UUID      `json:"uploadedBy"`
	UploadedAt    time.Time      `json:"uploadedAt"`
	Pending       int            `json:"pending"`
	Exceptions    int            `json:"exceptions"`
	Matched       int            `json:"matched"`
}

// importJSON is the 201.
type importJSON struct {
	File             bankFileJSON   `json:"file"`
	Transactions     int            `json:"transactions"`
	Matched          int            `json:"matched"`
	MatchedAmount    float64        `json:"matchedAmount"`
	Exceptions       int            `json:"exceptions"`
	ExceptionsAmount float64        `json:"exceptionsAmount"`
	Duplicates       int            `json:"duplicates"`
	Ignored          map[string]int `json:"ignored"`
	Pending          int            `json:"pending"`
}

// bankLineJSON is one line of a file as a client reads it.
type bankLineJSON struct {
	ID             int64   `json:"id"`
	LineRef        string  `json:"lineRef"`
	Account        string  `json:"account"`
	Direction      string  `json:"direction"`
	Negative       bool    `json:"negative"`
	BookedOn       string  `json:"bookedOn"`
	ValueOn        *string `json:"valueOn"`
	OrderedOn      *string `json:"orderedOn"`
	Amount         float64 `json:"amount"`
	Kid            *string `json:"kid"`
	RemittanceText string  `json:"remittanceText"`
	DebtorName     string  `json:"debtorName"`
	DebtorAccount  string  `json:"debtorAccount"`
	ArchiveRef     string  `json:"archiveRef"`
	Status         string  `json:"status"`
	Reason         *string `json:"reason"`
	DuplicateOfID  *int64  `json:"duplicateOfId"`
}

// bankFileDetailJSON is GET /bank-files/{id}.
type bankFileDetailJSON struct {
	File         bankFileJSON   `json:"file"`
	Transactions []bankLineJSON `json:"transactions"`
}

// bankAccountJSON is one account of GET /bank-accounts.
type bankAccountJSON struct {
	Account        string     `json:"account"`
	Format         string     `json:"format"`
	PreviousFormat *string    `json:"previousFormat"`
	CutoverThrough *string    `json:"cutoverThrough"`
	SetBy          uuid.UUID  `json:"setBy"`
	SetAt          time.Time  `json:"setAt"`
	LastFileID     *int64     `json:"lastFileId"`
	LastUploadedAt *time.Time `json:"lastUploadedAt"`
	LastBookedOn   *string    `json:"lastBookedOn"`
}

// duplicateJSON is bank_file_duplicate's 409.
type duplicateJSON struct {
	Code       string     `json:"code"`
	Detail     string     `json:"detail"`
	BankFileID *int64     `json:"bankFileId"`
	UploadedAt *time.Time `json:"uploadedAt"`
	UploadedBy *uuid.UUID `json:"uploadedBy"`
}

// imported uploads data and answers the 201, failing on anything else.
func imported(t *testing.T, c *modtest.Client, data []byte) importJSON {
	t.Helper()
	res := upload(t, c, data)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST /bank-files = %d %s, want 201", res.Status, res.Body)
	}
	var r importJSON
	res.JSON(&r)
	return r
}

// refusedImport asserts res is a 409 with code and answers its body.
func refusedImport(t *testing.T, what string, res *modtest.Response, code string) duplicateJSON {
	t.Helper()
	var p duplicateJSON
	if res.Status != http.StatusConflict {
		t.Errorf("%s = %d %s, want 409 %s", what, res.Status, res.Body, code)
		return p
	}
	res.JSON(&p)
	if p.Code != code {
		t.Errorf("%s = %s (%s), want %s", what, p.Code, p.Detail, code)
	}
	return p
}

// fileRefusalOf asserts res is the 400 on 'file' and answers its message.
func fileRefusalOf(t *testing.T, what string, res *modtest.Response) string {
	t.Helper()
	if res.Status != http.StatusBadRequest {
		t.Errorf("%s = %d %s, want 400 on file", what, res.Status, res.Body)
		return ""
	}
	p := problemOf(t, res)
	if len(p.Errors["file"]) != 1 {
		t.Errorf("%s = %+v, want one message on file", what, p.Errors)
		return ""
	}
	return p.Errors["file"][0]
}

// bankDetail reads one file with its lines.
func bankDetail(t *testing.T, h *harness, id int64) bankFileDetailJSON {
	t.Helper()
	res := importer(t, h).Do(http.MethodGet, fmt.Sprintf("%s/%d", bankFilesPath, id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /bank-files/%d = %d %s, want 200", id, res.Status, res.Body)
	}
	var d bankFileDetailJSON
	res.JSON(&d)
	return d
}

// bankAccounts reads GET /bank-accounts.
func bankAccounts(t *testing.T, h *harness) []bankAccountJSON {
	t.Helper()
	res := importer(t, h).Do(http.MethodGet, bankAccountsPath, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /bank-accounts = %d %s, want 200", res.Status, res.Body)
	}
	var body struct {
		Data []bankAccountJSON `json:"data"`
	}
	res.JSON(&body)
	return body.Data
}

// written is how many bank files, lines and accounts the installation holds.
func written(t *testing.T, h *harness) [3]int {
	t.Helper()
	return [3]int{
		h.Count(t, `SELECT count(*) FROM invoices.bank_files`),
		h.Count(t, `SELECT count(*) FROM invoices.bank_transactions`),
		h.Count(t, `SELECT count(*) FROM invoices.bank_import_accounts`),
	}
}

// oct is a day of October 2026, as the files book it.
func oct(day int) time.Time { return time.Date(2026, time.October, day, 0, 0, 0, 0, time.UTC) }

// giro is an OCR payment of type 10 (a giro with KID) settled on day.
func giro(account string, day int, amountMinor int64, kid, archiveRef string) bankfiletest.OCRPayment {
	return bankfiletest.OCRPayment{Type: 10, Account: account, Settled: oct(day), Ordered: oct(day), AmountMinor: amountMinor, KID: kid, ArchiveRef: archiveRef}
}

// camtOn is a camt.054.001.02 notification on account of one booked entry
// per tx, each of one transaction.
func camtOn(msgID, account string, day int, txs ...bankfiletest.CamtTx) []byte {
	entries := make([]bankfiletest.CamtEntry, 0, len(txs))
	for _, tx := range txs {
		entries = append(entries, bankfiletest.CamtEntry{BookedOn: oct(day), Txs: []bankfiletest.CamtTx{tx}})
	}
	return bankfiletest.Camt054("camt.054.001.02", msgID, oct(day).Add(18*time.Hour), account, entries...)
}

// ocrFixture is one of the parser's own OCR fixtures, on olderAccount.
func ocrFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("bankfile", "testdata", "ocr", name))
	if err != nil {
		t.Fatalf("read the fixture %s: %v", name, err)
	}
	return b
}

// parsedOn is what bankfile makes of b on bankDay — what the import must
// have stored.
func parsedOn(t *testing.T, b []byte) *bankfile.File {
	t.Helper()
	f, err := bankfile.Parse(b, oct(7))
	if err != nil {
		t.Fatalf("bankfile.Parse: %v", err)
	}
	return f
}

// bankKeys is every bank file the object store holds.
func bankKeys(h *harness) []string {
	keys, _, _ := h.objects.stored()
	var out []string
	for _, k := range keys {
		if strings.HasPrefix(k, "bank-files/") {
			out = append(out, k)
		}
	}
	return out
}

// bankObjects is how many bank files the object store holds.
func bankObjects(h *harness) int { return len(bankKeys(h)) }

func shaOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestBankFile_Upload400s: every way the upload itself is refused (D3 steps
// 1–3) is a 400 on 'file', and nothing is stored or written — no part, two,
// an empty one, one past 10 MiB, a body past the router's cap, a file that is
// neither format, and each parser refusal, whose message names the record or
// the element where it failed.
func TestBankFile_Upload400s(t *testing.T) {
	t.Parallel()
	h := bankHarness(t)
	c := importer(t, h)
	good := bankfiletest.OCR("1", giro(sellerAccount, 6, 125000, "0010017", "1"))

	contentType, body := fileBody(t, "upload", good)
	if msg := fileRefusalOf(t, "a part named upload", c.Do(http.MethodPost, bankFilesPath, nil, modtest.RawBody(contentType, body))); !strings.Contains(msg, "the multipart part named 'file'") {
		t.Errorf("a part named upload: %q", msg)
	}
	contentType, body = fileBody(t, "file", good, good)
	if msg := fileRefusalOf(t, "two parts", c.Do(http.MethodPost, bankFilesPath, nil, modtest.RawBody(contentType, body))); !strings.Contains(msg, "at most 10 MiB") {
		t.Errorf("two parts: %q", msg)
	}
	if msg := fileRefusalOf(t, "an empty part", upload(t, c, []byte{})); !strings.Contains(msg, "at most 10 MiB") {
		t.Errorf("an empty part: %q", msg)
	}
	tooBig := append(append([]byte{}, good...), bytes.Repeat([]byte(" "), bankfile.MaxBytes+1-len(good))...)
	if msg := fileRefusalOf(t, "10 MiB + 1", upload(t, c, tooBig)); !strings.Contains(msg, "at most 10 MiB") {
		t.Errorf("10 MiB + 1: %q", msg)
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	padding, _ := w.CreateFormField("padding")
	_, _ = padding.Write(bytes.Repeat([]byte("x"), bankfile.MaxBytes+128<<10))
	part, _ := w.CreateFormFile("file", "bank.ocr")
	_, _ = part.Write(good)
	_ = w.Close()
	if msg := fileRefusalOf(t, "a body past the cap", c.Do(http.MethodPost, bankFilesPath, nil, modtest.RawBody(w.FormDataContentType(), buf.Bytes()))); !strings.Contains(msg, "at most 10 MiB") {
		t.Errorf("a body past the cap: %q", msg)
	}
	if msg := fileRefusalOf(t, "not a bank file", upload(t, c, []byte("Dato;Beløp\n06.10.2026;1250,00\n"))); msg != "file: Not an OCR giro or camt.054 file" {
		t.Errorf("not a bank file: %q", msg)
	}
	res := c.Do(http.MethodPost, bankFilesPath, nil, modtest.RawBody("text/plain", good),
		modtest.SkipContract("a body that is not multipart is off-contract by construction"))
	if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["file"]) != 0 {
		t.Errorf("a body that is not multipart = %d %s, want the bare 400 of the decoder", res.Status, res.Body)
	}

	// Each parser refusal is the 400, its words bankfile's own — the place,
	// then what is wrong there.
	sumBroken := bytes.Replace(good, []byte("00000000000125000"), []byte("00000000000125001"), 1)
	future := bankfiletest.OCR("2", giro(sellerAccount, 8, 125000, "0010017", "1"))
	usd := bytes.ReplaceAll(camtOn("USD-1", sellerAccount, 6, bankfiletest.CamtTx{AmountMinor: 125000, KID: "0010017"}),
		[]byte(`<Amt Ccy="NOK">1250.00</Amt>`), []byte(`<Amt Ccy="USD">1250.00</Amt>`))
	for _, c2 := range []struct {
		name  string
		file  []byte
		where string
	}{
		{"a control sum that does not add up", sumBroken, "record "},
		{"a settlement after today", future, "record "},
		{"an amount in USD", usd, "Ntry"},
	} {
		_, parseErr := bankfile.Parse(c2.file, oct(7))
		var refusal *bankfile.Error
		if !errors.As(parseErr, &refusal) || !strings.HasPrefix(refusal.Where, c2.where) {
			t.Fatalf("%s: bankfile.Parse = %v, want a refusal at %s…", c2.name, parseErr, c2.where)
		}
		if msg := fileRefusalOf(t, c2.name, upload(t, c, c2.file)); msg != refusal.Where+": "+refusal.Message {
			t.Errorf("%s: file = %q, want bankfile's %q", c2.name, msg, refusal.Error())
		}
	}

	if got := written(t, h); got != [3]int{} {
		t.Errorf("files, lines, accounts = %v after refused uploads, want none", got)
	}
	if n := bankObjects(h); n != 0 {
		t.Errorf("%d bank files stored by refused uploads, want none", n)
	}
}

// TestBankFile_AccountUnknown: every account a file names must be the
// seller's or one an issued invoice printed (D3 step 4, case m) — otherwise a
// 409 naming its last four digits, before anything is stored. An older
// account a snapshot holds is accepted, and so is the seller's account
// written as its Norwegian IBAN.
func TestBankFile_AccountUnknown(t *testing.T) {
	t.Parallel()
	h := bankHarness(t)
	c := importer(t, h)

	stranger := "15032080119"
	res := upload(t, c, bankfiletest.OCR("1", giro(sellerAccount, 6, 125000, "0010017", "1"), giro(stranger, 6, 5000, "0010025", "2")))
	if p := refusedImport(t, "a file naming a stranger's account", res, "bank_account_unknown"); !strings.Contains(p.Detail, "0119") || strings.Contains(p.Detail, stranger) {
		t.Errorf("the detail = %q, want the account's last four digits alone", p.Detail)
	}
	if got := written(t, h); got != [3]int{} {
		t.Errorf("files, lines, accounts = %v after the refusal, want none", got)
	}
	if n := bankObjects(h); n != 0 {
		t.Errorf("%d bank files stored by the refusal, want none", n)
	}

	if r := imported(t, c, bankfiletest.OCR("2", giro(olderAccount, 6, 125000, "0010017", "1"))); r.File.Accounts[0] != olderAccount {
		t.Errorf("the older account's file = %+v", r.File)
	}
	if r := imported(t, c, camtOn("IBAN-1", "NO9386011117947", 6, bankfiletest.CamtTx{AmountMinor: 49950, KID: "0010025"})); !slices.Equal(r.File.Accounts, []string{sellerAccount}) {
		t.Errorf("the IBAN's file names %v, want the seller's BBAN", r.File.Accounts)
	}
}

// TestBankFile_Duplicate: the same bytes again, or the same file identity
// with other bytes, is 409 bank_file_duplicate naming the first import — its
// id, time and uploader — read on the pool before anything is stored, and
// nothing is written.
func TestBankFile_Duplicate(t *testing.T) {
	t.Parallel()
	h := bankHarness(t)
	first, firstUser := h.SignInUser(t, "invoices:access", "invoices:payments")
	file := bankfiletest.OCR("17", giro(sellerAccount, 6, 125000, "0010017", "1"))
	original := imported(t, first, file)
	before := written(t, h)
	_, putsBefore, _ := h.objects.stored()
	h.Advance(time.Hour)

	sameIdentity := bankfiletest.OCR("17", giro(sellerAccount, 6, 49950, "0010025", "2"))
	if shaOf(sameIdentity) == shaOf(file) {
		t.Fatal("the other bytes hash alike")
	}
	for _, c := range []struct {
		name string
		file []byte
	}{{"the same bytes", file}, {"the same identity in other bytes", sameIdentity}} {
		p := refusedImport(t, c.name, upload(t, importer(t, h), c.file), "bank_file_duplicate")
		if p.BankFileID == nil || *p.BankFileID != original.File.ID || p.UploadedAt == nil || !p.UploadedAt.Equal(original.File.UploadedAt) ||
			p.UploadedBy == nil || *p.UploadedBy != firstUser {
			t.Errorf("%s: bankFileId %v uploadedAt %v uploadedBy %v, want %d, %v, %v", c.name, p.BankFileID, p.UploadedAt, p.UploadedBy,
				original.File.ID, original.File.UploadedAt, firstUser)
		}
	}
	if got := written(t, h); got != before {
		t.Errorf("files, lines, accounts = %v after the duplicates, want %v", got, before)
	}
	if _, puts, _ := h.objects.stored(); puts != putsBefore {
		t.Errorf("objects stored = %d after the duplicates, want %d: a duplicate stores nothing", puts, putsBefore)
	}

	camt := camtOn("MSG-1", olderAccount, 6, bankfiletest.CamtTx{AmountMinor: 30000, KID: "0010033"})
	imported(t, importer(t, h), camt)
	again := camtOn("MSG-1", olderAccount, 6, bankfiletest.CamtTx{AmountMinor: 30000, KID: "0010033", AcctSvcrRef: "other"})
	refusedImport(t, "a camt.054 of the same MsgId and CreDtTm", upload(t, importer(t, h), again), "bank_file_duplicate")
}

// TestBankFile_FirstImportSetsAccountFormat: an account's first import sets
// its format, by the uploader at the import's time (D3 step 7.1); the list
// shows it with its latest file and the latest booking day of its lines.
func TestBankFile_FirstImportSetsAccountFormat(t *testing.T) {
	t.Parallel()
	h := bankHarness(t)
	c, user := h.SignInUser(t, "invoices:access", "invoices:payments")
	if got := bankAccounts(t, h); len(got) != 0 {
		t.Fatalf("accounts before any import = %+v, want none", got)
	}
	ocr := imported(t, c, bankfiletest.OCR("1", giro(olderAccount, 3, 125000, "0010017", "1"), giro(olderAccount, 5, 5000, "0010025", "2")))
	h.Advance(time.Hour)
	camt := imported(t, c, camtOn("FIRST-1", sellerAccount, 6, bankfiletest.CamtTx{AmountMinor: 49950, KID: "0010033"}))

	got := bankAccounts(t, h)
	if len(got) != 2 {
		t.Fatalf("accounts = %+v, want two", got)
	}
	for i, want := range []struct {
		account, format, lastBooked string
		file                        importJSON
	}{{olderAccount, "ocr", "2026-10-05", ocr}, {sellerAccount, "camt054", "2026-10-06", camt}} {
		a := got[i]
		if a.Account != want.account || a.Format != want.format || a.PreviousFormat != nil || a.CutoverThrough != nil ||
			a.SetBy != user || !a.SetAt.Equal(want.file.File.UploadedAt) {
			t.Errorf("account %d = %+v, want %s in %s set by %v at %v", i, a, want.account, want.format, user, want.file.File.UploadedAt)
		}
		if a.LastFileID == nil || *a.LastFileID != want.file.File.ID || a.LastUploadedAt == nil || !a.LastUploadedAt.Equal(want.file.File.UploadedAt) ||
			a.LastBookedOn == nil || *a.LastBookedOn != want.lastBooked {
			t.Errorf("account %s's latest = file %v at %v booked %v, want %d, %v, %s", a.Account, a.LastFileID, a.LastUploadedAt, a.LastBookedOn,
				want.file.File.ID, want.file.File.UploadedAt, want.lastBooked)
		}
	}
	if res := h.SignIn(t, "invoices:access").Do(http.MethodGet, bankAccountsPath, nil); res.Status != http.StatusForbidden {
		t.Errorf("GET /bank-accounts with invoices:access alone = %d, want 403", res.Status)
	}
}

// TestBankFile_FormatMismatch: a file of the other format than an account's
// is refused, naming the account and its format (D3 step 7.1); the refusal
// rolls back the whole import — an account the refused file named for the
// first time gets no row.
func TestBankFile_FormatMismatch(t *testing.T) {
	t.Parallel()
	h := bankHarness(t)
	c := importer(t, h)
	imported(t, c, camtOn("OLD-1", olderAccount, 5, bankfiletest.CamtTx{AmountMinor: 125000, KID: "0010017"}))
	before := written(t, h)

	both := bankfiletest.OCR("1", giro(sellerAccount, 6, 49950, "0010025", "1"), giro(olderAccount, 6, 5000, "0010033", "2"))
	p := refusedImport(t, "an OCR file naming a camt.054 account", upload(t, c, both), "bank_import_format_mismatch")
	if !strings.Contains(p.Detail, olderAccount) || !strings.Contains(p.Detail, "camt054") {
		t.Errorf("the detail = %q, want the account and its format", p.Detail)
	}
	if got := written(t, h); got != before {
		t.Errorf("files, lines, accounts = %v after the refusal, want %v: the seller's account was named for the first time and must get no row", got, before)
	}

	imported(t, c, bankfiletest.OCR("2", giro(sellerAccount, 6, 49950, "0010025", "1")))
	p = refusedImport(t, "a camt.054 file on an OCR account", upload(t, c, camtOn("NEW-1", sellerAccount, 6, bankfiletest.CamtTx{AmountMinor: 700, KID: "0010041"})),
		"bank_import_format_mismatch")
	if !strings.Contains(p.Detail, sellerAccount) || !strings.Contains(p.Detail, "as ocr") {
		t.Errorf("the detail = %q, want the account and ocr", p.Detail)
	}
}

// TestBankFile_FormatChangeSetsCutover: a manager's change of an account's
// format keeps the old one and the latest booking day of the account's own
// lines in it (D3, m14) — a file naming two accounts whose other account
// booked later does not push it; the same format again changes nothing; an
// account never imported is 404; invoices:payments alone may not.
func TestBankFile_FormatChangeSetsCutover(t *testing.T) {
	t.Parallel()
	h := bankHarness(t)
	c := importer(t, h)
	imported(t, c, bankfiletest.OCR("1", giro(sellerAccount, 1, 125000, "0010017", "1")))
	imported(t, c, bankfiletest.OCR("2", giro(sellerAccount, 3, 5000, "0010025", "2"), giro(olderAccount, 5, 49950, "0010033", "3")))
	h.Advance(time.Hour)
	manager, managerID := h.SignInUser(t, "invoices:access", "invoices:manage")
	path := bankAccountsPath + "/" + sellerAccount + "/format"

	res := manager.Do(http.MethodPut, path, map[string]any{"format": "camt054"})
	if res.Status != http.StatusOK {
		t.Fatalf("PUT format camt054 = %d %s, want 200", res.Status, res.Body)
	}
	var changed bankAccountJSON
	res.JSON(&changed)
	if changed.Format != "camt054" || changed.PreviousFormat == nil || *changed.PreviousFormat != "ocr" ||
		changed.CutoverThrough == nil || *changed.CutoverThrough != "2026-10-03" || changed.SetBy != managerID || !changed.SetAt.Equal(h.Now()) {
		t.Errorf("the change = %+v, want camt054 after ocr through 2026-10-03 (the account's own last booking, not the file's 10-05), by %v now", changed, managerID)
	}

	h.Advance(time.Hour)
	res = manager.Do(http.MethodPut, path, map[string]any{"format": "camt054"})
	var same bankAccountJSON
	res.JSON(&same)
	if res.Status != http.StatusOK || same.Format != "camt054" || !same.SetAt.Equal(changed.SetAt) || same.CutoverThrough == nil || *same.CutoverThrough != "2026-10-03" {
		t.Errorf("the same format again = %d %+v, want 200 and nothing changed", res.Status, same)
	}
	imported(t, c, camtOn("AFTER-1", sellerAccount, 6, bankfiletest.CamtTx{AmountMinor: 700, KID: "0010041"}))

	if res := manager.Do(http.MethodPut, bankAccountsPath+"/15032080119/format", map[string]any{"format": "ocr"}); res.Status != http.StatusNotFound {
		t.Errorf("PUT on an account never imported = %d %s, want 404", res.Status, res.Body)
	}
	if res := manager.Do(http.MethodPut, path, map[string]any{"format": "csv"}); res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["format"]) != 1 {
		t.Errorf("PUT format csv = %d %s, want 400 on format", res.Status, res.Body)
	}
	if res := c.Do(http.MethodPut, path, map[string]any{"format": "ocr"}); res.Status != http.StatusForbidden {
		t.Errorf("PUT with invoices:payments alone = %d, want 403", res.Status)
	}
}

// TestBankFile_Fingerprint: lines are recognised by their fingerprint (D3):
// two identical payments in one file are both kept — and both matched, here
// queued, since no invoice of this installation carries a KID; the same file again
// under a new transmission number makes every line a duplicate row naming
// its original; a file overlapping another makes the shared lines duplicates
// and keeps the rest live.
func TestBankFile_Fingerprint(t *testing.T) {
	t.Parallel()
	h := bankHarness(t)
	c := importer(t, h)

	identical := ocrFixture(t, "identical-lines.ocr")
	first := imported(t, c, identical)
	if first.Transactions != 2 || first.Duplicates != 0 || first.Exceptions != 2 || first.Pending != 0 {
		t.Errorf("identical-lines = %+v, want two lines, both live and queued", first)
	}
	originals := bankDetail(t, h, first.File.ID).Transactions
	for _, l := range originals {
		if l.Status != "exception" || l.DuplicateOfID != nil {
			t.Errorf("line %s = %s duplicate of %v, want a live line, queued", l.LineRef, l.Status, l.DuplicateOfID)
		}
	}

	renumbered := bytes.Replace(identical, []byte("NY000010000080800000128"), []byte("NY000010000080800000131"), 1)
	again := imported(t, c, renumbered)
	if again.Transactions != 2 || again.Duplicates != 2 || again.Pending != 0 || again.File.Duplicates != 2 {
		t.Errorf("the same lines under transmission 131 = %+v, want both duplicates", again)
	}
	byRef := map[string]int64{}
	for _, l := range originals {
		byRef[l.LineRef] = l.ID
	}
	for _, l := range bankDetail(t, h, again.File.ID).Transactions {
		if l.Status != "duplicate" || l.DuplicateOfID == nil || *l.DuplicateOfID != byRef[l.LineRef] {
			t.Errorf("line %s = %s duplicate of %v, want a duplicate of %d", l.LineRef, l.Status, l.DuplicateOfID, byRef[l.LineRef])
		}
	}

	// A third copy repeats the original, never one of its duplicates: only a
	// live line is ever named.
	third := imported(t, c, bytes.Replace(identical, []byte("NY000010000080800000128"), []byte("NY000010000080800000132"), 1))
	if third.Duplicates != 2 {
		t.Errorf("the same lines under transmission 132 = %+v, want both duplicates", third)
	}
	for _, l := range bankDetail(t, h, third.File.ID).Transactions {
		if l.Status != "duplicate" || l.DuplicateOfID == nil || *l.DuplicateOfID != byRef[l.LineRef] {
			t.Errorf("the third copy's line %s = %s duplicate of %v, want a duplicate of the original %d", l.LineRef, l.Status, l.DuplicateOfID, byRef[l.LineRef])
		}
	}

	a := imported(t, c, ocrFixture(t, "overlap-a.ocr"))
	b := imported(t, c, ocrFixture(t, "overlap-b.ocr"))
	if a.Duplicates != 0 || b.Transactions != 3 || b.Duplicates != 2 || b.Exceptions != 1 || b.Pending != 0 {
		t.Errorf("overlap-a = %+v, overlap-b = %+v, want b's two shared lines duplicates and its third live", a, b)
	}
	aLines := map[string]int64{}
	for _, l := range bankDetail(t, h, a.File.ID).Transactions {
		aLines[l.LineRef] = l.ID
	}
	var live, dup int
	for _, l := range bankDetail(t, h, b.File.ID).Transactions {
		switch {
		case l.Status == "duplicate" && l.DuplicateOfID != nil && *l.DuplicateOfID == aLines[l.LineRef]:
			dup++
		case l.Status == "exception" && l.DuplicateOfID == nil:
			live++
		default:
			t.Errorf("overlap-b's line %s = %s duplicate of %v", l.LineRef, l.Status, l.DuplicateOfID)
		}
	}
	if live != 1 || dup != 2 {
		t.Errorf("overlap-b = %d live, %d duplicates, want 1 and 2", live, dup)
	}
}

// TestBankFile_StoredOnce: a file's bytes are stored once, keyed by their
// hash (D3 step 6) — Exists before Put, so an object a request stored and
// then failed after is reused, never put again; such a request leaves
// nothing else behind, the account row included.
func TestBankFile_StoredOnce(t *testing.T) {
	h := bankHarness(t)
	c := importer(t, h)
	file := bankfiletest.OCR("5", giro(sellerAccount, 6, 125000, "0010017", "1"))
	key := "bank-files/" + shaOf(file) + ".ocr"

	_, putsAtStart, _ := h.objects.stored()
	failed := errors.New("the import fails after its inserts")
	restore := invoices.SetBankImportAfterInsert(func(context.Context, int64) error { return failed })
	res := upload(t, c, file, modtest.SkipContract("an import failing after its inserts is an infrastructure 500, deliberately off-contract"))
	restore()
	if res.Status != http.StatusInternalServerError {
		t.Fatalf("the failing import = %d %s, want 500", res.Status, res.Body)
	}
	_, putsBefore, _ := h.objects.stored()
	if keys := bankKeys(h); !slices.Equal(keys, []string{key}) || putsBefore != putsAtStart+1 {
		t.Fatalf("the bank files stored by the failed import = %v in %d puts, want %s put once", keys, putsBefore-putsAtStart, key)
	}
	if got := written(t, h); got != [3]int{} {
		t.Errorf("files, lines, accounts = %v after the failed import, want none: the account's format is written in the import's transaction", got)
	}

	r := imported(t, c, file)
	if _, puts, _ := h.objects.stored(); !slices.Equal(bankKeys(h), []string{key}) || puts != putsBefore || !bytes.Equal(h.objects.object(key), file) {
		t.Errorf("the bank files = %v after %d more puts, want %s found by Exists, not put again, and still the file's bytes", bankKeys(h), puts-putsBefore, key)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT object_key FROM invoices.bank_files WHERE id = $1`, r.File.ID); got != key || r.File.Sha256 != shaOf(file) {
		t.Errorf("the file's key %q and hash %q, want %s", got, r.File.Sha256, key)
	}

	camt := camtOn("STORE-1", olderAccount, 6, bankfiletest.CamtTx{AmountMinor: 500, KID: "0010025"})
	imported(t, c, camt)
	if _, puts, _ := h.objects.stored(); !slices.Contains(bankKeys(h), "bank-files/"+shaOf(camt)+".xml") || puts != putsBefore+1 {
		t.Errorf("the bank files = %v after %d more puts, want the camt.054 under .xml, put once", bankKeys(h), puts-putsBefore)
	}
}

// TestBankFile_StorageUnavailable: without an object store, or with one
// that fails, an import is 503 storage_unavailable and writes nothing — the
// file is the documentation of the payments booked from it (D3 step 6).
func TestBankFile_StorageUnavailable(t *testing.T) {
	t.Parallel()
	file := bankfiletest.OCR("5", giro(sellerAccount, 6, 125000, "0010017", "1"))

	none := newHarnessWithoutStore(t)
	saveSeller(t, none, completeSeller(1))
	none.Advance(bankDay.Sub(none.Now()))
	if res := upload(t, importer(t, none), file); res.Status != http.StatusServiceUnavailable || problemOf(t, res).Code != "storage_unavailable" {
		t.Errorf("an import without a store = %d %s, want 503 storage_unavailable", res.Status, res.Body)
	}
	if got := written(t, none); got != [3]int{} {
		t.Errorf("files, lines, accounts = %v without a store, want none", got)
	}

	h := bankHarness(t)
	h.objects.failPuts(errors.New("the bucket is gone"))
	if res := upload(t, importer(t, h), file); res.Status != http.StatusServiceUnavailable || problemOf(t, res).Code != "storage_unavailable" {
		t.Errorf("an import whose Put fails = %d %s, want 503 storage_unavailable", res.Status, res.Body)
	}
	if got := written(t, h); got != [3]int{} {
		t.Errorf("files, lines, accounts = %v after a failed Put, want none", got)
	}
}

// TestBankFile_IgnoredByKind: what a file holds that is not a transaction is
// counted by kind (D3) — a camt.054 debit that is not a reversal, an entry
// not booked, a 0.00 transaction, OCR card information — in the 201 and on
// the file's row.
func TestBankFile_IgnoredByKind(t *testing.T) {
	t.Parallel()
	h := bankHarness(t)
	c := importer(t, h)
	kid := bankfiletest.CamtTx{AmountMinor: 125000, KID: "0010017"}
	camt := bankfiletest.Camt054("camt.054.001.08", "IGN-1", oct(6).Add(18*time.Hour), sellerAccount,
		bankfiletest.CamtEntry{BookedOn: oct(6), Txs: []bankfiletest.CamtTx{kid, {AmountMinor: 0, KID: "0010025"}}},
		bankfiletest.CamtEntry{BookedOn: oct(6), CreditDebit: "DBIT", BankDomain: "ACMT/MDOP/CHRG", AmountMinor: 4500, AddtlInfo: "Gebyr"},
		bankfiletest.CamtEntry{BookedOn: oct(6), CreditDebit: "DBIT", BankDomain: "ACMT/MDOP/CHRG", AmountMinor: 500, AddtlInfo: "Gebyr"},
		bankfiletest.CamtEntry{Status: "PDNG", Txs: []bankfiletest.CamtTx{{AmountMinor: 700, KID: "0010033"}}},
	)
	r := imported(t, c, camt)
	want := map[string]int{"debit": 2, "notBooked": 1, "cardInformation": 0, "zeroAmount": 1}
	if r.Transactions != 1 || !mapsEqual(r.Ignored, want) || r.File.Ignored != 4 || !mapsEqual(r.File.IgnoredKinds, want) {
		t.Errorf("the camt.054 = %d transactions, ignored %v (file %d, %v), want 1 and %v", r.Transactions, r.Ignored, r.File.Ignored, r.File.IgnoredKinds, want)
	}
	if got := bankDetail(t, h, r.File.ID).File.IgnoredKinds; !mapsEqual(got, want) {
		t.Errorf("the stored file's ignoredKinds = %v, want %v", got, want)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT ignored_kinds::text FROM invoices.bank_files WHERE id = $1`, r.File.ID); got != `{"debit": 2, "not_booked": 1, "zero_amount": 1, "card_information": 0}` {
		t.Errorf("ignored_kinds = %s, want the four kinds by their stored names", got)
	}

	card := bankfiletest.OCRPayment{Type: 18, Account: olderAccount, Settled: oct(6), AmountMinor: 2500, ArchiveRef: "7"}
	r = imported(t, c, bankfiletest.OCR("3", giro(olderAccount, 6, 49950, "0010041", "1"), card, card))
	want = map[string]int{"debit": 0, "notBooked": 0, "cardInformation": 2, "zeroAmount": 0}
	if r.Transactions != 1 || !mapsEqual(r.Ignored, want) || !mapsEqual(r.File.IgnoredKinds, want) {
		t.Errorf("the OCR file = %d transactions, ignored %v, want 1 and %v", r.Transactions, r.Ignored, want)
	}
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// TestBankFile_ListAndDetail: the files newest first and paged, each with
// its lines counted by status — queued, here, since no invoice of this
// installation carries a KID; one file with every line as the bank wrote
// it, duplicates included, in the order stored (by fingerprint); 404 for an unknown file, 400
// for paging out of range, 403 without invoices:payments.
func TestBankFile_ListAndDetail(t *testing.T) {
	t.Parallel()
	h := bankHarness(t)
	c, user := h.SignInUser(t, "invoices:access", "invoices:payments")
	ordered := oct(4)
	detailed := bankfiletest.OCR("1",
		bankfiletest.OCRPayment{Type: 10, Account: sellerAccount, Settled: oct(5), Ordered: ordered, AmountMinor: 125000, KID: "0010017", ArchiveRef: "987654321", DebtorAccount: "98765432109"},
		bankfiletest.OCRPayment{Type: 13, Account: sellerAccount, Settled: oct(5), AmountMinor: 4995, ArchiveRef: "2"},
		bankfiletest.OCRPayment{Type: 10, Account: sellerAccount, Settled: oct(6), AmountMinor: 700, Negative: true, KID: "0010025", ArchiveRef: "3"},
	)
	var ids []int64
	for i, file := range [][]byte{
		detailed,
		bankfiletest.OCR("2", giro(sellerAccount, 6, 5000, "0010033", "4")),
		bankfiletest.OCR("3", giro(sellerAccount, 6, 5000, "0010033", "4")),
	} {
		ids = append(ids, imported(t, c, file).File.ID)
		if i < 2 {
			h.Advance(time.Hour)
		}
	}

	var page struct {
		Data       []bankFileJSON `json:"data"`
		Pagination struct {
			Page       int `json:"page"`
			PageSize   int `json:"pageSize"`
			TotalCount int `json:"totalCount"`
		} `json:"pagination"`
	}
	res := c.Do(http.MethodGet, bankFilesPath+"?page=1&pageSize=2", nil)
	res.JSON(&page)
	if res.Status != http.StatusOK || len(page.Data) != 2 || page.Data[0].ID != ids[2] || page.Data[1].ID != ids[1] || page.Pagination.TotalCount != 3 {
		t.Errorf("page 1 = %d %+v, want files %d and %d of 3", res.Status, page, ids[2], ids[1])
	}
	if f := page.Data[0]; f.Transactions != 1 || f.Duplicates != 1 || f.Pending != 0 || f.Exceptions != 0 || f.Matched != 0 || f.UploadedBy != user {
		t.Errorf("the newest file = %+v, want its one line a duplicate of the second's", f)
	}
	res = c.Do(http.MethodGet, bankFilesPath+"?page=2&pageSize=2", nil)
	res.JSON(&page)
	if len(page.Data) != 1 || page.Data[0].ID != ids[0] || page.Data[0].Exceptions != 3 || page.Data[0].Pending != 0 {
		t.Errorf("page 2 = %+v, want the oldest file, three lines queued", page.Data)
	}
	if res := c.Do(http.MethodGet, bankFilesPath+"?pageSize=101", nil); res.Status != http.StatusBadRequest {
		t.Errorf("pageSize=101 = %d, want 400", res.Status)
	}

	d := bankDetail(t, h, ids[0])
	if d.File.ID != ids[0] || d.File.Format != "ocr" || d.File.FirstBookedOn == nil || *d.File.FirstBookedOn != "2026-10-05" ||
		d.File.LastBookedOn == nil || *d.File.LastBookedOn != "2026-10-06" || !slices.Equal(d.File.Accounts, []string{sellerAccount}) {
		t.Errorf("the file = %+v", d.File)
	}
	parsed := parsedOn(t, detailed).Transactions
	if len(d.Transactions) != len(parsed) {
		t.Fatalf("the file's lines = %d, want %d", len(d.Transactions), len(parsed))
	}
	want := map[string]bankfile.Transaction{}
	for _, w := range parsed {
		want[w.LineRef] = w
	}
	for i, l := range d.Transactions {
		w := want[l.LineRef]
		var wantKid *string
		if w.KID != "" {
			wantKid = &w.KID
		}
		if i > 0 && l.ID <= d.Transactions[i-1].ID {
			t.Errorf("line %d's id %d is not after %d", i, l.ID, d.Transactions[i-1].ID)
		}
		if l.LineRef != w.LineRef || l.Account != w.Account || l.Direction != "credit" || l.Negative != w.Negative ||
			l.BookedOn != w.BookedOn.Format("2006-01-02") || l.Amount != float64(w.AmountMinor)/100 || !equalPtr(l.Kid, wantKid) ||
			l.RemittanceText != w.RemittanceText || l.DebtorAccount != w.DebtorAccount || l.ArchiveRef != w.ArchiveRef ||
			l.Status != "exception" || l.Reason == nil || l.DuplicateOfID != nil {
			t.Errorf("line %d = %+v, want %+v", i, l, w)
		}
	}
	for _, l := range d.Transactions {
		var wantOrdered *string
		if l.LineRef == parsed[0].LineRef {
			wantOrdered = ptrTo("2026-10-04")
		}
		if !equalPtr(l.OrderedOn, wantOrdered) {
			t.Errorf("line %s's ordering day = %v, want %v: the first payment's alone, the others written 000000", l.LineRef, l.OrderedOn, wantOrdered)
		}
	}

	if res := c.Do(http.MethodGet, fmt.Sprintf("%s/%d", bankFilesPath, ids[2]+100), nil); res.Status != http.StatusNotFound {
		t.Errorf("an unknown file = %d, want 404", res.Status)
	}
	reader := h.SignIn(t, "invoices:access")
	if res := reader.Do(http.MethodGet, bankFilesPath, nil); res.Status != http.StatusForbidden {
		t.Errorf("GET /bank-files with invoices:access alone = %d, want 403", res.Status)
	}
	if res := reader.Do(http.MethodGet, fmt.Sprintf("%s/%d", bankFilesPath, ids[0]), nil); res.Status != http.StatusForbidden {
		t.Errorf("GET /bank-files/{id} with invoices:access alone = %d, want 403", res.Status)
	}
	if res := upload(t, reader, detailed); res.Status != http.StatusForbidden {
		t.Errorf("POST /bank-files with invoices:access alone = %d, want 403", res.Status)
	}
}

func equalPtr[T comparable](a, b *T) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

// TestBankFile_NoCallUnderALock: the import's object-store calls are made
// before its transaction, never inside it (D18, module-boundaries rule 10):
// Exists, then Put, both outside a lock — and the harness's recorder, which
// fails any test whose request made a call under one, stays silent.
func TestBankFile_NoCallUnderALock(t *testing.T) {
	t.Parallel()
	h := bankHarness(t)
	c, user := h.SignInUser(t, "invoices:access", "invoices:payments")
	imported(t, c, bankfiletest.OCR("9", giro(sellerAccount, 6, 125000, "0010017", "1")))
	var methods []string
	for _, call := range contractCalls.by(user) {
		if call.locked {
			t.Errorf("%s was called under a lock", call.method)
		}
		methods = append(methods, call.method)
	}
	if !slices.Equal(methods, []string{"ObjectStore.Exists", "ObjectStore.Put"}) {
		t.Errorf("the import's calls out of the module = %v, want Exists then Put", methods)
	}
}

// TestBankFile_LockOrder: an import locks the file's accounts, in account
// order, and nothing else; a format change locks its account alone (D18).
func TestBankFile_LockOrder(t *testing.T) {
	h := bankHarness(t)
	c := importer(t, h)
	seen := &lockSeen{}
	restore := invoices.SetLockTaken(seen.note)
	defer restore()

	imported(t, c, bankfiletest.OCR("1", giro(sellerAccount, 6, 125000, "0010017", "1"), giro(olderAccount, 6, 5000, "0010025", "2")))
	if got, want := seen.take(), []string{"account " + olderAccount, "account " + sellerAccount}; !slices.Equal(got, want) {
		t.Errorf("the import's locks = %v, want %v", got, want)
	}
	res := h.SignIn(t, "invoices:access", "invoices:manage").Do(http.MethodPut, bankAccountsPath+"/"+sellerAccount+"/format", map[string]any{"format": "camt054"})
	if res.Status != http.StatusOK {
		t.Fatalf("PUT format = %d %s", res.Status, res.Body)
	}
	if got, want := seen.take(), []string{"account " + sellerAccount}; !slices.Equal(got, want) {
		t.Errorf("the format change's locks = %v, want %v", got, want)
	}
}

// TestMeta_CanImportBankFiles: the capability is invoices:payments (D1).
func TestMeta_CanImportBankFiles(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	can := func(permissions ...string) bool {
		t.Helper()
		var meta struct {
			Capabilities struct {
				CanImportBankFiles *bool `json:"canImportBankFiles"`
			} `json:"capabilities"`
		}
		res := h.SignIn(t, permissions...).Do(http.MethodGet, metaPath, nil)
		res.JSON(&meta)
		if meta.Capabilities.CanImportBankFiles == nil {
			t.Fatalf("GET /meta = %s, want canImportBankFiles", res.Body)
		}
		return *meta.Capabilities.CanImportBankFiles
	}
	if !can("invoices:access", "invoices:payments") {
		t.Error("canImportBankFiles with invoices:payments = false, want true")
	}
	if can("invoices:access", "invoices:manage", "invoices:issue") {
		t.Error("canImportBankFiles without invoices:payments = true, want false")
	}
}
