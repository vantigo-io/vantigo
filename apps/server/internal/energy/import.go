package energy

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
	"github.com/vantigo-io/vantigo/server/internal/fileimport"
)

// This file is a row of a metering-point import becoming a metering point and,
// when it names a customer, a supply period (energy import design D1, D3,
// D4). It is the customers import's row side (internal/customers/import.go)
// in energy's terms, and the same four things hold:
//
//   - A mapped field is written; an unmapped field is left as it is. On an
//     update the point's current values are laid under the mapped ones and
//     the whole is judged by the validator the endpoints use
//     (validateMeteringPointFields). A mapped cell that is blank clears an
//     optional field.
//   - Each row is written through the functions the endpoints call —
//     insertMeteringPoint, the update's own query, insertSupplyPeriod and
//     switchSupplyPeriod under lockSupplyPeriods — so a row that succeeds is
//     indistinguishable from the same edits made by hand, and a row that fails
//     leaves nothing half-written. Energy writes no events, so there are none
//     to follow.
//   - A dry run is the real run minus the commit: each row in its own
//     transaction, rolled back when the row has run (import_worker.go).
//   - Nothing leaves the database while a row's transaction is open: the
//     customers every organisation number names are resolved once, before the
//     first row (import_worker.go), and every permission the mapping needs
//     was checked when it was saved (imports_mapping.go).

// The fields a mapping may name (design D3), in the order the documentation
// lists them.
const (
	fieldGsrn             = "gsrn"
	fieldMeterNumber      = "meterNumber"
	fieldStreetAddress    = "streetAddress"
	fieldPostalCode       = "postalCode"
	fieldCity             = "city"
	fieldCountryCode      = "countryCode"
	fieldPriceArea        = "priceArea"
	fieldGridArea         = "gridArea"
	fieldExpectedAnnual   = "expectedAnnualConsumptionKwh"
	fieldLatitude         = "latitude"
	fieldLongitude        = "longitude"
	fieldConnectionStatus = "connectionStatus"
	fieldCustomerLegalID  = "customerLegalId"
	fieldSupplyStart      = "supplyStart"
)

var importFields = []string{
	fieldGsrn, fieldMeterNumber, fieldStreetAddress, fieldPostalCode, fieldCity, fieldCountryCode,
	fieldPriceArea, fieldGridArea, fieldExpectedAnnual, fieldLatitude, fieldLongitude,
	fieldConnectionStatus, fieldCustomerLegalID, fieldSupplyStart,
}

// importLayout is a mapping read against a file's header
// (imports_mapping.go's layoutFor): where each field comes from — a column of
// the file, or one value for every row — and the mode the rows run under.
type importLayout struct {
	column map[string]int
	fixed  map[string]string
	width  int
	mode   gen.EnergyImportMappingMode
}

// mapped reports whether field is written at all.
func (l importLayout) mapped(field string) bool {
	_, c := l.column[field]
	_, f := l.fixed[field]
	return c || f
}

// creates reports whether the mode makes every row a new point.
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
// A row shorter than its column is blank there.
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

// isTotalsLine reports whether a row is the totals line an export ends with
// (design D3): its GSRN blank, and at most one other cell not blank — a
// summed annual consumption under an otherwise empty row. It is skipped,
// never failed.
func (l importLayout) isTotalsLine(rec fileimport.Record) bool {
	gsrnColumn, mappedToColumn := l.column[fieldGsrn]
	if !mappedToColumn {
		return false
	}
	if gsrnColumn < len(rec.Cells) && strings.TrimSpace(rec.Cells[gsrnColumn]) != "" {
		return false
	}
	others := 0
	for i, cell := range rec.Cells {
		if i != gsrnColumn && strings.TrimSpace(cell) != "" {
			others++
		}
	}
	return others <= 1
}

// importError is one problem of one row: Field is the import field it is
// about ("" for the row as a whole), which the layout turns into a file
// column for the answer.
type importError struct {
	Row     int
	Field   string
	Message string
}

// wire is the error as the contract carries it.
func (l importLayout) wire(e importError) gen.EnergyImportRowError {
	return gen.EnergyImportRowError{Column: l.columnOf(e.Field), Message: e.Message}
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

// importRowRefusal is a row's errors travelling out of its transaction as the
// error that rolls it back.
type importRowRefusal struct {
	errs []importError
}

func (r *importRowRefusal) Error() string {
	return fmt.Sprintf("energy: import row %d refused", r.errs[0].Row)
}

func refuseRow(row int, field, message string) error {
	return &importRowRefusal{errs: []importError{{Row: row, Field: field, Message: message}}}
}

func refuseRowAll(errs []importError) error {
	return &importRowRefusal{errs: errs}
}

// importPlan is one row read through the layout: its raw values, its GSRN,
// the organisation number it names and the start it gives, each as read.
// Everything about the point itself is resolvePoint's, since on an update it
// depends on the point found.
type importPlan struct {
	Row     int
	Raw     map[string]string
	Gsrn    string
	LegalID string
	Start   *time.Time
}

// plan reads one row (design D3): its raw values, its GSRN, its organisation
// number and its start, and the errors its cells get before any database is
// asked — strict is "every unmapped field is blank", the truth for a create,
// and false when the row may be an update whose unmapped fields come from the
// point, in which case only the mapped fields' own errors are kept.
func (l importLayout) plan(rec fileimport.Record, strict bool) (importPlan, []importError) {
	p := importPlan{Row: rec.Row, Raw: l.raw(rec)}
	if len(rec.Cells) > l.width {
		return p, []importError{{Row: rec.Row, Message: fmt.Sprintf("This row has %d cells, but the header has %d", len(rec.Cells), l.width)}}
	}
	var errs []importError
	fail := func(field, message string) {
		errs = append(errs, importError{Row: rec.Row, Field: field, Message: message})
	}
	switch g := strings.TrimSpace(p.Raw[fieldGsrn]); {
	case g == "":
		fail(fieldGsrn, "This row has no GSRN; a metering point is found and created by its GSRN")
	case !gsrnValid(g):
		fail(fieldGsrn, "GSRN must contain exactly 18 digits.")
	default:
		p.Gsrn = g
	}
	if l.mapped(fieldCustomerLegalID) {
		if v := stripWhitespace(p.Raw[fieldCustomerLegalID]); v != "" {
			if validNorwegianOrgNumber(v) {
				p.LegalID = v
			} else {
				fail(fieldCustomerLegalID, fmt.Sprintf("A Norwegian organisation number must be nine digits with a valid check digit, but was '%s'", strings.TrimSpace(p.Raw[fieldCustomerLegalID])))
			}
		}
	}
	if l.mapped(fieldSupplyStart) {
		if v := strings.TrimSpace(p.Raw[fieldSupplyStart]); v != "" {
			if start, ok := parseImportDate(v); ok {
				p.Start = &start
			} else {
				fail(fieldSupplyStart, fmt.Sprintf("A start date must be a date such as 01.01.2026, 01/01/2026 or 2026-01-01, but was '%s'", v))
			}
		}
	}
	_, pointErrs := l.resolvePoint(p, nil, nil, strict)
	errs = append(errs, pointErrs...)
	return p, l.sorted(errs)
}

// resolvedPoint is a row's metering point as the write takes it: every column
// of the point, the point's own values laid under the mapped ones on an
// update, and on a create the first meter's number.
type resolvedPoint struct {
	streetAddress, postalCode, city, countryCode, priceArea string
	gridArea                                                *string
	expectedAnnual, latitude, longitude                     *float64
	connectionStatus                                        string
	meterNumber                                             string
}

// importRequiredFields are the fields a new point cannot be without (the
// create endpoint's own required fields): a create whose mapping lacks one is
// told so in those words rather than in the validator's.
var importRequiredFields = map[string]string{
	fieldMeterNumber:   "a meter number",
	fieldStreetAddress: "a street address",
	fieldPostalCode:    "a postal code",
	fieldCity:          "a city",
	fieldPriceArea:     "a price area",
}

// validatorFields is validateMeteringPointFields's and validateMeterNumber's
// error keys as the import's fields.
var validatorFields = map[string]string{
	"address.streetAddress":        fieldStreetAddress,
	"address.postalCode":           fieldPostalCode,
	"address.city":                 fieldCity,
	"address.countryCode":          fieldCountryCode,
	"priceArea":                    fieldPriceArea,
	"expectedAnnualConsumptionKwh": fieldExpectedAnnual,
	"connectionStatus":             fieldConnectionStatus,
	"meterNumber":                  fieldMeterNumber,
}

// resolvePoint is a row's point fields laid over cur (design D3): a mapped
// field from the row, an unmapped one from the point, blank or its default on
// a create (cur nil); the whole judged by the endpoints' own validator.
// meterNumber is the point's current meter's number on an update, which a
// mapped meter number must equal — a meter is replaced by hand, never by an
// import. With strict false only the mapped fields' errors are kept.
func (l importLayout) resolvePoint(p importPlan, cur *store.EnergyMeteringPoint, curMeter *string, strict bool) (resolvedPoint, []importError) {
	var r resolvedPoint
	var errs []importError
	fail := func(field, message string) {
		errs = append(errs, importError{Row: p.Row, Field: field, Message: message})
	}
	text := func(field string, current func() string) string {
		if v, ok := p.Raw[field]; ok {
			return strings.TrimSpace(v)
		}
		if cur != nil {
			return current()
		}
		return ""
	}
	r.streetAddress = text(fieldStreetAddress, func() string { return cur.StreetAddress })
	r.postalCode = text(fieldPostalCode, func() string { return cur.PostalCode })
	r.city = text(fieldCity, func() string { return cur.City })
	switch v, ok := p.Raw[fieldCountryCode]; {
	case ok && strings.TrimSpace(v) != "":
		r.countryCode = strings.TrimSpace(v)
	case ok || cur == nil:
		r.countryCode = "NO"
	default:
		r.countryCode = cur.CountryCode
	}
	if v, ok := p.Raw[fieldPriceArea]; ok {
		r.priceArea = normalisePriceArea(v)
	} else if cur != nil {
		r.priceArea = cur.PriceArea
	}
	if v, ok := p.Raw[fieldGridArea]; ok {
		r.gridArea = trimmedOrNil(&v)
	} else if cur != nil {
		r.gridArea = cur.GridArea
	}
	decimal := func(field, what string, current *float64) *float64 {
		v, ok := p.Raw[field]
		if !ok {
			return current
		}
		if strings.TrimSpace(v) == "" {
			return nil
		}
		f, valid := parseImportDecimal(v)
		if !valid {
			fail(field, fmt.Sprintf("%s must be a number, but was '%s'", what, strings.TrimSpace(v)))
			return nil
		}
		return &f
	}
	var curExpected, curLatitude, curLongitude *float64
	if cur != nil {
		curExpected, curLatitude, curLongitude = floatPtrFromNumeric(cur.ExpectedAnnualConsumptionKwh), cur.Latitude, cur.Longitude
	}
	r.expectedAnnual = decimal(fieldExpectedAnnual, "Expected annual consumption", curExpected)
	r.latitude = decimal(fieldLatitude, "Latitude", curLatitude)
	r.longitude = decimal(fieldLongitude, "Longitude", curLongitude)

	// The connection status: the Norwegian words read as the three values; a
	// blank cell is New on a create and the point's own on an update, since
	// the column cannot be empty.
	var status *string
	if v, ok := p.Raw[fieldConnectionStatus]; ok && strings.TrimSpace(v) != "" {
		s := importConnectionStatus(v)
		status = &s
	} else if cur != nil {
		status = &cur.ConnectionStatus
	}

	gsrn := p.Gsrn
	if gsrn == "" {
		gsrn = strings.Repeat("0", 18) // the GSRN's own error is plan's
	}
	address := gen.AddressRequest{StreetAddress: &r.streetAddress, PostalCode: &r.postalCode, City: &r.city, CountryCode: &r.countryCode}
	fieldErrs := validateMeteringPointFields(&gsrn, &address, &r.priceArea, r.expectedAnnual, r.latitude, r.longitude, status)
	if cur == nil {
		// A blank meter number is a create's error only: planned without
		// knowing whether it creates (strict false), the row may be an update,
		// where a blank leaves the meter alone.
		meter := text(fieldMeterNumber, nil)
		if msg := validateMeterNumber(&meter); msg != "" && (strict || meter != "") {
			fieldErrs["meterNumber"] = []string{msg}
		}
		r.meterNumber = meter
	} else if v := strings.TrimSpace(p.Raw[fieldMeterNumber]); v != "" && (curMeter == nil || *curMeter != v) {
		fail(fieldMeterNumber, "The meter number differs from the current meter's; use Replace meter.")
	}
	if status != nil {
		r.connectionStatus, _ = parseConnectionStatus(status)
	} else {
		r.connectionStatus = "New"
	}
	for key, messages := range fieldErrs {
		field, known := validatorFields[key]
		if key == "location" {
			field = fieldLatitude
			if validateLocation(r.latitude, nil) == "" {
				field = fieldLongitude
			}
			known = true
		}
		if !known {
			continue
		}
		for _, m := range messages {
			if label, required := importRequiredFields[field]; required && cur == nil && !l.mapped(field) {
				m = fmt.Sprintf("A new metering point needs %s, and no %s column is mapped", label, field)
			}
			fail(field, m)
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

// normalisePriceArea is a price area as a file spells it (design D3): its
// whitespace gone and upper-cased, so "NO 1", "no1" and "NO1" are all NO1.
func normalisePriceArea(raw string) string {
	return strings.ToUpper(stripWhitespace(raw))
}

// importConnectionStatus is a connection-status cell (design D3): the
// Norwegian words ny, tilkoblet and frakoblet read as New, Connected and
// Disconnected; anything else goes to the endpoint's own check as written.
func importConnectionStatus(raw string) string {
	v := strings.TrimSpace(raw)
	switch strings.ToLower(v) {
	case "ny":
		return "New"
	case "tilkoblet":
		return "Connected"
	case "frakoblet":
		return "Disconnected"
	}
	return v
}

// importDecimalPattern is the one shape a number cell may have once its
// spaces are gone and its decimal comma is a point: the customers import's
// rule (no exponent, no NaN, no thousands separator but the space).
var importDecimalPattern = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// parseImportDecimal reads a number cell (design D3): whitespace dropped — a
// Norwegian export groups thousands with a space — and a decimal comma read as
// a point.
func parseImportDecimal(raw string) (float64, bool) {
	v := strings.ReplaceAll(stripWhitespace(raw), ",", ".")
	if !importDecimalPattern.MatchString(v) {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	return f, err == nil
}

// The date shapes a start is read in (design D3): ISO, and day first with a
// dot or a slash — always day first, never the American month first.
var (
	importISODate      = regexp.MustCompile(`^([0-9]{4})-([0-9]{1,2})-([0-9]{1,2})$`)
	importDayFirstDate = regexp.MustCompile(`^([0-9]{1,2})[./]([0-9]{1,2})[./]([0-9]{4})$`)
)

// parseImportDate reads a start date as midnight UTC, the Assign customer
// dialog's own instant: 2026-01-01, 01.01.2026 or 01/01/2026. A day the
// calendar does not have (31.02.2026) is refused, not rolled over.
func parseImportDate(raw string) (time.Time, bool) {
	var y, m, d string
	if g := importISODate.FindStringSubmatch(raw); g != nil {
		y, m, d = g[1], g[2], g[3]
	} else if g := importDayFirstDate.FindStringSubmatch(raw); g != nil {
		d, m, y = g[1], g[2], g[3]
	} else {
		return time.Time{}, false
	}
	year, _ := strconv.Atoi(y)
	month, _ := strconv.Atoi(m)
	day, _ := strconv.Atoi(d)
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if t.Year() != year || int(t.Month()) != month || t.Day() != day {
		return time.Time{}, false
	}
	return t, true
}

// stripWhitespace removes every Unicode whitespace rune, a non-breaking space
// included: an organisation number is often grouped in threes, a number in
// thousands.
func stripWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == ' ' {
			return -1
		}
		return r
	}, s)
}

// validNorwegianOrgNumber is the customers module's own check (depguard
// forbids importing it): nine digits whose last is the mod-11 check digit of
// the first eight.
func validNorwegianOrgNumber(digits string) bool {
	if len(digits) != 9 {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	weights := [8]int{3, 2, 7, 6, 5, 4, 3, 2}
	sum := 0
	for i, w := range weights {
		sum += int(digits[i]-'0') * w
	}
	check := 0
	if r := sum % 11; r != 0 {
		check = 11 - r
		if check == 10 {
			return false
		}
	}
	return int(digits[8]-'0') == check
}

// importLegalCountry is the country every organisation number of a file is
// looked up in (design D3): a supplier's export has a Norwegian number and
// nothing else about the identity.
const importLegalCountry = "no"

// importCustomers is the customers a run's organisation numbers resolved to,
// read once before the first row (design D4): off when no module provides
// the customer directory.
type importCustomers struct {
	off     bool
	holders map[string][]contracts.CustomerEntry
}

// customersModuleOffMessage is a row's error, and the mapping's, when no
// customer directory is composed.
const customersModuleOffMessage = "The customers module is off, so an organisation number cannot be matched to a customer"

// customer is the one customer a row's organisation number names, or the
// row's error on that column (design D4): nobody, several, merged away or
// archived.
func (c importCustomers) customer(legalID string) (*contracts.CustomerEntry, string) {
	if c.off {
		return nil, customersModuleOffMessage
	}
	holders := c.holders[legalID]
	switch len(holders) {
	case 0:
		return nil, fmt.Sprintf("No customer has organisation number %s", legalID)
	case 1:
	default:
		names := make([]string, 0, len(holders))
		for _, h := range holders {
			names = append(names, fmt.Sprintf("%d %s", h.ID, h.Name))
		}
		return nil, fmt.Sprintf("%d customers have organisation number %s (%s); the file cannot say which one is meant", len(holders), legalID, strings.Join(names, ", "))
	}
	return &holders[0], customerRefusal(holders[0])
}

// customerRefusal is why a customer takes no supply period, "" when it does:
// merged away into another customer, or archived.
func customerRefusal(c contracts.CustomerEntry) string {
	switch {
	case c.MergedInto != nil:
		return fmt.Sprintf("Customer %d %s was merged into customer %d and takes no more supply periods", c.ID, c.Name, *c.MergedInto)
	case c.Archived:
		return fmt.Sprintf("Customer %d %s is archived and takes no more supply periods", c.ID, c.Name)
	}
	return ""
}

// importLink values (design D4), as import_rows.link holds them.
const (
	linkNone     = "none"
	linkSame     = "same"
	linkAssigned = "assigned"
	linkSwitched = "switched"
	linkPending  = "pending"
	linkAttached = "attached"
)

// importRowResult is what one row did: its outcome, the point it made or
// changed, its customer link, and the errors that refused it.
type importRowResult struct {
	Outcome     string
	PointID     *int32
	Gsrn        *string
	CustomerID  *int32
	Link        *string
	SwitchFrom  *int32
	SupplyStart *time.Time
	Errs        []importError
}

// switchBudget bounds how many points one run or attach switches (design
// D4): the number the person confirmed, kept on the import
// (imports.confirmed_switches) and spent one switch at a time inside the
// switching row's own transaction, so a resumed run starts from what is left.
// A dry run has none — it counts, it does not bound.
type switchBudget struct {
	bounded  bool
	left     int
	importID int32
}

// importRow writes one planned row inside tx (design D3, D4): the customer the
// row names resolved first — a row naming nobody, several, a merged-away or an
// archived customer writes nothing — then the point found by GSRN and updated,
// or none found and one created, as the mode says, then the customer link
// under the point's supply-period lock. Anything only the database can tell is
// an importRowRefusal, which rolls the row back; any other error is the run's.
func (s *server) importRow(ctx context.Context, tx pgx.Tx, l importLayout, p importPlan, customers importCustomers, budget *switchBudget) (importRowResult, error) {
	var customer *contracts.CustomerEntry
	if p.LegalID != "" {
		c, refusal := customers.customer(p.LegalID)
		if refusal != "" {
			return importRowResult{}, refuseRow(p.Row, fieldCustomerLegalID, refusal)
		}
		customer = c
	}

	txq := store.New(tx)
	cur, err := txq.MeteringPointByGsrnForUpdate(ctx, p.Gsrn)
	found := true
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		found = false
	case err != nil:
		return importRowResult{}, err
	}
	result := importRowResult{Gsrn: &p.Gsrn}
	var pointID int32
	switch {
	case found && l.creates():
		return importRowResult{}, refuseRow(p.Row, fieldGsrn, "A metering point with that GSRN already exists.")
	case !found && l.mode == gen.Update:
		return importRowResult{}, refuseRow(p.Row, fieldGsrn, fmt.Sprintf("No metering point has GSRN %s", p.Gsrn))
	case !found:
		r, errs := l.resolvePoint(p, nil, nil, true)
		if len(errs) > 0 {
			return importRowResult{}, refuseRowAll(l.sorted(errs))
		}
		expected, err := numericFromFloatPtr(r.expectedAnnual)
		if err != nil {
			return importRowResult{}, refuseRow(p.Row, fieldExpectedAnnual, "Expected annual consumption is not a number that can be stored")
		}
		created, err := insertMeteringPoint(ctx, txq, store.InsertMeteringPointParams{
			Gsrn: p.Gsrn, StreetAddress: r.streetAddress, PostalCode: r.postalCode, City: r.city,
			CountryCode: strings.ToUpper(r.countryCode), PriceArea: r.priceArea, GridArea: r.gridArea,
			ExpectedAnnualConsumptionKwh: expected, Latitude: r.latitude, Longitude: r.longitude,
			ConnectionStatus: r.connectionStatus, Now: s.deps.Clock(),
		}, r.meterNumber)
		if db.IsUniqueViolation(err, "ux_metering_points_gsrn") {
			return importRowResult{}, refuseRow(p.Row, fieldGsrn, "A metering point with that GSRN already exists.")
		}
		if err != nil {
			return importRowResult{}, err
		}
		pointID, result.Outcome = created.ID, string(gen.Created)
	default:
		meter, err := activeMeterNumber(ctx, txq, cur.ID)
		if err != nil {
			return importRowResult{}, err
		}
		r, errs := l.resolvePoint(p, &cur, meter, true)
		if len(errs) > 0 {
			return importRowResult{}, refuseRowAll(l.sorted(errs))
		}
		if err := s.importUpdatePoint(ctx, txq, p, cur, r); err != nil {
			return importRowResult{}, err
		}
		pointID, result.Outcome = cur.ID, string(gen.Updated)
	}
	result.PointID = &pointID

	link := linkNone
	if customer != nil {
		result.CustomerID = &customer.ID
		result.SupplyStart = p.Start
		link, result.SwitchFrom, err = importLink(ctx, tx, p, pointID, customer.ID, p.Start, budget)
		if err != nil {
			return importRowResult{}, err
		}
		if link == linkSame {
			result.SupplyStart = nil
		}
	}
	result.Link = &link
	return result, nil
}

// importUpdatePoint is PUT /metering-points/{id}'s write for a row, on the
// point the row matched: the point's own values laid under the row's, and
// written only when they differ, so a row that repeats what is on file writes
// nothing and moves no updated_at.
func (s *server) importUpdatePoint(ctx context.Context, txq *store.Queries, p importPlan, cur store.EnergyMeteringPoint, r resolvedPoint) error {
	expected, err := numericFromFloatPtr(r.expectedAnnual)
	if err != nil {
		return refuseRow(p.Row, fieldExpectedAnnual, "Expected annual consumption is not a number that can be stored")
	}
	country := strings.ToUpper(r.countryCode)
	same := cur.StreetAddress == r.streetAddress && cur.PostalCode == r.postalCode && cur.City == r.city &&
		cur.CountryCode == country && cur.PriceArea == r.priceArea && stringPtrEqual(cur.GridArea, r.gridArea) &&
		floatPtrEqual(floatPtrFromNumeric(cur.ExpectedAnnualConsumptionKwh), r.expectedAnnual) &&
		floatPtrEqual(cur.Latitude, r.latitude) && floatPtrEqual(cur.Longitude, r.longitude) &&
		cur.ConnectionStatus == r.connectionStatus
	if same {
		return nil
	}
	_, err = txq.UpdateMeteringPoint(ctx, store.UpdateMeteringPointParams{
		ID: cur.ID, Gsrn: cur.Gsrn, StreetAddress: r.streetAddress, PostalCode: r.postalCode, City: r.city,
		CountryCode: country, PriceArea: r.priceArea, GridArea: r.gridArea, ExpectedAnnualConsumptionKwh: expected,
		Latitude: r.latitude, Longitude: r.longitude, ConnectionStatus: r.connectionStatus, UpdatedAt: s.deps.Clock(),
	})
	return err
}

// importLink is a row's customer link (design D4's table), under the point's
// supply-period lock, given the customer C the row's number resolved to and
// the start S (nil when the row gives none), against the point's open Active
// period P:
//
//   - P is already C's: same, nothing written, whatever S says.
//   - no P and S: assigned, a period from S (insertSupplyPeriod), refused when
//     it would overlap an ended or later period.
//   - no P and no S: pending.
//   - P another customer's and S: switched (switchSupplyPeriod) — S must be
//     after P's start — within the run's confirmed switches.
//   - P another customer's and no S: pending, switch_from set.
func importLink(ctx context.Context, tx pgx.Tx, p importPlan, pointID, customerID int32, start *time.Time, budget *switchBudget) (string, *int32, error) {
	if err := lockSupplyPeriods(ctx, tx, pointID); err != nil {
		return "", nil, err
	}
	txq := store.New(tx)
	active, err := txq.GetActiveOpenSupplyPeriod(ctx, pointID)
	hasActive := true
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		hasActive = false
	case err != nil:
		return "", nil, err
	}
	switch {
	case hasActive && active.CustomerID == customerID:
		return linkSame, nil, nil
	case !hasActive && start == nil:
		return linkPending, nil, nil
	case hasActive && start == nil:
		from := active.CustomerID
		return linkPending, &from, nil
	case !hasActive:
		if err := assignSupplyPeriod(ctx, tx, p.Row, pointID, customerID, *start); err != nil {
			return "", nil, err
		}
		return linkAssigned, nil, nil
	}
	from := active.CustomerID
	if err := switchSupplyPeriodFor(ctx, tx, p.Row, pointID, from, customerID, *start, budget); err != nil {
		return "", nil, err
	}
	return linkSwitched, &from, nil
}

// assignSupplyPeriod is the create endpoint's overlap pre-check and write for
// one row, the overlap the row's error in the endpoint's own words.
func assignSupplyPeriod(ctx context.Context, tx pgx.Tx, row int, pointID, customerID int32, start time.Time) error {
	overlaps, err := store.New(tx).SupplyPeriodOverlapExists(ctx, store.SupplyPeriodOverlapExistsParams{MeteringPointID: pointID, Start: start})
	if err != nil {
		return err
	}
	if overlaps {
		return refuseRow(row, fieldSupplyStart, overlapDetail)
	}
	if _, err := insertSupplyPeriod(ctx, tx, pointID, customerID, start); err != nil {
		if isExclusionViolation(err) {
			return refuseRow(row, fieldSupplyStart, overlapDetail)
		}
		return err
	}
	return nil
}

// switchSupplyPeriodFor is the switch endpoint's write for one row, with the
// endpoint's refusals — a start at or before the current period's, an
// overlap — the row's error, and within the budget the person confirmed.
func switchSupplyPeriodFor(ctx context.Context, tx pgx.Tx, row int, pointID, fromCustomerID, customerID int32, at time.Time, budget *switchBudget) error {
	out, err := switchSupplyPeriod(ctx, tx, pointID, customerID, at)
	if isExclusionViolation(err) {
		return refuseRow(row, fieldSupplyStart, overlapDetail)
	}
	if err != nil {
		return err
	}
	if out.conflict {
		return refuseRow(row, fieldSupplyStart, overlapDetail)
	}
	for _, messages := range out.validationErrs {
		return refuseRow(row, fieldSupplyStart, messages[0])
	}
	// The budget is asked once the switch itself has been judged, so a row
	// the switch refuses says why in the switch's own words; a switch beyond
	// the budget is refused here, and the refusal rolls it back.
	if budget.bounded {
		if budget.left <= 0 {
			return refuseRow(row, fieldCustomerLegalID, fmt.Sprintf(
				"This row would switch the metering point from customer %d to customer %d, which was not among the switches confirmed; check again and confirm them", fromCustomerID, customerID))
		}
		if err := store.New(tx).SpendConfirmedSwitch(ctx, budget.importID); err != nil {
			return err
		}
	}
	budget.left--
	return nil
}

// isExclusionViolation reports whether err is the supply periods' GiST
// exclusion constraint (23P01) refusing an overlap a concurrent write slipped
// past the pre-check.
func isExclusionViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23P01"
}

func stringPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func floatPtrEqual(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// errImportDryRun rolls a dry-run row's transaction back once the row ran.
var errImportDryRun = errors.New("energy: import dry run rolled back")

// importRowAttempts bounds the retry of one row's transaction against a
// serialization failure or a deadlock, the supply-period endpoints' own bound.
const importRowAttempts = supplyPeriodRaceAttempts

// runImportRow runs one row (design D6) in its own transaction on `on`, the
// real run and the dry run alike — the dry run's rolled back at its end
// instead of committed, so a checked row takes the very statements and locks
// an imported one does and keeps none of them. In the real run the row's
// outcome is written inside the same transaction, so the point, its period
// and its outcome commit together or not at all. A refusal is the row's
// result; any other error is the run's.
func (s *server) runImportRow(ctx context.Context, on db.TxBeginner, importID int32, l importLayout, p importPlan, planErrs []importError, customers importCustomers, budget *switchBudget, dryRun bool) (importRowResult, error) {
	if len(planErrs) > 0 {
		return importRowResult{Outcome: string(gen.Failed), Errs: planErrs}, nil
	}
	var result importRowResult
	err := db.RetrySerializable(ctx, importRowAttempts, func() error {
		result = importRowResult{}
		attempt := *budget
		return db.WithTx(ctx, on, pgx.TxOptions{}, func(tx pgx.Tx) error {
			var err error
			if result, err = s.importRow(ctx, tx, l, p, customers, &attempt); err != nil {
				return err
			}
			if dryRun {
				*budget = attempt
				// A point a check would create has no id: the dry run keeps none.
				if result.Outcome == string(gen.Created) {
					result.PointID = nil
				}
				return errImportDryRun
			}
			if err := store.New(tx).WriteImportRowOutcome(ctx, store.WriteImportRowOutcomeParams{
				ImportID: importID, RowNo: int32(p.Row), Outcome: &result.Outcome, MeteringPointID: result.PointID,
				Gsrn: result.Gsrn, CustomerID: result.CustomerID, Link: result.Link, SwitchFrom: result.SwitchFrom,
				SupplyStart: result.SupplyStart, Errors: nil,
			}); err != nil {
				return err
			}
			*budget = attempt
			return nil
		})
	})
	var refused *importRowRefusal
	switch {
	case errors.Is(err, errImportDryRun):
		return result, nil
	case errors.As(err, &refused):
		gsrn := &p.Gsrn
		if p.Gsrn == "" {
			gsrn = nil
		}
		return importRowResult{Outcome: string(gen.Failed), Gsrn: gsrn, Errs: l.sorted(refused.errs)}, nil
	case err != nil:
		return importRowResult{}, err
	}
	return result, nil
}
