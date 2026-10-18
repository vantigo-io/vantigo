package customers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/text/unicode/norm"

	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/fileimport"
)

// This file is a row of an import becoming a customer (customers import
// wizard design D3, D5): the mapping read into a layout, a row planned
// through it, and the row written through the functions the endpoints
// themselves call. Four things about it are worth saying once, because every
// function below follows from them:
//
//   - A mapped field is written; an unmapped field is left as it is. On an
//     update the row's group is the customer's current group with the mapped
//     fields laid over it (resolve), then validated and written as a whole
//     through the same function the endpoint uses — so mapping the email
//     column alone keeps the phone and the website. On a create an unmapped
//     field is blank. A mapped cell that is blank still clears its field: the
//     file said blank.
//   - Each row is written through the functions the endpoints themselves call
//     (insertNewCustomer, writeCustomerCore, writeContactInfo,
//     insertAddress/replaceAddress/removeAddress, writeBillingProfile,
//     writeCustomerGroup, replaceCustomerTags), behind the same validators and
//     followed by the same events, the uploader as actor — so a row that
//     succeeds is indistinguishable from the same edits made by hand, and one
//     that fails leaves nothing half-written. No batch marker and no
//     customer.imported event: the granular events are the audit trail.
//   - A dry run is the real run minus the commit: each row in its own
//     transaction, rolled back when the row has run (import_worker.go). What
//     it cannot see is an earlier row's effect on a later one; the one such
//     case a file makes likely — two rows with one legal identity — is found
//     on the file before any row runs (import_worker.go's duplicate pass).
//   - Nothing leaves the database while a row's transaction is open: the actor
//     and the group and tag vocabularies are resolved once, before the first
//     row, and every permission the mapping needs was checked when it was
//     saved (imports_mapping.go).

// importLayout is a mapping read against a file's header
// (imports_mapping.go's layoutFor): where each field comes from — a column
// of the file, or one value for every row — and the mode and match key the
// rows run under.
type importLayout struct {
	column                 map[string]int
	fixed                  map[string]string
	width                  int
	mode                   gen.CustomerImportMappingMode
	matchBy                gen.CustomerImportMappingMatchBy
	allowDuplicateIdentity bool
}

// mapped reports whether field is written at all.
func (l importLayout) mapped(field string) bool {
	_, c := l.column[field]
	_, f := l.fixed[field]
	return c || f
}

// has reports whether any field of the group is mapped — the group is then
// touched, and the row's unmapped fields of it are overlaid (resolve).
func (l importLayout) has(g csvGroup) bool {
	for _, name := range csvColumnsOf(g) {
		if l.mapped(name) {
			return true
		}
	}
	return false
}

// creates reports whether the mode makes every row a new customer.
func (l importLayout) creates() bool { return l.mode == gen.Create }

// columnOf is the file column a field's error points at: nil for a fixed
// value and for a field that is not mapped (a row-level problem).
func (l importLayout) columnOf(field string) *int32 {
	if i, ok := l.column[field]; ok {
		c := int32(i)
		return &c
	}
	return nil
}

// raw is a row's value for every mapped field: the cell, or the fixed value.
// A row shorter than its column is blank there — a spreadsheet drops its
// trailing empty cells — and a row longer than the header is the row's own
// problem (plan).
func (l importLayout) raw(rec fileimport.Record) map[string]string {
	values := make(map[string]string, len(l.column)+len(l.fixed))
	for field, i := range l.column {
		if i < len(rec.Cells) {
			values[field] = rec.Cells[i]
		} else {
			values[field] = ""
		}
	}
	for field, v := range l.fixed {
		values[field] = v
	}
	return values
}

// sameMappedCells reports whether two rows read alike through the layout:
// every mapped column's cell equal once trimmed, as the row planning trims
// them, and a fixed value equal by definition — the duplicate pass's test of
// a repeated customer (import_worker.go).
func (l importLayout) sameMappedCells(a, b []string) bool {
	ra, rb := l.raw(fileimport.Record{Cells: a}), l.raw(fileimport.Record{Cells: b})
	for field := range l.column {
		if strings.TrimSpace(ra[field]) != strings.TrimSpace(rb[field]) {
			return false
		}
	}
	return true
}

// importError is one problem of one row: Field is the import field it is
// about ("" for the row as a whole), which the layout turns into a file
// column for the answer.
type importError struct {
	Row     int
	Field   string
	Message string
}

// wire is the error as the contract carries it (CustomerImportRowError): the
// field's file column when it has one, else none.
func (l importLayout) wire(e importError) gen.CustomerImportRowError {
	return gen.CustomerImportRowError{Column: l.columnOf(e.Field), Message: e.Message}
}

// importRowRefusal is a row's errors travelling out of its transaction as the
// error that rolls it back — errDuplicateIdentity's technique, one row wide.
type importRowRefusal struct {
	errs []importError
}

func (r *importRowRefusal) Error() string {
	return fmt.Sprintf("customers: import row %d refused", r.errs[0].Row)
}

func refuseRow(row int, field, message string) error {
	return &importRowRefusal{errs: []importError{{Row: row, Field: field, Message: message}}}
}

func refuseRowAll(errs []importError) error {
	return &importRowRefusal{errs: errs}
}

// importVocabulary is the group and tag names a file may use, read once per
// run and matched through vocabKey — without regard to case, the
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
	if l.mapped("group") {
		groups, err := q.ListCustomerGroups(ctx)
		if err != nil {
			return importVocabulary{}, fmt.Errorf("customers: read the group vocabulary: %w", err)
		}
		for _, g := range groups {
			vocab.groups[vocabKey(g.Name)] = groupSnapshot{GroupID: g.ID, Name: g.Name}
		}
	}
	if l.mapped("tags") {
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

// identityKey is a legal identity as the match and the duplicate pass compare
// it: the country lowercased and the id without its whitespace — the
// duplicate guard's own comparison (identityCountryAndIDEqual), with the
// import's default country applied.
type identityKey struct {
	Country string
	ID      string
}

// importDefaultCountry is the country a legal id is taken to be from when the
// file says none (design D3): a Norwegian file has an organisation number and
// nothing else about the identity.
const importDefaultCountry = "no"

// identityKeyOf is the key of a raw country and id, ok=false for a blank id.
func identityKeyOf(country, id string) (identityKey, bool) {
	id = stripWhitespace(id)
	if id == "" {
		return identityKey{}, false
	}
	country = strings.ToLower(strings.TrimSpace(country))
	if country == "" {
		country = importDefaultCountry
	}
	return identityKey{Country: country, ID: id}, true
}

// importPlan is one row read through the layout: its raw values, and the key
// it matches a customer by. Everything else — the typed, validated groups —
// is resolve's, since on an update it depends on the customer found.
type importPlan struct {
	Row            int
	Raw            map[string]string
	CustomerNumber *int64
	MatchIdentity  *identityKey
}

// plan reads one row (design D3): its raw values, its match key, and the
// errors its cells get before any database is asked — strict is "every
// unmapped field is blank", the truth for a create, and false when the row
// may be an update whose unmapped fields come from the customer, in which
// case only the mapped fields' own errors are kept (the rest are judged
// again, laid over the customer, in importRow).
func (l importLayout) plan(rec fileimport.Record, vocab importVocabulary, strict bool) (importPlan, []importError) {
	p := importPlan{Row: rec.Row, Raw: l.raw(rec)}
	if len(rec.Cells) > l.width {
		return p, []importError{{Row: rec.Row, Message: fmt.Sprintf("This row has %d cells, but the header has %d", len(rec.Cells), l.width)}}
	}
	var errs []importError
	if l.matchBy == gen.CustomerNumber && !l.creates() {
		if n := strings.TrimSpace(p.Raw["customerNumber"]); n != "" {
			if v, err := strconv.ParseInt(n, 10, 64); err != nil || v < 1 {
				errs = append(errs, importError{Row: rec.Row, Field: "customerNumber", Message: fmt.Sprintf("A customer number must be a whole number, but was '%s'", n)})
			} else {
				p.CustomerNumber = &v
			}
		}
	}
	if l.matchBy == gen.LegalId && !l.creates() {
		if key, ok := identityKeyOf(p.Raw["legalCountry"], p.Raw["legalId"]); ok {
			p.MatchIdentity = &key
		}
	}
	_, resolveErrs := l.resolve(p, nil, vocab, strict)
	errs = append(errs, resolveErrs...)
	return p, l.sorted(errs)
}

// sorted orders a row's errors by where their column sits in the file, a
// row-level error and a fixed value's first.
func (l importLayout) sorted(errs []importError) []importError {
	position := func(field string) int {
		if i, ok := l.column[field]; ok {
			return i
		}
		return -1
	}
	sort.SliceStable(errs, func(i, j int) bool { return position(errs[i].Field) < position(errs[j].Field) })
	return errs
}

// customerState is what is on file for the customer a row updates — the
// values an unmapped field keeps — read under the customer's lock
// (importUpdate). nil is a create: every unmapped field blank.
type customerState struct {
	Name     string
	Type     string
	Status   string
	Identity *legalIdentity
	Contact  contactInfo
	Postal   *addressSnapshot
	Invoice  *addressSnapshot
	Billing  billingProfile
	Group    *groupSnapshot
	Tags     []tagSnapshot
}

// resolvedRow is a row as the write paths take it: each group with a has
// flag — false is "no field of it is mapped, leave it alone" — and inside a
// touched group the whole value, the customer's own fields laid under the
// mapped ones. A nil pointer inside a touched group is "clear it": no
// identity, no primary address of that type, no group.
type resolvedRow struct {
	Name        string
	Type        string
	Status      string
	HasIdentity bool
	Identity    *legalIdentity
	HasContact  bool
	Contact     contactInfo
	HasPostal   bool
	Postal      *validatedAddress
	HasInvoice  bool
	Invoice     *validatedAddress
	HasBilling  bool
	Billing     billingProfile
	HasGroup    bool
	Group       *groupSnapshot
	HasTags     bool
	Tags        []tagSnapshot
}

// identityColumns is validateLegalIdentity's field keys as the import's fields.
var identityColumns = map[string]string{"country": "legalCountry", "type": "legalType", "id": "legalId", "name": "legalName"}

// importCustomerType is a type cell as a Norwegian file spells it (design D3):
// the words for a business and a private person folded onto the two values,
// in the import alone, before the validator judges the rest.
func importCustomerType(raw string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "bedrift", "firma", "selskap", "company":
		return "business", ""
	case "privat", "privatperson", "private":
		return "person", ""
	}
	return validateCustomerType(raw)
}

// resolve is a row's values laid over cur (design D3): a mapped field from the
// row, an unmapped one from the customer, blank on a create; each touched
// group validated whole through its endpoint's validator; the identity's
// defaults applied (country NO, type the customer type, name the customer's
// name) when a legal id is there. Errors are keyed by field; with strict
// false only the mapped fields' are kept.
func (l importLayout) resolve(p importPlan, cur *customerState, vocab importVocabulary, strict bool) (resolvedRow, []importError) {
	var r resolvedRow
	var errs []importError
	fail := func(field, message string) {
		errs = append(errs, importError{Row: p.Row, Field: field, Message: message})
	}
	failAll := func(fieldOf func(string) string, fieldErrs map[string][]string) {
		for field, messages := range fieldErrs {
			for _, m := range messages {
				fail(fieldOf(field), m)
			}
		}
	}
	// value is a field's value for this row: the cell when mapped, else what
	// the customer has — "" on a create — through current.
	value := func(field string, current func() string) string {
		if v, ok := p.Raw[field]; ok {
			return v
		}
		if cur != nil && current != nil {
			return current()
		}
		return ""
	}
	trimmed := func(field string, current func() string) string { return strings.TrimSpace(value(field, current)) }
	ptr := func(s string) *string { return &s }
	derefCur := func(get func() *string) func() string {
		return func() string { return deref(get()) }
	}

	// The row: name, type, status.
	if l.mapped("name") {
		if name, msg := validateFriendlyName(p.Raw["name"]); msg != "" {
			fail("name", msg)
		} else {
			r.Name = name
		}
	} else if cur != nil {
		r.Name = cur.Name
	} else {
		fail("", "A new customer needs a name, and no name column is mapped")
	}
	typeFailed := false
	if v := trimmed("type", nil); v != "" {
		if t, msg := importCustomerType(v); msg != "" {
			fail("type", msg)
			typeFailed = true
		} else {
			r.Type = t
		}
	} else if cur != nil {
		r.Type = cur.Type
	} else {
		r.Type = "business"
	}
	if v := trimmed("status", nil); v != "" {
		if st, msg := validateCustomerStatus(v); msg != "" {
			fail("status", msg)
		} else {
			r.Status = st
		}
	} else if cur != nil {
		r.Status = cur.Status
	} else {
		r.Status = "active"
	}

	// The legal identity, with the defaults a legal id brings (design D3).
	if l.has(csvGroupIdentity) {
		r.HasIdentity = true
		curIdentity := func(get func(*legalIdentity) string) func() string {
			return func() string {
				if cur.Identity == nil {
					return ""
				}
				return get(cur.Identity)
			}
		}
		id := trimmed("legalId", curIdentity(func(i *legalIdentity) string { return i.ID }))
		if id != "" {
			country := trimmed("legalCountry", curIdentity(func(i *legalIdentity) string { return i.Country }))
			typ := trimmed("legalType", curIdentity(func(i *legalIdentity) string { return i.Type }))
			name := trimmed("legalName", curIdentity(func(i *legalIdentity) string { return i.Name }))
			if country == "" {
				country = importDefaultCountry
			}
			if typ == "" {
				typ = r.Type
			}
			if name == "" {
				name = r.Name
			}
			identity, idErrs := validateLegalIdentity(country, typ, id, name, "manual")
			failAll(func(field string) string { return identityColumns[field] }, idErrs)
			if idErrs == nil {
				r.Identity = &identity
				if !typeFailed {
					if mismatch := identityTypeMismatch(r.Type, r.Identity); mismatch != "" {
						fail("legalType", mismatch)
					}
				}
			}
		}
	} else if cur != nil {
		r.Identity = cur.Identity
	}

	// Contact info.
	if l.has(csvGroupContact) {
		r.HasContact = true
		cc := func(get func() *string) func() string {
			return derefCur(func() *string {
				if cur == nil {
					return nil
				}
				return get()
			})
		}
		info, ciErrs := validateContactInfo(
			ptr(value("email", cc(func() *string { return cur.Contact.Email }))),
			ptr(value("phone", cc(func() *string { return cur.Contact.Phone }))),
			ptr(value("website", cc(func() *string { return cur.Contact.Website }))),
		)
		failAll(func(field string) string { return field }, ciErrs)
		r.Contact = info
	}

	// The two primary addresses.
	var curPostal, curInvoice *addressSnapshot
	if cur != nil {
		curPostal, curInvoice = cur.Postal, cur.Invoice
	}
	r.HasPostal, r.Postal = l.address(p, csvGroupPostal, "postal", curPostal, failAll)
	r.HasInvoice, r.Invoice = l.address(p, csvGroupInvoice, "invoice", curInvoice, failAll)

	// The billing profile.
	if l.has(csvGroupBilling) {
		r.HasBilling = true
		var current billingProfile
		if cur != nil {
			current = cur.Billing
		}
		req := gen.PutCustomerBillingProfileRequest{
			InvoiceEmail:     ptr(value("invoiceEmail", derefCur(func() *string { return current.InvoiceEmail }))),
			ReminderEmail:    ptr(value("reminderEmail", derefCur(func() *string { return current.ReminderEmail }))),
			Currency:         ptr(value("currency", derefCur(func() *string { return current.Currency }))),
			Language:         ptr(value("language", derefCur(func() *string { return current.Language }))),
			InvoiceDelivery:  ptr(value("invoiceDelivery", derefCur(func() *string { return current.InvoiceDelivery }))),
			ReminderDelivery: ptr(value("reminderDelivery", derefCur(func() *string { return current.ReminderDelivery }))),
			PeppolId:         ptr(value("peppolId", derefCur(func() *string { return current.PeppolID }))),
			Gln:              ptr(value("gln", derefCur(func() *string { return current.Gln }))),
			BuyerReference:   ptr(value("buyerReference", derefCur(func() *string { return current.BuyerReference }))),
		}
		if l.mapped("paymentTermsDays") {
			if v := strings.TrimSpace(p.Raw["paymentTermsDays"]); v != "" {
				if n, err := strconv.ParseInt(v, 10, 32); err != nil {
					fail("paymentTermsDays", fmt.Sprintf("Payment terms must be a whole number of days, but was '%s'", v))
				} else {
					days := int32(n)
					req.PaymentTermsDays = &days
				}
			}
		} else {
			req.PaymentTermsDays = current.PaymentTermsDays
		}
		if l.mapped("defaultBillRate") {
			if v := strings.TrimSpace(p.Raw["defaultBillRate"]); v != "" {
				if rate, ok := parseCSVDecimal(v); !ok {
					fail("defaultBillRate", fmt.Sprintf("A default bill rate must be a number, but was '%s'", v))
				} else {
					req.DefaultBillRate = &rate
				}
			}
		} else {
			req.DefaultBillRate = current.DefaultBillRate
		}
		profile, bErrs := validateBillingProfile(req)
		failAll(func(field string) string { return field }, bErrs)
		r.Billing = profile
	}

	// The group and the tags: vocabulary by name, never a word created.
	if l.mapped("group") {
		r.HasGroup = true
		if name := strings.TrimSpace(p.Raw["group"]); name != "" {
			if g, ok := vocab.groups[vocabKey(name)]; ok {
				r.Group = &g
			} else {
				fail("group", fmt.Sprintf("No customer group is named '%s'", name))
			}
		}
	}
	if l.mapped("tags") {
		// The cell is the export's: names joined by csvTagSeparator, which no
		// tag name may hold (validateTagName), so every piece is one name.
		r.HasTags = true
		seen := map[uuid.UUID]bool{}
		for _, part := range strings.Split(p.Raw["tags"], csvTagSeparator) {
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
				r.Tags = append(r.Tags, tag)
			}
		}
	}

	if !strict {
		kept := errs[:0]
		for _, e := range errs {
			if e.Field != "" && l.mapped(e.Field) {
				kept = append(kept, e)
			}
		}
		errs = kept
	}
	return r, errs
}

// address reads one address group (design D3): no field of it mapped, left
// alone; the mapped fields laid over the address on file; every resulting
// cell blank, "no primary address of this type" (importPrimaryAddress says
// when that can be); otherwise validateAddress's request, its field errors
// keyed back to the import's fields ("line1" → "postalLine1").
func (l importLayout) address(p importPlan, g csvGroup, addrType string, cur *addressSnapshot, failAll func(func(string) string, map[string][]string)) (bool, *validatedAddress) {
	if !l.has(g) {
		return false, nil
	}
	field := func(part string) string { return addrType + strings.ToUpper(part[:1]) + part[1:] }
	value := func(part string, current string) string {
		if v, ok := p.Raw[field(part)]; ok {
			return v
		}
		if cur != nil {
			return current
		}
		return ""
	}
	var line1, line2, postalCode, city, region, country string
	if cur != nil {
		line1, line2, postalCode, city, region, country = cur.Line1, deref(cur.Line2), deref(cur.PostalCode), deref(cur.City), deref(cur.Region), cur.Country
	}
	line1, line2, postalCode = value("line1", line1), value("line2", line2), value("postalCode", postalCode)
	city, region, country = value("city", city), value("region", region), value("country", country)
	if fileimport.AllBlank([]string{line1, line2, postalCode, city, region, country}) {
		return true, nil
	}
	optional := func(v string) *string { return &v }
	parsed, errs := validateAddress(gen.CustomerAddressRequest{
		Type: addrType, Line1: line1, Line2: optional(line2), PostalCode: optional(postalCode),
		City: optional(city), Region: optional(region), Country: country,
	})
	failAll(field, errs)
	if errs != nil {
		return true, nil
	}
	return true, &parsed
}

// importOptions is what every row of one run shares, resolved before the
// first row's transaction opens.
type importOptions struct {
	act         actor
	vocab       importVocabulary
	nameHolders bool
}

// importOutcome is what a row that succeeded did.
type importOutcome int

const (
	importCreated importOutcome = iota + 1
	importUpdated
)

// importRow writes one planned row inside the transaction txq belongs to:
// the customer it matches is found and updated, or none is and one is
// created, as the mode says. Anything wrong that only the database can tell —
// an unknown number, two holders of an identity, a duplicate identity, a type
// change, a group that needs a line — is an importRowRefusal, which rolls the
// row back; any other error is the run's.
func (s *server) importRow(ctx context.Context, txq *store.Queries, l importLayout, p importPlan, o importOptions) (importOutcome, int32, error) {
	if l.creates() {
		id, err := s.importCreate(ctx, txq, l, p, o)
		return importCreated, id, err
	}
	id, found, err := s.importFind(ctx, txq, l, p)
	if err != nil {
		return 0, 0, err
	}
	if !found {
		// In createOrUpdate a row that names nobody creates — unless it names
		// a customer number nobody has: a blank number is a new customer, a
		// number that is not on file is a typo, never a customer with another
		// number. A legal id nobody holds is the case the mode is for.
		if l.mode == gen.Update || (l.matchBy == gen.CustomerNumber && p.CustomerNumber != nil) {
			return 0, 0, refuseRow(p.Row, string(l.matchBy), importNoMatchMessage(l, p))
		}
		id, err := s.importCreate(ctx, txq, l, p, o)
		return importCreated, id, err
	}
	err = s.importUpdate(ctx, txq, l, p, o, id)
	return importUpdated, id, err
}

// importNoMatchMessage is the update-mode refusal of a row that found no
// customer, in the match key's own words.
func importNoMatchMessage(l importLayout, p importPlan) string {
	if l.matchBy == gen.CustomerNumber {
		if p.CustomerNumber == nil {
			return "This row has no customer number to find the customer by"
		}
		return fmt.Sprintf("No customer has number %d", *p.CustomerNumber)
	}
	if p.MatchIdentity == nil {
		return "This row has no legal id to find the customer by"
	}
	return fmt.Sprintf("No customer has the legal identity %s %s", strings.ToUpper(p.MatchIdentity.Country), p.MatchIdentity.ID)
}

// importFind is the customer a row matches (design D3): by number, or by legal
// identity — the duplicate guard's own query, every status included — where
// two holders are the row's error naming both, since the file cannot say
// which it means.
func (s *server) importFind(ctx context.Context, txq *store.Queries, l importLayout, p importPlan) (int32, bool, error) {
	switch l.matchBy {
	case gen.CustomerNumber:
		if p.CustomerNumber == nil {
			return 0, false, nil
		}
		id, err := txq.CustomerIDByNumber(ctx, *p.CustomerNumber)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return id, err == nil, err
	default:
		if p.MatchIdentity == nil {
			return 0, false, nil
		}
		rows, err := txq.CustomersByLegalIdentity(ctx, store.CustomersByLegalIdentityParams{
			Country: p.MatchIdentity.Country, LegalID: p.MatchIdentity.ID, ExcludeID: 0,
		})
		if err != nil {
			return 0, false, err
		}
		switch len(rows) {
		case 0:
			return 0, false, nil
		case 1:
			return rows[0].ID, true, nil
		}
		holders := make([]string, 0, len(rows))
		for _, r := range rows {
			holders = append(holders, fmt.Sprintf("%d %s", r.CustomerNumber, r.Name))
		}
		return 0, false, refuseRow(p.Row, "legalId", fmt.Sprintf("%d customers have this legal identity (%s); the file cannot say which one is meant", len(rows), strings.Join(holders, ", ")))
	}
}

// importCreate is POST /customers for a row: every unmapped field blank, the
// row validated whole (strict), its name, type, status, identity and contact
// info in the create's own insert, and the rest through importRelations.
func (s *server) importCreate(ctx context.Context, txq *store.Queries, l importLayout, p importPlan, o importOptions) (int32, error) {
	r, errs := l.resolve(p, nil, o.vocab, true)
	if len(errs) > 0 {
		return 0, refuseRowAll(l.sorted(errs))
	}
	now := s.deps.Clock()
	created, conflict, err := s.insertNewCustomer(ctx, txq,
		newCustomer{Name: r.Name, Status: r.Status, Type: r.Type, Identity: r.Identity, Contact: r.Contact},
		r.Identity != nil && !l.allowDuplicateIdentity, o.nameHolders, now, o.act)
	if errors.Is(err, errDuplicateIdentity) {
		return 0, refuseRow(p.Row, "legalId", duplicateIdentityRowMessage(conflict))
	}
	if err != nil {
		return 0, err
	}
	return created.ID, s.importRelations(ctx, txq, created.ID, p.Row, customerState{}, r, o)
}

// importUpdate is PUT /customers/{id} and PUT …/contact-info for a row, on the
// customer the row matched, under that customer's row lock — the lock every
// address and tag write takes first, so nothing a colleague does lands
// between a read here and the write it decides. What is on file is read
// first, the row laid over it (resolve, strict), and only what differs is
// written.
func (s *server) importUpdate(ctx context.Context, txq *store.Queries, l importLayout, p importPlan, o importOptions, id int32) error {
	if _, err := lockWritableCustomer(ctx, txq, id); isReadOnlyCustomer(err) {
		// A customer merged away or anonymised takes no more changes (customers
		// merge design D2, GDPR design D4) — this row would otherwise restore it,
		// write into a history nobody reads, or put a person back into what was
		// kept for bookkeeping. The row is refused; a merged-away customer's
		// survivor is the one to import it under.
		if isAnonymisedRefusal(err) {
			return refuseRow(p.Row, string(l.matchBy), fmt.Sprintf("Customer %s was anonymised and takes no more changes", importMatchPhrase(l, p)))
		}
		return refuseRow(p.Row, string(l.matchBy), fmt.Sprintf("Customer %s was merged into %s and takes no more changes; use that customer instead",
			importMatchPhrase(l, p), mergedAwayInto(err)))
	} else if err != nil {
		return err
	}
	existing, err := txq.GetCustomer(ctx, id)
	if err != nil {
		return err
	}
	cur, err := s.importCurrentState(ctx, txq, l, id, existing)
	if err != nil {
		return err
	}
	r, errs := l.resolve(p, &cur, o.vocab, true)
	if len(errs) > 0 {
		return refuseRowAll(l.sorted(errs))
	}
	if l.mapped("type") && r.Type != existing.Type {
		return refuseRow(p.Row, "type", fmt.Sprintf("A customer's type is changed on its own, never by an import; this row says '%s', but customer %d is '%s'", r.Type, existing.CustomerNumber, existing.Type))
	}

	now := s.deps.Clock()
	before := customerCoreFrom(existing)
	after := customerCore{Name: r.Name, Status: r.Status, Identity: before.Identity}
	if r.HasIdentity {
		after.Identity = importedIdentity(before.Identity, r.Identity)
	}
	if !customerCoreEqual(before, after) {
		duplicateCheck := after.Identity != nil && !identityCountryAndIDEqual(before.Identity, after.Identity) && !l.allowDuplicateIdentity
		if _, conflict, err := s.writeCustomerCore(ctx, txq, id, existing.Type, before, after, nil, duplicateCheck, o.nameHolders, now, o.act); err != nil {
			if errors.Is(err, errDuplicateIdentity) {
				return refuseRow(p.Row, "legalId", duplicateIdentityRowMessage(conflict))
			}
			return err
		}
	}
	if r.HasContact && !contactInfoEqual(cur.Contact, r.Contact) {
		if _, err := writeContactInfo(ctx, txq, id, cur.Contact, r.Contact, nil, now, o.act); err != nil {
			return err
		}
	}
	return s.importRelations(ctx, txq, id, p.Row, cur, r, o)
}

// importMatchPhrase names the customer a row matched, as the row named it.
func importMatchPhrase(l importLayout, p importPlan) string {
	if l.matchBy == gen.CustomerNumber && p.CustomerNumber != nil {
		return strconv.FormatInt(*p.CustomerNumber, 10)
	}
	if p.MatchIdentity != nil {
		return "with legal id " + p.MatchIdentity.ID
	}
	return "this row names"
}

// importCurrentState is what is on file for the groups the mapping touches,
// read under the customer's lock: the row's own fields always, each other
// group only when a field of it is mapped, so a row that maps the email alone
// reads nothing about addresses or billing.
func (s *server) importCurrentState(ctx context.Context, txq *store.Queries, l importLayout, id int32, existing store.GetCustomerRow) (customerState, error) {
	cur := customerState{
		Name: existing.Name, Type: existing.Type, Status: existing.Status,
		Identity: identityFromRow(existing.LegalCountry, existing.LegalID, existing.LegalName, existing.LegalSource, existing.LegalType),
		Contact:  contactInfoFromRow(existing.Email, existing.Phone, existing.Website),
	}
	for _, addr := range []struct {
		group csvGroup
		typ   string
		into  **addressSnapshot
	}{{csvGroupPostal, "postal", &cur.Postal}, {csvGroupInvoice, "invoice", &cur.Invoice}} {
		if !l.has(addr.group) {
			continue
		}
		row, err := txq.PrimaryCustomerAddressOfType(ctx, store.PrimaryCustomerAddressOfTypeParams{CustomerID: id, Type: addr.typ})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return customerState{}, err
		}
		snapshot := addressSnapshotFromRow(row)
		*addr.into = &snapshot
	}
	if l.has(csvGroupBilling) {
		row, err := txq.GetCustomerBillingProfile(ctx, id)
		if err != nil {
			return customerState{}, err
		}
		rate, err := floatPtrFromNumeric(row.DefaultBillRate)
		if err != nil {
			return customerState{}, err
		}
		cur.Billing = billingProfileFromRow(row.InvoiceEmail, row.ReminderEmail, row.PaymentTermsDays, row.Currency, row.Language,
			row.InvoiceDelivery, row.ReminderDelivery, row.PeppolID, row.Gln, row.BuyerReference, rate)
	}
	if l.mapped("group") {
		membership, err := txq.CustomerGroupMembership(ctx, id)
		if err != nil {
			return customerState{}, err
		}
		if membership.GroupID != nil {
			cur.Group = &groupSnapshot{GroupID: *membership.GroupID, Name: deref(membership.GroupName)}
		}
	}
	if l.mapped("tags") {
		links, err := txq.CustomerTagsForCustomers(ctx, []int32{id})
		if err != nil {
			return customerState{}, err
		}
		cur.Tags = make([]tagSnapshot, 0, len(links))
		for _, link := range links {
			cur.Tags = append(cur.Tags, tagSnapshot{TagID: link.ID, Name: link.Name})
		}
	}
	return cur, nil
}

// importRelations is everything a create does not insert with the row, and an
// update does after it: the two primary addresses, the billing profile, the
// group and the tags — each compared with what is on file (cur, empty for a
// create) and written through its endpoint's function only when it differs,
// so a row that repeats what is on file writes nothing.
func (s *server) importRelations(ctx context.Context, txq *store.Queries, id int32, row int, cur customerState, r resolvedRow, o importOptions) error {
	now := s.deps.Clock()
	if r.HasPostal {
		if err := importPrimaryAddress(ctx, txq, id, "postal", r.Postal, now, o.act); err != nil {
			return addressRefusal(row, "postalLine1", err)
		}
	}
	if r.HasInvoice {
		if err := importPrimaryAddress(ctx, txq, id, "invoice", r.Invoice, now, o.act); err != nil {
			return addressRefusal(row, "invoiceLine1", err)
		}
	}
	if r.HasBilling && !billingProfileEqual(cur.Billing, r.Billing) {
		if _, err := writeBillingProfile(ctx, txq, id, cur.Billing, r.Billing, nil, now, o.act); err != nil {
			return err
		}
	}
	if r.HasGroup && !uuidPtrEqual(groupIDOf(cur.Group), groupIDOf(r.Group)) {
		if _, err := writeCustomerGroup(ctx, txq, id, cur.Group, r.Group, nil, now, o.act); err != nil {
			if db.IsForeignKeyViolation(err, customersGroupFK) {
				return refuseRow(row, "group", groupNotFound(r.Group.GroupID))
			}
			return err
		}
	}
	if r.HasTags {
		added, removed := tagSetDiff(cur.Tags, r.Tags)
		if len(added) > 0 || len(removed) > 0 {
			wanted := make([]uuid.UUID, 0, len(r.Tags))
			for _, tag := range r.Tags {
				wanted = append(wanted, tag.TagID)
			}
			if err := replaceCustomerTags(ctx, txq, id, wanted, added, removed, now, o.act); err != nil {
				if db.IsForeignKeyViolation(err, customerTagsTagFK) {
					return refuseRow(row, "tags", "A tag this row names was deleted while the file was being imported")
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
// clear, as the row's error on the group's first field; anything else passes
// through.
func addressRefusal(row int, field string, err error) error {
	if errors.Is(err, errAddressCapReached) {
		return refuseRow(row, field, addressCapMessage)
	}
	var others otherAddressesOfType
	if errors.As(err, &others) {
		return refuseRow(row, field, fmt.Sprintf(
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
// (design D3); a row repeating the identity on file in all four of the file's
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
	return message + ". Tick 'Allow a customer to share a legal identity' to keep both."
}

// importValueRefusal is a row's error when the database refuses one of its
// values as too long or out of range (SQLSTATE class 22).
const importValueRefusal = "A value is out of range or too long for its field."

// errImportDryRun rolls a dry-run row's transaction back once the row ran.
var errImportDryRun = errors.New("customers: import dry run rolled back")

// importRowResult is what one row did: its outcome, the customer it made or
// changed, and the errors that refused it.
type importRowResult struct {
	Outcome    string
	CustomerID *int32
	Errs       []importError
}

// runImportRow runs one row (design D5) in its own transaction on `on`, the
// real run and the dry run alike — the dry run's rolled back at its end
// instead of committed, so a checked row takes the very statements, locks and
// events an imported one does, and keeps none of them (the customer-number
// counter's increment rolls back with the rest; the row ids' identity
// sequences do not, so internal ids skip). In the real run the row's outcome
// is written inside the same transaction (WriteImportRowOutcome), so the
// customer and its outcome commit together or not at all. Each row is retried
// on the deadlock a tag replace can lose to a tag delete (tagWriteAttempts,
// tags.go), the tags PUT's own retry, in both runs. A refusal is the row's
// result; any other error is the run's.
func (s *server) runImportRow(ctx context.Context, on db.TxBeginner, importID int32, l importLayout, p importPlan, planErrs []importError, o importOptions, dryRun bool) (importRowResult, error) {
	if len(planErrs) > 0 {
		return importRowResult{Outcome: "failed", Errs: planErrs}, nil
	}
	var result importRowResult
	err := db.RetrySerializable(ctx, tagWriteAttempts, func() error {
		result = importRowResult{}
		return db.WithTx(ctx, on, pgx.TxOptions{}, func(tx pgx.Tx) error {
			txq := store.New(tx)
			outcome, id, err := s.importRow(ctx, txq, l, p, o)
			if err != nil {
				return err
			}
			result.Outcome, result.CustomerID = "updated", &id
			if outcome == importCreated {
				result.Outcome = "created"
			}
			if dryRun {
				return errImportDryRun
			}
			return txq.WriteImportRowOutcome(ctx, store.WriteImportRowOutcomeParams{
				ImportID: importID, RowNo: int32(p.Row), Outcome: &result.Outcome, CustomerID: &id, Errors: nil,
			})
		})
	})
	// A data exception is a value of the row's the column cannot hold that
	// the checks above let through: the row's refusal, never the run's, so
	// one cell cannot stall the import and every import queued behind it.
	if db.IsDataException(err) {
		err = refuseRow(p.Row, "", importValueRefusal)
	}
	var refused *importRowRefusal
	switch {
	case errors.Is(err, errImportDryRun):
		return result, nil
	case errors.As(err, &refused):
		return importRowResult{Outcome: "failed", Errs: l.sorted(refused.errs)}, nil
	case err != nil:
		return importRowResult{}, err
	}
	return result, nil
}
