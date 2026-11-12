package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// A transfer's actions (phase 2 design I38–I45): send, receive and cancel.
// Each writes one action and its lines, and posts its movement through the
// engine, in I38's order, in one transaction, inside its own
// serialization-and-deadlock retry (runAction):
//
//  1. the transfer row FOR UPDATE;
//  2. the replay check: the action's client request id looked up in
//     transfer_actions. The same request hash answers the transfer as it is
//     now; another is client_request_reused for a client's id and, for an id
//     derived from the transfer, the status refusal of step 3 — a retried
//     send replays although its transfer is no longer a draft;
//  3. the status, then If-Match (send, cancel);
//  4. the two sites FOR SHARE, and only then their status (site_archived);
//  5. the engine's posting, the action's per-line rules in postOptions.check
//     once the packagings are resolved to units;
//  6. the action, its lines, the lines' running totals and the status.
//
// A movement's client_request_id is its action's, so one key claims both,
// and its source reference names the transfer and, as the line, the action
// (I45) — whose id is reserved before the movement is posted.
//
// A failing line's index is, for a send and a cancel, the line's place on the
// transfer (by line number); for a receive, its place in the request.

// transferKeySpace is the UUID namespace a transfer's derived action ids are
// made in (I39, I43).
var transferKeySpace = uuid.MustParse("2d6b9c41-3f1e-4b7a-8c55-9e0d7a4f1b23")

// derivedActionID is the client request id of a send, or of a send and
// receive's receive, derived from the transfer as an issue's is from its
// document (ledger.go).
func derivedActionID(transferID int64, kind string) uuid.UUID {
	return uuid.NewSHA1(transferKeySpace, []byte(fmt.Sprintf("%d\x00%s", transferID, kind)))
}

// The action kinds and their lines' kinds, as the store keeps them.
const (
	actionSend    = string(gen.Send)
	actionReceive = string(gen.Receive)
	actionCancel  = string(gen.Cancel)

	actionLineSent     = string(gen.InventoryTransferActionLineKindSent)
	actionLineReceived = string(gen.InventoryTransferActionLineKindReceived)
	actionLineMissing  = string(gen.InventoryTransferActionLineKindMissing)
	actionLineReturned = string(gen.InventoryTransferActionLineKindReturned)
)

// transferAction is one action as runAction runs it.
type transferAction struct {
	transferID      int64
	clientRequestID uuid.UUID
	// derived is whether the id is the transfer's rather than the client's.
	derived bool
	hash    []byte
	// revision is If-Match's, nil for a receive (I41).
	revision *int32
	// status refuses the transfer's status for this action (step 3).
	status func(t store.InventoryTransfer) error
	// post is steps 5 and 6, against the locked transfer and its lines.
	post func(ctx context.Context, tx pgx.Tx, txq *store.Queries, t store.InventoryTransfer, lines []store.InventoryTransferLine) error
}

// runAction runs one action in I38's order, again in a new transaction when
// it loses a race to a deadlock or a serialization failure.
func (s *server) runAction(ctx context.Context, a transferAction) error {
	return db.RetrySerializable(ctx, postAttempts, func() error {
		return s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
			// 1. The transfer.
			t, err := lockTransfer(ctx, txq, a.transferID)
			if err != nil {
				return err
			}
			// 2. The replay, before anything is judged again.
			prior, err := txq.ActionByClientRequest(ctx, a.clientRequestID)
			switch {
			case err == nil:
				if prior.TransferID == t.ID && bytes.Equal(prior.RequestHash, a.hash) {
					return nil
				}
				if a.derived {
					if err := a.status(t); err != nil {
						return err
					}
				}
				return clientRequestReused(a.clientRequestID)
			case !noRows(err):
				return fmt.Errorf("inventory: read action %s: %w", a.clientRequestID, err)
			}
			// 3. The status, then If-Match: a cancel after a receive is
			// transfer_not_cancellable whatever revision it read (§9, case 2).
			if err := a.status(t); err != nil {
				return err
			}
			if a.revision != nil && t.Revision != *a.revision {
				return refuse(revisionConflict("transfer", t.Revision, *a.revision))
			}
			// 4. The sites, shared, then judged.
			if _, err := shareSites(ctx, txq, t.FromSiteID, t.ToSiteID); err != nil {
				return err
			}
			lines, err := txq.TransferLines(ctx, t.ID)
			if err != nil {
				return fmt.Errorf("inventory: read the lines of transfer %d: %w", t.ID, err)
			}
			// 5 and 6.
			return a.post(ctx, tx, txq, t, lines)
		})
	})
}

// clientRequestReused is the 409 of a client request id another body used.
func clientRequestReused(id uuid.UUID) error {
	return refuse(problem(http.StatusConflict, codeClientRequestReused, "Client request reused",
		fmt.Sprintf("The clientRequestId %s was used before with a different body; make a new one for a new action.", id)))
}

// actionPosting is one action's movement and record as step 6 writes them.
type actionPosting struct {
	id         int64
	kind       string
	clientID   uuid.UUID
	hash       []byte
	movementID *int64
	reason     *string
	note       *string
	lineIDs    []int64
	lineKinds  []string
	quantities []*big.Rat
	locations  []int64
}

// add is one action line: quantity of kind on the transfer line, at location
// (0 for none).
func (p *actionPosting) add(lineID int64, kind string, quantity *big.Rat, location int64) {
	p.lineIDs = append(p.lineIDs, lineID)
	p.lineKinds = append(p.lineKinds, kind)
	p.quantities = append(p.quantities, quantity)
	p.locations = append(p.locations, location)
}

// write records the action and its lines.
func (p *actionPosting) write(ctx context.Context, txq *store.Queries, t store.InventoryTransfer, who actor, now pgtype.Timestamptz) error {
	err := txq.InsertTransferAction(ctx, store.InsertTransferActionParams{
		ID: p.id, TransferID: t.ID, Kind: p.kind, ClientRequestID: p.clientID, RequestHash: p.hash, MovementID: p.movementID,
		CloseReason: p.reason, CloseNote: p.note, PostedByUserID: who.UserID, PostedByDisplay: &who.Display, PostedAt: now.Time,
	})
	if db.IsUniqueViolation(err, "ux_transfer_actions_client_request") {
		return clientRequestReused(p.clientID)
	}
	if err != nil {
		return fmt.Errorf("inventory: write the %s of transfer %d: %w", p.kind, t.ID, err)
	}
	quantities := make([]pgtype.Numeric, len(p.quantities))
	for i, q := range p.quantities {
		n, err := numericFromRat(q)
		if err != nil {
			return err
		}
		quantities[i] = n
	}
	if err := txq.InsertTransferActionLines(ctx, store.InsertTransferActionLinesParams{
		ActionID: p.id, TransferLineIds: p.lineIDs, Kinds: p.lineKinds, Quantities: quantities, LocationIds: p.locations,
	}); err != nil {
		return fmt.Errorf("inventory: write the lines of the %s of transfer %d: %w", p.kind, t.ID, err)
	}
	return nil
}

// reserveAction is an action's id, taken before its movement is posted so the
// movement's source line names it (I45).
func reserveAction(ctx context.Context, txq *store.Queries) (int64, error) {
	id, err := txq.NextTransferActionID(ctx)
	if err != nil {
		return 0, fmt.Errorf("inventory: reserve an action id: %w", err)
	}
	return id, nil
}

// transferSource is the source reference of a transfer's movement (I45).
func transferSource(t store.InventoryTransfer, actionID int64) *movementSource {
	return &movementSource{module: "inventory", kind: "transfer", id: strconv.FormatInt(t.ID, 10), lineID: ptr(strconv.FormatInt(actionID, 10))}
}

// lineTotals is the running totals an action adds to its lines (I38 step 6).
type lineTotals struct {
	ids                         []int64
	received, missing, returned []pgtype.Numeric
}

// add adds one line's received, missing and returned.
func (lt *lineTotals) add(id int64, received, missing, returned *big.Rat) error {
	r, err := numericFromRat(received)
	if err != nil {
		return err
	}
	m, err := numericFromRat(missing)
	if err != nil {
		return err
	}
	ret, err := numericFromRat(returned)
	if err != nil {
		return err
	}
	lt.ids = append(lt.ids, id)
	lt.received, lt.missing, lt.returned = append(lt.received, r), append(lt.missing, m), append(lt.returned, ret)
	return nil
}

// write applies the totals.
func (lt *lineTotals) write(ctx context.Context, txq *store.Queries, transferID int64) error {
	if len(lt.ids) == 0 {
		return nil
	}
	if err := txq.AddTransferLineTotals(ctx, store.AddTransferLineTotalsParams{
		TransferID: transferID, Ids: lt.ids, Received: lt.received, Missing: lt.missing, Returned: lt.returned,
	}); err != nil {
		return fmt.Errorf("inventory: add to the lines of transfer %d: %w", transferID, err)
	}
	return nil
}

// storedEntry is a draft line's stored entry as the engine resolves it again
// at send (I39): the packagings and the loose rest, against the packagings as
// they are locked. nil when the line was given in units.
func storedEntry(raw []byte) *entry {
	if len(raw) == 0 {
		return nil
	}
	var snap enteredSnapshot
	if json.Unmarshal(raw, &snap) != nil {
		return nil
	}
	e := &entry{packagings: make([]gen.InventoryEnteredPackaging, 0, len(snap.Packagings))}
	for _, p := range snap.Packagings {
		e.packagings = append(e.packagings, gen.InventoryEnteredPackaging{PackagingId: p.PackagingID, Count: p.Count})
	}
	if snap.Loose != nil {
		e.loose = ratFromText(*snap.Loose)
	}
	return e
}

// actionHash is the request hash of an action: its kind, its transfer and its
// body.
func actionHash(kind string, transferID int64, body any) ([]byte, error) {
	return requestHash("transfer_"+kind, struct {
		Transfer int64
		Body     any
	}{transferID, body})
}

// actionAnswer is how a handler turns an action's outcome into its typed
// answer, reading the transfer as it is now.
type actionAnswer struct {
	status int
	body   gen.InventoryProblem
	out    gen.InventoryTransfer
}

// finishAction reads the transfer after an action, or answers the action's
// refusal.
func (s *server) finishAction(ctx context.Context, id int64, err error) (actionAnswer, error) {
	if status, p, ok := answerOf(err); ok {
		return actionAnswer{status: status, body: p}, nil
	}
	if err != nil {
		return actionAnswer{}, err
	}
	out, err := s.transferOf(ctx, id)
	if status, p, ok := answerOf(err); ok {
		return actionAnswer{status: status, body: p}, nil
	}
	if err != nil {
		return actionAnswer{}, err
	}
	return actionAnswer{status: http.StatusOK, out: out}, nil
}

// SendInventoryTransfer Send a transfer
// (POST /api/v1/inventory/transfers/{id}/send)
func (s *server) SendInventoryTransfer(ctx context.Context, req gen.SendInventoryTransferRequestObject) (gen.SendInventoryTransferResponseObject, error) {
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.SendInventoryTransfer400ApplicationProblemPlusJSONResponse(invalid(invalidTransferTitle, badIfMatch())), nil
	}
	if req.Body == nil {
		return gen.SendInventoryTransfer400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidTransferTitle, "The request has no body.")), nil
	}
	in := *req.Body
	if f := s.unknownProperties(ctx, transferSendInputSchema); f.failed() {
		return gen.SendInventoryTransfer400ApplicationProblemPlusJSONResponse(invalid(invalidTransferTitle, f)), nil
	}
	hash, err := actionHash(actionSend, req.Id, in)
	if err != nil {
		return nil, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.runAction(ctx, transferAction{
		transferID: req.Id, clientRequestID: derivedActionID(req.Id, actionSend), derived: true, hash: hash, revision: &revision,
		status: func(t store.InventoryTransfer) error {
			if t.Status != transferDraft {
				return notDraft(t)
			}
			return nil
		},
		post: func(ctx context.Context, tx pgx.Tx, txq *store.Queries, t store.InventoryTransfer, lines []store.InventoryTransferLine) error {
			return s.send(ctx, tx, txq, who, t, lines, in, hash)
		},
	})
	a, err := s.finishAction(ctx, req.Id, err)
	if err != nil {
		return nil, err
	}
	switch a.status {
	case http.StatusOK:
		return gen.SendInventoryTransfer200JSONResponse(a.out), nil
	case http.StatusNotFound:
		return gen.SendInventoryTransfer404Response{}, nil
	case http.StatusConflict:
		return gen.SendInventoryTransfer409ApplicationProblemPlusJSONResponse(a.body), nil
	case http.StatusServiceUnavailable:
		return gen.SendInventoryTransfer503ApplicationProblemPlusJSONResponse(a.body), nil
	}
	return gen.SendInventoryTransfer400ApplicationProblemPlusJSONResponse(a.body), nil
}

// lineIndex is a transfer's lines by id, each with its place on the transfer.
func lineIndex(lines []store.InventoryTransferLine) map[int64]int {
	out := make(map[int64]int, len(lines))
	for i, l := range lines {
		out[l.ID] = i
	}
	return out
}

// namedLines judges the line ids a request names: each on the transfer
// (transfer_line_unknown) and named once.
func namedLines(f *fieldErrors, field string, ids []int64, onTransfer map[int64]int) {
	seen := map[int64]bool{}
	for i, id := range ids {
		name := fmt.Sprintf("%s[%d].lineId", field, i)
		if _, ok := onTransfer[id]; !ok {
			f.addCoded(name, codeTransferLineUnknown, fmt.Sprintf("The transfer has no line %d", id))
			continue
		}
		if seen[id] {
			f.add(name, fmt.Sprintf("The line %d is named twice; name it once", id))
		}
		seen[id] = true
	}
}

// send is a send's steps 5 and 6 (I39) and, when the request carries a
// receive, the receive of everything sent (I43): two movements, two actions,
// both ids derived.
func (s *server) send(ctx context.Context, tx pgx.Tx, txq *store.Queries, who actor, t store.InventoryTransfer, lines []store.InventoryTransferLine,
	in gen.InventoryTransferSendInput, hash []byte,
) error {
	onTransfer := lineIndex(lines)
	var f fieldErrors
	from := map[int64]*int64{}
	sendLot := map[int64]int64{}
	ids := make([]int64, 0, len(in.Lines))
	for i, l := range in.Lines {
		ids = append(ids, l.LineId)
		from[l.LineId] = l.FromLocationId
		if id := lotIDOf(&f, fmt.Sprintf("lines[%d].lotId", i), l.LotId); id != 0 {
			sendLot[l.LineId] = id
		}
	}
	namedLines(&f, "lines", ids, onTransfer)
	var to map[int64]int64
	if in.Receive != nil {
		to = map[int64]int64{}
		ids = ids[:0]
		for _, l := range in.Receive.Lines {
			ids = append(ids, l.LineId)
			to[l.LineId] = l.ToLocationId
		}
		namedLines(&f, "receive.lines", ids, onTransfer)
	}
	if f.failed() {
		return refuseInvalid(invalidTransferTitle, f)
	}
	if to != nil {
		var missing []string
		for _, l := range lines {
			if _, ok := to[l.ID]; !ok {
				missing = append(missing, strconv.Itoa(int(l.LineNo)))
			}
		}
		if len(missing) > 0 {
			return refuse(problem(http.StatusBadRequest, codeReceiveIncomplete, invalidTransferTitle,
				fmt.Sprintf("A send and receive names a destination for every line; line(s) %v have none.", missing)))
		}
	}

	// 5. The transfer_out at the from site, each line from its source, in
	// its lot — the send's, else the draft's (M70); a tracked variant's line
	// with none is the engine's lot_required.
	engine := make([]engineLine, 0, len(lines))
	for i, l := range lines {
		line := engineLine{index: i, variantID: l.VariantID, from: from[l.ID], lotID: sendLot[l.ID]}
		if line.lotID == 0 && l.LotID != nil {
			line.lotID = *l.LotID
		}
		if line.from == nil {
			line.failCode, line.failText = codeFromLocationRequired, fmt.Sprintf("Line %d names no location to send it from.", l.LineNo)
		}
		if e := storedEntry(l.Entered); e != nil {
			line.entry = e
		} else {
			line.quantity = ratFromNumeric(l.Quantity)
		}
		engine = append(engine, line)
	}
	var (
		resolved []engineLine
		variants map[int32]contracts.VariantEntry
	)
	check := func(ls []engineLine, vs map[int32]contracts.VariantEntry, _ map[balanceKey]*big.Rat) error {
		var failures []lineFailure
		for _, l := range ls {
			if v := vs[l.variantID]; v.ProductType != productTypeGoods {
				failures = append(failures, lineFailure{Index: l.index, Code: codeVariantNotStockable,
					Detail: fmt.Sprintf("%s is a service; only goods are kept in stock.", v.SKU)})
			}
		}
		if len(failures) > 0 {
			return refuseLines(failures)
		}
		resolved, variants = ls, vs
		return nil
	}
	sendID, err := reserveAction(ctx, txq)
	if err != nil {
		return err
	}
	sendKey := derivedActionID(t.ID, actionSend)
	res, err := s.post(ctx, tx, txq, movementHeader{
		clientRequestID: sendKey, requestHash: hash, siteID: t.FromSiteID, kind: string(gen.TransferOut), poster: who,
		source: transferSource(t, sendID),
	}, engine, postOptions{maxLines: maxAPILines, check: check})
	if err != nil {
		return err
	}

	// 6. The lines resolved and snapshotted again, sent whole; the action.
	now := pgtype.Timestamptz{Time: s.deps.Clock(), Valid: true}
	sent := store.SendTransferLinesParams{TransferID: t.ID}
	sendAction := actionPosting{id: sendID, kind: actionSend, clientID: sendKey, hash: hash, movementID: &res.movementID}
	for _, l := range resolved {
		tl, v := lines[l.index], variants[l.variantID]
		q, err := numericFromRat(l.quantity)
		if err != nil {
			return err
		}
		sent.Ids = append(sent.Ids, tl.ID)
		sent.Skus = append(sent.Skus, v.SKU)
		sent.ProductNames = append(sent.ProductNames, v.ProductName)
		sent.Units = append(sent.Units, v.Unit)
		sent.Quantities = append(sent.Quantities, q)
		sent.Entereds = append(sent.Entereds, string(l.entered))
		sent.FromLocationIds = append(sent.FromLocationIds, *l.from)
		sent.LotIds = append(sent.LotIds, l.lotID)
		sendAction.add(tl.ID, actionLineSent, l.quantity, *l.from)
	}
	if err := txq.SendTransferLines(ctx, sent); err != nil {
		return fmt.Errorf("inventory: send the lines of transfer %d: %w", t.ID, err)
	}
	if err := sendAction.write(ctx, txq, t, who, now); err != nil {
		return err
	}
	if err := txq.MarkTransferSent(ctx, store.MarkTransferSentParams{
		ID: t.ID, Status: transferSent, Now: now.Time, SentByUserID: who.UserID, SentByDisplay: &who.Display,
	}); err != nil {
		return fmt.Errorf("inventory: mark transfer %d sent: %w", t.ID, err)
	}
	if err := s.recordEvent(ctx, txq, who, event{
		EntityKind: entityTransfer, EntityID: t.ID, SiteID: &t.FromSiteID, Kind: eventTransferSent,
		Payload: map[string]any{"number": t.Number, "movementId": res.movementID, "lines": len(resolved)},
	}); err != nil {
		return err
	}
	if to == nil {
		return nil
	}

	// The receive of everything sent (I43): the transfer_in at the to site.
	receiveLines := make([]engineLine, 0, len(resolved))
	for _, l := range resolved {
		dest := to[lines[l.index].ID]
		receiveLines = append(receiveLines, engineLine{index: l.index, variantID: l.variantID, to: &dest, quantity: l.quantity, lotID: l.lotID})
	}
	receiveID, err := reserveAction(ctx, txq)
	if err != nil {
		return err
	}
	receiveKey := derivedActionID(t.ID, actionReceive)
	res, err = s.post(ctx, tx, txq, movementHeader{
		clientRequestID: receiveKey, requestHash: hash, siteID: t.ToSiteID, kind: string(gen.TransferIn), poster: who,
		source: transferSource(t, receiveID),
	}, receiveLines, postOptions{maxLines: maxAPILines})
	if err != nil {
		return err
	}
	receiveAction := actionPosting{id: receiveID, kind: actionReceive, clientID: receiveKey, hash: hash, movementID: &res.movementID}
	var totals lineTotals
	zero := new(big.Rat)
	for _, l := range receiveLines {
		tl := lines[l.index]
		receiveAction.add(tl.ID, actionLineReceived, l.quantity, *l.to)
		if err := totals.add(tl.ID, l.quantity, zero, zero); err != nil {
			return err
		}
	}
	if err := totals.write(ctx, txq, t.ID); err != nil {
		return err
	}
	if err := receiveAction.write(ctx, txq, t, who, now); err != nil {
		return err
	}
	if err := txq.CloseTransfer(ctx, store.CloseTransferParams{
		ID: t.ID, Status: transferReceived, Now: now.Time, ClosedByUserID: who.UserID, ClosedByDisplay: &who.Display,
	}); err != nil {
		return fmt.Errorf("inventory: close transfer %d: %w", t.ID, err)
	}
	return s.recordEvent(ctx, txq, who, event{
		EntityKind: entityTransfer, EntityID: t.ID, SiteID: &t.ToSiteID, Kind: eventTransferReceived,
		Payload: map[string]any{"number": t.Number, "movementId": res.movementID, "lines": len(receiveLines)},
	})
}

// receiveLine is one line of a receive judged for its shape.
type receiveLine struct {
	lineID   int64
	to       int64
	quantity *big.Rat
	entry    *entry
	failCode string
	failText string
	zero     bool
}

// ReceiveInventoryTransfer Receive a transfer
// (POST /api/v1/inventory/transfers/{id}/receive)
//
// No If-Match (I41): two receivers both land, serialised by the row's lock,
// each judged against the other's result.
func (s *server) ReceiveInventoryTransfer(ctx context.Context, req gen.ReceiveInventoryTransferRequestObject) (gen.ReceiveInventoryTransferResponseObject, error) {
	if req.Body == nil {
		return gen.ReceiveInventoryTransfer400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidTransferTitle, "The request has no body.")), nil
	}
	in := *req.Body
	f := s.unknownProperties(ctx, transferReceiveInputSchema)
	var reason, note *string
	if in.Close != nil {
		if !in.Close.Reason.Valid() {
			f.add("close.reason", "The reason must be lost, damaged, not_packed or other")
		}
		reason = ptr(string(in.Close.Reason))
		note = optionalText(&f, "close.note", in.Close.Note, maxCloseNote)
	}
	if len(in.Lines) > maxAPILines {
		f.addCoded("lines", codeTooManyLines, fmt.Sprintf("A receive has at most %d lines", maxAPILines))
	}
	if f.failed() {
		return gen.ReceiveInventoryTransfer400ApplicationProblemPlusJSONResponse(invalid(invalidTransferTitle, f)), nil
	}
	lines := make([]receiveLine, 0, len(in.Lines))
	for _, l := range in.Lines {
		line := receiveLine{lineID: l.LineId, to: l.ToLocationId}
		switch {
		case l.Quantity != nil && l.Entered == nil && *l.Quantity == 0:
			line.zero = true
		case l.Quantity != nil && *l.Quantity < 0:
			line.failCode, line.failText = codeQuantityInvalid, "The quantity must be 0 or more"
		default:
			line.quantity, line.entry, line.failCode, line.failText = readEntered(l.Quantity, l.Entered, positiveQuantity)
		}
		lines = append(lines, line)
	}
	hash, err := actionHash(actionReceive, req.Id, in)
	if err != nil {
		return nil, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.runAction(ctx, transferAction{
		transferID: req.Id, clientRequestID: in.ClientRequestId, hash: hash,
		status: func(t store.InventoryTransfer) error {
			if !transferInTransit(t.Status) {
				return refuse(problem(http.StatusConflict, codeTransferNotReceivable, "Transfer is not receivable",
					fmt.Sprintf("Transfer %d is %s; only a sent or partly received transfer is received.", t.Number, statusWords(t.Status))))
			}
			return nil
		},
		post: func(ctx context.Context, tx pgx.Tx, txq *store.Queries, t store.InventoryTransfer, tl []store.InventoryTransferLine) error {
			return s.receive(ctx, tx, txq, who, t, tl, lines, in.ClientRequestId, hash, reason, note)
		},
	})
	a, err := s.finishAction(ctx, req.Id, err)
	if err != nil {
		return nil, err
	}
	switch a.status {
	case http.StatusOK:
		return gen.ReceiveInventoryTransfer200JSONResponse(a.out), nil
	case http.StatusNotFound:
		return gen.ReceiveInventoryTransfer404Response{}, nil
	case http.StatusConflict:
		return gen.ReceiveInventoryTransfer409ApplicationProblemPlusJSONResponse(a.body), nil
	case http.StatusServiceUnavailable:
		return gen.ReceiveInventoryTransfer503ApplicationProblemPlusJSONResponse(a.body), nil
	}
	return gen.ReceiveInventoryTransfer400ApplicationProblemPlusJSONResponse(a.body), nil
}

// statusWords is a status as a refusal's detail says it.
func statusWords(status string) string {
	if status == transferPartlyReceived {
		return "partly received"
	}
	return status
}

// receive is a receive's steps 5 and 6 (I40, I42): the quantities that
// arrived, each at most its line's outstanding in units, posted as one
// transfer_in at the to site; then, with close, every line's remaining
// outstanding recorded missing.
func (s *server) receive(ctx context.Context, tx pgx.Tx, txq *store.Queries, who actor, t store.InventoryTransfer, lines []store.InventoryTransferLine,
	in []receiveLine, clientID uuid.UUID, hash []byte, reason, note *string,
) error {
	onTransfer := lineIndex(lines)
	var f fieldErrors
	ids := make([]int64, 0, len(in))
	for _, l := range in {
		ids = append(ids, l.lineID)
	}
	namedLines(&f, "lines", ids, onTransfer)
	if f.failed() {
		return refuseInvalid(invalidTransferTitle, f)
	}
	outstanding := make([]*big.Rat, len(lines))
	for i, l := range lines {
		outstanding[i] = inTransitOf(l)
	}

	engine := make([]engineLine, 0, len(in))
	for i, l := range in {
		if l.zero {
			continue
		}
		to := l.to
		tl := lines[onTransfer[l.lineID]]
		line := engineLine{
			index: i, variantID: tl.VariantID, to: &to, quantity: l.quantity, entry: l.entry,
			failCode: l.failCode, failText: l.failText,
		}
		// The lot that was sent arrives (M70).
		if tl.LotID != nil {
			line.lotID = *tl.LotID
		}
		engine = append(engine, line)
	}
	if len(engine) == 0 && reason == nil {
		return nothingToPost()
	}
	received := make([]*big.Rat, len(lines))
	for i := range received {
		received[i] = new(big.Rat)
	}
	actionID, err := reserveAction(ctx, txq)
	if err != nil {
		return err
	}
	action := actionPosting{id: actionID, kind: actionReceive, clientID: clientID, hash: hash, reason: reason, note: note}
	if len(engine) > 0 {
		var resolved []engineLine
		check := func(ls []engineLine, _ map[int32]contracts.VariantEntry, _ map[balanceKey]*big.Rat) error {
			now := make([]*big.Rat, len(lines))
			for i := range now {
				now[i] = new(big.Rat)
			}
			var failures []lineFailure
			for _, l := range ls {
				at := onTransfer[in[l.index].lineID]
				now[at].Add(now[at], l.quantity)
				if now[at].Cmp(outstanding[at]) > 0 {
					failures = append(failures, lineFailure{Index: l.index, Code: codeTransferOverReceived, Detail: fmt.Sprintf(
						"%s on line %d: %s outstanding, the line receives %s.", lines[at].Sku, lines[at].LineNo,
						decimalText(outstanding[at]), decimalText(l.quantity))})
				}
			}
			if len(failures) > 0 {
				return refuseLines(failures)
			}
			resolved = ls
			return nil
		}
		res, err := s.post(ctx, tx, txq, movementHeader{
			clientRequestID: clientID, requestHash: hash, siteID: t.ToSiteID, kind: string(gen.TransferIn), poster: who,
			source: transferSource(t, actionID),
		}, engine, postOptions{maxLines: maxAPILines, check: check})
		if err != nil {
			return err
		}
		action.movementID = &res.movementID
		for _, l := range resolved {
			at := onTransfer[in[l.index].lineID]
			received[at].Add(received[at], l.quantity)
			action.add(lines[at].ID, actionLineReceived, l.quantity, *l.to)
		}
	}

	// 6. The totals, the difference closed, the status.
	var totals lineTotals
	left := new(big.Rat)
	missingLines := 0
	zero := new(big.Rat)
	for i, l := range lines {
		rest := new(big.Rat).Sub(outstanding[i], received[i])
		missing := zero
		if reason != nil && rest.Sign() > 0 {
			missing = rest
			action.add(l.ID, actionLineMissing, rest, 0)
			missingLines++
		} else {
			left.Add(left, rest)
		}
		if received[i].Sign() > 0 || missing.Sign() > 0 {
			if err := totals.add(l.ID, received[i], missing, zero); err != nil {
				return err
			}
		}
	}
	if reason != nil && missingLines == 0 {
		return refuse(problem(http.StatusConflict, codeNothingToClose, "Nothing to close",
			fmt.Sprintf("Nothing of transfer %d is outstanding once this receive is counted; receive it without closing.", t.Number)))
	}
	if err := totals.write(ctx, txq, t.ID); err != nil {
		return err
	}
	now := pgtype.Timestamptz{Time: s.deps.Clock(), Valid: true}
	if err := action.write(ctx, txq, t, who, now); err != nil {
		return err
	}
	if left.Sign() == 0 {
		err = txq.CloseTransfer(ctx, store.CloseTransferParams{
			ID: t.ID, Status: transferReceived, CloseReason: reason, CloseNote: note, Now: now.Time,
			ClosedByUserID: who.UserID, ClosedByDisplay: &who.Display,
		})
	} else {
		err = txq.SetTransferStatus(ctx, store.SetTransferStatusParams{ID: t.ID, Status: transferPartlyReceived, Now: now.Time})
	}
	if err != nil {
		return fmt.Errorf("inventory: set the status of transfer %d: %w", t.ID, err)
	}
	if action.movementID != nil {
		if err := s.recordEvent(ctx, txq, who, event{
			EntityKind: entityTransfer, EntityID: t.ID, SiteID: &t.ToSiteID, Kind: eventTransferReceived,
			Payload: map[string]any{"number": t.Number, "movementId": *action.movementID, "lines": len(engine)},
		}); err != nil {
			return err
		}
	}
	if reason == nil {
		return nil
	}
	return s.recordEvent(ctx, txq, who, event{
		EntityKind: entityTransfer, EntityID: t.ID, SiteID: &t.ToSiteID, Kind: eventTransferClosed,
		Payload: map[string]any{"number": t.Number, "reason": *reason, "note": note, "lines": missingLines},
	})
}

// CancelInventoryTransfer Cancel a transfer
// (POST /api/v1/inventory/transfers/{id}/cancel)
//
// Only a sent transfer with nothing received or missing on any line (I44):
// one transfer_in at the from site, each line back to the location it left.
func (s *server) CancelInventoryTransfer(ctx context.Context, req gen.CancelInventoryTransferRequestObject) (gen.CancelInventoryTransferResponseObject, error) {
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.CancelInventoryTransfer400ApplicationProblemPlusJSONResponse(invalid(invalidTransferTitle, badIfMatch())), nil
	}
	if req.Body == nil {
		return gen.CancelInventoryTransfer400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidTransferTitle, "The request has no body.")), nil
	}
	in := *req.Body
	if f := s.unknownProperties(ctx, transferCancelInputSchema); f.failed() {
		return gen.CancelInventoryTransfer400ApplicationProblemPlusJSONResponse(invalid(invalidTransferTitle, f)), nil
	}
	hash, err := actionHash(actionCancel, req.Id, in)
	if err != nil {
		return nil, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.runAction(ctx, transferAction{
		transferID: req.Id, clientRequestID: in.ClientRequestId, hash: hash, revision: &revision,
		status: func(t store.InventoryTransfer) error {
			if t.Status != transferSent {
				return notCancellable(t)
			}
			return nil
		},
		post: func(ctx context.Context, tx pgx.Tx, txq *store.Queries, t store.InventoryTransfer, lines []store.InventoryTransferLine) error {
			return s.cancel(ctx, tx, txq, who, t, lines, in.ClientRequestId, hash)
		},
	})
	a, err := s.finishAction(ctx, req.Id, err)
	if err != nil {
		return nil, err
	}
	switch a.status {
	case http.StatusOK:
		return gen.CancelInventoryTransfer200JSONResponse(a.out), nil
	case http.StatusNotFound:
		return gen.CancelInventoryTransfer404Response{}, nil
	case http.StatusConflict:
		return gen.CancelInventoryTransfer409ApplicationProblemPlusJSONResponse(a.body), nil
	case http.StatusServiceUnavailable:
		return gen.CancelInventoryTransfer503ApplicationProblemPlusJSONResponse(a.body), nil
	}
	return gen.CancelInventoryTransfer400ApplicationProblemPlusJSONResponse(a.body), nil
}

// notCancellable is the 409 of a cancel of a transfer that is not sent, or
// has had something received or recorded missing (I44).
func notCancellable(t store.InventoryTransfer) error {
	detail := fmt.Sprintf("Transfer %d is %s; only a sent transfer with nothing received is cancelled.", t.Number, statusWords(t.Status))
	if t.Status == transferDraft {
		detail = fmt.Sprintf("Transfer %d is a draft; delete it instead.", t.Number)
	}
	return refuse(problem(http.StatusConflict, codeTransferNotCancellable, "Transfer is not cancellable", detail))
}

// cancel is a cancel's steps 5 and 6.
func (s *server) cancel(ctx context.Context, tx pgx.Tx, txq *store.Queries, who actor, t store.InventoryTransfer, lines []store.InventoryTransferLine,
	clientID uuid.UUID, hash []byte,
) error {
	engine := make([]engineLine, 0, len(lines))
	for i, l := range lines {
		if ratFromNumeric(l.Received).Sign() != 0 || ratFromNumeric(l.Missing).Sign() != 0 {
			return notCancellable(t)
		}
		sent := ratFromNumeric(l.Sent)
		if sent.Sign() == 0 || l.FromLocationID == nil {
			continue
		}
		back := *l.FromLocationID
		line := engineLine{index: i, variantID: l.VariantID, to: &back, quantity: sent}
		// The lot goes back where it left (M70).
		if l.LotID != nil {
			line.lotID = *l.LotID
		}
		engine = append(engine, line)
	}
	actionID, err := reserveAction(ctx, txq)
	if err != nil {
		return err
	}
	res, err := s.post(ctx, tx, txq, movementHeader{
		clientRequestID: clientID, requestHash: hash, siteID: t.FromSiteID, kind: string(gen.TransferIn), poster: who,
		source: transferSource(t, actionID),
	}, engine, postOptions{})
	if err != nil {
		return err
	}
	action := actionPosting{id: actionID, kind: actionCancel, clientID: clientID, hash: hash, movementID: &res.movementID}
	var totals lineTotals
	zero := new(big.Rat)
	for _, l := range engine {
		tl := lines[l.index]
		action.add(tl.ID, actionLineReturned, l.quantity, *l.to)
		if err := totals.add(tl.ID, zero, zero, l.quantity); err != nil {
			return err
		}
	}
	if err := totals.write(ctx, txq, t.ID); err != nil {
		return err
	}
	now := pgtype.Timestamptz{Time: s.deps.Clock(), Valid: true}
	if err := action.write(ctx, txq, t, who, now); err != nil {
		return err
	}
	if err := txq.CloseTransfer(ctx, store.CloseTransferParams{
		ID: t.ID, Status: transferCancelled, Now: now.Time, ClosedByUserID: who.UserID, ClosedByDisplay: &who.Display,
	}); err != nil {
		return fmt.Errorf("inventory: cancel transfer %d: %w", t.ID, err)
	}
	return s.recordEvent(ctx, txq, who, event{
		EntityKind: entityTransfer, EntityID: t.ID, SiteID: &t.FromSiteID, Kind: eventTransferCancelled,
		Payload: map[string]any{"number": t.Number, "movementId": res.movementID},
	})
}
