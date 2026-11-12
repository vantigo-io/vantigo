package inventory

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/fileimport"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// The opening-balance import (design I32): a CSV of location, sku and
// quantity for one site, read by srv/fileimport — UTF-8 or Windows-1252, the
// separator ; , or tab detected — with a header naming exactly those three
// columns, in any order and any case, and at most 5000 rows.
//
// Every row is resolved before any transaction: the location by its full code
// among the site's (an archived one is location_not_found), the variant by
// its SKU through the catalog's LookupCode (a barcode or a packaging's
// barcode does not match), and the quantity as a decimal more than 0 with at
// most three places, its separator a point or a comma and no thousands
// separator. A dry run — the default — answers each row's outcome. A commit
// with any failing row posts nothing and answers the rows with ok false; one
// with every row ok posts ONE adjustment, reason opening_balance, every row a
// change adding its quantity, through the engine with no line cap. Its key is
// derived from the SHA-256 of the file and the site, so the same file posted
// twice to the same site is one movement: the second answers it, 200.
//
// The file may carry the lot columns too (manufacturing phase 2 M71): lot,
// supplier_lot, production_date, best_before and expiry, each optional, the
// dates as YYYY-MM-DD or DD.MM.YYYY. A tracked variant's row names its lot by
// number (lot_required), an untracked one's none (lot_not_allowed), and a
// serial's row is of quantity 1 (serial_quantity); a number or a date that
// does not read, or dates out of order, is lot_invalid. The lots are found or
// made as a receipt's are (M57), the import — its movement — their origin.
// This is how stock already on hand is brought in with lots: set the
// variant's tracking while it holds nothing, then import its opening
// balance.

const (
	maxImportRows         = 5000
	maxImportBytes        = 4 << 20
	maxImportRequestBytes = maxImportBytes + 64<<10
	maxImportColumns      = 20
)

var importLimits = fileimport.Limits{MaxBytes: maxImportBytes, MaxRows: maxImportRows, MaxColumns: maxImportColumns}

// importColumns are the header's three names, as fileimport.HeaderKey reads
// them; importLotColumns the optional ones of a file with lots (M71) —
// supplier_lot, production_date and best_before as HeaderKey reads them.
var (
	importColumns    = []string{"location", "sku", "quantity"}
	importLotColumns = []string{"lot", "supplierlot", "productiondate", "bestbefore", "expiry"}
)

// lotOriginImport is the origin kind of a lot the import made (M71); its id
// is the import's movement.
const lotOriginImport = "import"

// parseImportDate is a date cell as a date, nil when blank, or why it is not
// one: YYYY-MM-DD, or DD.MM.YYYY as a Norwegian spreadsheet writes it.
func parseImportDate(text, what string) (*time.Time, string) {
	t := strings.TrimSpace(text)
	if t == "" {
		return nil, ""
	}
	for _, layout := range []string{time.DateOnly, "02.01.2006", "2.1.2006"} {
		if d, err := time.Parse(layout, t); err == nil {
			return &d, ""
		}
	}
	return nil, fmt.Sprintf("The %s %q is not a date such as 2026-12-31 or 31.12.2026", what, t)
}

// importKeySpace is the namespace an import's clientRequestId is derived in.
var importKeySpace = uuid.MustParse("0b6a3f0e-8f53-4d7e-a1c2-5e9b7d4c3a10")

// importQuantityPattern is a quantity as the file may write it.
var importQuantityPattern = regexp.MustCompile(`^[0-9]+([.,][0-9]+)?$`)

// parseImportQuantity is a row's quantity as an exact decimal, or why it is
// not one.
func parseImportQuantity(text string) (*big.Rat, string) {
	q, msg := parseImportDecimal(text, "quantity")
	if msg == "" && q.Sign() <= 0 {
		return nil, "The quantity must be more than 0"
	}
	return q, msg
}

// parseImportDecimal is a cell as an exact decimal of 0 or more, or why it
// is not one, naming the cell as what.
func parseImportDecimal(text, what string) (*big.Rat, string) {
	t := strings.TrimSpace(text)
	if !importQuantityPattern.MatchString(t) {
		return nil, "The " + what + " is not a number such as 12 or 2.5"
	}
	t = strings.Replace(t, ",", ".", 1)
	if _, decimals, ok := strings.Cut(t, "."); ok && len(decimals) > quantityPlaces {
		return nil, fmt.Sprintf("The %s %s has more than three decimals", what, t)
	}
	q, ok := new(big.Rat).SetString(t)
	switch {
	case !ok:
		return nil, "The " + what + " is not a number such as 12 or 2.5"
	case q.Cmp(maxQuantity) > 0:
		return nil, "The " + what + " is more than 99,999,999,999.999"
	}
	return q, ""
}

// importProblem is the 400 of a file or a request the import cannot read.
func importProblem(field, message string) gen.ImportInventoryOpeningBalances400ApplicationProblemPlusJSONResponse {
	var f fieldErrors
	f.add(field, message)
	return gen.ImportInventoryOpeningBalances400ApplicationProblemPlusJSONResponse(invalid(invalidImportTitle, f))
}

// ImportInventoryOpeningBalances Import opening balances
// (POST /api/v1/inventory/imports/opening-balances)
func (s *server) ImportInventoryOpeningBalances(ctx context.Context, req gen.ImportInventoryOpeningBalancesRequestObject) (gen.ImportInventoryOpeningBalancesResponseObject, error) {
	if req.Body == nil {
		return importProblem("file", fileimport.EmptyMessage), nil
	}
	data, err := io.ReadAll(io.LimitReader(req.Body, maxImportBytes+1))
	if err != nil {
		return importProblem("file", fmt.Sprintf(fileimport.UnreadableMessage, err)), nil
	}
	if len(data) > maxImportBytes {
		return importProblem("file", fmt.Sprintf("The file is larger than %d MB, which is more than one import takes", maxImportBytes>>20)), nil
	}
	file, refusal := fileimport.Read(data, importLimits)
	if refusal != "" {
		return importProblem("file", refusal), nil
	}
	column := map[string]int{}
	for i, h := range file.Header {
		column[fileimport.HeaderKey(h)] = i
	}
	known := 0
	for name := range column {
		if slices.Contains(importColumns, name) || slices.Contains(importLotColumns, name) {
			known++
		}
	}
	for _, name := range importColumns {
		if _, ok := column[name]; !ok || known != len(file.Header) {
			return importProblem("file", "The header must name the columns location, sku and quantity, and may name lot, supplier_lot, production_date, best_before and expiry"), nil
		}
	}
	_, withLots := column["lot"]
	site, err := store.New(s.deps.Pool).GetSite(ctx, req.Params.SiteId)
	if noRows(err) {
		return importProblem("siteId", "No site has this id"), nil
	}
	if err != nil {
		return nil, fmt.Errorf("inventory: read site %d: %w", req.Params.SiteId, err)
	}

	rows, lines, err := s.resolveImport(ctx, site.ID, file, column)
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.ImportInventoryOpeningBalances503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	result := gen.InventoryImportResult{Ok: len(lines) == len(rows), Rows: rows}
	if (req.Params.DryRun == nil || *req.Params.DryRun) || !result.Ok {
		return gen.ImportInventoryOpeningBalances200JSONResponse(result), nil
	}
	// Today, on the pool before the transaction (M62): what a new lot's
	// dates default from.
	var today time.Time
	if withLots {
		if today, err = s.today(ctx); err != nil {
			return nil, err
		}
	}

	sum := sha256.Sum256(data)
	key := append(sum[:], []byte(":"+strconv.FormatInt(site.ID, 10))...)
	hash := sha256.Sum256(append([]byte("import\n"), key...))
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	reason := string(gen.InventoryAdjustmentReasonOpeningBalance)
	var res postResult
	err = db.RetrySerializable(ctx, postAttempts, func() error {
		return s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
			var err error
			res, err = s.post(ctx, tx, txq, movementHeader{
				clientRequestID: uuid.NewSHA1(importKeySpace, key), requestHash: hash[:], siteID: site.ID,
				kind: string(gen.Adjustment), reason: &reason, poster: who,
			}, lines, postOptions{today: today, lotOrigin: lotOriginImport})
			return err
		})
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.ImportInventoryOpeningBalances400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusConflict:
			return gen.ImportInventoryOpeningBalances409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.ImportInventoryOpeningBalances503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	result.MovementId = &res.movementID
	if res.replay {
		return gen.ImportInventoryOpeningBalances200JSONResponse(result), nil
	}
	return gen.ImportInventoryOpeningBalances201JSONResponse(result), nil
}

// resolveImport answers every row's outcome and, for the rows that are ok,
// the engine's lines, each indexed by its place among the file's rows. It
// reads the catalog once for every distinct SKU together, before any
// transaction.
func (s *server) resolveImport(ctx context.Context, siteID int64, file fileimport.File, column map[string]int) ([]gen.InventoryImportRow, []engineLine, error) {
	cell := func(r fileimport.Record, name string) string {
		if i, ok := column[name]; ok && i < len(r.Cells) {
			return strings.TrimSpace(r.Cells[i])
		}
		return ""
	}
	var codes []string
	skus := map[string]bool{}
	for _, r := range file.Rows {
		codes = append(codes, strings.ToUpper(cell(r, "location")))
		skus[strings.ToLower(cell(r, "sku"))] = true
	}
	locations, err := store.New(s.deps.Pool).SiteLocationsByFullCodes(ctx, store.SiteLocationsByFullCodesParams{
		SiteID: siteID, FullCodes: distinct(codes),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("inventory: read the import's locations: %w", err)
	}
	byCode := make(map[string]store.InventoryLocation, len(locations))
	for _, l := range locations {
		byCode[l.FullCode] = l
	}
	type variant struct {
		id        int32
		stockable bool
		tracking  string
	}
	var distinctSKUs []string
	for sku := range skus {
		if sku != "" {
			distinctSKUs = append(distinctSKUs, sku)
		}
	}
	found, err := s.catalogLookupSKUs(ctx, distinctSKUs)
	if err != nil {
		return nil, nil, err
	}
	bySKU := map[string]variant{}
	for sku, entries := range found {
		// The first by id, as the lookup by code answered one SKU before.
		bySKU[sku] = variant{id: entries[0].ID, stockable: entries[0].ProductType == productTypeGoods, tracking: entries[0].Tracking}
	}

	rows := make([]gen.InventoryImportRow, 0, len(file.Rows))
	var lines []engineLine
	for i, r := range file.Rows {
		row := gen.InventoryImportRow{
			Row: int32(r.Row + 1), Location: cell(r, "location"), Sku: cell(r, "sku"), Quantity: cell(r, "quantity"),
			Outcome: gen.InventoryImportOutcomeOk,
		}
		if _, ok := column["lot"]; ok {
			row.Lot = ptr(cell(r, "lot"))
		}
		loc, locOK := byCode[strings.ToUpper(row.Location)]
		v, variantOK := bySKU[strings.ToLower(row.Sku)]
		q, msg := parseImportQuantity(row.Quantity)
		lot, lotMsg := importLot(cell, r)
		isTracked := v.tracking != "" && v.tracking != contracts.TrackingNone
		switch {
		case extraCells(r, len(file.Header)):
			// A decimal comma in a comma-separated file splits the quantity:
			// "2,5" read as 2 and a fifth cell would import the wrong amount.
			row.Outcome, row.Detail = gen.InventoryImportOutcomeQuantityInvalid, ptr("The row has more cells than the header. Put a quantity with a decimal comma in quotes, such as \"2,5\", or separate the columns with ;")
		case !locOK:
			row.Outcome, row.Detail = gen.InventoryImportOutcomeLocationNotFound, ptr("No location of this site has this full code")
		case loc.Status != string(gen.Active):
			row.Outcome, row.Detail = gen.InventoryImportOutcomeLocationNotFound, ptr(loc.FullCode+" is archived and takes no stock")
		case !variantOK:
			row.Outcome, row.Detail = gen.InventoryImportOutcomeVariantNotFound, ptr("No variant has this SKU")
		case !v.stockable:
			row.Outcome, row.Detail = gen.InventoryImportOutcomeVariantNotStockable, ptr(row.Sku+" is a service; only goods are kept in stock")
		case msg != "":
			row.Outcome, row.Detail = gen.InventoryImportOutcomeQuantityInvalid, &msg
		case isTracked && lot == nil && lotMsg == "":
			row.Outcome, row.Detail = gen.InventoryImportOutcomeLotRequired, ptr(row.Sku+" is tracked in lots; the row must name its lot")
		case !isTracked && (lot != nil || lotMsg != ""):
			row.Outcome, row.Detail = gen.InventoryImportOutcomeLotNotAllowed, ptr(row.Sku+" is not tracked in lots; the row names none")
		case lotMsg != "":
			row.Outcome, row.Detail = gen.InventoryImportOutcomeLotInvalid, &lotMsg
		case v.tracking == contracts.TrackingSerial && q.Cmp(big.NewRat(1, 1)) != 0:
			row.Outcome, row.Detail = gen.InventoryImportOutcomeSerialQuantity, ptr(row.Sku+" is tracked in serials; a serial's row is of quantity 1")
		default:
			to := loc.ID
			lines = append(lines, engineLine{index: i, variantID: v.id, to: &to, quantity: q, newLot: lot})
		}
		rows = append(rows, row)
	}
	return rows, lines, nil
}

// importLot is a row's lot as the engine takes it, nil when the row names
// none — no lot column, or every lot cell blank — or why its cells do not
// read.
func importLot(cell func(fileimport.Record, string) string, r fileimport.Record) (*lotRequest, string) {
	number := cell(r, "lot")
	supplier := cell(r, "supplierlot")
	production, msg1 := parseImportDate(cell(r, "productiondate"), "production date")
	bestBefore, msg2 := parseImportDate(cell(r, "bestbefore"), "best-before date")
	expiry, msg3 := parseImportDate(cell(r, "expiry"), "expiry")
	if number == "" && supplier == "" && production == nil && bestBefore == nil && expiry == nil && msg1+msg2+msg3 == "" {
		return nil, ""
	}
	switch {
	case number == "":
		return nil, "The row gives a lot's details but no lot number"
	case !lotNumberPattern.MatchString(number):
		return nil, "A lot number is 1–20 characters A–Z, a–z, 0–9 and . _ / -"
	case utf8.RuneCountInString(supplier) > 40:
		return nil, "A supplier lot is at most 40 characters"
	case msg1 != "":
		return nil, msg1
	case msg2 != "":
		return nil, msg2
	case msg3 != "":
		return nil, msg3
	}
	var f fieldErrors
	checkLotDates(&f, "", production, bestBefore, expiry)
	if f.failed() {
		return nil, "The dates are out of order: the expiry is before the production date, or the best-before after the expiry"
	}
	out := &lotRequest{number: number, production: production, bestBefore: bestBefore, expiry: expiry, compareProduction: true}
	if supplier != "" {
		out.supplierLot = &supplier
	}
	return out, ""
}

// extraCells reports whether the record has a non-blank cell past the
// header's columns; blank trailing cells, as spreadsheets write, are harmless.
func extraCells(r fileimport.Record, columns int) bool {
	for i := columns; i < len(r.Cells); i++ {
		if strings.TrimSpace(r.Cells[i]) != "" {
			return true
		}
	}
	return false
}
