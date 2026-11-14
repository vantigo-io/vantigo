package accounting

// SAF-T Financial export (A24). A person with accounting:export asks for a
// fiscal year, or periods within it; the request is checked and queued, and
// the worker accounting.saft writes the file. The worker claims an export in
// one short transaction under a lease, then writes with no transaction and
// no pooled connection held: every read is one query on the pool, a page of
// vouchers at a time, and the document streams through a pipe into the
// object store as it is written, so a file of any size is written in
// constant memory. Every read is bounded by the highest voucher committed
// when the run began, and the run holds what it streamed to the totals it
// wrote first. A file that would pass the split threshold is written again
// as parts, split by period, each a valid file alone.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/saft"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/buildinfo"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

const (
	saftWorkerName = "accounting.saft"
	saftInterval   = 10 * time.Second
	// saftPage is the vouchers read at a time.
	saftPage = 500
	// saftDefaultSplit is the split threshold without a configuration:
	// Skatteetaten takes files of up to 2 GB.
	saftDefaultSplit = 2_000_000_000
	// saftPerCycle bounds the exports one cycle takes up.
	saftPerCycle = 5
)

// saftLease is the lease a claim takes, renewed at every page. A test may
// shorten it.
var saftLease = 10 * time.Minute

// saftJournals groups the vouchers into journals by kind (A24): a small fixed
// mapping, in the file's order. Type is the technical description's GL, AR,
// AP or A.
var saftJournals = []struct {
	id, typ string
	kinds   []string
	en, nb  string
}{
	{"SALES", "AR", []string{"sales_invoice", "sales_credit_note", "charge", "write_off"}, "Sales", "Salg"},
	{"PURCHASES", "AP", []string{"supplier_invoice", "supplier_credit_note", "expense"}, "Purchases", "Innkjøp"},
	{"BANK", "GL", []string{"customer_payment", "supplier_payment", "reimbursement", "bank"}, "Bank and payments", "Bank og betalinger"},
	{"GENERAL", "GL", []string{"manual", "opening", "vat_settlement", "import"}, "General journal", "Diverse bilag"},
}

// voucherKindWords name a voucher kind as VoucherDescription says it.
var voucherKindWords = map[string][2]string{
	"manual": {"Manual journal", "Manuelt bilag"}, "opening": {"Opening balances", "Åpningsbalanse"},
	"sales_invoice": {"Sales invoice", "Salgsfaktura"}, "sales_credit_note": {"Sales credit note", "Kreditnota"},
	"customer_payment": {"Customer payment", "Innbetaling"}, "charge": {"Fee or interest", "Gebyr eller renter"},
	"write_off": {"Write-off", "Avskrivning av fordring"}, "supplier_invoice": {"Supplier invoice", "Leverandørfaktura"},
	"supplier_credit_note": {"Supplier credit note", "Leverandørkreditnota"}, "supplier_payment": {"Supplier payment", "Utbetaling"},
	"expense": {"Expense", "Utlegg"}, "reimbursement": {"Reimbursement", "Refusjon av utlegg"},
	"vat_settlement": {"VAT settlement", "Mva-oppgjør"}, "bank": {"Bank", "Bank"}, "import": {"Import", "Import"},
}

// saftSelection is an export's selection: a fiscal year's periods, as days.
type saftSelection struct {
	year           int32
	fromP, toP     int
	from, to       time.Time
	fiscalStart    time.Time
	fiscalStartMon int32
}

// periodStart is the first day of period p (1 is the fiscal year's first
// month).
func (s saftSelection) periodStart(p int) time.Time { return s.fiscalStart.AddDate(0, p-1, 0) }

func (s saftSelection) periodEnd(p int) time.Time { return s.fiscalStart.AddDate(0, p, -1) }

// sub is the selection of periods from to to within the same year.
func (s saftSelection) sub(from, to int) saftSelection {
	s.fromP, s.toP = from, to
	s.from, s.to = s.periodStart(from), s.periodEnd(to)
	return s
}

// period is the accounting period date falls in, 1 to 12 (the technical
// description: a deviating fiscal year's own period).
func (s saftSelection) period(date time.Time) int {
	return (date.Year()*12 + int(date.Month())) - (s.fiscalStart.Year()*12 + int(s.fiscalStart.Month())) + 1
}

// selectionOf checks a fiscal year and its periods against the books: the
// year from the one books_start falls in to the one today falls in.
func selectionOf(settings store.AccountingSetting, today time.Time, year int32, from, to *int32) (saftSelection, error) {
	month := int32(settings.FiscalYearStartMonth)
	sel := saftSelection{year: year, fromP: 1, toP: 12, fiscalStartMon: month,
		fiscalStart: time.Date(int(year), time.Month(month), 1, 0, 0, 0, 0, time.UTC)}
	if from != nil {
		sel.fromP = int(*from)
	}
	if to != nil {
		sel.toP = int(*to)
	}
	var f fieldErrors
	if year < fiscalYear(settings.BooksStart, month) || year > fiscalYear(today, month) {
		f.addCoded("fiscalYear", codePeriodInvalid, fmt.Sprintf("A fiscal year from %d to %d.",
			fiscalYear(settings.BooksStart, month), fiscalYear(today, month)))
	}
	if sel.fromP < 1 || sel.fromP > 12 {
		f.addCoded("fromPeriod", codePeriodInvalid, "A period from 1 to 12.")
	}
	if sel.toP < sel.fromP || sel.toP > 12 {
		f.addCoded("toPeriod", codePeriodInvalid, "A period from fromPeriod to 12.")
	}
	if err := f.refusal("The selection cannot be exported"); err != nil {
		return sel, err
	}
	return sel.sub(sel.fromP, sel.toP), nil
}

// unmappedAccounts is every account with no grouping code for the year: each
// exports NA (A3).
func unmappedAccounts(ctx context.Context, q *store.Queries, year int32) ([]gen.AccountingSaftUnmappedAccount, error) {
	rows, err := q.SaftAccounts(ctx, year)
	if err != nil {
		return nil, err
	}
	out := []gen.AccountingSaftUnmappedAccount{}
	for _, a := range rows {
		if a.GroupingCode == "" {
			out = append(out, gen.AccountingSaftUnmappedAccount{AccountId: a.ID, Number: a.Number, Name: a.Name})
		}
	}
	return out, nil
}

// seller is the company the file is from, nil when Invoices has none with an
// organisation number.
func sellerOf(ctx context.Context, st contracts.SalesTax) (*contracts.SellerProfile, string, error) {
	if st == nil {
		return nil, "", nil
	}
	seller, err := st.Seller(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("accounting: read the seller: %w", err)
	}
	n := strings.NewReplacer(" ", "", ".", "").Replace(strings.TrimSpace(seller.OrganisationNumber))
	n = strings.TrimSuffix(strings.TrimPrefix(strings.ToUpper(n), "NO"), "MVA")
	if len(n) != 9 || strings.Trim(n, "0123456789") != "" || strings.TrimSpace(seller.LegalName) == "" {
		return nil, "", nil
	}
	return &seller, n, nil
}

func sellerMissing() error {
	return conflict(codeSaftSellerMissing, "The company's details are missing",
		"A SAF-T file names the company by its organisation number. Enter the company's name and organisation number in Invoices' settings first.")
}

// CheckAccountingSaftExport is what an export of the selection would meet.
func (s *server) CheckAccountingSaftExport(ctx context.Context, req gen.CheckAccountingSaftExportRequestObject) (gen.CheckAccountingSaftExportResponseObject, error) {
	settings, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	today, err := s.today(ctx)
	if err != nil {
		return nil, err
	}
	sel, err := selectionOf(settings, today, req.Params.FiscalYear, req.Params.FromPeriod, req.Params.ToPeriod)
	if err != nil {
		return nil, err
	}
	seller, _, err := sellerOf(ctx, s.deps.SalesTax)
	if err != nil {
		return nil, err
	}
	q := store.New(s.deps.Pool)
	unmapped, err := unmappedAccounts(ctx, q, sel.year)
	if err != nil {
		return nil, err
	}
	max, err := q.MaxVoucherID(ctx)
	if err != nil {
		return nil, err
	}
	totals, err := q.SaftTotals(ctx, store.SaftTotalsParams{FromDate: sel.from, ToDate: sel.to, ThroughVoucherID: max})
	if err != nil {
		return nil, err
	}
	return gen.CheckAccountingSaftExport200JSONResponse{FiscalYear: sel.year, FromPeriod: int32(sel.fromP), ToPeriod: int32(sel.toP),
		From: apiDate(sel.from), To: apiDate(sel.to), SellerMissing: seller == nil, UnmappedAccounts: unmapped,
		Vouchers: totals.Vouchers}, nil
}

// CreateAccountingSaftExport queues an export, logged with who asked and for
// what.
func (s *server) CreateAccountingSaftExport(ctx context.Context, req gen.CreateAccountingSaftExportRequestObject) (gen.CreateAccountingSaftExportResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The selection cannot be exported", "body", "", "A body is required.")
	}
	settings, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	today, err := s.today(ctx)
	if err != nil {
		return nil, err
	}
	sel, err := selectionOf(settings, today, req.Body.FiscalYear, req.Body.FromPeriod, req.Body.ToPeriod)
	if err != nil {
		return nil, err
	}
	seller, _, err := sellerOf(ctx, s.deps.SalesTax)
	if err != nil {
		return nil, err
	}
	if seller == nil {
		return nil, sellerMissing()
	}
	if _, err := s.objectStore(); err != nil || !s.storageConfigured {
		if err == nil {
			err = storage.ErrNotConfigured
		}
		return nil, storageRefusal(err)
	}
	q := store.New(s.deps.Pool)
	unmapped, err := unmappedAccounts(ctx, q, sel.year)
	if err != nil {
		return nil, err
	}
	row, err := q.InsertSaftExport(ctx, store.InsertSaftExportParams{FiscalYear: sel.year, FromPeriod: int16(sel.fromP),
		ToPeriod: int16(sel.toP), PeriodStart: sel.from, PeriodEnd: sel.to, UnmappedAccounts: int32(len(unmapped)),
		Now: s.now(), UserID: callerID(ctx)})
	if err != nil {
		return nil, err
	}
	s.logger().InfoContext(ctx, "accounting: a SAF-T export was asked for", "export_id", row.ID, "user_id", callerID(ctx),
		"fiscal_year", sel.year, "from_period", sel.fromP, "to_period", sel.toP, "unmapped_accounts", len(unmapped))
	views, err := s.exportViews(ctx, q, []store.AccountingSaftExport{row})
	if err != nil {
		return nil, err
	}
	return gen.CreateAccountingSaftExport202JSONResponse{Export: views[0], UnmappedAccounts: unmapped}, nil
}

// exportViews are exports as the contract answers them, with their files
// and the names of who asked.
func (s *server) exportViews(ctx context.Context, q *store.Queries, rows []store.AccountingSaftExport) ([]gen.AccountingSaftExport, error) {
	ids := make([]int64, 0, len(rows))
	users := []uuid.UUID{}
	for _, r := range rows {
		ids = append(ids, r.ID)
		users = append(users, r.RequestedByUserID)
	}
	files, err := q.SaftExportFiles(ctx, ids)
	if err != nil {
		return nil, err
	}
	byExport := map[int64][]gen.AccountingSaftExportFile{}
	for _, f := range files {
		byExport[f.ExportID] = append(byExport[f.ExportID], gen.AccountingSaftExportFile{Part: f.Part, Parts: f.Parts,
			FileName: f.FileName, SizeBytes: f.SizeBytes, Sha256: f.Sha256, FromPeriod: int32(f.FromPeriod),
			ToPeriod: int32(f.ToPeriod), From: apiDate(f.PeriodStart), To: apiDate(f.PeriodEnd)})
	}
	names := map[uuid.UUID]string{}
	if s.deps.Users != nil && len(users) > 0 {
		entries, err := s.deps.Users.Users(ctx, users)
		if err != nil {
			return nil, fmt.Errorf("accounting: read the users: %w", err)
		}
		for _, e := range entries {
			names[e.ID] = e.DisplayName
		}
	}
	out := make([]gen.AccountingSaftExport, 0, len(rows))
	for _, r := range rows {
		v := gen.AccountingSaftExport{Id: r.ID, FiscalYear: r.FiscalYear, FromPeriod: int32(r.FromPeriod),
			ToPeriod: int32(r.ToPeriod), From: apiDate(r.PeriodStart), To: apiDate(r.PeriodEnd),
			State: gen.AccountingSaftExportState(r.State), Error: r.Error, Attempts: r.Attempts,
			RequestedAt: r.RequestedAt, RequestedBy: r.RequestedByUserID, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
			TotalBytes: r.TotalBytes, UnmappedAccounts: r.UnmappedAccounts, Files: byExport[r.ID]}
		if v.Files == nil {
			v.Files = []gen.AccountingSaftExportFile{}
		}
		if name, ok := names[r.RequestedByUserID]; ok {
			v.RequestedByName = &name
		}
		out = append(out, v)
	}
	return out, nil
}

// ListAccountingSaftExports is the latest exports, newest first.
func (s *server) ListAccountingSaftExports(ctx context.Context, _ gen.ListAccountingSaftExportsRequestObject) (gen.ListAccountingSaftExportsResponseObject, error) {
	q := store.New(s.deps.Pool)
	rows, err := q.ListSaftExports(ctx, 100)
	if err != nil {
		return nil, err
	}
	views, err := s.exportViews(ctx, q, rows)
	if err != nil {
		return nil, err
	}
	return gen.ListAccountingSaftExports200JSONResponse{Items: views}, nil
}

// GetAccountingSaftExport is one export.
func (s *server) GetAccountingSaftExport(ctx context.Context, req gen.GetAccountingSaftExportRequestObject) (gen.GetAccountingSaftExportResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetSaftExport(ctx, req.Id)
	if noRows(err) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	views, err := s.exportViews(ctx, q, []store.AccountingSaftExport{row})
	if err != nil {
		return nil, err
	}
	return gen.GetAccountingSaftExport200JSONResponse(views[0]), nil
}

// saftDownload serves a file as a download under its name, never cached.
type saftDownload struct {
	name string
	body io.ReadCloser
	size int64
}

func (r saftDownload) visit(w http.ResponseWriter) error {
	defer func() { _ = r.body.Close() }()
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": r.name}))
	return gen.DownloadAccountingSaftExport200ApplicationxmlResponse{Body: r.body, ContentLength: r.size}.VisitDownloadAccountingSaftExportResponse(w)
}

type saftExportDownload struct{ saftDownload }

func (r saftExportDownload) VisitDownloadAccountingSaftExportResponse(w http.ResponseWriter) error {
	return r.visit(w)
}

// DownloadAccountingSaftExport streams one file of a finished export.
func (s *server) DownloadAccountingSaftExport(ctx context.Context, req gen.DownloadAccountingSaftExportRequestObject) (gen.DownloadAccountingSaftExportResponseObject, error) {
	f, err := store.New(s.deps.Pool).GetSaftExportFile(ctx, store.GetSaftExportFileParams{ExportID: req.Id, Part: req.Part})
	if noRows(err) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	st, err := s.objectStore()
	if err != nil {
		return nil, storageRefusal(err)
	}
	body, err := st.Get(ctx, f.ObjectKey)
	if err != nil {
		s.logger().ErrorContext(ctx, "accounting: a SAF-T file could not be read from the store", "export_id", f.ExportID, "error", err.Error())
		return nil, storageRefusal(err)
	}
	s.logger().InfoContext(ctx, "accounting: a SAF-T file was downloaded", "export_id", f.ExportID, "part", f.Part,
		"user_id", callerID(ctx), "sha256", f.Sha256)
	return saftExportDownload{saftDownload{name: f.FileName, body: body, size: f.SizeBytes}}, nil
}

// SaftWorker writes the queued SAF-T exports. It implements worker.Worker.
type SaftWorker struct {
	deps    module.Deps
	objects storage.ObjectStore
	err     error
}

var _ worker.Worker = (*SaftWorker)(nil)

// NewSaftWorker builds the worker over worker mode's Deps: the pool, the
// object store, the seller (SalesTax), Customers' directory and Projects'.
func NewSaftWorker(d module.Deps) *SaftWorker {
	objects, err := moduleObjectStore(d)
	return &SaftWorker{deps: d, objects: objects, err: err}
}

// Name identifies this worker in the runner's logs.
func (w *SaftWorker) Name() string { return saftWorkerName }

// Interval is the poll cadence.
func (w *SaftWorker) Interval() time.Duration { return saftInterval }

// Run runs a cycle, sleeps the interval, and repeats until ctx is done.
func (w *SaftWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(saftInterval)
	defer ticker.Stop()
	for {
		if err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
			w.logger().ErrorContext(ctx, "accounting: the SAF-T worker failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *SaftWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}

// RunOnce takes up the exports that wait, one after another: queued ones,
// and running ones whose lease ran out (a worker that stopped).
func (w *SaftWorker) RunOnce(ctx context.Context) error {
	if w.deps.Pool == nil {
		return nil
	}
	if _, err := store.New(w.deps.Pool).GetSettings(ctx); noRows(err) {
		return nil
	} else if err != nil {
		return err
	}
	now := clockOf(w.deps)
	for range saftPerCycle {
		lease := uuid.New()
		var row store.AccountingSaftExport
		claimed := false
		err := db.WithTx(ctx, w.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
			var err error
			row, err = store.New(tx).ClaimSaftExport(ctx, store.ClaimSaftExportParams{LeaseID: &lease,
				LeaseUntil: ptr(now().Add(saftLease)), Now: ptr(now())})
			if noRows(err) {
				return nil
			}
			claimed = err == nil
			return err
		})
		if err != nil || !claimed {
			return err
		}
		w.run(ctx, row, lease)
	}
	return nil
}

// errLeaseGone is a write that found the export no longer under this claim.
var errLeaseGone = errors.New("accounting: the SAF-T export's lease was lost")

// errTooLarge is a part that passed the split threshold.
var errTooLarge = errors.New("accounting: the SAF-T file passed the split threshold")

// writtenPart is one file written.
type writtenPart struct {
	sel       saftSelection
	key, hash string
	size      int64
}

// run writes one claimed export and records how it ended.
func (w *SaftWorker) run(ctx context.Context, row store.AccountingSaftExport, lease uuid.UUID) {
	now := clockOf(w.deps)
	q := store.New(w.deps.Pool)
	parts, err := w.write(ctx, row, lease)
	finish := context.WithoutCancel(ctx)
	if err == nil {
		err = w.record(finish, row, lease, parts)
	}
	if err == nil {
		return
	}
	if errors.Is(err, errLeaseGone) || ctx.Err() != nil {
		// Another worker took it, or this one is stopping: the lease runs
		// out and the export is taken up again.
		w.deleteParts(finish, parts)
		return
	}
	w.deleteParts(finish, parts)
	msg := truncate(err.Error(), 1000)
	if _, ferr := q.FailSaftExport(finish, store.FailSaftExportParams{ID: row.ID, LeaseID: &lease, Error: &msg, Now: ptr(now())}); ferr != nil {
		w.logger().ErrorContext(ctx, "accounting: a failed SAF-T export could not be recorded", "export_id", row.ID, "error", ferr)
	}
	w.logger().ErrorContext(ctx, "accounting: a SAF-T export failed", "export_id", row.ID, "error", err)
}

func (w *SaftWorker) deleteParts(ctx context.Context, parts []writtenPart) {
	for _, p := range parts {
		if err := w.objects.Delete(ctx, p.key); err != nil {
			w.logger().ErrorContext(ctx, "accounting: an unused SAF-T object could not be removed", "key", p.key, "error", err)
		}
	}
}

// record writes the files' rows and marks the export done, under the lease.
func (w *SaftWorker) record(ctx context.Context, row store.AccountingSaftExport, lease uuid.UUID, parts []writtenPart) error {
	_, org, err := sellerOf(ctx, w.deps.SalesTax)
	if err != nil {
		return err
	}
	if org == "" {
		org = "000000000"
	}
	at := clockOf(w.deps)()
	stamp := at
	if loc, err := time.LoadLocation("Europe/Oslo"); err == nil {
		stamp = at.In(loc)
	}
	var total int64
	err = db.WithTx(ctx, w.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		for i, p := range parts {
			total += p.size
			if err := q.InsertSaftExportFile(ctx, store.InsertSaftExportFileParams{ExportID: row.ID, LeaseID: &lease,
				Part: int32(i + 1), Parts: int32(len(parts)), FileName: saft.FileName(org, stamp, i+1, len(parts)),
				ObjectKey: p.key, SizeBytes: p.size, Sha256: p.hash, FromPeriod: int16(p.sel.fromP), ToPeriod: int16(p.sel.toP),
				PeriodStart: p.sel.from, PeriodEnd: p.sel.to}); err != nil {
				return err
			}
		}
		n, err := q.FinishSaftExport(ctx, store.FinishSaftExportParams{ID: row.ID, LeaseID: &lease, TotalBytes: &total, Now: ptr(at)})
		if err != nil {
			return err
		}
		if n != 1 {
			return errLeaseGone
		}
		return nil
	})
	if err != nil {
		return err
	}
	hashes := make([]string, 0, len(parts))
	for _, p := range parts {
		hashes = append(hashes, p.hash)
	}
	w.logger().InfoContext(ctx, "accounting: a SAF-T export was written", "export_id", row.ID,
		"user_id", row.RequestedByUserID, "fiscal_year", row.FiscalYear, "from_period", row.FromPeriod,
		"to_period", row.ToPeriod, "parts", len(parts), "bytes", total, "sha256", strings.Join(hashes, ","))
	return nil
}

// splitThreshold is the configured threshold, or Skatteetaten's 2 GB.
func (w *SaftWorker) splitThreshold() int64 {
	if w.deps.Config != nil && w.deps.Config.AccountingSaftSplitBytes > 0 {
		return w.deps.Config.AccountingSaftSplitBytes
	}
	return saftDefaultSplit
}

// write writes the export's file, or its parts: a part past the threshold
// is thrown away and its periods written again as two halves.
func (w *SaftWorker) write(ctx context.Context, row store.AccountingSaftExport, lease uuid.UUID) ([]writtenPart, error) {
	if w.err != nil {
		return nil, w.err
	}
	q := store.New(w.deps.Pool)
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	through, err := q.MaxVoucherID(ctx)
	if err != nil {
		return nil, err
	}
	seller, org, err := sellerOf(ctx, w.deps.SalesTax)
	if err != nil {
		return nil, err
	}
	if seller == nil {
		return nil, errors.New("the company's name and organisation number are missing in Invoices' settings")
	}
	whole := saftSelection{year: row.FiscalYear, fiscalStartMon: int32(settings.FiscalYearStartMonth),
		fiscalStart: time.Date(int(row.FiscalYear), time.Month(settings.FiscalYearStartMonth), 1, 0, 0, 0, 0, time.UTC)}
	whole = whole.sub(int(row.FromPeriod), int(row.ToPeriod))
	gen := &saftGenerator{w: w, q: q, lease: lease, exportID: row.ID, through: through, seller: *seller, org: org,
		language: settings.ChartLanguage, userID: row.RequestedByUserID, customers: map[int32]*contracts.CustomerBillingProfile{}}
	limit := w.splitThreshold()
	queue := []saftSelection{whole}
	var done []writtenPart
	for len(queue) > 0 {
		sel := queue[0]
		queue = queue[1:]
		part, err := gen.part(ctx, sel, limit)
		if errors.Is(err, errTooLarge) {
			if sel.fromP == sel.toP {
				return done, fmt.Errorf("period %d of %d alone is over %d bytes, the split threshold", sel.fromP, sel.year, limit)
			}
			mid := (sel.fromP + sel.toP) / 2
			queue = append([]saftSelection{sel.sub(sel.fromP, mid), sel.sub(mid+1, sel.toP)}, queue...)
			continue
		}
		if err != nil {
			return done, err
		}
		done = append(done, part)
	}
	return done, nil
}

// saftGenerator holds what every part of one export shares.
type saftGenerator struct {
	w         *SaftWorker
	q         *store.Queries
	lease     uuid.UUID
	exportID  int64
	through   int64
	seller    contracts.SellerProfile
	org       string
	language  string
	userID    uuid.UUID
	customers map[int32]*contracts.CustomerBillingProfile
}

// countingWriter counts and hashes what passes, and refuses past the limit.
type countingWriter struct {
	w     io.Writer
	n     int64
	limit int64
	h     interface{ Write([]byte) (int, error) }
}

func (c *countingWriter) Write(p []byte) (int, error) {
	if c.n+int64(len(p)) > c.limit {
		return 0, errTooLarge
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	_, _ = c.h.Write(p[:n])
	return n, err
}

// part writes one file for sel into the store, streaming through a pipe.
func (g *saftGenerator) part(ctx context.Context, sel saftSelection, limit int64) (writtenPart, error) {
	key := fmt.Sprintf("saft-exports/%d/%s.xml", g.exportID, uuid.New().String())
	pr, pw := io.Pipe()
	hash := sha256.New()
	cw := &countingWriter{w: pw, limit: limit, h: hash}
	genErr := make(chan error, 1)
	go func() {
		err := g.document(ctx, cw, sel)
		_ = pw.CloseWithError(err)
		genErr <- err
	}()
	putErr := g.w.objects.Put(ctx, key, pr, "application/xml")
	_ = pr.CloseWithError(putErr)
	err := <-genErr
	if err == nil {
		err = putErr
	}
	if err != nil {
		if derr := g.w.objects.Delete(context.WithoutCancel(ctx), key); derr != nil && !errors.Is(derr, storage.ErrNotExist) {
			g.w.logger().ErrorContext(ctx, "accounting: an unused SAF-T object could not be removed", "key", key, "error", derr)
		}
		return writtenPart{}, err
	}
	return writtenPart{sel: sel, key: key, hash: hex.EncodeToString(hash.Sum(nil)), size: cw.n}, nil
}

// renew extends the lease, and says when it is gone.
func (g *saftGenerator) renew(ctx context.Context) error {
	n, err := g.q.ExtendSaftExportLease(ctx, store.ExtendSaftExportLeaseParams{ID: g.exportID, LeaseID: &g.lease,
		LeaseUntil: ptr(clockOf(g.w.deps)().Add(saftLease))})
	if err != nil {
		return err
	}
	if n != 1 {
		return errLeaseGone
	}
	return nil
}

func (g *saftGenerator) word(en, nb string) string {
	if g.language == "nb" {
		return nb
	}
	return en
}

// document writes one whole file of sel to out.
func (g *saftGenerator) document(ctx context.Context, out io.Writer, sel saftSelection) error {
	if err := g.renew(ctx); err != nil {
		return err
	}
	q := g.q
	w := saft.NewWriter(out)
	at := clockOf(g.w.deps)()
	header := saft.Header{Created: at, SoftwareCompany: "Vantigo", SoftwareID: "Vantigo", SoftwareVersion: buildinfo.Version,
		Company: saft.Company{RegistrationNumber: g.org, Name: g.seller.LegalName, Telephone: g.seller.Phone, Email: g.seller.Email,
			BankAccount: digitsOnly(g.seller.BankAccount), IBAN: g.seller.IBAN, BIC: g.seller.BIC},
		PeriodStart: sel.fromP, PeriodStartYear: int(sel.year), PeriodEnd: sel.toP, PeriodEndYear: int(sel.year),
		Comment: fmt.Sprintf("%s %s–%s", g.word("General ledger and master files,", "Hovedbok og grunndata,"),
			sel.from.Format(time.DateOnly), sel.to.Format(time.DateOnly))}
	if g.seller.VATRegistered {
		header.Company.VATNumber = g.org + "MVA"
	}
	if g.seller.AddressLine1 != "" || g.seller.City != "" {
		header.Company.Address = &saft.Address{Street: g.seller.AddressLine1, Additional: g.seller.AddressLine2,
			City: g.seller.City, PostalCode: g.seller.PostalCode, Country: countryCode(g.seller.Country), Type: "StreetAddress"}
	}
	w.Start(header)

	accounts, err := g.accounts(ctx, sel)
	if err != nil {
		return err
	}
	customers, err := g.customerParties(ctx, sel)
	if err != nil {
		return err
	}
	suppliers, err := g.supplierParties(ctx, sel)
	if err != nil {
		return err
	}
	taxes, err := g.taxTable(ctx, sel)
	if err != nil {
		return err
	}
	analysis, err := g.analysis(ctx, sel)
	if err != nil {
		return err
	}
	w.MasterFiles(accounts, customers, suppliers, taxes, analysis)

	totals, err := q.SaftTotals(ctx, store.SaftTotalsParams{FromDate: sel.from, ToDate: sel.to, ThroughVoucherID: g.through})
	if err != nil {
		return err
	}
	debit, credit := mustRat(totals.Debit), mustRat(totals.Credit)
	w.BeginEntries(totals.Vouchers, debit, credit)
	customerIDs := map[int32]string{}
	for id, p := range g.customers {
		customerIDs[id] = customerSaftID(id, p)
	}
	for _, j := range saftJournals {
		begun := false
		afterYear, afterNumber := int32(0), int32(0)
		for {
			vouchers, err := q.SaftVoucherPage(ctx, store.SaftVoucherPageParams{FromDate: sel.from, ToDate: sel.to,
				ThroughVoucherID: g.through, Kinds: j.kinds, AfterYear: afterYear, AfterNumber: afterNumber, RowLimit: saftPage})
			if err != nil {
				return err
			}
			if len(vouchers) == 0 {
				break
			}
			if !begun {
				w.BeginJournal(saft.Journal{ID: j.id, Description: g.word(j.en, j.nb), Type: j.typ})
				begun = true
			}
			ids := make([]int64, len(vouchers))
			for i, v := range vouchers {
				ids[i] = v.ID
			}
			postings, err := q.SaftPostings(ctx, ids)
			if err != nil {
				return err
			}
			byVoucher := map[int64][]store.SaftPostingsRow{}
			for _, p := range postings {
				byVoucher[p.VoucherID] = append(byVoucher[p.VoucherID], p)
			}
			for _, v := range vouchers {
				w.Transaction(g.transaction(sel, v, byVoucher[v.ID], customerIDs))
			}
			if err := w.Err(); err != nil {
				return err
			}
			last := vouchers[len(vouchers)-1]
			afterYear, afterNumber = last.FiscalYear, last.Number
			if err := g.renew(ctx); err != nil {
				return err
			}
			if len(vouchers) < saftPage {
				break
			}
		}
		if begun {
			w.EndJournal()
		}
	}
	if err := w.End(true); err != nil {
		return err
	}
	n, d, c := w.Written()
	if n != totals.Vouchers || d.Cmp(debit) != 0 || c.Cmp(credit) != 0 {
		return fmt.Errorf("the books changed while the file was written (%d vouchers, %s/%s, against %d, %s/%s); export again",
			n, d.FloatString(2), c.FloatString(2), totals.Vouchers, debit.FloatString(2), credit.FloatString(2))
	}
	return nil
}

// digitsOnly is s with everything but digits taken out.
func digitsOnly(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

// countryCode is a country as two upper-case letters, "" when it is not one.
func countryCode(c string) string {
	c = strings.ToUpper(strings.TrimSpace(c))
	if len(c) != 2 {
		return ""
	}
	return c
}

// accounts is every account with its grouping and its balances (the whole
// chart: Skatteetaten's rule for a file sent to it). The earlier years'
// result the books have not closed is carried on the year_result account, so
// the file's balances sum to zero as a closed year's would.
func (g *saftGenerator) accounts(ctx context.Context, sel saftSelection) ([]saft.Account, error) {
	rows, err := g.q.SaftAccounts(ctx, sel.year)
	if err != nil {
		return nil, err
	}
	balances, err := g.q.SaftAccountBalances(ctx, store.SaftAccountBalancesParams{FromDate: sel.from, ToDate: sel.to,
		YearStart: sel.fiscalStart, ThroughVoucherID: g.through})
	if err != nil {
		return nil, err
	}
	byID := map[int32]store.SaftAccountBalancesRow{}
	for _, b := range balances {
		byID[b.ID] = b
	}
	earlierText, err := g.q.SaftEarlierResult(ctx, store.SaftEarlierResultParams{YearStart: sel.fiscalStart, ThroughVoucherID: g.through})
	if err != nil {
		return nil, err
	}
	earlier := mustRat(earlierText)
	out := make([]saft.Account, 0, len(rows))
	for _, a := range rows {
		b, ok := byID[a.ID]
		opening, closing := new(big.Rat), new(big.Rat)
		if ok {
			opening, closing = mustRat(b.Opening), mustRat(b.Closing)
		}
		if a.SystemRole != nil && *a.SystemRole == "year_result" {
			opening, closing = add(opening, earlier), add(closing, earlier)
		}
		category, code := a.GroupingCategory, a.GroupingCode
		if code == "" {
			category, code = "NA", "NA"
		}
		created := a.CreatedAt
		out = append(out, saft.Account{ID: a.Number, Description: a.Name, GroupingCategory: category, GroupingCode: code,
			Created: &created, Opening: opening, Closing: closing})
	}
	return out, nil
}

// customerSaftID is a customer's CustomerID: its customer number in
// Customers, its id when Customers does not answer it.
func customerSaftID(id int32, p *contracts.CustomerBillingProfile) string {
	if p != nil && p.CustomerNumber > 0 {
		return strconv.FormatInt(p.CustomerNumber, 10)
	}
	return strconv.Itoa(int(id))
}

// customerParties is the customers with a posting in the selection or a
// balance at either end, with their names, organisation numbers and
// addresses read through Customers' directory.
func (g *saftGenerator) customerParties(ctx context.Context, sel saftSelection) ([]saft.Party, error) {
	rows, err := g.q.SaftCustomerBalances(ctx, store.SaftCustomerBalancesParams{FromDate: sel.from, ToDate: sel.to,
		ThroughVoucherID: g.through})
	if err != nil {
		return nil, err
	}
	accountNumbers, err := g.accountNumbers(ctx)
	if err != nil {
		return nil, err
	}
	var out []saft.Party
	index := map[int32]int{}
	for _, r := range rows {
		opening, closing := mustRat(r.Opening), mustRat(r.Closing)
		if r.Postings == 0 && opening.Sign() == 0 && closing.Sign() == 0 {
			continue
		}
		i, seen := index[r.CustomerID]
		if !seen {
			p, err := g.customerProfile(ctx, r.CustomerID)
			if err != nil {
				return nil, err
			}
			party := saft.Party{ID: customerSaftID(r.CustomerID, p), Type: "Company", Active: true}
			party.Name = fmt.Sprintf("%s %d", g.word("Customer", "Kunde"), r.CustomerID)
			if p != nil {
				party.Name = firstNonEmpty(p.LegalName, p.Name)
				party.Active = p.Status == "active"
				if p.Type == "person" {
					party.Type = "Private"
				} else if strings.EqualFold(p.LegalCountry, "no") || p.LegalCountry == "" {
					party.RegistrationNumber = digitsOnly(p.LegalID)
				} else {
					party.RegistrationNumber = p.LegalID
				}
				if a := p.InvoiceAddress; a != nil {
					party.Address = &saft.Address{Street: a.Line1, Additional: a.Line2, City: a.City, PostalCode: a.PostalCode,
						Country: countryCode(a.Country), Type: "BillingAddress"}
				}
				party.Email = p.InvoiceEmail
				party.PaymentDays = intPtr(p.PaymentTermsDays)
			}
			out = append(out, party)
			i = len(out) - 1
			index[r.CustomerID] = i
		}
		out[i].Balances = append(out[i].Balances, saft.BalanceAccount{AccountID: accountNumbers[r.AccountID],
			Opening: opening, Closing: closing})
	}
	return out, nil
}

func intPtr(p *int32) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// customerProfile is a customer's billing profile, read once per export.
func (g *saftGenerator) customerProfile(ctx context.Context, id int32) (*contracts.CustomerBillingProfile, error) {
	if p, ok := g.customers[id]; ok {
		return p, nil
	}
	var p *contracts.CustomerBillingProfile
	if g.w.deps.Directory != nil {
		var err error
		if p, err = g.w.deps.Directory.BillingProfile(ctx, id, contracts.BillingProfileOptions{}); err != nil {
			return nil, fmt.Errorf("accounting: read customer %d: %w", id, err)
		}
	}
	g.customers[id] = p
	return p, nil
}

func (g *saftGenerator) accountNumbers(ctx context.Context) (map[int32]string, error) {
	rows, err := g.q.SaftAccounts(ctx, 0)
	if err != nil {
		return nil, err
	}
	out := make(map[int32]string, len(rows))
	for _, a := range rows {
		out[a.ID] = a.Number
	}
	return out, nil
}

// supplierParties is the suppliers the same way, from this module's own
// suppliers.
func (g *saftGenerator) supplierParties(ctx context.Context, sel saftSelection) ([]saft.Party, error) {
	rows, err := g.q.SaftSupplierBalances(ctx, store.SaftSupplierBalancesParams{FromDate: sel.from, ToDate: sel.to,
		ThroughVoucherID: g.through})
	if err != nil {
		return nil, err
	}
	accountNumbers, err := g.accountNumbers(ctx)
	if err != nil {
		return nil, err
	}
	var out []saft.Party
	index := map[int64]int{}
	for _, r := range rows {
		opening, closing := mustRat(r.Opening), mustRat(r.Closing)
		if r.Postings == 0 && opening.Sign() == 0 && closing.Sign() == 0 {
			continue
		}
		i, seen := index[r.SupplierID]
		if !seen {
			party := saft.Party{ID: supplierSaftID(r.SupplierID, r.SupplierNumber), Active: r.Active, Type: "Company"}
			party.Name = firstNonEmpty(r.Name, fmt.Sprintf("%s %d", g.word("Supplier", "Leverandør"), r.SupplierID))
			if r.IsPerson {
				party.Type = "Private"
			}
			party.RegistrationNumber = deref(r.OrganisationNumber)
			if r.AddressLine != nil || r.City != nil {
				party.Address = &saft.Address{Street: deref(r.AddressLine), City: deref(r.City), PostalCode: deref(r.PostalCode),
					Country: countryCode(r.Country), Type: "PostalAddress"}
			}
			party.Email = deref(r.Email)
			party.BankAccount, party.IBAN, party.BIC = deref(r.BankAccount), deref(r.Iban), deref(r.Bic)
			if r.SupplierNumber != nil {
				days := int(r.PaymentTermsDays)
				party.PaymentDays = &days
			}
			out = append(out, party)
			i = len(out) - 1
			index[r.SupplierID] = i
		}
		out[i].Balances = append(out[i].Balances, saft.BalanceAccount{AccountID: accountNumbers[r.AccountID],
			Opening: opening, Closing: closing})
	}
	return out, nil
}

// supplierSaftID is a supplier's SupplierID: its number in these books, its
// id for a supplier from before the table.
func supplierSaftID(id int64, number *int32) string {
	if number != nil {
		return strconv.Itoa(int(*number))
	}
	return strconv.FormatInt(id, 10)
}

// taxTable is every tax code with its standard code (the code itself: these
// books keep the standard list), its rate at the selection's end and the
// share deductible — 0 for an input code whose VAT is a cost.
func (g *saftGenerator) taxTable(ctx context.Context, sel saftSelection) ([]saft.TaxCode, error) {
	rows, err := g.q.SaftTaxCodes(ctx, sel.to)
	if err != nil {
		return nil, err
	}
	out := make([]saft.TaxCode, 0, len(rows))
	for _, t := range rows {
		base := "100"
		if t.Direction == "input" && t.VatAccountID == nil {
			base = "0"
		}
		var effective *time.Time
		if from, err := time.Parse(time.DateOnly, t.ValidFrom); err == nil {
			effective = &from
		}
		out = append(out, saft.TaxCode{Code: t.Code, Description: t.Name, Percentage: t.RatePercent, Effective: effective,
			StandardCode: t.Code, BaseRate: base})
	}
	return out, nil
}

// analysis is the project dimension: every project a posting of the
// selection names, with its name from Projects when Projects is there.
func (g *saftGenerator) analysis(ctx context.Context, sel saftSelection) ([]saft.AnalysisEntry, error) {
	ids, err := g.q.SaftProjects(ctx, store.SaftProjectsParams{FromDate: sel.from, ToDate: sel.to, ThroughVoucherID: g.through})
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	names := map[int32]string{}
	if g.w.deps.Projects != nil {
		entries, err := g.w.deps.Projects.Projects(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("accounting: read the projects: %w", err)
		}
		for _, p := range entries {
			names[p.ID] = strings.TrimSpace(p.Code + " " + p.Name)
		}
	}
	out := make([]saft.AnalysisEntry, 0, len(ids))
	for _, id := range ids {
		name := names[id]
		if name == "" {
			name = fmt.Sprintf("%s %d", g.word("Project", "Prosjekt"), id)
		}
		out = append(out, saft.AnalysisEntry{Type: "P", TypeDescription: g.word("Project", "Prosjekt"),
			ID: strconv.Itoa(int(id)), Description: name})
	}
	return out, nil
}

// transaction is one voucher as a Transaction. TaxInformation follows each
// posting as stored (reference: the TaxInformation mapping): a base line
// carries its code, rate and its own amount as TaxBase with a tax amount of
// 0.00 on its own side; a tax line (tax or tax_reverse) carries its code,
// rate, the base it was computed from and its own amount as the tax amount.
func (g *saftGenerator) transaction(sel saftSelection, v store.SaftVoucherPageRow, postings []store.SaftPostingsRow, customers map[int32]string) saft.Transaction {
	words := voucherKindWords[v.Kind]
	entered := v.PostedAt
	if loc, err := time.LoadLocation("Europe/Oslo"); err == nil {
		entered = entered.In(loc)
	}
	entryDay := time.Date(entered.Year(), entered.Month(), entered.Day(), 0, 0, 0, 0, time.UTC)
	t := saft.Transaction{ID: display(v.FiscalYear, v.Number), Period: sel.period(v.VoucherDate), PeriodYear: int(v.FiscalYear),
		Date: v.VoucherDate, VoucherType: v.Kind, VoucherDescription: g.word(words[0], words[1]), Description: v.Description,
		SystemEntry: entryDay, GLPosting: entryDay, SystemID: strconv.FormatInt(v.ID, 10)}
	source := ""
	if v.SourceModule != nil && v.SourceKind != nil && v.SourceID != nil {
		source = fmt.Sprintf("%s:%s:%d", *v.SourceModule, *v.SourceKind, *v.SourceID)
	}
	for _, p := range postings {
		amount := mustRat(p.Amount)
		l := saft.Line{RecordID: strconv.Itoa(int(p.LineNo)), AccountID: p.AccountNumber, Description: firstNonEmpty(p.Description, v.Description),
			Amount: amount, ReferenceNumber: p.DocumentNumber, DueDate: p.DueDate, SourceDocumentID: firstNonEmpty(p.DocumentNumber, source)}
		if p.ProjectID != nil {
			l.AnalysisType, l.AnalysisID = "P", strconv.Itoa(int(*p.ProjectID))
		}
		switch p.AccountRole {
		case roleReceivables:
			if p.CustomerID != nil {
				l.CustomerID = customers[*p.CustomerID]
				if l.CustomerID == "" {
					l.CustomerID = strconv.Itoa(int(*p.CustomerID))
				}
			}
		case rolePayables:
			if p.SupplierID != nil {
				l.SupplierID = supplierSaftID(*p.SupplierID, p.SupplierNumber)
			}
		}
		if p.Currency != "" && p.CurrencyAmount != "" {
			l.Currency, l.CurrencyAmount, l.ExchangeRate = p.Currency, mustRat(p.CurrencyAmount), p.ExchangeRate
		}
		if p.TaxCode != "" {
			tax := &saft.TaxInformation{Code: p.TaxCode, Percentage: p.RatePercent}
			if p.TaxRole == "base" {
				tax.Base, tax.Amount = abs(amount), new(big.Rat)
			} else {
				tax.Amount = amount
				if p.TaxBase != "" {
					tax.Base = abs(mustRat(p.TaxBase))
				}
				if l.Currency != "" {
					tax.Currency, tax.CurrencyAmount = l.Currency, l.CurrencyAmount
				}
			}
			l.Tax = tax
		}
		t.Lines = append(t.Lines, l)
	}
	return t
}
