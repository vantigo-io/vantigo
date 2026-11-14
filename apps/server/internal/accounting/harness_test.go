package accounting_test

import (
	"context"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vantigo-io/vantigo/server/internal/accounting"
	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// harness is one accounting installation for one test: internal/modtest's
// shared harness composing this module beside identity, every exchange
// validated against accounting.yaml, with fakes of Customers' directory,
// Invoices' sales journal and the object store in place of the modules
// depguard keeps out of this package. modtest lists invoices in MODULES
// whenever accounting is composed, so config's rule holds.
type harness struct {
	*modtest.Harness
	journal *fakeJournal
	objects *memoryStore
}

func newHarness(t *testing.T, opts ...modtest.Option) *harness {
	t.Helper()
	h := &harness{journal: &fakeJournal{}, objects: &memoryStore{data: map[string][]byte{}}}
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(accounting.Module()),
		modtest.WithDirectory(fakeDirectory{names: map[int32]string{customerAcme: "Acme AS", customerBeta: "Beta AS"}}),
		modtest.WithSalesJournal(h.journal),
		modtest.WithObjectStore(h.objects),
	}
	h.Harness = modtest.New(t, append(base, opts...)...)
	return h
}

const (
	customerAcme int32 = 501
	customerBeta int32 = 502
)

// The permission sets a test signs in with (A1).
var (
	reader     = []string{"accounting:read"}
	poster     = []string{"accounting:read", "accounting:post"}
	closer     = []string{"accounting:read", "accounting:close"}
	exporter   = []string{"accounting:read", "accounting:export"}
	accountant = []string{"accounting:read", "accounting:post", "accounting:close", "accounting:export"}
)

const base = "/api/v1/accounting"

// setUp sets the books up from 2026-01-01, in Norwegian unless said.
func setUp(t *testing.T, c *modtest.Client, language string) gen.AccountingSettings {
	t.Helper()
	res := c.Do(http.MethodPost, base+"/setup", map[string]any{"booksStart": "2026-01-01", "vatTerm": "bimonthly", "language": language})
	if res.Status != http.StatusCreated {
		t.Fatalf("setup = %d %s", res.Status, res.Body)
	}
	var out gen.AccountingSettings
	res.JSON(&out)
	return out
}

// accounts is the chart by number.
func accounts(t *testing.T, c *modtest.Client) map[string]gen.AccountingAccount {
	t.Helper()
	res := c.Do(http.MethodGet, base+"/accounts", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("accounts = %d %s", res.Status, res.Body)
	}
	var list gen.AccountingAccountList
	res.JSON(&list)
	out := map[string]gen.AccountingAccount{}
	for _, a := range list.Items {
		out[a.Number] = a
	}
	return out
}

// taxCodes is the tax codes by code.
func taxCodes(t *testing.T, c *modtest.Client) map[string]gen.AccountingTaxCode {
	t.Helper()
	res := c.Do(http.MethodGet, base+"/tax-codes", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("tax codes = %d %s", res.Status, res.Body)
	}
	var list gen.AccountingTaxCodeList
	res.JSON(&list)
	out := map[string]gen.AccountingTaxCode{}
	for _, c := range list.Items {
		out[c.Code] = c
	}
	return out
}

// line is a journal line for a test.
type line map[string]any

// journal posts a manual journal and answers the response.
func journal(c *modtest.Client, date, description string, lines ...line) *modtest.Response {
	return c.Do(http.MethodPost, base+"/vouchers", map[string]any{"voucherDate": date, "description": description, "lines": lines})
}

// mustPost posts a manual journal that must succeed.
func mustPost(t *testing.T, c *modtest.Client, date, description string, lines ...line) gen.AccountingVoucher {
	t.Helper()
	res := journal(c, date, description, lines...)
	if res.Status != http.StatusCreated {
		t.Fatalf("post %s = %d %s", description, res.Status, res.Body)
	}
	var v gen.AccountingVoucher
	res.JSON(&v)
	return v
}

// problemCode is a problem body's code.
func problemCode(t *testing.T, res *modtest.Response) string {
	t.Helper()
	var p gen.AccountingProblem
	res.JSON(&p)
	if p.Code == nil {
		return ""
	}
	return *p.Code
}

// expectProblem fails t unless res is status with code.
func expectProblem(t *testing.T, res *modtest.Response, status int, code string) {
	t.Helper()
	if res.Status != status {
		t.Fatalf("status = %d %s, want %d %s", res.Status, res.Body, status, code)
	}
	if got := problemCode(t, res); got != code {
		t.Fatalf("code = %q (%s), want %q", got, res.Body, code)
	}
}

// fakeDirectory is Customers' directory as far as this module reads it: the
// customers' names.
type fakeDirectory struct {
	contracts.CustomerDirectory
	names map[int32]string
}

func (d fakeDirectory) Customers(_ context.Context, ids []int32) ([]contracts.CustomerEntry, error) {
	out := []contracts.CustomerEntry{}
	for _, id := range ids {
		if name, ok := d.names[id]; ok {
			out = append(out, contracts.CustomerEntry{ID: id, Name: name})
		}
	}
	return out, nil
}

// fakeJournal is Invoices' sales journal: the open invoices a test sets, in
// number order and pages of the limit asked, call n answering seqs[n] as its
// AsOfSeq (the last again past the end, 0 without any).
type fakeJournal struct {
	mu    sync.Mutex
	open  []contracts.OpenDocument
	seqs  []int64
	asked []time.Time
	calls int
}

func (j *fakeJournal) Documents(context.Context, int64, int) ([]contracts.JournalDocument, error) {
	return nil, nil
}

func (j *fakeJournal) Facts(context.Context, int64, int) ([]contracts.JournalFact, error) {
	return nil, nil
}

func (j *fakeJournal) OpenAt(_ context.Context, day time.Time, afterNumber int64, limit int) (contracts.OpenPage, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.asked = append(j.asked, day)
	size := min(limit, contracts.MaxJournalPage)
	page := contracts.OpenPage{Documents: []contracts.OpenDocument{}}
	if len(j.seqs) > 0 {
		page.AsOfSeq = j.seqs[min(j.calls, len(j.seqs)-1)]
	}
	j.calls++
	for _, d := range j.open {
		if d.Number > afterNumber && len(page.Documents) < size {
			page.Documents = append(page.Documents, d)
		}
	}
	return page, nil
}

// memoryStore is an object store in memory.
type memoryStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (m *memoryStore) Put(_ context.Context, key string, r io.Reader, _ string) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = b
	return nil
}

func (m *memoryStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.data[key]
	if !ok {
		return nil, fmt.Errorf("%s: %w", key, storage.ErrNotExist)
	}
	return io.NopCloser(strings.NewReader(string(b))), nil
}

func (m *memoryStore) Exists(_ context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.data[key]
	return ok, nil
}

func (m *memoryStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

// systemSeq tells each postSystem call's source apart.
var systemSeq int64

// postSystem posts a system voucher, as delivery B will, through the engine:
// debit, credit, 100 each, with a source and a rule, dated date.
func postSystem(t *testing.T, h *harness, debit, credit int32, date string) int64 {
	t.Helper()
	day, err := time.Parse(time.DateOnly, date)
	if err != nil {
		t.Fatal(err)
	}
	systemSeq++
	var posted accounting.Posted
	err = db.WithTx(t.Context(), h.Pool(), pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		posted, err = accounting.Post(t.Context(), tx, accounting.Voucher{Kind: "sales_invoice", Date: day, Description: "Invoice",
			Source: &accounting.Source{Module: "invoices", Kind: "document", ID: systemSeq}, Rule: "sales.invoice", RuleVersion: 1,
			Lines: []accounting.Line{{AccountID: debit, Amount: big.NewRat(100, 1)}, {AccountID: credit, Amount: big.NewRat(-100, 1)}}})
		return err
	})
	if err != nil {
		t.Fatalf("post a system voucher: %v", err)
	}
	return posted.ID
}
