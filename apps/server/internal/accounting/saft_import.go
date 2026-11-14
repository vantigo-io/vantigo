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
	file, err := saft.Read(tmp)
	if err != nil {
		var inv *saft.InvalidError
		switch {
		case errors.Is(err, saft.ErrUnsupportedVersion):
			return nil, invalidField("The file cannot be imported", attachmentFormField, codeSaftVersionUnsupported,
				"This is not SAF-T Financial 1.30 or 1.40 ("+err.Error()+"). Export the file again from the other system as version 1.30 or 1.40.")
		case errors.As(err, &inv):
			return nil, invalidField("The file cannot be imported", attachmentFormField, codeSaftFileInvalid,
				"This is not a SAF-T Financial file Vantigo can read: "+inv.Reason+".")
		}
		return nil, err
	}
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
	id, err := q.InsertSaftImport(ctx, store.InsertSaftImportParams{FileName: cleanFileName(name), ObjectKey: key,
		SizeBytes: size, Sha256: sum, AuditFileVersion: file.Version, SelectionStart: file.SelectionStart,
		SelectionEnd: file.SelectionEnd, Preview: preview, Now: s.now(), UserID: callerID(ctx)})
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
	file    saft.FileParty
	closing *big.Rat
	// accounts are the file accounts its balances are on.
	accounts []string
	// customer: the match.
	match        string
	customerID   *int32
	customerName *string
	// supplier: what is kept, and what was dropped.
	fields supplierFields
	notes  []string
	org    *string
}

// importPlan is everything Apply writes, and what stands in its way.
type importPlan struct {
	row                       store.AccountingSaftImport
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
	p := &importPlan{row: row, file: file, booksStart: file.SelectionEnd.AddDate(0, 0, 1), roles: map[string]string{},
		addedReason: map[string]string{}, result: new(big.Rat), vatMoved: new(big.Rat), recvDiff: new(big.Rat), payDiff: new(big.Rat)}
	p.year = p.booksStart.Year()
	word := func(en, nb string) string {
		if language == "en" {
			return en
		}
		return nb
	}
	if p.booksStart.Before(firstRateDay) {
		p.block(codeBooksStartTooEarly, fmt.Sprintf("The books would start on %s; they start on %s or later.",
			p.booksStart.Format(time.DateOnly), firstRateDay.Format(time.DateOnly)))
	}
	if file.SelectionByPeriod {
		p.notes = append(p.notes, "The file names its selection by periods; they are read as calendar months, so the books start on "+
			p.booksStart.Format(time.DateOnly)+".")
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

	// The parties and their control accounts.
	control := func(parties []saft.FileParty, customer bool) ([]importParty, string, error) {
		out := make([]importParty, 0, len(parties))
		accounts := map[string]bool{}
		for _, fp := range parties {
			ip := importParty{file: fp, closing: new(big.Rat)}
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
	if p.customers, p.receivablesNo, err = control(file.Customers, true); err != nil {
		return nil, err
	}
	if p.suppliers, p.payablesNo, err = control(file.Suppliers, false); err != nil {
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

	// The customers, matched to Customers.
	var links map[string]int32
	if body != nil && body.CustomerLinks != nil {
		links = *body.CustomerLinks
	}
	if err := s.matchCustomers(ctx, p.customers, links); err != nil {
		return nil, err
	}
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
		if a.action != "import" || !isBalanceKind(a.kind) || a.closing.Sign() == 0 {
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
	docDate := file.SelectionEnd
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

// matchCustomers finds each file customer in Customers: by a link the apply
// gives, else by organisation number. Accounting writes no customer.
func (s *server) matchCustomers(ctx context.Context, customers []importParty, links map[string]int32) error {
	linked := []int32{}
	for _, id := range links {
		linked = append(linked, id)
	}
	names := map[int32]string{}
	if s.deps.Directory != nil && len(linked) > 0 {
		entries, err := s.deps.Directory.Customers(ctx, linked)
		if err != nil {
			return fmt.Errorf("accounting: read the customers: %w", err)
		}
		for _, e := range entries {
			names[e.ID] = e.Name
		}
	}
	orgs := []string{}
	for i := range customers {
		if org := digitsOnly(customers[i].file.RegistrationNumber); validOrganisationNumber(org) {
			customers[i].org = ptr(org)
			orgs = append(orgs, org)
		}
	}
	holders := map[string][]struct {
		id   int32
		name string
	}{}
	if s.deps.Directory != nil && len(orgs) > 0 {
		found, err := s.deps.Directory.CustomersByLegalID(ctx, "no", orgs)
		if err != nil {
			return fmt.Errorf("accounting: match the customers: %w", err)
		}
		for org, entries := range found {
			for _, e := range entries {
				id := e.ID
				if e.MergedInto != nil {
					id = *e.MergedInto
				}
				holders[org] = append(holders[org], struct {
					id   int32
					name string
				}{id, e.Name})
			}
		}
	}
	for i := range customers {
		c := &customers[i]
		if id, ok := links[c.file.ID]; ok {
			if name, found := names[id]; found {
				c.match, c.customerID, c.customerName = "linked", ptr(id), ptr(name)
				continue
			}
		}
		var hs []struct {
			id   int32
			name string
		}
		if c.org != nil {
			hs = holders[*c.org]
		}
		switch {
		case len(hs) == 1:
			c.match, c.customerID, c.customerName = "matched", ptr(hs[0].id), ptr(hs[0].name)
		case c.closing.Sign() == 0:
			c.match = "not_needed"
		case len(hs) > 1:
			c.match = "ambiguous"
		default:
			c.match = "unmatched"
		}
	}
	return nil
}

// importView is an import as the contract answers it.
func (s *server) importView(ctx context.Context, id int64, body *gen.AccountingSaftImportApply) (gen.AccountingSaftImport, error) {
	row, err := store.New(s.deps.Pool).GetSaftImport(ctx, id)
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

func (p *importPlan) view() gen.AccountingSaftImport {
	f := p.file
	out := gen.AccountingSaftImport{Id: p.row.ID, State: gen.AccountingSaftImportState(p.row.State), FileName: p.row.FileName,
		SizeBytes: p.row.SizeBytes, Sha256: p.row.Sha256, UploadedAt: p.row.UploadedAt, AppliedAt: p.row.AppliedAt,
		OpeningVoucherId: p.row.OpeningVoucherID, AuditFileVersion: f.Version, CompanyName: f.CompanyName,
		CompanyOrganisationNumber: f.CompanyNumber, SelectionStart: apiDate(f.SelectionStart), SelectionEnd: apiDate(f.SelectionEnd),
		BooksStart: apiDate(p.booksStart), FiscalYearStartMonth: int32(f.SelectionStart.Month()), Transactions: f.Entries,
		Blockers: p.blockers, Notes: p.notes, TaxCodes: p.taxCodes,
		Accounts: []gen.AccountingSaftImportAccount{}, AddedAccounts: []gen.AccountingSaftImportAddedAccount{},
		Roles: []gen.AccountingSaftImportRole{}, Customers: []gen.AccountingSaftImportCustomer{},
		Suppliers: []gen.AccountingSaftImportSupplier{}}
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
	for _, c := range p.customers {
		out.Customers = append(out.Customers, gen.AccountingSaftImportCustomer{FileCustomerId: c.file.ID, Name: c.file.Name,
			OrganisationNumber: c.org, ClosingBalance: money(c.closing), Match: gen.AccountingSaftImportCustomerMatch(c.match),
			CustomerId: c.customerID, CustomerName: c.customerName})
	}
	for _, sp := range p.suppliers {
		notes := sp.notes
		if notes == nil {
			notes = []string{}
		}
		out.Suppliers = append(out.Suppliers, gen.AccountingSaftImportSupplier{FileSupplierId: sp.file.ID, Name: sp.fields.name,
			OrganisationNumber: sp.fields.organisationNumber, City: sp.fields.city, BankAccount: sp.fields.bankAccount,
			Iban: sp.fields.iban, ClosingBalance: money(sp.closing), Notes: notes})
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
	// The plan is read with no transaction open: matching the customers is a
	// read of Customers.
	p, err := s.plan(ctx, row, &b)
	if err != nil {
		return nil, err
	}
	month = int32(p.file.SelectionStart.Month())
	if b.FiscalYearStartMonth != nil {
		month = *b.FiscalYearStartMonth
		if month < 1 || month > 12 {
			return nil, invalidField(title, "fiscalYearStartMonth", "", "A month, 1 to 12.")
		}
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
