package accounting

// SAF-T import (A25): the way in from another system, as the other way to
// Set up the books. A person with accounting:close uploads a 1.30 or 1.40
// file while the books are not set up; it is read as a stream with no
// DOCTYPE, entity or external reference allowed, kept in the object store
// with its hash, and its master data are stored as a preview. GET reads the
// preview again, matching the customers to Customers by organisation number
// each time; Apply writes everything in one transaction through the same
// seeding Set up the books uses: the settings, the chart from the file, the
// roles, the standard tax codes, the suppliers and the opening voucher from
// the file's closing balances. History is not imported as vouchers: the file
// is the record of the earlier years.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime/multipart"
	"net/http"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/saft"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// saftImportDefaultMax is the largest file without a configuration.
const saftImportDefaultMax = 512 << 20

// accountNumberPattern4to6 is a number layer A takes: 4 to 6 digits in the
// classes 1 to 8.
var importableNumber = regexp.MustCompile(`^[1-8][0-9]{3,5}$`)

func (s *server) saftImportMax() int64 {
	if s.deps.Config != nil && s.deps.Config.AccountingSaftImportMaxBytes > 0 {
		return s.deps.Config.AccountingSaftImportMaxBytes
	}
	return saftImportDefaultMax
}

func tooLargeImport(limit int64) error {
	return invalidField("The file cannot be read", attachmentFormField, codeSaftFileTooLarge,
		fmt.Sprintf("A SAF-T file is at most %d MB here.", limit>>20))
}

// importGate refuses an import once the books are set up or have vouchers:
// an import is the other way to set them up.
func importGate(ctx context.Context, q *store.Queries) error {
	n, err := q.CountVouchers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return conflict(codeSaftLedgerHasVouchers, "The ledger has vouchers",
			"A SAF-T file sets up books that have no vouchers yet; these books have some.")
	}
	if _, err := q.GetSettings(ctx); err == nil {
		return conflict(codeBooksAlreadySetUp, "The books are already set up",
			"A SAF-T file is the other way to set up the books, and these are set up.")
	} else if !noRows(err) {
		return err
	}
	return nil
}

// spool copies the upload's file to a temporary file, at most limit bytes,
// hashing it as it goes.
func spool(mr *multipart.Reader, limit int64) (*os.File, string, string, int64, error) {
	const title = "The file cannot be read"
	if mr == nil {
		return nil, "", "", 0, invalidField(title, attachmentFormField, "", "Send one file in the multipart field file.")
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, "", "", 0, invalidField(title, attachmentFormField, "", "Send one file in the multipart field file.")
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return nil, "", "", 0, tooLargeImport(limit)
			}
			return nil, "", "", 0, invalidField(title, attachmentFormField, "", "The body is not a readable multipart form.")
		}
		if part.FormName() != attachmentFormField {
			_ = part.Close()
			continue
		}
		tmp, err := os.CreateTemp("", "vantigo-saft-*.xml")
		if err != nil {
			return nil, "", "", 0, err
		}
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(part, limit+1))
		_ = part.Close()
		if err != nil || n > limit {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			var tooLarge *http.MaxBytesError
			if n > limit || errors.As(err, &tooLarge) {
				return nil, "", "", 0, tooLargeImport(limit)
			}
			return nil, "", "", 0, invalidField(title, attachmentFormField, "", "The file could not be read.")
		}
		if n == 0 {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			return nil, "", "", 0, invalidField(title, attachmentFormField, "", "The file is empty.")
		}
		return tmp, part.FileName(), hex.EncodeToString(h.Sum(nil)), n, nil
	}
}

// UploadAccountingSaftImport reads a file into a preview and keeps it.
func (s *server) UploadAccountingSaftImport(ctx context.Context, req gen.UploadAccountingSaftImportRequestObject) (gen.UploadAccountingSaftImportResponseObject, error) {
	q := store.New(s.deps.Pool)
	if err := importGate(ctx, q); err != nil {
		return nil, err
	}
	tmp, name, sum, size, err := spool(req.Body, s.saftImportMax())
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	file, err := saft.Read(tmp, saft.Limits{Accounts: saftMaxAccounts, Parties: saftMaxParties})
	if err != nil {
		var inv *saft.InvalidError
		var many *saft.TooManyError
		switch {
		case errors.As(err, &many):
			return nil, invalidField("The file cannot be imported", attachmentFormField, codeSaftFileTooLarge,
				fmt.Sprintf("The file has too many %s: a SAF-T file to set up books from holds at most %d accounts and %d customers and suppliers.",
					strings.TrimPrefix(many.What, "too many "), saftMaxAccounts, saftMaxParties))
		case errors.Is(err, saft.ErrUnsupportedVersion):
			return nil, invalidField("The file cannot be imported", attachmentFormField, codeSaftVersionUnsupported,
				"This is not SAF-T Financial 1.30 or 1.40 ("+err.Error()+"). Export the file again from the other system as version 1.30 or 1.40.")
		case errors.As(err, &inv):
			return nil, invalidField("The file cannot be imported", attachmentFormField, codeSaftFileInvalid,
				"This is not a SAF-T Financial file Vantigo can read: "+inv.Reason+".")
		}
		return nil, err
	}
	customers, suppliers := file.Customers, file.Suppliers
	file.Customers, file.Suppliers = nil, nil
	preview, err := json.Marshal(file)
	if err != nil {
		return nil, err
	}
	st, err := s.objectStore()
	if err != nil || !s.storageConfigured {
		if err == nil {
			err = storage.ErrNotConfigured
		}
		return nil, storageRefusal(err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	key := "saft-imports/" + uuid.New().String() + ".xml"
	if err := st.Put(ctx, key, tmp, "application/xml"); err != nil {
		s.logger().ErrorContext(ctx, "accounting: a SAF-T file could not be written to the store", "error", err.Error())
		return nil, storageRefusal(err)
	}
	var id int64
	err = s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		var err error
		if id, err = q.InsertSaftImport(ctx, store.InsertSaftImportParams{FileName: cleanFileName(name), ObjectKey: key,
			SizeBytes: size, Sha256: sum, AuditFileVersion: file.Version, SelectionStart: file.SelectionStart,
			SelectionEnd: file.SelectionEnd, Preview: preview, Now: s.now(), UserID: callerID(ctx)}); err != nil {
			return err
		}
		if err := storeParties(ctx, q, id, "customer", customers); err != nil {
			return err
		}
		return storeParties(ctx, q, id, "supplier", suppliers)
	})
	if err == nil {
		// The customers are matched once, now, with no transaction open.
		var row store.AccountingSaftImport
		if row, err = q.GetSaftImport(ctx, id); err == nil {
			err = s.matchCustomers(ctx, row)
		}
	}
	if err != nil {
		if derr := st.Delete(context.WithoutCancel(ctx), key); derr != nil {
			s.logger().ErrorContext(ctx, "accounting: an orphaned SAF-T object could not be removed", "error", derr.Error())
		}
		return nil, err
	}
	s.logger().InfoContext(ctx, "accounting: a SAF-T file was uploaded", "import_id", id, "user_id", callerID(ctx),
		"version", file.Version, "sha256", sum, "bytes", size)
	view, err := s.importView(ctx, id, nil)
	if err != nil {
		return nil, err
	}
	return gen.UploadAccountingSaftImport201JSONResponse(view), nil
}

// GetAccountingSaftImport is an import's preview, matched again.
func (s *server) GetAccountingSaftImport(ctx context.Context, req gen.GetAccountingSaftImportRequestObject) (gen.GetAccountingSaftImportResponseObject, error) {
	view, err := s.importView(ctx, req.Id, nil)
	if err != nil {
		return nil, err
	}
	return gen.GetAccountingSaftImport200JSONResponse(view), nil
}

// ListAccountingSaftImports is the files uploaded, newest first.
func (s *server) ListAccountingSaftImports(ctx context.Context, _ gen.ListAccountingSaftImportsRequestObject) (gen.ListAccountingSaftImportsResponseObject, error) {
	rows, err := store.New(s.deps.Pool).ListSaftImports(ctx)
	if err != nil {
		return nil, err
	}
	out := gen.ListAccountingSaftImports200JSONResponse{Items: []gen.AccountingSaftImportSummary{}}
	for _, r := range rows {
		out.Items = append(out.Items, gen.AccountingSaftImportSummary{Id: r.ID, State: r.State, FileName: r.FileName,
			SizeBytes: r.SizeBytes, Sha256: r.Sha256, AuditFileVersion: r.AuditFileVersion,
			SelectionStart: apiDate(r.SelectionStart), SelectionEnd: apiDate(r.SelectionEnd), UploadedAt: r.UploadedAt,
			AppliedAt: r.AppliedAt, OpeningVoucherId: r.OpeningVoucherID})
	}
	return out, nil
}

type saftImportDownload struct{ saftDownload }

func (r saftImportDownload) VisitDownloadAccountingSaftImportFileResponse(w http.ResponseWriter) error {
	return r.visit(w)
}

// DownloadAccountingSaftImportFile streams the file as uploaded.
func (s *server) DownloadAccountingSaftImportFile(ctx context.Context, req gen.DownloadAccountingSaftImportFileRequestObject) (gen.DownloadAccountingSaftImportFileResponseObject, error) {
	row, err := store.New(s.deps.Pool).GetSaftImport(ctx, req.Id)
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
	body, err := st.Get(ctx, row.ObjectKey)
	if err != nil {
		return nil, storageRefusal(err)
	}
	return saftImportDownload{saftDownload{name: row.FileName, body: body, size: row.SizeBytes}}, nil
}

// importLine is a line of the opening voucher before the accounts and the
// suppliers it names have ids: its account by number, its supplier by its
// place in the plan's suppliers plus one.
type importLine struct {
	number   string
	supplier int
	line     Line
}

// importAccount is a file account as the plan takes it.
type importAccount struct {
	fileID, name, number, kind string
	category, code             string
	closing                    *big.Rat
	action, problem            string
}

// importParty is a file customer or supplier as the plan takes it.
type importParty struct {
	position int32
	file     saft.FileParty
	closing  *big.Rat
	// accounts are the file accounts its balances are on.
	accounts []string
	// customer: the match.
	match        string
	customerID   *int32
	customerName *string
	customerOrg  *string
	// supplier: what is kept, and what was dropped.
	fields supplierFields
	notes  []string
	org    *string
}

// importPlan is everything Apply writes, and what stands in its way.
type importPlan struct {
	row                       store.AccountingSaftImport
	month                     int32
	monthGiven, needsMonth    bool
	selectionStart            time.Time
	selectionEnd              time.Time
	midYear, companyMismatch  bool
	sellerOrg                 string
	file                      saft.File
	booksStart                time.Time
	year                      int
	accounts                  []importAccount
	added                     []seedAccount
	addedReason               map[string]string
	roles                     map[string]string
	customers, suppliers      []importParty
	receivablesNo, payablesNo string
	lines                     []gen.AccountingSaftImportOpeningLine
	opening                   []importLine
	result, vatMoved          *big.Rat
	recvDiff, payDiff         *big.Rat
	blockers                  []string
	// blockCode is the refusal Apply answers while blockers stand: the first
	// that applies.
	blockCode string
	notes     []string
	taxCodes  []gen.AccountingSaftImportTaxCode
}

func (p *importPlan) block(code, text string) {
	p.blockers = append(p.blockers, text)
	if p.blockCode == "" {
		p.blockCode = code
	}
}

// loadImport reads an import's row and its stored master data.
func loadImport(row store.AccountingSaftImport) (saft.File, error) {
	var f saft.File
	if err := json.Unmarshal(row.Preview, &f); err != nil {
		return f, fmt.Errorf("accounting: read import %d's preview: %w", row.ID, err)
	}
	return f, nil
}

// signedRat is a stored signed amount.
func signedRat(s string) *big.Rat {
	r, ok := saft.Amount(s)
	if !ok {
		return new(big.Rat)
	}
	return r
}

// plan builds what applying the file would write, with the overrides of an
// apply's body; the customers are matched through Customers here, with no
// transaction open.
func (s *server) plan(ctx context.Context, row store.AccountingSaftImport, body *gen.AccountingSaftImportApply) (*importPlan, error) {
	file, err := loadImport(row)
	if err != nil {
		return nil, err
	}
	language := "nb"
	if body != nil {
		language = string(body.Language)
	}
	p := &importPlan{row: row, file: file, roles: map[string]string{},
		addedReason: map[string]string{}, result: new(big.Rat), vatMoved: new(big.Rat), recvDiff: new(big.Rat), payDiff: new(big.Rat)}
	word := func(en, nb string) string {
		if language == "en" {
			return en
		}
		return nb
	}
	// The fiscal year's first month: as the apply says; else, from the file —
	// the selection's first month when it is twelve months, January
	// otherwise. A file that names its selection by periods says nothing of
	// the days they are until the month is known: Apply asks for it.
	p.month = 1
	start, end := file.SelectionStart, file.SelectionEnd
	if !file.SelectionByPeriod && start.Day() == 1 && start.AddDate(1, 0, -1).Equal(end) {
		p.month = int32(start.Month())
	}
	if file.SelectionByPeriod && file.PeriodStart == 1 && file.PeriodEnd == 12 && file.PeriodStartYear == file.PeriodEndYear {
		p.month = 1
	}
	if body != nil && body.FiscalYearStartMonth != nil {
		p.month = *body.FiscalYearStartMonth
		p.monthGiven = true
	}
	if file.SelectionByPeriod {
		p.needsMonth = true
		fy := func(year, period int) time.Time {
			return time.Date(year, time.Month(p.month), 1, 0, 0, 0, 0, time.UTC).AddDate(0, period-1, 0)
		}
		start = fy(file.PeriodStartYear, max(1, min(file.PeriodStart, 12)))
		end = fy(file.PeriodEndYear, max(1, min(file.PeriodEnd, 12))).AddDate(0, 1, -1)
		p.notes = append(p.notes, fmt.Sprintf("The file names its selection by periods %d/%d to %d/%d: with the fiscal year starting in month %d they are %s to %s. Confirm the month before applying.",
			file.PeriodStart, file.PeriodStartYear, file.PeriodEnd, file.PeriodEndYear, p.month, start.Format(time.DateOnly), end.Format(time.DateOnly)))
	}
	p.selectionStart, p.selectionEnd = start, end
	p.booksStart = end.AddDate(0, 0, 1)
	p.year = p.booksStart.Year()
	p.midYear = !(p.booksStart.Day() == 1 && int32(p.booksStart.Month()) == p.month)
	if p.midYear {
		p.notes = append(p.notes, "The books start inside a fiscal year: the income and expense accounts open with their year-to-date balances, and no result goes to equity.")
	} else {
		p.notes = append(p.notes, "The books start a fiscal year: the income and expense accounts open at zero, and the file's result goes to the opening balance account in equity.")
	}
	if p.booksStart.Before(firstRateDay) {
		p.block(codeBooksStartTooEarly, fmt.Sprintf("The books would start on %s; they start on %s or later.",
			p.booksStart.Format(time.DateOnly), firstRateDay.Format(time.DateOnly)))
	}
	// The file's company against the seller in Invoices.
	if _, org, err := sellerOf(ctx, s.deps.SalesTax); err != nil {
		return nil, err
	} else if org != "" {
		p.sellerOrg = org
		if fileOrg := digitsOnly(file.CompanyNumber); fileOrg != "" && fileOrg != org {
			p.companyMismatch = true
			if body == nil || body.ConfirmOtherCompany == nil || !*body.ConfirmOtherCompany {
				p.block(codeSaftOtherCompany, fmt.Sprintf("The file is %s's (%s), and the company in Invoices is %s: confirm that these books are to start from another company's file.",
					file.CompanyName, fileOrg, org))
			}
		}
	}
	p.notes = append(p.notes, fmt.Sprintf("The file's %d transactions are not imported as vouchers: the file is kept as the record of those years, and the law asks you to keep it.", file.Entries))
	list, err := groupingsFor(p.year)
	if err != nil {
		return nil, err
	}

	// The accounts.
	var overrides map[string]string
	if body != nil && body.Accounts != nil {
		overrides = *body.Accounts
	}
	taken := map[string]string{}
	total := new(big.Rat)
	onlyStandard := 0
	for _, fa := range file.Accounts {
		a := importAccount{fileID: fa.ID, name: strings.TrimSpace(fa.Description), closing: signedRat(fa.Closing)}
		total.Add(total, a.closing)
		if a.name == "" {
			a.name = fa.ID
		}
		if len([]rune(a.name)) > 200 {
			a.name = string([]rune(a.name)[:200])
		}
		number, overridden := overrides[fa.ID]
		switch {
		case overridden && number == "":
			a.action = "leave_out"
		case overridden:
			a.number = strings.TrimSpace(number)
			if !importableNumber.MatchString(a.number) {
				a.action, a.problem, a.number = "needs_number", "The number given, "+number+", is not 4 to 6 digits in the classes 1 to 8.", ""
			}
		case importableNumber.MatchString(fa.ID):
			a.number = fa.ID
		default:
			digits := digitsOnly(fa.ID)
			if importableNumber.MatchString(digits) && digits != fa.ID {
				a.number = digits
				a.problem = "The number " + fa.ID + " is not 4 to 6 digits; it is taken as " + digits + "."
			} else if a.closing.Sign() == 0 {
				a.action = "leave_out"
				a.problem = "The number " + fa.ID + " does not fit the chart (4 to 6 digits in the classes 1 to 8) and has no balance: it is left out."
			} else {
				a.action = "needs_number"
				a.problem = "The number " + fa.ID + " does not fit the chart (4 to 6 digits in the classes 1 to 8) and has a balance: give it a number."
			}
		}
		if a.number != "" {
			if other, dup := taken[a.number]; dup {
				a.action, a.problem = "needs_number", "The number "+a.number+" is taken by "+other+": give this one another."
				a.number = ""
			}
		}
		if a.number != "" && a.action == "" {
			a.action = "import"
			taken[a.number] = fa.ID
			a.kind = kindOf(a.number)
			switch {
			case fa.GroupingCode != "" && fa.GroupingCode != "NA" && validGrouping(p.year, fa.GroupingCategory, fa.GroupingCode):
				a.category, a.code = fa.GroupingCategory, fa.GroupingCode
			case fa.GroupingCode != "" && fa.GroupingCode != "NA":
				a.problem = strings.TrimSpace(a.problem + fmt.Sprintf(" Its grouping %s %s is not in the %d list: it has none; choose one in the chart.",
					fa.GroupingCategory, fa.GroupingCode, list.year))
			case fa.StandardAccountID != "":
				onlyStandard++
			}
		}
		// An income or expense account's balance is part of the result, which
		// the opening carries whole; a balance-sheet account's balance is not
		// dropped.
		if a.action == "leave_out" && a.closing.Sign() != 0 {
			if k := kindOfID(fa.ID); k != "income" && k != "expense" {
				a.action = "needs_number"
				a.problem = "Account " + fa.ID + " has a balance of " + money(a.closing) + ": it cannot be left out."
			}
		}
		if a.action == "needs_number" {
			p.block(codeSaftImportUnresolved, a.problem)
		}
		p.accounts = append(p.accounts, a)
	}
	if onlyStandard > 0 {
		p.notes = append(p.notes, fmt.Sprintf("%d accounts carry only the old StandardAccountID and no grouping code: they have none; choose one in the chart.", onlyStandard))
	}
	if total.Sign() != 0 {
		p.block(codeSaftImportUnresolved, "The file's closing balances do not sum to zero (the difference is "+money(total)+").")
	}
	byFileID := map[string]*importAccount{}
	byNumber := map[string]*importAccount{}
	for i := range p.accounts {
		byFileID[p.accounts[i].fileID] = &p.accounts[i]
		if p.accounts[i].action == "import" {
			byNumber[p.accounts[i].number] = &p.accounts[i]
		}
	}

	// The parties, from their table, and their control accounts.
	load := func(kind string) ([]importParty, error) {
		rows, err := store.New(s.deps.Pool).SaftImportParties(ctx, store.SaftImportPartiesParams{ImportID: row.ID, Kind: kind})
		if err != nil {
			return nil, err
		}
		out := make([]importParty, 0, len(rows))
		for _, r := range rows {
			var fp saft.FileParty
			if err := json.Unmarshal(r.Party, &fp); err != nil {
				return nil, fmt.Errorf("accounting: read import %d's party: %w", row.ID, err)
			}
			out = append(out, importParty{position: r.Position, file: fp, match: r.Match, customerID: r.CustomerID,
				customerName: r.CustomerName, customerOrg: r.CustomerOrg})
		}
		return out, nil
	}
	customers, err := load("customer")
	if err != nil {
		return nil, err
	}
	suppliers, err := load("supplier")
	if err != nil {
		return nil, err
	}
	control := func(parties []importParty, customer bool) ([]importParty, string, error) {
		out := make([]importParty, 0, len(parties))
		accounts := map[string]bool{}
		for _, ip := range parties {
			fp := ip.file
			ip.closing = new(big.Rat)
			for _, b := range fp.Balances {
				c := signedRat(b.Closing)
				if c.Sign() == 0 {
					continue
				}
				ip.closing.Add(ip.closing, c)
				if b.AccountID != "" && !slices.Contains(ip.accounts, b.AccountID) {
					ip.accounts = append(ip.accounts, b.AccountID)
					accounts[b.AccountID] = true
				}
			}
			out = append(out, ip)
		}
		var ids []string
		for id := range accounts {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		who := word("suppliers", "leverandørene")
		if customer {
			who = word("customers", "kundene")
		}
		if len(ids) > 1 {
			p.block(codeSaftImportUnresolved, fmt.Sprintf("The %s' balances are on several accounts (%s); these books keep one control account. Move them onto one in the other system and export again.", who, strings.Join(ids, ", ")))
		}
		if len(ids) == 0 {
			return out, "", nil
		}
		a := byFileID[ids[0]]
		if a == nil || a.action != "import" {
			p.block(codeSaftImportUnresolved, "The "+who+"' account "+ids[0]+" is not among the accounts taken.")
			return out, "", nil
		}
		return out, a.number, nil
	}
	if p.customers, p.receivablesNo, err = control(customers, true); err != nil {
		return nil, err
	}
	if p.suppliers, p.payablesNo, err = control(suppliers, false); err != nil {
		return nil, err
	}

	// The roles: by the parties' control accounts, by the default chart's
	// numbers, or as the apply says; a role the file has no account for adds
	// the default chart's.
	var roleOverrides map[string]string
	if body != nil && body.Roles != nil {
		roleOverrides = *body.Roles
	}
	defaultRole := map[string]string{}
	for _, a := range defaultChart {
		if a.role != "" {
			defaultRole[a.role] = a.number
		}
	}
	addDefault := func(number, reason string) (bool, error) {
		if byNumber[number] != nil {
			return true, nil
		}
		for _, a := range p.added {
			if a.number == number {
				return true, nil
			}
		}
		acc, ok, err := defaultAccount(number, language, list)
		if err != nil || !ok {
			return false, err
		}
		acc.role = ""
		p.added = append(p.added, acc)
		p.addedReason[number] = reason
		return true, nil
	}
	held := map[string]string{}
	for _, role := range systemRoles {
		number := defaultRole[role]
		if role == roleReceivables && p.receivablesNo != "" {
			number = p.receivablesNo
		}
		if role == rolePayables && p.payablesNo != "" {
			number = p.payablesNo
		}
		if o, ok := roleOverrides[role]; ok && strings.TrimSpace(o) != "" {
			number = strings.TrimSpace(o)
			if byNumber[number] == nil {
				p.block(codeAccountUnknown, "The role "+role+" is to be on account "+number+", which the chart will not have.")
				continue
			}
		}
		if ok, err := addDefault(number, word("the role ", "rollen ")+role); err != nil {
			return nil, err
		} else if !ok {
			p.block(codeAccountUnknown, "No account for the role "+role+".")
			continue
		}
		if kindOf(number) != roleKinds[role] {
			p.block(codeAccountKindMismatch, "The role "+role+" sits on an account of kind "+roleKinds[role]+"; "+number+" is "+kindOf(number)+".")
		}
		if other, dup := held[number]; dup {
			p.block(codeSaftImportUnresolved, "Account "+number+" would hold both "+other+" and "+role+"; an account holds one role.")
		}
		held[number] = role
		p.roles[role] = number
	}
	if p.receivablesNo != "" && p.roles[roleReceivables] != p.receivablesNo {
		p.block(codeSaftImportUnresolved, "The customers' balances are on "+p.receivablesNo+"; receivables must be on that account.")
	}
	if p.payablesNo != "" && p.roles[rolePayables] != p.payablesNo {
		p.block(codeSaftImportUnresolved, "The suppliers' balances are on "+p.payablesNo+"; payables must be on that account.")
	}
	// The tax codes' VAT accounts.
	vatNumbers := taxCodeAccountNumbers()
	for _, number := range sortedKeys(vatNumbers) {
		if _, err := addDefault(number, word("the tax codes' VAT accounts", "mva-kodenes kontoer")); err != nil {
			return nil, err
		}
	}

	// The tax table, mapped to the standard codes by StandardTaxCode.
	standard := map[string]bool{}
	for _, t := range standardTaxCodes {
		standard[t.code] = true
	}
	unmapped := 0
	for _, t := range file.TaxCodes {
		v := gen.AccountingSaftImportTaxCode{FileTaxCode: t.Code, Description: t.Description, StandardTaxCode: t.StandardCode}
		if t.Percentage != "" {
			v.Percentage = ptr(t.Percentage)
		}
		if standard[t.StandardCode] {
			v.Code = ptr(t.StandardCode)
		} else {
			unmapped++
		}
		p.taxCodes = append(p.taxCodes, v)
	}
	if unmapped > 0 {
		p.notes = append(p.notes, fmt.Sprintf("%d of the file's tax codes map to no standard code; the books use Skatteetaten's standard codes, so choose the code on each new entry.", unmapped))
	}

	// The suppliers, made from the file.
	orgs := map[string]bool{}
	for i := range p.suppliers {
		sp := &p.suppliers[i]
		fp := sp.file
		f := supplierFields{name: strings.TrimSpace(fp.Name), country: "NO", currency: currency, paymentTermsDays: 14, active: true}
		if f.name == "" {
			f.name = word("Supplier ", "Leverandør ") + fp.ID
		}
		if len([]rune(f.name)) > 200 {
			f.name = string([]rune(f.name)[:200])
		}
		if c := countryCode(fp.Country); c != "" {
			f.country = c
		}
		if org := digitsOnly(fp.RegistrationNumber); fp.RegistrationNumber != "" {
			switch {
			case f.country == "NO" && validOrganisationNumber(org) && !orgs[org]:
				f.organisationNumber = ptr(org)
				orgs[org] = true
			case orgs[org]:
				sp.notes = append(sp.notes, "Its organisation number is another supplier's in the file; it is left out.")
			default:
				sp.notes = append(sp.notes, "Its registration number "+fp.RegistrationNumber+" is not a Norwegian organisation number; it is left out.")
			}
		}
		if line := strings.TrimSpace(fp.Street + " " + fp.Number); line != "" {
			f.addressLine = ptr(clipText(line, 200))
		}
		if fp.PostalCode != "" {
			f.postalCode = ptr(clipText(fp.PostalCode, 20))
		}
		if fp.City != "" {
			f.city = ptr(clipText(fp.City, 100))
		}
		if fp.Email != "" && strings.Contains(fp.Email, "@") {
			f.email = ptr(clipText(fp.Email, 200))
		}
		if b := digitsOnly(fp.BankAccount); fp.BankAccount != "" {
			if validBankAccount(b) {
				f.bankAccount = ptr(b)
			} else {
				sp.notes = append(sp.notes, "Its bank account "+fp.BankAccount+" is not eleven digits passing mod 11; it is left out.")
			}
		}
		if fp.IBAN != "" {
			f.iban = ptr(clipText(strings.ReplaceAll(fp.IBAN, " ", ""), 34))
		}
		if fp.BIC != "" {
			f.bic = ptr(clipText(fp.BIC, 11))
		}
		sp.fields = f
	}

	// The customers, as matched at the upload or linked by a person since.
	for _, c := range p.customers {
		if c.closing.Sign() != 0 && c.match != "matched" && c.match != "linked" {
			p.block(codeSaftUnmatchedCustomers, fmt.Sprintf("Customer %s %s has a balance of %s and is %s in Customers: create or find it there, and link it.",
				c.file.ID, c.file.Name, money(c.closing), c.match))
		}
	}

	// The opening voucher: the balance-sheet accounts' closing balances, the
	// parties' on the control accounts, the VAT accounts' on the settlement
	// account, and the result to the opening account.
	sum := new(big.Rat)
	addLine := func(number string, amount *big.Rat, text string, party *string, supplier int, l Line) {
		if amount.Sign() == 0 {
			return
		}
		sum.Add(sum, amount)
		l.Amount = new(big.Rat).Set(amount)
		l.Text = text
		p.opening = append(p.opening, importLine{number: number, supplier: supplier, line: l})
		p.lines = append(p.lines, gen.AccountingSaftImportOpeningLine{AccountNumber: number, Amount: money(amount), Text: text, Party: party})
	}
	vatAccount := p.roles["vat_settlement"]
	controlBalance := map[string]*big.Rat{}
	for _, a := range p.accounts {
		if a.action != "import" || a.closing.Sign() == 0 {
			continue
		}
		// Inside a fiscal year the income and expense accounts open with
		// their year-to-date balances; at its start they open at zero, and
		// their sum is the result the opening account takes.
		if !isBalanceKind(a.kind) {
			if p.midYear {
				addLine(a.number, a.closing, a.name, nil, 0, Line{})
			}
			continue
		}
		switch {
		case a.number == p.receivablesNo || a.number == p.payablesNo:
			controlBalance[a.number] = a.closing
		case vatNumbers[a.number]:
			p.vatMoved.Add(p.vatMoved, a.closing)
		default:
			addLine(a.number, a.closing, a.name, nil, 0, Line{})
		}
	}
	if p.vatMoved.Sign() != 0 {
		text := word("VAT owed at the start, from the VAT accounts", "Mva ved start, fra mva-kontoene")
		addLine(vatAccount, p.vatMoved, text, nil, 0, Line{})
		p.notes = append(p.notes, fmt.Sprintf("VAT owed at the start: NOK %s, placed on %s.", money(neg(p.vatMoved)), vatAccount))
	}
	docNumber := word("Opening balance", "Inngående saldo")
	docDate := p.selectionEnd
	partySum := func(parties []importParty, number string) *big.Rat {
		total := new(big.Rat)
		for _, ip := range parties {
			total.Add(total, ip.closing)
		}
		diff := new(big.Rat).Set(total)
		if c, ok := controlBalance[number]; ok {
			diff.Sub(diff, c)
		}
		return diff
	}
	p.recvDiff = partySum(p.customers, p.receivablesNo)
	p.payDiff = partySum(p.suppliers, p.payablesNo)
	if p.receivablesNo == "" {
		p.recvDiff = new(big.Rat)
		for _, ip := range p.customers {
			p.recvDiff.Add(p.recvDiff, ip.closing)
		}
	}
	if p.payablesNo == "" {
		p.payDiff = new(big.Rat)
		for _, ip := range p.suppliers {
			p.payDiff.Add(p.payDiff, ip.closing)
		}
	}
	if p.recvDiff.Sign() != 0 {
		p.block(codeSaftPartiesMismatch, "The customers' balances differ from the receivables account's by "+money(p.recvDiff)+".")
	}
	if p.payDiff.Sign() != 0 {
		p.block(codeSaftPartiesMismatch, "The suppliers' balances differ from the payables account's by "+money(p.payDiff)+".")
	}
	for _, c := range p.customers {
		if c.closing.Sign() == 0 {
			continue
		}
		name := c.file.Name
		addLine(p.roles[roleReceivables], c.closing, docNumber+" "+c.file.ID, &name, 0,
			Line{CustomerID: c.customerID,
				Item: &OpenItemSpec{DocumentNumber: clipText(docNumber+" "+c.file.ID, 40), DocumentDate: &docDate}})
	}
	for i, sp := range p.suppliers {
		if sp.closing.Sign() == 0 {
			continue
		}
		name := sp.fields.name
		addLine(p.roles[rolePayables], sp.closing, docNumber+" "+sp.file.ID, &name, i+1,
			Line{
				Item: &OpenItemSpec{DocumentNumber: clipText(docNumber+" "+sp.file.ID, 40), DocumentDate: &docDate}})
	}
	p.result = neg(sum)
	if p.result.Sign() != 0 {
		addLine(p.roles["opening"], p.result, word("The result for the period before the books", "Resultat før bøkene starter"), nil, 0, Line{})
	}
	return p, nil
}

// isBalanceKind reports whether kind is a balance-sheet account's.
func isBalanceKind(kind string) bool {
	return kind == "asset" || kind == "liability" || kind == "equity"
}

// kindOfID is the kind a file's account number's class gives, "" when it
// names no class.
func kindOfID(id string) string {
	if id == "" || id[0] < '1' || id[0] > '8' {
		return ""
	}
	return kindOf(id)
}

func clipText(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// saftMaxAccounts and saftMaxParties bound a file an import takes: a company
// has fewer, and a file past them is refused rather than held (M6).
const (
	saftMaxAccounts = 20_000
	saftMaxParties  = 200_000
	// saftPreviewParties is how many customers and suppliers a preview
	// lists; the rest are paged.
	saftPreviewParties = 200
	// saftPreviewBlockers is how many blockers a preview lists.
	saftPreviewBlockers = 100
	// saftMatchBatch is how many organisation numbers one directory read
	// takes, and how many matches one write records.
	saftMatchBatch = 1000
)

// storeParties writes a file's customers and suppliers to their table, a
// page at a time, on the upload's transaction.
func storeParties(ctx context.Context, q *store.Queries, id int64, kind string, parties []saft.FileParty) error {
	for start := 0; start < len(parties); start += saftMatchBatch {
		page := parties[start:min(start+saftMatchBatch, len(parties))]
		arg := store.InsertSaftImportPartiesParams{ImportID: id, Kind: kind}
		for i, fp := range page {
			data, err := json.Marshal(fp)
			if err != nil {
				return err
			}
			closing := new(big.Rat)
			for _, b := range fp.Balances {
				closing.Add(closing, signedRat(b.Closing))
			}
			arg.Positions = append(arg.Positions, int32(start+i+1))
			arg.FileIds = append(arg.FileIds, clipText(fp.ID, 70))
			arg.Names = append(arg.Names, clipText(fp.Name, 256))
			arg.Parties = append(arg.Parties, data)
			arg.Closings = append(arg.Closings, closing.FloatString(2))
		}
		if err := q.InsertSaftImportParties(ctx, arg); err != nil {
			return err
		}
	}
	return nil
}

// matchCustomers matches an import's customers to Customers by organisation
// number and records it: once at the upload, and again when a person asks.
// A link a person made is kept. Accounting writes no customer.
func (s *server) matchCustomers(ctx context.Context, row store.AccountingSaftImport) error {
	q := store.New(s.deps.Pool)
	rows, err := q.SaftImportParties(ctx, store.SaftImportPartiesParams{ImportID: row.ID, Kind: "customer"})
	if err != nil {
		return err
	}
	type holder struct {
		id        int32
		name, org string
	}
	holders := map[string][]holder{}
	var orgs []string
	for _, r := range rows {
		if r.Match == "linked" {
			continue
		}
		var fp saft.FileParty
		if err := json.Unmarshal(r.Party, &fp); err != nil {
			return err
		}
		if org := digitsOnly(fp.RegistrationNumber); validOrganisationNumber(org) {
			orgs = append(orgs, org)
		}
	}
	if s.deps.Directory != nil {
		for start := 0; start < len(orgs); start += saftMatchBatch {
			found, err := s.deps.Directory.CustomersByLegalID(ctx, "no", orgs[start:min(start+saftMatchBatch, len(orgs))])
			if err != nil {
				return fmt.Errorf("accounting: match the customers: %w", err)
			}
			for org, entries := range found {
				for _, e := range entries {
					id, name := e.ID, e.Name
					if e.MergedInto != nil {
						id = *e.MergedInto
					}
					holders[org] = append(holders[org], holder{id: id, name: name, org: org})
				}
			}
		}
	}
	arg := store.SetSaftImportMatchesParams{ImportID: row.ID}
	flush := func() error {
		if len(arg.Positions) == 0 {
			return nil
		}
		if err := q.SetSaftImportMatches(ctx, arg); err != nil {
			return err
		}
		arg = store.SetSaftImportMatchesParams{ImportID: row.ID}
		return nil
	}
	for _, r := range rows {
		if r.Match == "linked" {
			continue
		}
		var fp saft.FileParty
		if err := json.Unmarshal(r.Party, &fp); err != nil {
			return err
		}
		match, id, name, org := "unmatched", int32(0), "", ""
		hs := holders[digitsOnly(fp.RegistrationNumber)]
		switch {
		case len(hs) == 1:
			match, id, name, org = "matched", hs[0].id, hs[0].name, hs[0].org
		case mustRat(r.Closing).Sign() == 0:
			match = "not_needed"
		case len(hs) > 1:
			match = "ambiguous"
		}
		arg.Positions = append(arg.Positions, r.Position)
		arg.Matches = append(arg.Matches, match)
		arg.CustomerIds = append(arg.CustomerIds, id)
		arg.CustomerNames = append(arg.CustomerNames, name)
		arg.CustomerOrgs = append(arg.CustomerOrgs, org)
		if len(arg.Positions) == saftMatchBatch {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	return q.SetSaftImportMatchedAt(ctx, store.SetSaftImportMatchedAtParams{ID: row.ID, Now: ptr(s.now())})
}

// importView is an import as the contract answers it: its plan, with the
// first customers and suppliers and the first blockers; the rest are paged.
func (s *server) importView(ctx context.Context, id int64, body *gen.AccountingSaftImportApply) (gen.AccountingSaftImport, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetSaftImport(ctx, id)
	if noRows(err) {
		return gen.AccountingSaftImport{}, notFound()
	}
	if err != nil {
		return gen.AccountingSaftImport{}, err
	}
	p, err := s.plan(ctx, row, body)
	if err != nil {
		return gen.AccountingSaftImport{}, err
	}
	return p.view(), nil
}

func customerView(c importParty) gen.AccountingSaftImportCustomer {
	match := c.match
	if match == "" {
		match = "unmatched"
	}
	org := (*string)(nil)
	if o := digitsOnly(c.file.RegistrationNumber); o != "" {
		org = ptr(o)
	}
	closing := c.closing
	if closing == nil {
		closing = new(big.Rat)
		for _, b := range c.file.Balances {
			closing.Add(closing, signedRat(b.Closing))
		}
	}
	return gen.AccountingSaftImportCustomer{Position: c.position, FileCustomerId: c.file.ID, Name: c.file.Name,
		OrganisationNumber: org, ClosingBalance: money(closing), Match: gen.AccountingSaftImportCustomerMatch(match),
		CustomerId: c.customerID, CustomerName: c.customerName, CustomerOrganisationNumber: c.customerOrg}
}

func importSupplierView(sp importParty) gen.AccountingSaftImportSupplier {
	notes := sp.notes
	if notes == nil {
		notes = []string{}
	}
	return gen.AccountingSaftImportSupplier{Position: sp.position, FileSupplierId: sp.file.ID, Name: sp.fields.name,
		OrganisationNumber: sp.fields.organisationNumber, City: sp.fields.city, BankAccount: sp.fields.bankAccount,
		Iban: sp.fields.iban, ClosingBalance: money(sp.closing), Notes: notes}
}

func (p *importPlan) view() gen.AccountingSaftImport {
	f := p.file
	out := gen.AccountingSaftImport{Id: p.row.ID, State: gen.AccountingSaftImportState(p.row.State), FileName: p.row.FileName,
		SizeBytes: p.row.SizeBytes, Sha256: p.row.Sha256, UploadedAt: p.row.UploadedAt, AppliedAt: p.row.AppliedAt,
		OpeningVoucherId: p.row.OpeningVoucherID, AuditFileVersion: f.Version, CompanyName: f.CompanyName,
		CompanyOrganisationNumber: f.CompanyNumber, SelectionStart: apiDate(p.selectionStart), SelectionEnd: apiDate(p.selectionEnd),
		BooksStart: apiDate(p.booksStart), FiscalYearStartMonth: p.month, Transactions: f.Entries,
		Notes: p.notes, TaxCodes: p.taxCodes, MatchedAt: p.row.MatchedAt, MidYear: p.midYear,
		NeedsFiscalYearStartMonth: p.needsMonth, CompanyMismatch: p.companyMismatch,
		BlockersTotal: int32(len(p.blockers)), CustomersTotal: int32(len(p.customers)), SuppliersTotal: int32(len(p.suppliers)),
		Accounts: []gen.AccountingSaftImportAccount{}, AddedAccounts: []gen.AccountingSaftImportAddedAccount{},
		Roles: []gen.AccountingSaftImportRole{}, Customers: []gen.AccountingSaftImportCustomer{},
		Suppliers: []gen.AccountingSaftImportSupplier{}}
	if p.sellerOrg != "" {
		out.SellerOrganisationNumber = ptr(p.sellerOrg)
	}
	out.Blockers = p.blockers[:min(len(p.blockers), saftPreviewBlockers)]
	if out.Blockers == nil {
		out.Blockers = []string{}
	}
	if out.Notes == nil {
		out.Notes = []string{}
	}
	if out.TaxCodes == nil {
		out.TaxCodes = []gen.AccountingSaftImportTaxCode{}
	}
	out.CanApply = len(p.blockers) == 0 && p.row.State == "preview"
	for _, a := range p.accounts {
		v := gen.AccountingSaftImportAccount{FileAccountId: a.fileID, Name: a.name, ClosingBalance: money(a.closing),
			Action: gen.AccountingSaftImportAccountAction(a.action)}
		if a.number != "" {
			v.Number = ptr(a.number)
		}
		if a.kind != "" {
			v.Kind = ptr(gen.AccountingAccountKind(a.kind))
		}
		if a.code != "" {
			v.GroupingCategory, v.GroupingCode = ptr(a.category), ptr(a.code)
		}
		if a.problem != "" {
			v.Problem = ptr(a.problem)
		}
		out.Accounts = append(out.Accounts, v)
	}
	added := map[string]bool{}
	for _, a := range p.added {
		added[a.number] = true
		out.AddedAccounts = append(out.AddedAccounts, gen.AccountingSaftImportAddedAccount{Number: a.number, Name: a.name,
			Kind: gen.AccountingAccountKind(a.kind), Reason: p.addedReason[a.number]})
	}
	for _, role := range systemRoles {
		if n, ok := p.roles[role]; ok {
			out.Roles = append(out.Roles, gen.AccountingSaftImportRole{Role: gen.AccountingSystemRole(role), AccountNumber: n, Added: added[n]})
		}
	}
	for _, c := range p.customers[:min(len(p.customers), saftPreviewParties)] {
		out.Customers = append(out.Customers, customerView(c))
	}
	for _, sp := range p.suppliers[:min(len(p.suppliers), saftPreviewParties)] {
		out.Suppliers = append(out.Suppliers, importSupplierView(sp))
	}
	lines := p.lines
	if lines == nil {
		lines = []gen.AccountingSaftImportOpeningLine{}
	}
	out.Opening = gen.AccountingSaftImportOpening{Lines: lines, Result: money(p.result), VatMoved: money(p.vatMoved),
		ReceivablesDifference: money(p.recvDiff), PayablesDifference: money(p.payDiff)}
	if n := p.roles["vat_settlement"]; n != "" {
		out.Opening.VatSettlementAccount = ptr(n)
	}
	return out
}

// pageParams is a page's offset and limit, at most 500.
func pageParams(offset, limit *int32) (int32, int32) {
	o, l := int32(0), int32(100)
	if offset != nil && *offset > 0 {
		o = *offset
	}
	if limit != nil && *limit > 0 {
		l = min(*limit, 500)
	}
	return o, l
}

// ListAccountingSaftImportCustomers is a page of the file's customers.
func (s *server) ListAccountingSaftImportCustomers(ctx context.Context, req gen.ListAccountingSaftImportCustomersRequestObject) (gen.ListAccountingSaftImportCustomersResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetSaftImport(ctx, req.Id); noRows(err) {
		return nil, notFound()
	} else if err != nil {
		return nil, err
	}
	offset, limit := pageParams(req.Params.Offset, req.Params.Limit)
	rows, err := q.PageSaftImportParties(ctx, store.PageSaftImportPartiesParams{ImportID: req.Id, Kind: "customer",
		Attention: req.Params.Attention != nil && *req.Params.Attention, RowOffset: offset, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	out := gen.ListAccountingSaftImportCustomers200JSONResponse{Items: []gen.AccountingSaftImportCustomer{}}
	for _, r := range rows {
		var fp saft.FileParty
		if err := json.Unmarshal(r.Party, &fp); err != nil {
			return nil, err
		}
		out.Total = r.Total
		out.Items = append(out.Items, customerView(importParty{position: r.Position, file: fp, match: r.Match,
			customerID: r.CustomerID, customerName: r.CustomerName, customerOrg: r.CustomerOrg, closing: mustRat(r.Closing)}))
	}
	return out, nil
}

// ListAccountingSaftImportSuppliers is a page of the suppliers Apply makes,
// as the plan makes them.
func (s *server) ListAccountingSaftImportSuppliers(ctx context.Context, req gen.ListAccountingSaftImportSuppliersRequestObject) (gen.ListAccountingSaftImportSuppliersResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetSaftImport(ctx, req.Id)
	if noRows(err) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	p, err := s.plan(ctx, row, nil)
	if err != nil {
		return nil, err
	}
	offset, limit := pageParams(req.Params.Offset, req.Params.Limit)
	out := gen.ListAccountingSaftImportSuppliers200JSONResponse{Items: []gen.AccountingSaftImportSupplier{}, Total: int32(len(p.suppliers))}
	for _, sp := range p.suppliers[min(int(offset), len(p.suppliers)):min(int(offset+limit), len(p.suppliers))] {
		out.Items = append(out.Items, importSupplierView(sp))
	}
	return out, nil
}

// LinkAccountingSaftImportCustomer links a file customer to the customer a
// person picked in Customers: refused when it names none, the one it was
// merged into when it was merged away.
func (s *server) LinkAccountingSaftImportCustomer(ctx context.Context, req gen.LinkAccountingSaftImportCustomerRequestObject) (gen.LinkAccountingSaftImportCustomerResponseObject, error) {
	const title = "The customer cannot be linked"
	if req.Body == nil {
		return nil, invalidField(title, "body", "", "A body is required.")
	}
	q := store.New(s.deps.Pool)
	row, err := q.GetSaftImport(ctx, req.Id)
	if noRows(err) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	if row.State == "applied" {
		return nil, conflict(codeSaftImportApplied, "The file is applied already", "")
	}
	party, err := q.GetSaftImportParty(ctx, store.GetSaftImportPartyParams{ImportID: req.Id, Kind: "customer", Position: req.Position})
	if noRows(err) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	id := req.Body.CustomerId
	resolve := func(id int32) (*contracts.CustomerEntry, error) {
		if s.deps.Directory == nil {
			return nil, nil
		}
		entries, err := s.deps.Directory.Customers(ctx, []int32{id})
		if err != nil || len(entries) == 0 {
			return nil, err
		}
		return &entries[0], nil
	}
	entry, err := resolve(id)
	if err != nil {
		return nil, fmt.Errorf("accounting: read the customer: %w", err)
	}
	if entry != nil && entry.MergedInto != nil {
		if entry, err = resolve(*entry.MergedInto); err != nil {
			return nil, fmt.Errorf("accounting: read the customer: %w", err)
		}
	}
	if entry == nil {
		return nil, invalidField(title, "customerId", codeSaftLinkUnknown, "Customers holds no such customer.")
	}
	org := ""
	if profile, err := s.deps.Directory.BillingProfile(ctx, entry.ID, contracts.BillingProfileOptions{}); err != nil {
		return nil, fmt.Errorf("accounting: read the customer: %w", err)
	} else if profile != nil {
		org = digitsOnly(profile.LegalID)
	}
	if err := q.SetSaftImportMatches(ctx, store.SetSaftImportMatchesParams{ImportID: req.Id, Positions: []int32{party.Position},
		Matches: []string{"linked"}, CustomerIds: []int32{entry.ID}, CustomerNames: []string{entry.Name},
		CustomerOrgs: []string{org}}); err != nil {
		return nil, err
	}
	var fp saft.FileParty
	if err := json.Unmarshal(party.Party, &fp); err != nil {
		return nil, err
	}
	var orgPtr *string
	if org != "" {
		orgPtr = &org
	}
	return gen.LinkAccountingSaftImportCustomer200JSONResponse(customerView(importParty{position: party.Position, file: fp,
		match: "linked", customerID: &entry.ID, customerName: &entry.Name, customerOrg: orgPtr, closing: mustRat(party.Closing)})), nil
}

// MatchAccountingSaftImport matches the customers again, on a person's ask.
func (s *server) MatchAccountingSaftImport(ctx context.Context, req gen.MatchAccountingSaftImportRequestObject) (gen.MatchAccountingSaftImportResponseObject, error) {
	row, err := store.New(s.deps.Pool).GetSaftImport(ctx, req.Id)
	if noRows(err) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	if row.State == "applied" {
		return nil, conflict(codeSaftImportApplied, "The file is applied already", "")
	}
	if err := s.matchCustomers(ctx, row); err != nil {
		return nil, err
	}
	view, err := s.importView(ctx, row.ID, nil)
	if err != nil {
		return nil, err
	}
	return gen.MatchAccountingSaftImport200JSONResponse(view), nil
}

// ApplyAccountingSaftImport sets the books up from the file, in one
// transaction.
func (s *server) ApplyAccountingSaftImport(ctx context.Context, req gen.ApplyAccountingSaftImportRequestObject) (gen.ApplyAccountingSaftImportResponseObject, error) {
	const title = "The books cannot be set up from the file"
	if req.Body == nil {
		return nil, invalidField(title, "body", "", "A body is required.")
	}
	b := *req.Body
	var f fieldErrors
	month := int32(1)
	switch b.VatTerm {
	case gen.AccountingVatTermBimonthly, gen.AccountingVatTermYearly, gen.AccountingVatTermNone:
	default:
		f.add("vatTerm", "bimonthly, yearly or none.")
	}
	switch b.Language {
	case gen.En, gen.Nb:
	default:
		f.add("language", "en or nb.")
	}
	if err := f.refusal(title); err != nil {
		return nil, err
	}
	q := store.New(s.deps.Pool)
	row, err := q.GetSaftImport(ctx, req.Id)
	if noRows(err) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	if row.State == "applied" {
		return nil, conflict(codeSaftImportApplied, "The file is applied already", "")
	}
	if err := importGate(ctx, q); err != nil {
		return nil, err
	}
	// The plan is read with no transaction open; the customers' matches are
	// the stored ones, made at the upload or by a person's link.
	p, err := s.plan(ctx, row, &b)
	if err != nil {
		return nil, err
	}
	month = p.month
	if month < 1 || month > 12 {
		return nil, invalidField(title, "fiscalYearStartMonth", "", "A month, 1 to 12.")
	}
	if p.needsMonth && !p.monthGiven {
		return nil, invalidField(title, "fiscalYearStartMonth", "",
			"The file names its selection by periods: say which month the fiscal year starts in, so the books start on the right day.")
	}
	if b.BooksStart != nil && !day(*b.BooksStart).Equal(p.booksStart) {
		return nil, invalidField(title, "booksStart", codeSaftBooksStartMismatch,
			"The books start the day after the file's selection ends: "+p.booksStart.Format(time.DateOnly)+".")
	}
	if len(p.blockers) > 0 {
		detail := strings.Join(p.blockers, " ")
		switch p.blockCode {
		case codeBooksStartTooEarly, codeAccountKindMismatch, codeAccountUnknown:
			return nil, invalidField(title, "roles", p.blockCode, detail)
		}
		return nil, conflict(p.blockCode, title, detail)
	}
	now := s.now()
	user := callerID(ctx)
	var voucherID *int64
	err = s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		locked, err := q.LockSaftImport(ctx, row.ID)
		if err != nil {
			return err
		}
		if locked.State == "applied" {
			return conflict(codeSaftImportApplied, "The file is applied already", "")
		}
		if n, err := q.CountVouchers(ctx); err != nil {
			return err
		} else if n > 0 {
			return conflict(codeSaftLedgerHasVouchers, "The ledger has vouchers", "")
		}
		if _, err := q.InsertSettings(ctx, store.InsertSettingsParams{BooksStart: p.booksStart, FiscalYearStartMonth: int16(month),
			VatTerm: string(b.VatTerm), ChartLanguage: string(b.Language), Now: now, UserID: user}); noRows(err) {
			return conflict(codeBooksAlreadySetUp, "The books are already set up", "")
		} else if err != nil {
			return err
		}
		// The chart: the file's accounts and the default chart's added ones,
		// each role on its account.
		roleOf := map[string]string{}
		for role, number := range p.roles {
			roleOf[number] = role
		}
		var accounts []seedAccount
		for _, a := range p.accounts {
			if a.action == "import" {
				accounts = append(accounts, seedAccount{number: a.number, name: a.name, kind: a.kind, role: roleOf[a.number],
					category: a.category, code: a.code})
			}
		}
		for _, a := range p.added {
			a.role = roleOf[a.number]
			accounts = append(accounts, a)
		}
		ids, err := seedChart(ctx, q, accounts, p.year, now)
		if err != nil {
			return err
		}
		if err := seedTaxCodes(ctx, q, string(b.Language), ids, accounts, now); err != nil {
			return err
		}
		if err := seedDefaultRules(ctx, q, string(b.Language), now); err != nil {
			return err
		}
		supplierIDs := make([]int64, len(p.suppliers))
		for i, sp := range p.suppliers {
			if supplierIDs[i], err = insertSupplier(ctx, q, sp.fields, now); err != nil {
				return err
			}
		}
		lines := make([]Line, 0, len(p.opening))
		for _, il := range p.opening {
			l := il.line
			l.AccountID = ids[il.number]
			if il.supplier > 0 {
				l.SupplierID = ptr(supplierIDs[il.supplier-1])
			}
			lines = append(lines, l)
		}
		// The opening's own rule: no line on a tax code's VAT or counter
		// account; the plan has put their balances on vat_settlement.
		if err := checkOpeningLines(ctx, q, lines); err != nil {
			return err
		}
		if len(lines) > 0 {
			posted, err := Post(ctx, tx, Voucher{Kind: "opening", Date: p.booksStart,
				Description: fmt.Sprintf("SAF-T %s %s–%s", p.file.CompanyName, p.file.SelectionStart.Format(time.DateOnly), p.file.SelectionEnd.Format(time.DateOnly)),
				PostedBy:    &user, At: now, Lines: lines})
			if err != nil {
				return err
			}
			voucherID = &posted.ID
		}
		return q.MarkSaftImportApplied(ctx, store.MarkSaftImportAppliedParams{ID: row.ID, Now: &now, UserID: &user, OpeningVoucherID: voucherID})
	})
	if err != nil {
		return nil, err
	}
	s.logger().InfoContext(ctx, "accounting: the books were set up from a SAF-T file", "import_id", row.ID, "user_id", user,
		"books_start", p.booksStart.Format(time.DateOnly), "sha256", row.Sha256, "status", http.StatusCreated)
	view, err := s.importView(ctx, row.ID, &b)
	if err != nil {
		return nil, err
	}
	return gen.ApplyAccountingSaftImport201JSONResponse(view), nil
}
