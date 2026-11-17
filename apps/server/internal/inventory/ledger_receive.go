package inventory

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// The ledger's receive (purchasing design P3, A5, A6, A13): another module's
// receiving document — Purchasing's order — puts goods in stock on its own
// transaction through the engine the receipt endpoint uses, with its rules,
// and takes a receipt back. Receive is the receipt endpoint's posting with
// the source columns set and a Discontinued variant received; ReverseReceipt
// is a movement of its own kind, receipt_reversal, on the unbuild's
// precedent: the receipt's own lines taken back out, whatever the lots'
// holds, and the inspection holds that receipt set released.

// receiveKeySpace is the UUID namespace a Receive's key is made in from its
// document, so a second Receive of one document is a replay; its own, so a
// receipt never shares a key with an issue or a production.
var receiveKeySpace = uuid.MustParse("3d0f6b52-8e1a-4c37-9b2e-71f4a5c9d608")

// reversalKeySpace is the same for a receipt's reversal, keyed by the
// receipt's document: one reversal per receipt.
var reversalKeySpace = uuid.MustParse("a4e27c19-5b6d-4f03-8a1e-2c9b7d5e3f14")

// receiveBody is what a Receive's request hash is taken over: the document
// and the receiving source, the site, the lines as given and the movement's
// texts.
type receiveBody struct {
	Source            contracts.StockSource
	Receiving         contracts.StockSource
	SiteID            int64
	Lines             []contracts.ReceiveLine
	SupplierName      string
	SupplierReference string
	Note              string
}

// clipMovementText is s cut to n characters, nil when empty: a movement's optional
// text from a caller that has no field errors to answer.
func clipMovementText(s string, n int) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if utf8.RuneCountInString(s) > n {
		s = string([]rune(s)[:n])
	}
	return &s
}

// clipDisplay is a poster's display name cut to the 200 characters a
// movement keeps.
func clipDisplay(s string) string {
	if utf8.RuneCountInString(s) > 200 {
		return string([]rune(s)[:200])
	}
	return s
}

// Receive posts the receipt as one movement of kind receipt through the
// engine, as the receipt endpoint does, for src's document.
func (l ledger) Receive(ctx context.Context, tx pgx.Tx, src contracts.StockSource, r contracts.StockReceipt) (contracts.StockPosted, error) {
	if err := src.Validate(); err != nil {
		return contracts.StockPosted{}, err
	}
	if r.Today.IsZero() {
		return contracts.StockPosted{}, errors.New("contracts: a stock call without today")
	}
	doc := r.Document
	if doc.IsZero() {
		doc = src
	}
	if err := doc.Validate(); err != nil {
		return contracts.StockPosted{}, err
	}
	if len(r.Lines) == 0 {
		return contracts.StockPosted{}, siteRefusal(contracts.StockNothingToPost, "A receipt names no line; nothing was posted.")
	}
	lineOf := func(i int) (int32, int64) { return r.Lines[i].VariantID, r.Lines[i].LineID }
	var bad []contracts.StockLineRefusal
	refuseLine := func(i int, code, detail string) {
		bad = append(bad, contracts.StockLineRefusal{Code: code, VariantID: r.Lines[i].VariantID, LineID: r.Lines[i].LineID, Index: i, Detail: detail})
	}
	lines := make([]engineLine, 0, len(r.Lines))
	for i, line := range r.Lines {
		if line.LineID <= 0 {
			return contracts.StockPosted{}, fmt.Errorf("inventory: a receipt names line id %d", line.LineID)
		}
		named := 0
		for _, set := range []bool{line.LotID != 0, line.Lot != nil && len(line.Serials) == 0, len(line.Serials) > 0} {
			if set {
				named++
			}
		}
		if named > 1 {
			return contracts.StockPosted{}, fmt.Errorf("inventory: receipt line %d names its lot more than one way", line.LineID)
		}
		q, err := ledgerQuantity(line.Quantity)
		if err != nil {
			refuseLine(i, contracts.StockQuantityInvalid, fmt.Sprintf("The quantity %q is not more than 0 with at most three decimals.", line.Quantity))
			continue
		}
		to := line.ToLocationID
		base := engineLine{index: i, variantID: line.VariantID, to: &to, quantity: q, lotID: line.LotID}
		var lot *lotRequest
		if line.Lot != nil {
			lot = &lotRequest{compareProduction: true, number: strings.TrimSpace(line.Lot.Number),
				supplierLot: clipMovementText(line.Lot.SupplierLot, 40), production: line.Lot.ProductionDate,
				bestBefore: line.Lot.BestBefore, expiry: line.Lot.Expiry}
			if lot.number != "" && !lotNumberPattern.MatchString(lot.number) {
				refuseLine(i, contracts.StockLotNumberInvalid, fmt.Sprintf("%q is not a lot number: 1–20 characters A–Z, a–z, 0–9 and . _ / -.", lot.number))
				continue
			}
			var f fieldErrors
			if checkLotDates(&f, "", lot.production, lot.bestBefore, lot.expiry); f.failed() {
				refuseLine(i, contracts.StockLotDatesInvalid, "The lot's dates are out of order: an expiry before the production date, or a best-before date after the expiry.")
				continue
			}
		}
		if len(line.Serials) == 0 {
			base.newLot = lot
			lines = append(lines, base)
			continue
		}
		// Serials (M57): one line of 1 per serial, each a lot by number with
		// the line's lot's dates; the line's quantity is their number.
		if lot == nil {
			lot = &lotRequest{compareProduction: true}
		}
		if lot.number != "" {
			return contracts.StockPosted{}, fmt.Errorf("inventory: receipt line %d names serials and a lot number", line.LineID)
		}
		if len(line.Serials) > maxSerials || q.Cmp(big.NewRat(int64(len(line.Serials)), 1)) != 0 {
			refuseLine(i, contracts.StockSerialQuantity, fmt.Sprintf("The line's quantity is %s; its serials number %d, at most %d.", line.Quantity, len(line.Serials), maxSerials))
			continue
		}
		seen := map[string]bool{}
		var failed bool
		for _, n := range line.Serials {
			n = strings.TrimSpace(n)
			switch {
			case !lotNumberPattern.MatchString(n):
				refuseLine(i, contracts.StockLotNumberInvalid, fmt.Sprintf("%q is not a serial number: 1–20 characters A–Z, a–z, 0–9 and . _ / -.", n))
			case seen[strings.ToLower(n)]:
				refuseLine(i, contracts.StockSerialQuantity, fmt.Sprintf("The serial %s is named twice.", n))
			default:
				seen[strings.ToLower(n)] = true
				serial := base
				one := *lot
				one.number = n
				serial.newLot, serial.quantity = &one, big.NewRat(1, 1)
				lines = append(lines, serial)
				continue
			}
			failed = true
			break
		}
		if failed {
			continue
		}
	}
	if len(bad) > 0 {
		return contracts.StockPosted{}, refusalOf(bad)
	}
	hash, err := requestHash(movementKindReceipt, receiveBody{Source: doc, Receiving: src, SiteID: r.SiteID, Lines: r.Lines,
		SupplierName: r.SupplierName, SupplierReference: r.SupplierReference, Note: r.Note})
	if err != nil {
		return contracts.StockPosted{}, err
	}
	header := movementHeader{
		clientRequestID: uuid.NewSHA1(receiveKeySpace, []byte(fmt.Sprintf("%s\x00%s\x00%d", doc.Module, doc.Kind, doc.ID))),
		requestHash:     hash, siteID: r.SiteID, kind: movementKindReceipt,
		note: clipMovementText(r.Note, maxNoteLength), supplierName: clipMovementText(r.SupplierName, maxSupplierName),
		supplierReference: clipMovementText(r.SupplierReference, maxSupplierRef),
		poster:            actor{UserID: r.PostedByUserID, Display: clipDisplay(r.PostedByDisplay)},
		// The source line is the receiving document's id — the order the
		// receipt receives for — so the movement links to it (A13).
		source: &movementSource{module: doc.Module, kind: doc.Kind, id: strconv.FormatInt(doc.ID, 10), lineID: ptr(strconv.FormatInt(src.ID, 10))},
	}
	txq := store.New(tx)
	// A replay first, before the site or the variants are judged again (I14).
	if res, done, err := replayOf(ctx, txq, header); done || err != nil {
		if err != nil {
			return contracts.StockPosted{}, issueError(err, lineOf, len(r.Lines))
		}
		return contracts.StockPosted{MovementID: res.movementID}, nil
	}
	// A receiving site need only exist and be active, as a plant need not
	// fulfil orders.
	if _, err := fulfillingSite(ctx, txq, r.SiteID, false); err != nil {
		return contracts.StockPosted{}, err
	}
	ids := make([]int32, 0, len(r.Lines))
	for _, line := range r.Lines {
		ids = append(ids, line.VariantID)
	}
	_, failures, err := l.stockedVariants(ctx, tx, ids, lineOf, len(r.Lines))
	if err != nil {
		return contracts.StockPosted{}, err
	}
	if len(failures) > 0 {
		return contracts.StockPosted{}, refusalOf(failures)
	}
	res, err := l.s.post(ctx, tx, txq, header, lines, postOptions{receipt: true, receivesDiscontinued: true, today: r.Today})
	if err != nil {
		return contracts.StockPosted{}, issueError(err, lineOf, len(r.Lines))
	}
	return contracts.StockPosted{MovementID: res.movementID}, nil
}

// reversalBody is what a reversal's request hash is taken over: the
// receipt's document and the note.
type reversalBody struct {
	Receipt contracts.StockSource
	Note    string
}

// ReverseReceipt takes the receipt Receive posted for the document receipt
// back out, line for line, and releases the holds that receipt set.
func (l ledger) ReverseReceipt(ctx context.Context, tx pgx.Tx, receipt contracts.StockSource, r contracts.StockReversal) (contracts.StockPosted, error) {
	if err := receipt.Validate(); err != nil {
		return contracts.StockPosted{}, err
	}
	txq := store.New(tx)
	text := sourceText(receipt)
	found, err := txq.ReceiptBySource(ctx, store.ReceiptBySourceParams{SourceModule: &text.module, SourceKind: &text.kind, SourceID: &text.id})
	if noRows(err) {
		return contracts.StockPosted{}, siteRefusal(contracts.StockReceiptUnknown,
			fmt.Sprintf("No receipt was posted for %s/%s/%d.", receipt.Module, receipt.Kind, receipt.ID))
	}
	if err != nil {
		return contracts.StockPosted{}, fmt.Errorf("inventory: read the receipt of %s/%s/%d: %w", receipt.Module, receipt.Kind, receipt.ID, err)
	}
	posted, err := txq.MovementLines(ctx, found.ID)
	if err != nil {
		return contracts.StockPosted{}, fmt.Errorf("inventory: read the lines of movement %d: %w", found.ID, err)
	}
	lines := make([]engineLine, 0, len(posted))
	for i, p := range posted {
		if p.ToLocationID == nil {
			return contracts.StockPosted{}, fmt.Errorf("inventory: receipt %d has a line with no location", found.ID)
		}
		from := *p.ToLocationID
		var lot int64
		if p.LotID != nil {
			lot = *p.LotID
		}
		lines = append(lines, engineLine{index: i, variantID: p.VariantID, from: &from, quantity: ratFromNumeric(p.Quantity), lotID: lot})
	}
	lineOf := func(i int) (int32, int64) { return posted[i].VariantID, 0 }
	hash, err := requestHash(movementKindReceiptReversal, reversalBody{Receipt: receipt, Note: r.Note})
	if err != nil {
		return contracts.StockPosted{}, err
	}
	header := movementHeader{
		clientRequestID: uuid.NewSHA1(reversalKeySpace, []byte(fmt.Sprintf("%s\x00%s\x00%d", receipt.Module, receipt.Kind, receipt.ID))),
		requestHash:     hash, siteID: found.SiteID, kind: movementKindReceiptReversal,
		note: clipMovementText(r.Note, maxNoteLength), supplierName: found.SupplierName, supplierReference: found.SupplierReference,
		poster: actor{UserID: r.PostedByUserID, Display: clipDisplay(r.PostedByDisplay)},
	}
	// The reversal names the receipt's own source and source line, so the
	// movement page links both to the same document.
	header.source = &movementSource{module: text.module, kind: text.kind, id: text.id, lineID: found.SourceLineID}
	if res, done, err := replayOf(ctx, txq, header); done || err != nil {
		if err != nil {
			return contracts.StockPosted{}, issueError(err, lineOf, len(lines))
		}
		return contracts.StockPosted{MovementID: res.movementID}, nil
	}
	// The receipt's own lots whatever their holds: no take's lot rules (A5).
	res, err := l.s.post(ctx, tx, txq, header, lines, postOptions{})
	if err != nil {
		return contracts.StockPosted{}, issueError(err, lineOf, len(lines))
	}
	if res.replay {
		return contracts.StockPosted{MovementID: res.movementID}, nil
	}
	// The holds the receipt set — its inspection's quarantine (M100) — end
	// with it.
	held := movementSource{module: lotOriginModule, kind: receiptSourceKind, id: strconv.FormatInt(found.ID, 10)}
	lotIDs, err := txq.LotsHeldBySource(ctx, store.LotsHeldBySourceParams{SourceModule: held.module, SourceKind: held.kind, SourceID: held.id})
	if err != nil {
		return contracts.StockPosted{}, fmt.Errorf("inventory: read the lots receipt %d holds: %w", found.ID, err)
	}
	if len(lotIDs) > 0 {
		if err := l.s.releaseHolds(ctx, tx, txq, held, lotIDs, contracts.LotActor{UserID: r.PostedByUserID, Display: r.PostedByDisplay}); err != nil {
			return contracts.StockPosted{}, err
		}
	}
	return contracts.StockPosted{MovementID: res.movementID}, nil
}
