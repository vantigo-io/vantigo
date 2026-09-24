package customers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/text/unicode/norm"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is POST /customers/import (customers import/export design D3):
// the customers file (csvfile.go) read back into customers. Four things about
// it are worth saying once, because every function below follows from them:
//
//   - A file may only say what its sender could say by hand. There is no
//     import key: the router admits customers:create and customers:update
//     together, and the file's columns are then checked against the caller
//     before a row is read — the legal identity's against
//     customers:legal-identity-manage, the billing profile's against
//     customers:billing-manage. A column the sender may not write refuses the
//     file whole rather than being skipped: a skipped column is a change the
//     sender believes was made.
//   - Each row is written through the functions the endpoints themselves call
//     (insertNewCustomer, writeCustomerCore, writeContactInfo,
//     insertAddress/replaceAddress/removeAddress, writeBillingProfile,
//     writeCustomerGroup, replaceCustomerTags), behind the same validators and
//     followed by the same events, the importer as actor — so a row that
//     succeeds is indistinguishable from the same edits made by hand, and one
//     that fails leaves nothing half-written. No batch marker and no
//     customer.imported event: the granular events are the audit trail.
//   - A dry run is the real run minus the commit: each row in its own
//     transaction, rolled back when the row has run. Nothing is held past a
//     row — no lock, no customer-number counter increment, no event. (The
//     tables' own id sequences are identity columns, which a rollback does not
//     rewind, so internal ids skip; nothing a user sees.) What it cannot see is an
//     earlier row's effect on a later one; the one such case a file makes
//     likely — two rows creating one legal identity — is checked in memory
//     before any row runs (inFileDuplicates), and any other the real run still
//     refuses cleanly, as that row's error.
//   - Nothing leaves the database while a row's transaction is open: the actor
//     and the group and tag vocabularies are resolved once, before the first
//     row, and every access check the file needs is made on its header.

// The upload's bounds (design D3). maxImportRequestBytes is the router's cap
// for the operation (module.RouterOptions.BodyLimits): the file plus room for
// the multipart framing, expenses' receipt margin. The router's
// http.MaxBytesReader is in place before the generated wrapper hands the
// multipart reader over, so a body past it fails the part read, which
// importFilePart answers as the same 400 an oversized part gets.
const (
	maxImportFileBytes    = 5 * 1024 * 1024
	maxImportRequestBytes = maxImportFileBytes + 64*1024
	importFormField       = "file"
	// importErrorColumn is the column the browser's failed-rows file adds
	// (design D4), ignored so that file imports as it is.
	importErrorColumn = "error"
	// maxImportRows is how many data rows one import takes: the export's cap
	// (customersFileMaxRows), so a round trip always fits. The timing test
	// (csvimport_cap_test.go) measured a real run at the cap at about 30 s on
	// four CPUs, inside the ~100 s a hosted installation's proxy allows; were a
	// run ever to need more, this is the number to lower, and
	// docs/customers.md the paragraph that says so.
	maxImportRows = customersFileMaxRows
	// maxNamelessColumnsNamed is how many positions of nameless columns the
	// refusal lists before it counts the rest.
	maxNamelessColumnsNamed = 10
)

// maxImportHeaderWidth is the widest header the table can describe: every
// column once, the export-only four included, and the error column. A wider
// one cannot be a customers file, and is refused before its cells are looked
// at one by one — a 5 MB header of semicolons is five million cells, and a
// refusal per cell would be a response hundreds of MB large.
var maxImportHeaderWidth = len(customerCSVColumns) + 1

// importBodyLimits raises the router's request-body cap for the import. Every
// other operation of this module keeps the platform default (1 MiB).
var importBodyLimits = map[string]int64{
	"postCustomersImport": maxImportRequestBytes,
}

// The file-level 400: every refusal on "file", under one title.
const (
	invalidImportFileTitle     = "Invalid import file"
	invalidImportUploadMessage = "A customer import is one CSV file of at most 5 MB, sent as the multipart part named 'file'"
)

// The 409 a second import gets while one runs (PostCustomersImport).
const (
	importRunningTitle  = "An import is already running"
	importRunningDetail = "Another customer import is running. Try again once it has finished."
)

// importHeldForTest, when set, runs once the import lock is held and the
// vocabularies are read, before the first row, and the rows run under the
// context it answers: the one point a test can hold an import open at (a
// second import meanwhile is the 409), change the database under it (a group
// or tag deleted after the file named it) or cancel it (the caller gone).
// Set only through export_test.go's SetImportHeldHook, by a test that does not
// run in parallel; nil in production.
var importHeldForTest func(context.Context) context.Context

func invalidImportFile(messages ...string) apicommon.HttpValidationProblemDetails {
	return apicommon.ValidationProblem(invalidImportFileTitle, map[string][]string{"file": messages})
}

// importFilePart reads the one multipart part named "file" — expenses'
// receiptPart for a CSV. A missing part, a second one, an empty one, one past
// maxImportFileBytes and a read that fails (a malformed body, or the router's
// cap firing) are all ok=false.
func importFilePart(mr *multipart.Reader) ([]byte, bool) {
	if mr == nil {
		return nil, false
	}
	var data []byte
	found := false
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, false
		}
		if part.FormName() != importFormField {
			_ = part.Close()
			continue
		}
		if found {
			_ = part.Close()
			return nil, false
		}
		found = true
		data, err = io.ReadAll(io.LimitReader(part, maxImportFileBytes+1))
		_ = part.Close()
		if err != nil {
			return nil, false
		}
	}
	return data, found && len(data) > 0 && len(data) <= maxImportFileBytes
}

// importFlag is one of the two query flags: absent is fallback, and anything
// but the literals 'true' and 'false' is refused in the list's own words.
func importFlag(name string, raw *string, fallback bool) (bool, string) {
	if raw == nil {
		return fallback, ""
	}
	switch *raw {
	case "true":
		return true, ""
	case "false":
		return false, ""
	}
	return fallback, fmt.Sprintf("'%s' must be one of 'true' or 'false', but was '%s'.", name, *raw)
}

// importLayout is a header read against the column table: where each column a
// row is written from sits, how the file spells it, which groups the file
// carries, and how many cells a row must have.
type importLayout struct {
	index    map[string]int
	spelling map[string]string
	groups   map[csvGroup]bool
	width    int
}

func (l importLayout) has(g csvGroup) bool { return l.groups[g] }

// cell is rec's value in column, "" for a column the file does not carry.
func (l importLayout) cell(rec csvRecord, column string) string {
	if i, ok := l.index[column]; ok {
		return rec.Cells[i]
	}
	return ""
}

// spelt is column as the file's header spells it — headers match without
// regard to case, and an error names the cell the person will look for —
// and "" (a row-level error) as it is.
func (l importLayout) spelt(column string) string {
	if s, ok := l.spelling[column]; ok {
		return s
	}
	return column
}

// position orders a row's errors by where their column sits in the file, a
// row-level error first.
func (l importLayout) position(column string) int {
	if i, ok := l.index[column]; ok {
		return i
	}
	return -1
}

// importLayoutFor reads the header (design D3): every column one the table
// names, the export-only four or the error column, each at most once, matched
// without regard to case; a group's columns all together or not at all; and
// no column the caller could not write by hand. It answers every refusal at
// once, so a file is fixed in one pass rather than one error at a time — but
// never more refusals than the table has columns: a header wider than the
// table can describe is one refusal, and nameless columns are one between
// them, so the answer stays small whatever the file holds.
func (s *server) importLayoutFor(ctx context.Context, header []string) (importLayout, []string) {
	if len(header) == 1 && strings.Contains(header[0], ",") {
		return importLayout{}, []string{"The file is comma-separated; the import reads semicolon-separated files, the form the export writes and a Norwegian Excel saves as CSV"}
	}
	if len(header) > maxImportHeaderWidth {
		return importLayout{}, []string{fmt.Sprintf("The header has %d columns, but the file describes at most %d", len(header), maxImportHeaderWidth)}
	}
	known := make(map[string]csvColumn, len(customerCSVColumns))
	for _, c := range customerCSVColumns {
		known[strings.ToLower(c.Name)] = c
	}
	l := importLayout{index: map[string]int{}, spelling: map[string]string{}, groups: map[csvGroup]bool{}, width: len(header)}
	var refusals, unknown []string
	var nameless []int
	for i, raw := range header {
		name := strings.TrimSpace(raw)
		if name == "" {
			nameless = append(nameless, i+1)
			continue
		}
		if strings.EqualFold(name, importErrorColumn) {
			continue
		}
		c, ok := known[strings.ToLower(name)]
		if !ok {
			unknown = append(unknown, "'"+name+"'")
			continue
		}
		if c.Group == csvGroupExportOnly {
			continue
		}
		if _, repeated := l.index[c.Name]; repeated {
			refusals = append(refusals, fmt.Sprintf("The column %s appears more than once", c.Name))
			continue
		}
		l.index[c.Name] = i
		l.spelling[c.Name] = name
		l.groups[c.Group] = true
	}
	if len(nameless) > 0 {
		refusals = append(refusals, namelessColumnsRefusal(nameless))
	}
	if len(unknown) > 0 {
		refusals = append(refusals, fmt.Sprintf("Unknown columns: %s. A column is one the export and the template carry, and a misspelt one is refused rather than ignored", strings.Join(unknown, ", ")))
	}
	for _, g := range csvImportGroups {
		if !l.groups[g] {
			continue
		}
		var missing []string
		for _, name := range csvColumnsOf(g) {
			if _, ok := l.index[name]; !ok {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			refusals = append(refusals, fmt.Sprintf("The file carries some of the %s columns but not %s; a group's columns are imported together, all of them or none", g, strings.Join(missing, ", ")))
		}
	}
	if len(l.index) == 0 && len(refusals) == 0 {
		refusals = append(refusals, "The file carries no column an import writes")
	}
	if l.groups[csvGroupIdentity] && !s.hasPermission(ctx, legalIdentityManage) {
		refusals = append(refusals, importPermissionRefusal(csvGroupIdentity, legalIdentityManage))
	}
	if l.groups[csvGroupBilling] && !s.hasPermission(ctx, billingManage) {
		refusals = append(refusals, importPermissionRefusal(csvGroupBilling, billingManage))
	}
	return l, refusals
}

// namelessColumnsRefusal is every header cell with no name, as one refusal:
// "Column 3 has no name", "Columns 3, 7 and 12 have no name", and past
// maxNamelessColumnsNamed positions the rest counted rather than listed.
func namelessColumnsRefusal(positions []int) string {
	if len(positions) == 1 {
		return fmt.Sprintf("Column %d has no name", positions[0])
	}
	named := positions
	rest := 0
	if len(named) > maxNamelessColumnsNamed {
		named, rest = positions[:maxNamelessColumnsNamed], len(positions)-maxNamelessColumnsNamed
	}
	list := make([]string, len(named))
	for i, p := range named {
		list[i] = strconv.Itoa(p)
	}
	if rest > 0 {
		return fmt.Sprintf("Columns %s and %d more have no name", strings.Join(list, ", "), rest)
	}
	return fmt.Sprintf("Columns %s and %s have no name", strings.Join(list[:len(list)-1], ", "), list[len(list)-1])
}

// importPermissionRefusal names a group's columns and the key they need.
func importPermissionRefusal(g csvGroup, key string) string {
	return fmt.Sprintf("The columns %s need the %s permission, which you do not have: remove them from the file, or ask for the permission",
		strings.Join(csvColumnsOf(g), ", "), key)
}

// importVocabulary is the group and tag names a file may use, read once per
// file and matched through vocabKey — without regard to case, the
// vocabularies' own uniqueness (lower(name) indexes, migrations 00024 and
// 00027), and in their own normal form.
type importVocabulary struct {
	groups map[string]groupSnapshot
	tags   map[string]tagSnapshot
}

// vocabKey is a group or tag name as the import matches it, on both sides:
// the vocabularies' own normal form (NFC, validateTagName and
// validateGroupName) and their own uniqueness (lower(name)), so a name a file
// spells decomposed or in another case still finds its word.
func vocabKey(name string) string {
	return strings.ToLower(norm.NFC.String(strings.TrimSpace(name)))
}

func importVocabularyFor(ctx context.Context, q *store.Queries, l importLayout) (importVocabulary, error) {
	vocab := importVocabulary{groups: map[string]groupSnapshot{}, tags: map[string]tagSnapshot{}}
	if l.has(csvGroupMembership) {
		groups, err := q.ListCustomerGroups(ctx)
		if err != nil {
			return importVocabulary{}, fmt.Errorf("customers: read the group vocabulary: %w", err)
		}
		for _, g := range groups {
			vocab.groups[vocabKey(g.Name)] = groupSnapshot{GroupID: g.ID, Name: g.Name}
		}
	}
	if l.has(csvGroupTags) {
		tags, err := q.ListCustomerTags(ctx)
		if err != nil {
			return importVocabulary{}, fmt.Errorf("customers: read the tag vocabulary: %w", err)
		}
		for _, t := range tags {
			vocab.tags[vocabKey(t.Name)] = tagSnapshot{TagID: t.ID, Name: t.Name}
		}
	}
	return vocab, nil
}

// importError is one entry of CustomerImportResult.errors: Column "" is a
// problem with the row as a whole.
type importError struct {
	Row     int
	Column  string
	Message string
}

// importRowRefusal is a row's errors travelling out of its transaction as the
// error that rolls it back — errDuplicateIdentity's technique, one row wide.
type importRowRefusal struct {
	errs []importError
}

func (r *importRowRefusal) Error() string {
	return fmt.Sprintf("customers: import row %d refused", r.errs[0].Row)
}

func refuseRow(row int, column, message string) error {
	return &importRowRefusal{errs: []importError{{Row: row, Column: column, Message: message}}}
}

// importPlan is one row, validated and in the shapes its write paths take.
// Each group carries a has flag — false is "not in the file, leave it alone" —
// and inside a carried group a nil pointer is "clear it": no identity, no
// primary address of that type, no group.
type importPlan struct {
	Row            int
	CustomerNumber *int64
	HasCore        bool
	Name           string
	Type           string // "" is blank: a create takes business, an update checks nothing
	Status         string // "" is blank: a create takes active, an update keeps its own
	HasIdentity    bool
	Identity       *legalIdentity
	HasContact     bool
	Contact        contactInfo
	HasPostal      bool
	Postal         *validatedAddress
	HasInvoice     bool
	Invoice        *validatedAddress
	HasBilling     bool
	Billing        billingProfile
	HasGroup       bool
	Group          *groupSnapshot
	HasTags        bool
	Tags           []tagSnapshot
}

// identityColumns is validateLegalIdentity's field keys as the file's columns.
var identityColumns = map[string]string{"country": "legalCountry", "type": "legalType", "id": "legalId", "name": "legalName"}

// plan reads one row through the endpoints' own validators, before any
// transaction: everything wrong with the row's cells, each keyed by the column
// it came from and in the file's column order. Only what needs the database —
// the customer a number names, a duplicate identity, the type on file — is
// left for importRow.
func (l importLayout) plan(rec csvRecord, vocab importVocabulary) (importPlan, []importError) {
	p := importPlan{Row: rec.Row}
	if len(rec.Cells) != l.width {
		return p, []importError{{Row: rec.Row, Message: fmt.Sprintf("This row has %d cells, but the header has %d", len(rec.Cells), l.width)}}
	}
	var errs []importError
	fail := func(column, message string) {
		errs = append(errs, importError{Row: rec.Row, Column: column, Message: message})
	}
	failAll := func(columnOf func(string) string, fieldErrs map[string][]string) {
		for field, messages := range fieldErrs {
			for _, m := range messages {
				fail(columnOf(field), m)
			}
		}
	}
	trimmed := func(column string) string { return strings.TrimSpace(l.cell(rec, column)) }
	raw := func(column string) *string { v := l.cell(rec, column); return &v }

	if n := trimmed("customerNumber"); n != "" {
		if v, err := strconv.ParseInt(n, 10, 64); err != nil || v < 1 {
			fail("customerNumber", fmt.Sprintf("A customer number must be a whole number, but was '%s'", n))
		} else {
			p.CustomerNumber = &v
		}
	}

	if _, ok := l.index["name"]; ok {
		p.HasCore = true
		if name, msg := validateFriendlyName(l.cell(rec, "name")); msg != "" {
			fail("name", msg)
		} else {
			p.Name = name
		}
	} else if p.CustomerNumber == nil {
		// The row's, not a cell's: the column it would name is not in the file.
		fail("", "A new customer needs a name, and this file has no name column")
	}
	// typeFailed keeps a refused type cell to its own error: the identity
	// below is then judged against no type rather than the business a blank
	// cell would default to.
	typeFailed := false
	if v := trimmed("type"); v != "" {
		if t, msg := validateCustomerType(v); msg != "" {
			fail("type", msg)
			typeFailed = true
		} else {
			p.Type = t
		}
	}
	if v := trimmed("status"); v != "" {
		if st, msg := validateCustomerStatus(v); msg != "" {
			fail("status", msg)
		} else {
			p.Status, p.HasCore = st, true
		}
	}

	if l.has(csvGroupIdentity) {
		p.HasIdentity = true
		country, typ, id, name := trimmed("legalCountry"), trimmed("legalType"), trimmed("legalId"), trimmed("legalName")
		if country != "" || typ != "" || id != "" || name != "" {
			// A file says nothing about where an identity came from: a new one
			// is manual (design D1). importedIdentity keeps a repeated one's.
			identity, idErrs := validateLegalIdentity(country, typ, id, name, "manual")
			failAll(func(field string) string { return identityColumns[field] }, idErrs)
			if idErrs == nil {
				p.Identity = &identity
				if p.CustomerNumber == nil && !typeFailed {
					customerType := p.Type
					if customerType == "" {
						customerType = "business"
					}
					if mismatch := identityTypeMismatch(customerType, p.Identity); mismatch != "" {
						fail("legalType", mismatch)
					}
				}
			}
		}
	}

	if l.has(csvGroupContact) {
		p.HasContact = true
		info, ciErrs := validateContactInfo(raw("email"), raw("phone"), raw("website"))
		failAll(func(field string) string { return field }, ciErrs)
		p.Contact = info
	}

	p.HasPostal, p.Postal = l.address(rec, csvGroupPostal, "postal", failAll)
	p.HasInvoice, p.Invoice = l.address(rec, csvGroupInvoice, "invoice", failAll)

	if l.has(csvGroupBilling) {
		p.HasBilling = true
		req := gen.PutCustomerBillingProfileRequest{
			InvoiceEmail: raw("invoiceEmail"), ReminderEmail: raw("reminderEmail"), Currency: raw("currency"),
			Language: raw("language"), InvoiceDelivery: raw("invoiceDelivery"), ReminderDelivery: raw("reminderDelivery"),
			PeppolId: raw("peppolId"), Gln: raw("gln"), BuyerReference: raw("buyerReference"),
		}
		if v := trimmed("paymentTermsDays"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 32); err != nil {
				fail("paymentTermsDays", fmt.Sprintf("Payment terms must be a whole number of days, but was '%s'", v))
			} else {
				days := int32(n)
				req.PaymentTermsDays = &days
			}
		}
		if v := trimmed("defaultBillRate"); v != "" {
			if rate, ok := parseCSVDecimal(v); !ok {
				fail("defaultBillRate", fmt.Sprintf("A default bill rate must be a number, but was '%s'", v))
			} else {
				req.DefaultBillRate = &rate
			}
		}
		profile, bErrs := validateBillingProfile(req)
		failAll(func(field string) string { return field }, bErrs)
		p.Billing = profile
	}

	if l.has(csvGroupMembership) {
		p.HasGroup = true
		if name := trimmed("group"); name != "" {
			if g, ok := vocab.groups[vocabKey(name)]; ok {
				p.Group = &g
			} else {
				fail("group", fmt.Sprintf("No customer group is named '%s'", name))
			}
		}
	}

	if l.has(csvGroupTags) {
		// The cell is the export's: names joined by csvTagSeparator, which no
		// tag name may hold (validateTagName), so every piece is one name.
		p.HasTags = true
		seen := map[uuid.UUID]bool{}
		for _, part := range strings.Split(l.cell(rec, "tags"), csvTagSeparator) {
			name := strings.TrimSpace(part)
			if name == "" {
				continue
			}
			tag, ok := vocab.tags[vocabKey(name)]
			if !ok {
				fail("tags", fmt.Sprintf("No tag is named '%s'", name))
				continue
			}
			if !seen[tag.TagID] {
				seen[tag.TagID] = true
				p.Tags = append(p.Tags, tag)
			}
		}
	}

	sort.SliceStable(errs, func(i, j int) bool { return l.position(errs[i].Column) < l.position(errs[j].Column) })
	return p, errs
}

// address reads one address group (design D3): not in the file, left alone;
// every cell blank, "no primary address of this type" (importPrimaryAddress
// says when that can be); otherwise validateAddress's request, its field
// errors keyed back to the file's columns ("line1" → "postalLine1").
func (l importLayout) address(rec csvRecord, g csvGroup, addrType string, failAll func(func(string) string, map[string][]string)) (bool, *validatedAddress) {
	if !l.has(g) {
		return false, nil
	}
	column := func(field string) string { return addrType + strings.ToUpper(field[:1]) + field[1:] }
	value := func(field string) string { return l.cell(rec, column(field)) }
	optional := func(field string) *string { v := value(field); return &v }
	if allBlank([]string{value("line1"), value("line2"), value("postalCode"), value("city"), value("region"), value("country")}) {
		return true, nil
	}
	parsed, errs := validateAddress(gen.CustomerAddressRequest{
		Type: addrType, Line1: value("line1"), Line2: optional("line2"), PostalCode: optional("postalCode"),
		City: optional("city"), Region: optional("region"), Country: value("country"),
	})
	failAll(column, errs)
	if errs != nil {
		return true, nil
	}
	return true, &parsed
}

// importOptions is what every row of one request shares, resolved before the
// first row's transaction opens.
type importOptions struct {
	act                    actor
	allowDuplicateIdentity bool
	nameHolders            bool
}

// importOutcome is what a row that succeeded did.
type importOutcome int

const (
	importCreated importOutcome = iota + 1
	importUpdated
)

// importRow writes one planned row inside the transaction txq belongs to.
// Anything wrong that only the database can tell — an unknown number, a
// duplicate identity, a type change — is an importRowRefusal, which rolls the
// row back; any other error is the request's.
func (s *server) importRow(ctx context.Context, txq *store.Queries, p importPlan, o importOptions) (importOutcome, error) {
	now := s.deps.Clock()
	if p.CustomerNumber == nil {
		id, err := s.importCreate(ctx, txq, p, o, now)
		if err != nil {
			return 0, err
		}
		return importCreated, s.importRelations(ctx, txq, id, p, o, now)
	}
	id, err := s.importUpdate(ctx, txq, p, o, now)
	if err != nil {
		return 0, err
	}
	return importUpdated, s.importRelations(ctx, txq, id, p, o, now)
}

// importCreate is POST /customers for a row: its name, type, status, identity
// and contact info in the create's own insert.
func (s *server) importCreate(ctx context.Context, txq *store.Queries, p importPlan, o importOptions, now time.Time) (int32, error) {
	customerType, status := p.Type, p.Status
	if customerType == "" {
		customerType = "business"
	}
	if status == "" {
		status = "active"
	}
	created, conflict, err := s.insertNewCustomer(ctx, txq,
		newCustomer{Name: p.Name, Status: status, Type: customerType, Identity: p.Identity, Contact: p.Contact},
		p.Identity != nil && !o.allowDuplicateIdentity, o.nameHolders, now, o.act)
	if errors.Is(err, errDuplicateIdentity) {
		return 0, refuseRow(p.Row, "legalId", duplicateIdentityRowMessage(conflict))
	}
	if err != nil {
		return 0, err
	}
	return created.ID, nil
}

// importUpdate is PUT /customers/{id} and PUT …/contact-info for a row, on the
// customer its number names, under that customer's row lock — the lock every
// address and tag write takes first, so nothing a colleague does lands between
// a read here and the write it decides.
func (s *server) importUpdate(ctx context.Context, txq *store.Queries, p importPlan, o importOptions, now time.Time) (int32, error) {
	id, err := txq.CustomerIDByNumber(ctx, *p.CustomerNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, refuseRow(p.Row, "customerNumber", fmt.Sprintf("No customer has number %d", *p.CustomerNumber))
	}
	if err != nil {
		return 0, err
	}
	if _, err := lockWritableCustomer(ctx, txq, id); isReadOnlyCustomer(err) {
		// A customer merged away or anonymised takes no more changes (customers
		// merge design D2, GDPR design D4) — this row would otherwise restore it,
		// write into a history nobody reads, or put a person back into what was
		// kept for bookkeeping. The row is refused; a merged-away customer's
		// survivor is the one to import it under.
		if isAnonymisedRefusal(err) {
			return 0, refuseRow(p.Row, "customerNumber", fmt.Sprintf("Customer %d was anonymised and takes no more changes", *p.CustomerNumber))
		}
		return 0, refuseRow(p.Row, "customerNumber", fmt.Sprintf("Customer %d was merged into %s and takes no more changes; use that customer's number instead",
			*p.CustomerNumber, mergedAwayInto(err)))
	} else if err != nil {
		return 0, err
	}
	existing, err := txq.GetCustomer(ctx, id)
	if err != nil {
		return 0, err
	}
	if p.Type != "" && p.Type != existing.Type {
		return 0, refuseRow(p.Row, "type", fmt.Sprintf("A customer's type is changed on its own, never by an import; this row says '%s', but customer %d is '%s'", p.Type, existing.CustomerNumber, existing.Type))
	}

	before := customerCoreFrom(existing)
	after := before
	if p.HasCore {
		if p.Name != "" {
			after.Name = p.Name
		}
		if p.Status != "" {
			after.Status = p.Status
		}
	}
	if p.HasIdentity {
		after.Identity = importedIdentity(before.Identity, p.Identity)
		if mismatch := identityTypeMismatch(existing.Type, after.Identity); mismatch != "" {
			return 0, refuseRow(p.Row, "legalType", mismatch)
		}
	}
	if !customerCoreEqual(before, after) {
		duplicateCheck := after.Identity != nil && !identityCountryAndIDEqual(before.Identity, after.Identity) && !o.allowDuplicateIdentity
		if _, conflict, err := s.writeCustomerCore(ctx, txq, id, existing.Type, before, after, nil, duplicateCheck, o.nameHolders, now, o.act); err != nil {
			if errors.Is(err, errDuplicateIdentity) {
				return 0, refuseRow(p.Row, "legalId", duplicateIdentityRowMessage(conflict))
			}
			return 0, err
		}
	}
	if p.HasContact {
		current := contactInfoFromRow(existing.Email, existing.Phone, existing.Website)
		if !contactInfoEqual(current, p.Contact) {
			if _, err := writeContactInfo(ctx, txq, id, current, p.Contact, nil, now, o.act); err != nil {
				return 0, err
			}
		}
	}
	return id, nil
}

// importRelations is everything a create does not insert with the row, and an
// update does after it: the two primary addresses, the billing profile, the
// group and the tags — each read under the transaction, compared, and written
// through its endpoint's function only when it differs, so a row that repeats
// what is on file writes nothing.
func (s *server) importRelations(ctx context.Context, txq *store.Queries, id int32, p importPlan, o importOptions, now time.Time) error {
	if p.HasPostal {
		if err := importPrimaryAddress(ctx, txq, id, "postal", p.Postal, now, o.act); err != nil {
			return addressRefusal(p.Row, "postalLine1", err)
		}
	}
	if p.HasInvoice {
		if err := importPrimaryAddress(ctx, txq, id, "invoice", p.Invoice, now, o.act); err != nil {
			return addressRefusal(p.Row, "invoiceLine1", err)
		}
	}
	if p.HasBilling {
		row, err := txq.GetCustomerBillingProfile(ctx, id)
		if err != nil {
			return err
		}
		rate, err := floatPtrFromNumeric(row.DefaultBillRate)
		if err != nil {
			return err
		}
		current := billingProfileFromRow(row.InvoiceEmail, row.ReminderEmail, row.PaymentTermsDays, row.Currency, row.Language,
			row.InvoiceDelivery, row.ReminderDelivery, row.PeppolID, row.Gln, row.BuyerReference, rate)
		if !billingProfileEqual(current, p.Billing) {
			if _, err := writeBillingProfile(ctx, txq, id, current, p.Billing, nil, now, o.act); err != nil {
				return err
			}
		}
	}
	if p.HasGroup {
		membership, err := txq.CustomerGroupMembership(ctx, id)
		if err != nil {
			return err
		}
		var current *groupSnapshot
		if membership.GroupID != nil {
			current = &groupSnapshot{GroupID: *membership.GroupID, Name: deref(membership.GroupName)}
		}
		if !uuidPtrEqual(groupIDOf(current), groupIDOf(p.Group)) {
			if _, err := writeCustomerGroup(ctx, txq, id, current, p.Group, nil, now, o.act); err != nil {
				if db.IsForeignKeyViolation(err, customersGroupFK) {
					return refuseRow(p.Row, "group", groupNotFound(p.Group.GroupID))
				}
				return err
			}
		}
	}
	if p.HasTags {
		links, err := txq.CustomerTagsForCustomers(ctx, []int32{id})
		if err != nil {
			return err
		}
		current := make([]tagSnapshot, 0, len(links))
		for _, link := range links {
			current = append(current, tagSnapshot{TagID: link.ID, Name: link.Name})
		}
		added, removed := tagSetDiff(current, p.Tags)
		if len(added) > 0 || len(removed) > 0 {
			wanted := make([]uuid.UUID, 0, len(p.Tags))
			for _, tag := range p.Tags {
				wanted = append(wanted, tag.TagID)
			}
			if err := replaceCustomerTags(ctx, txq, id, wanted, added, removed, now, o.act); err != nil {
				if db.IsForeignKeyViolation(err, customerTagsTagFK) {
					return refuseRow(p.Row, "tags", "A tag this row names was deleted while the file was being imported")
				}
				return err
			}
		}
	}
	return nil
}

// importPrimaryAddress makes the customer's primary address of addrType the
// row's (design D3): none on file and none in the row, nothing; none on file,
// insertAddress it as primary; one on file and none in the row, removeAddress
// it — but only when it is the only address of its type; one on file and one
// in the row, replaceAddress it unless it already says the same. The file has
// no label column, so the address keeps its own.
//
// The blank row removes only a lone address because anything else would not
// be idempotent: removing a primary makes the oldest remaining one of its
// type primary, as a delete by hand does, so the same file imported again
// would remove that one too, and a third time the next. A file describes the
// primary address alone and cannot say which of the others is meant, so it
// is refused (otherAddressesOfType) and the others are left to a person.
func importPrimaryAddress(ctx context.Context, txq *store.Queries, customerID int32, addrType string, after *validatedAddress, now time.Time, act actor) error {
	current, err := txq.PrimaryCustomerAddressOfType(ctx, store.PrimaryCustomerAddressOfTypeParams{CustomerID: customerID, Type: addrType})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if after == nil {
			return nil
		}
		_, err := insertAddress(ctx, txq, customerID, *after, true, now, act)
		return err
	case err != nil:
		return err
	case after == nil:
		others, err := txq.CountCustomerAddressesOfType(ctx, store.CountCustomerAddressesOfTypeParams{CustomerID: customerID, Type: addrType, ExcludeID: current.ID})
		if err != nil {
			return err
		}
		if others > 0 {
			return otherAddressesOfType{addrType: addrType, count: others + 1}
		}
		return removeAddress(ctx, txq, customerID, current, now, act)
	}
	next := *after
	next.Label = current.Label
	if addressSnapshotEqual(addressSnapshotFromRow(current), addressSnapshot{
		Type: next.Type, Label: next.Label, Line1: next.Line1, Line2: next.Line2, PostalCode: next.PostalCode,
		City: next.City, Region: next.Region, Country: next.Country, IsPrimary: true,
	}) {
		return nil
	}
	_, err = replaceAddress(ctx, txq, customerID, current, next, true, now, act)
	return err
}

// otherAddressesOfType is importPrimaryAddress refusing to clear a primary
// address that is not the only one of its type: count is how many there are.
type otherAddressesOfType struct {
	addrType string
	count    int64
}

func (e otherAddressesOfType) Error() string {
	return fmt.Sprintf("customers: %d %s addresses, and the row clears the primary", e.count, e.addrType)
}

// addressRefusal is the address cap, and a blank address group that cannot
// clear, as the row's error on the group's first column; anything else passes
// through.
func addressRefusal(row int, column string, err error) error {
	if errors.Is(err, errAddressCapReached) {
		return refuseRow(row, column, addressCapMessage)
	}
	var others otherAddressesOfType
	if errors.As(err, &others) {
		return refuseRow(row, column, fmt.Sprintf(
			"This customer has %d %s addresses; the file can only describe the primary one — remove the others by hand before clearing it",
			others.count, others.addrType))
	}
	return err
}

// addressSnapshotEqual is two addresses' every field, nil included.
func addressSnapshotEqual(a, b addressSnapshot) bool {
	return a.Type == b.Type && stringPtrEqual(a.Label, b.Label) && a.Line1 == b.Line1 && stringPtrEqual(a.Line2, b.Line2) &&
		stringPtrEqual(a.PostalCode, b.PostalCode) && stringPtrEqual(a.City, b.City) && stringPtrEqual(a.Region, b.Region) &&
		a.Country == b.Country && a.IsPrimary == b.IsPrimary
}

// groupIDOf is a snapshot's group id, nil for no group.
func groupIDOf(g *groupSnapshot) *uuid.UUID {
	if g == nil {
		return nil
	}
	return &g.GroupID
}

// customerCoreFrom is a persisted row's name, status and identity.
func customerCoreFrom(c store.GetCustomerRow) customerCore {
	return customerCore{Name: c.Name, Status: c.Status, Identity: identityFromRow(c.LegalCountry, c.LegalID, c.LegalName, c.LegalSource, c.LegalType)}
}

// customerCoreEqual is the no-op rule (customers foundation design D5) for
// the three: the same name, status and identity, all five identity fields.
func customerCoreEqual(a, b customerCore) bool {
	return a.Name == b.Name && a.Status == b.Status && identityEqual(a.Identity, b.Identity)
}

// importedIdentity is the identity a row asks for. A new one is manual
// (design D1); a row repeating the identity on file in all four of the file's
// fields keeps the identity on file, source included, so re-importing an
// export never turns a Brreg pick into a manual entry.
func importedIdentity(current, row *legalIdentity) *legalIdentity {
	if row == nil || current == nil {
		return row
	}
	if current.Country == row.Country && current.Type == row.Type && current.ID == row.ID && current.Name == row.Name {
		return current
	}
	return row
}

// duplicateIdentityRowMessage is the create endpoint's 409 as a row's error:
// its detail, the holders when the caller may be told them, and the way past.
func duplicateIdentityRowMessage(problem *gen.CustomerConflictProblem) string {
	message := strings.TrimSuffix(duplicateIdentityDetail, ".")
	if problem != nil && problem.Duplicates != nil {
		holders := make([]string, 0, len(*problem.Duplicates))
		for _, d := range *problem.Duplicates {
			holders = append(holders, fmt.Sprintf("%d %s", d.CustomerNumber, d.Name))
		}
		message += " (" + strings.Join(holders, ", ") + ")"
	}
	return message + ". Import with allowDuplicateIdentity=true to keep both."
}

// importTally is what the rows did.
type importTally struct {
	rows, created, updated, failed int
	errs                           []importError
}

func (t importTally) result(dryRun bool) gen.CustomerImportResult {
	errs := make([]gen.CustomerImportError, 0, len(t.errs))
	for _, e := range t.errs {
		item := gen.CustomerImportError{Row: int32(e.Row), Message: e.Message}
		if e.Column != "" {
			column := e.Column
			item.Column = &column
		}
		errs = append(errs, item)
	}
	return gen.CustomerImportResult{
		DryRun: dryRun, Rows: int32(t.rows), Created: int32(t.created), Updated: int32(t.updated),
		Failed: int32(t.failed), Errors: errs,
	}
}

// importRows runs every row in file order. Every row is planned first — its
// cells validated, no database — and then the file's own rows are checked
// against each other (inFileDuplicates) before any row is applied, so a dry
// run, whose rows cannot see each other's writes, still refuses what the real
// run would. A refusal is that row's failure and the next row runs; any other
// error ends the request.
//
// So does a cancelled request, checked before each row's transaction opens:
// a caller that went away — a Check abandoned, a tab closed, a proxy that
// gave up — has nobody left to read the result, and a run at the cap would
// otherwise go on for half a minute holding the one-import lock, the next
// Check refused until it ended. The rows already run stay as they are (a real
// run's committed, a dry run's rolled back); the deferred unlock frees the
// lock as the handler returns.
func importRows(ctx context.Context, file csvFile, l importLayout, vocab importVocabulary, allowDuplicateIdentity bool, apply func(importPlan) (importOutcome, error)) (importTally, error) {
	tally := importTally{rows: len(file.Rows), errs: []importError{}}
	plans := make([]importPlan, len(file.Rows))
	planErrs := make([][]importError, len(file.Rows))
	for i, rec := range file.Rows {
		plans[i], planErrs[i] = l.plan(rec, vocab)
	}
	if !allowDuplicateIdentity {
		inFileDuplicates(plans, planErrs)
	}
	for i, p := range plans {
		errs := planErrs[i]
		if len(errs) == 0 {
			if err := ctx.Err(); err != nil {
				return importTally{}, err
			}
			outcome, err := apply(p)
			var refused *importRowRefusal
			switch {
			case errors.As(err, &refused):
				errs = refused.errs
			case err != nil:
				return importTally{}, err
			case outcome == importCreated:
				tally.created++
			default:
				tally.updated++
			}
		}
		if len(errs) > 0 {
			tally.failed++
			for _, e := range errs {
				e.Column = l.spelt(e.Column)
				tally.errs = append(tally.errs, e)
			}
		}
	}
	return tally, nil
}

// inFileDuplicates is the duplicate-legal-identity guard (customers foundation
// design D6) within one file (customers import/export design D3): of two rows
// that create customers with the same identity — country and id, the guard's
// own comparison — the second is refused on legalId, in both runs alike. The
// database check cannot see it in a dry run, whose rows each roll back before
// the next one runs; this check needs no database, so the two runs agree. A
// row whose cells already failed takes no identity for itself.
func inFileDuplicates(plans []importPlan, planErrs [][]importError) {
	first := map[string]int{}
	for i, p := range plans {
		if p.CustomerNumber != nil || p.Identity == nil || len(planErrs[i]) > 0 {
			continue
		}
		key := p.Identity.Country + "\x00" + p.Identity.ID
		if row, seen := first[key]; seen {
			planErrs[i] = []importError{{Row: p.Row, Column: "legalId", Message: fmt.Sprintf(
				"Row %d of this file already creates a customer with this legal identity. Import with allowDuplicateIdentity=true to keep both.", row)}}
			continue
		}
		first[key] = p.Row
	}
}

// errImportDryRun rolls a dry-run row's transaction back once the row ran.
var errImportDryRun = errors.New("customers: import dry run rolled back")

// importFile runs the file a transaction per row — the real run and the dry
// run alike, the dry run's rolled back at its end instead of committed, so a
// checked row takes the very statements, locks and events an imported one
// does, and keeps none of them: the customer-number counter's increment rolls
// back with the rest (the row ids' identity sequences do not, so internal ids
// skip), and no lock outlives its row. Each row is retried on the
// deadlock a tag replace can lose to a tag delete (tagWriteAttempts, tags.go),
// the tags PUT's own retry, in both runs.
func (s *server) importFile(ctx context.Context, file csvFile, l importLayout, vocab importVocabulary, o importOptions, dryRun bool) (importTally, error) {
	return importRows(ctx, file, l, vocab, o.allowDuplicateIdentity, func(p importPlan) (importOutcome, error) {
		var outcome importOutcome
		err := db.RetrySerializable(ctx, tagWriteAttempts, func() error {
			return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
				var err error
				outcome, err = s.importRow(ctx, store.New(tx), p, o)
				if err == nil && dryRun {
					return errImportDryRun
				}
				return err
			})
		})
		if errors.Is(err, errImportDryRun) {
			return outcome, nil
		}
		return outcome, err
	})
}

// PostCustomersImport Import customers from CSV
// (POST /api/v1/customers/import)
//
// The order of refusals is the order that costs least: the flags, the part,
// the file's bytes, then its header against the table and the caller — every
// one a 400 before a single row is read — and only then the rows, whose
// problems are the result rather than a refusal.
func (s *server) PostCustomersImport(ctx context.Context, req gen.PostCustomersImportRequestObject) (gen.PostCustomersImportResponseObject, error) {
	dryRun, dryRunMsg := importFlag("dryRun", req.Params.DryRun, true)
	allowDuplicateIdentity, allowMsg := importFlag("allowDuplicateIdentity", req.Params.AllowDuplicateIdentity, false)
	if paramErrs := nonEmptyMessages(map[string]string{"dryRun": dryRunMsg, "allowDuplicateIdentity": allowMsg}); len(paramErrs) > 0 {
		return gen.PostCustomersImport400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid query parameters", paramErrs)), nil
	}

	data, ok := importFilePart(req.Body)
	if !ok {
		return gen.PostCustomersImport400ApplicationProblemPlusJSONResponse(invalidImportFile(invalidImportUploadMessage)), nil
	}
	file, refusal := readCSVFile(data, maxImportRows)
	if refusal != "" {
		return gen.PostCustomersImport400ApplicationProblemPlusJSONResponse(invalidImportFile(refusal)), nil
	}
	layout, refusals := s.importLayoutFor(ctx, file.Header)
	if len(refusals) > 0 {
		return gen.PostCustomersImport400ApplicationProblemPlusJSONResponse(invalidImportFile(refusals...)), nil
	}

	// One import at a time, real or dry: a run at the cap is half a minute of
	// row transactions, and two of them side by side — a Check clicked twice,
	// a script in a loop — would only share the pool between them. Taken after
	// every refusal, so a file that cannot run never holds it, and never
	// waited for: the second caller is told, not queued behind the first.
	if !s.importing.TryLock() {
		return gen.PostCustomersImport409ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(importRunningTitle, importRunningDetail, http.StatusConflict)), nil
	}
	defer s.importing.Unlock()

	vocab, err := importVocabularyFor(ctx, store.New(s.deps.Pool), layout)
	if err != nil {
		return nil, err
	}
	if importHeldForTest != nil {
		ctx = importHeldForTest(ctx)
	}
	tally := importTally{errs: []importError{}}
	if len(file.Rows) > 0 {
		// Resolved once, before any row's transaction, and only when there is a
		// row to write (customers foundation design D1, actor.go).
		act, err := s.actorFor(ctx, generatedFallbackActor)
		if err != nil {
			return nil, fmt.Errorf("customers: resolve actor: %w", err)
		}
		o := importOptions{
			act:                    act,
			allowDuplicateIdentity: allowDuplicateIdentity,
			// Whether a duplicate may name its holder (duplicates.go). The router
			// admits an import only with customers:view (the rule in
			// customers.yaml), so the answer is yes whenever a row could raise it.
			nameHolders: layout.has(csvGroupIdentity) && !allowDuplicateIdentity,
		}
		tally, err = s.importFile(ctx, file, layout, vocab, o, dryRun)
		if err != nil {
			return nil, fmt.Errorf("customers: import customers: %w", err)
		}
	}
	return gen.PostCustomersImport200JSONResponse(tally.result(dryRun)), nil
}

// nonEmptyMessages is a field error map of the messages that were set.
func nonEmptyMessages(messages map[string]string) map[string][]string {
	errs := map[string][]string{}
	for field, message := range messages {
		if message != "" {
			errs[field] = []string{message}
		}
	}
	return errs
}
