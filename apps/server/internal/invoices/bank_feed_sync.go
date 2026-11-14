package invoices

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/bankfeed"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is one cycle of the bank feed for one account (invoices bank
// feed design F5; plan C2): a sync is an import without an upload. The
// account's transactions are read from the bank outside any transaction;
// the batch is refused whole when one of them breaks a rule; the incoming
// credit transfers are kept and the rest only counted; what an earlier cycle
// stored is dropped; the document of what is left is stored in the object
// store; then the import's own transaction, and matching by no user after
// it. Every step that talks to the bank or the object store holds no
// database connection, so a cycle runs on a pool of one.

const (
	// bankFeedOverlap is how far back each read starts before the latest
	// day the feed booked (F5 step 1): a bank may book a transaction on an
	// earlier day than it first appeared.
	bankFeedOverlap = 10 * 24 * time.Hour
	// The lengths a line's columns hold (plan finding 12): the remittance
	// text and the debtor's name are cut on a character boundary; a debtor
	// account longer than its column is stored empty; a KID longer than 25
	// characters is no KID (the camt.054 rule).
	feedTextSize          = 1000
	feedNameSize          = 140
	feedDebtorAccountSize = 34
	feedKIDSize           = 25
	feedLineRefSize       = 60
	feedArchiveRefSize    = 35
	// maxFeedAmountMinor is the largest amount numeric(14,2) holds, in øre.
	maxFeedAmountMinor = 99_999_999_999_999
)

// The reasons a batch is refused whole (F5 step 2, plan finding 9;
// bank_feed_accounts.reason).
const (
	refusedBookedInFuture        = "booked_in_future"
	refusedCurrencyNotNOK        = "currency_not_nok"
	refusedDirectionUnknown      = "direction_unknown"
	refusedDuplicateTransaction  = "duplicate_transaction"
	refusedTransactionUnreadable = "transaction_unreadable"
	refusedFormatMismatch        = "format_mismatch"
)

// errFeedClaimLost is a write of a sync that found its account no longer its
// claim's — switched off, disconnected, the key refused, or claimed again
// after the lease ran out: the claim ends and writes nothing more.
var errFeedClaimLost = errors.New("invoices: the bank feed account moved under the claim")

// errFeedStorage is a batch the object store could not keep, or an
// installation without one: the cycle fails and is tried again (F5 step 5).
var errFeedStorage = errors.New("invoices: the object store could not keep the bank feed's batch")

// bankFeedSyncAfterStore, when a test sets it (export_test.go), runs after a
// cycle's import has committed and before its lines are matched — the crash
// point between storing a batch and matching it; an error ends the cycle
// there, as a process that stopped would. Nil in production.
var bankFeedSyncAfterStore func(ctx context.Context, bankFileID int64) error

// feedRefusal is a batch refused whole: the rule it broke and the bank's id
// of the transaction that broke it — never a name or a text.
type feedRefusal struct{ reason, detail string }

// syncOutcome is what a cycle that did not fail did: the last batch it
// stored, nil when nothing new was read, or the refusal of a batch; and more
// when the read stopped short of today — a backlog the account is due again
// at once to drain.
type syncOutcome struct {
	bankFileID *int64
	refused    *feedRefusal
	more       bool
	// unmatched is the bank's id of the first line matching could not
	// match, left pending; "" for none.
	unmatched string
	// keyVersion is the stored key's version (the connection's updated_at)
	// the cycle read with, nil before it had one.
	keyVersion *time.Time
}

// feedClaim is one claimed account: the row as the claim saw it, its lease
// and the claim's one clock read.
type feedClaim struct {
	row   store.InvoicesBankFeedAccount
	lease string
	now   time.Time
}

// The windows a cycle reads (plan C review, item 1): at most 31 days each,
// each its own batch, budgeted by calls to the bank alone —
// bankFeedCallsPerCycle in all, a window too large for one answer halved and
// asked again, the smaller size kept for the rest of the cycle — so a cycle
// never outlives its lease.
const bankFeedWindowDays = 31

// bankFeedCallsPerCycle is the calls a cycle makes at most; a test lowers
// it (export_test.go).
var bankFeedCallsPerCycle = 8

// errFeedNoProgress is a cycle that spent its calls and read nothing past
// the cursor it started with: the bank's answers are too large to move
// forward. It fails as answer_too_large, backing off instead of spinning.
var errFeedNoProgress = errors.New("invoices: the bank feed read nothing past its cursor this cycle")

// errFeedAnswerTooLarge is a window of a single day whose answer is still too
// large to read or to store as one batch: the account fails with its own
// reason, never as a bank that could not be reached.
var errFeedAnswerTooLarge = errors.New("invoices: the bank's answer for one day is too large")

// syncAccount is one cycle of F5 for the claimed account. A failure is
// answered as an error — the bank's typed ones (bankfeed.ErrUnauthorized,
// *bankfeed.ErrRateLimited) among them — and a refusal as the outcome;
// either way nothing of the window that failed is stored, and the windows
// before it stand. Matching stopping early is not a failure: the batch
// stands and the lines it did not reach stay pending, matched by the next
// cycle.
func (s *server) syncAccount(ctx context.Context, c feedClaim) (syncOutcome, error) {
	loc, err := s.businessZone(ctx)
	if err != nil {
		return syncOutcome{}, err
	}
	out, err := s.readAccount(ctx, c, loc)
	if errors.Is(err, errFeedClaimLost) || errors.Is(err, bankfeed.ErrUnauthorized) {
		// Not this claim's any more, or the key refused: nothing more.
		return out, err
	}
	// 7. Matching, by no user, of every feed batch of the account with a
	// line still pending — this cycle's, and any an earlier cycle stored and
	// did not get to match — after a refused or failed read too, so the
	// batches it stored before are matched (second review, M1). A line that
	// cannot be matched is passed over and named on the account; the rest
	// are matched.
	unmatched, merr := s.matchPendingFeed(ctx, c.row.Account, c.now, loc)
	if err == nil && out.refused == nil {
		out.unmatched = unmatched
		err = merr
	}
	return out, err
}

// matchPendingFeed matches every feed batch of account with a line still
// pending, oldest first, past the lines that fail (matchFilePastFailures):
// the first such line's line_ref, and the error that stopped matching — the
// context's, a read that failed, or errFeedMatchingStopped.
func (s *server) matchPendingFeed(ctx context.Context, account string, now time.Time, loc *time.Location) (string, error) {
	files, err := store.New(s.deps.Pool).PendingFeedFilesOfAccount(ctx, account)
	if err != nil {
		return "", fmt.Errorf("invoices: read account %s's pending feed batches: %w", account, err)
	}
	unmatched := ""
	for _, id := range files {
		failed, err := s.matchFilePastFailures(ctx, id, now, loc)
		if unmatched == "" {
			unmatched = failed
		}
		if err != nil {
			// Logged by matching; what it did not reach stays pending for
			// the next cycle.
			return unmatched, err
		}
	}
	return unmatched, nil
}

// readAccount is a cycle's read of the claimed account (steps 1–6): the
// format, the windows, each judged and stored. It answers what it stored
// and a refusal as the outcome, a failure as the error — with the outcome
// so far, so the batches it stored before are matched all the same.
func (s *server) readAccount(ctx context.Context, c feedClaim, loc *time.Location) (syncOutcome, error) {
	account := c.row.Account
	today := holidays.DayIn(c.now, loc)
	q := store.New(s.deps.Pool)

	// The format first: an account a person moved back to files is not read
	// (plan finding 9). The import's transaction checks it again.
	acct, err := q.ImportAccount(ctx, account)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return syncOutcome{refused: &feedRefusal{reason: refusedFormatMismatch, detail: "The account has no bank import format."}}, nil
	case err != nil:
		return syncOutcome{}, fmt.Errorf("invoices: read account %s: %w", account, err)
	}
	if acct.Format != bankFeedFormatFeed {
		return syncOutcome{refused: &feedRefusal{reason: refusedFormatMismatch, detail: "The account's format is " + acct.Format + "."}}, nil
	}

	// 1. The days to read. Forward first: from the day after the last day
	// read to the end (the read cursor), or from the start day, to today —
	// so every cycle moves the cursor on, however small the bank's answers
	// make its windows. Then the overlap again: the ten days before the
	// cursor this cycle started with, since a bank may book a payment on an
	// earlier day than it first showed it.
	startDay := utcDay(c.row.StartDay.Time)
	var readThrough *time.Time
	if c.row.ReadThrough.Valid {
		rt := utcDay(c.row.ReadThrough.Time)
		readThrough = &rt
	}
	// The cursor this cycle started with: a cycle that ends with more to
	// read and nothing past it made no progress.
	started := readThrough
	type span struct{ from, to time.Time }
	var spans []span
	forward := startDay
	if started != nil && started.AddDate(0, 0, 1).After(forward) {
		forward = started.AddDate(0, 0, 1)
	}
	if !forward.After(today) {
		spans = append(spans, span{forward, today})
	}
	if started != nil {
		back := started.Add(-bankFeedOverlap)
		if back.Before(startDay) {
			back = startDay
		}
		end := *started
		if end.After(today) {
			end = today
		}
		if !back.After(end) {
			spans = append(spans, span{back, end})
		}
	}

	feed, err := s.bankFeed(ctx)
	if err != nil {
		return syncOutcome{}, err
	}
	var out syncOutcome
	if noted, ok := feed.(notedFeed); ok {
		out.keyVersion = noted.version
	}
	windowDays := bankFeedWindowDays
	calls := 0
spans:
	for i, sp := range spans {
		inForward := i == 0 && sp.from.Equal(forward)
		from := sp.from
		for !from.After(sp.to) {
			if calls == bankFeedCallsPerCycle {
				if !inForward {
					// Only the overlap is left unread: the next cycle reads
					// it again.
					break spans
				}
				// The calls of this cycle are spent with days left to read:
				// due again at once — unless the cycle read nothing past
				// the cursor it started with, which would only spin.
				if readThrough == nil || (started != nil && !readThrough.After(*started)) {
					return out, errFeedNoProgress
				}
				out.more = true
				break spans
			}
			to := from.AddDate(0, 0, windowDays-1)
			if to.After(sp.to) {
				to = sp.to
			}
			calls++
			txs, err := feed.Transactions(ctx, account, from, to)
			tooLarge := errors.Is(err, bankfeed.ErrTooLarge) || (err == nil && feedAnswerTooLarge(txs))
			if err != nil && !tooLarge {
				return out, err
			}
			if tooLarge {
				days := int(to.Sub(from).Hours()/24) + 1
				if days == 1 {
					return out, errFeedAnswerTooLarge
				}
				// Too large for one answer or one batch: half the days,
				// rounded up, and keep the smaller window for the rest of
				// the cycle.
				windowDays = (days + 1) / 2
				continue
			}

			// 2 and 3. Refuse the whole batch, or keep the incoming credit
			// transfers; what is not kept is counted only when booked after
			// the last day read, so the overlap is not counted again.
			kept, raws, ignored, refusal := judgeFeedBatch(txs, account, today, startDay, readThrough)
			if refusal != nil {
				out.refused = refusal
				return out, nil
			}
			// 4. Drop what an earlier cycle stored.
			kept, raws, refusal, err = dropStoredFeedLines(ctx, q, account, kept, raws)
			if err != nil {
				return out, err
			}
			if refusal != nil {
				out.refused = refusal
				return out, nil
			}
			if len(kept) > 0 {
				id, err := s.storeFeedBatch(ctx, c, from, to, kept, raws, ignored)
				if err != nil {
					return out, err
				}
				if id == nil {
					out.refused = &feedRefusal{reason: refusedFormatMismatch, detail: "The account's format is no longer feed."}
					return out, nil
				}
				out.bankFileID = id
				if hook := bankFeedSyncAfterStore; hook != nil {
					if err := hook(ctx, *id); err != nil {
						return out, err
					}
				}
			}
			// The window is read and judged to its end: the cursor moves —
			// never back, an overlap's window ending before it.
			n, err := q.AdvanceBankFeedRead(ctx, store.AdvanceBankFeedReadParams{ReadThrough: pgDate(to), Account: account, LeaseID: c.lease})
			if err != nil {
				return out, fmt.Errorf("invoices: move account %s's read cursor: %w", account, err)
			}
			if n == 0 {
				return out, errFeedClaimLost
			}
			if readThrough == nil || to.After(*readThrough) {
				readThrough = &to
			}
			from = to.AddDate(0, 0, 1)
		}
	}

	return out, nil
}

// feedAnswerTooLarge reports whether an answer is more than one batch may
// hold — the transactions a bank file may carry, or their bytes past a bank
// file's bound — so the window is halved rather than stored.
func feedAnswerTooLarge(txs []bankfeed.Transaction) bool {
	if len(txs) > bankfile.MaxTransactions {
		return true
	}
	size := 0
	for _, tx := range txs {
		size += len(tx.Raw) + 1
	}
	return size > bankfile.MaxBytes
}

// judgeFeedBatch is F5 steps 2 and 3 over everything the bank answered, in
// the order it answered: the batch refused whole on the first transaction
// that breaks a rule — one id twice with an incoming credit among them, a
// direction the adapter cannot place,
// or an incoming credit of a kind the port does not name, unreadable, booked
// after today or not in NOK — else the incoming credit transfers above zero
// built as lines (findings 1 and 12) with the bytes the bank sent for each,
// and the rest counted by kind, however odd: money out, a card, a transfer
// between the customer's own accounts is never a refusal. A transaction
// booked before the start day is left out unjudged and uncounted: a person
// chose to read nothing before it.
func judgeFeedBatch(txs []bankfeed.Transaction, account string, today, startDay time.Time, readThrough *time.Time) ([]bankfile.Transaction, []json.RawMessage, map[bankfile.IgnoredKind]int, *feedRefusal) {
	ignored := map[bankfile.IgnoredKind]int{}
	// count is a transaction not kept, counted on the batch unless an
	// earlier window already read its day.
	count := func(kind bankfile.IgnoredKind, booked time.Time) {
		if readThrough == nil || booked.After(*readThrough) {
			ignored[kind]++
		}
	}
	seen := make(map[string]bool, len(txs))
	var kept []bankfile.Transaction
	var raws []json.RawMessage
	for _, tx := range txs {
		booked := utcDay(tx.BookedOn)
		if booked.Before(startDay) {
			// A person chose to read nothing before the start day: not
			// judged, not counted.
			continue
		}
		refuse := func(reason string) ([]bankfile.Transaction, []json.RawMessage, map[bankfile.IgnoredKind]int, *feedRefusal) {
			return nil, nil, nil, &feedRefusal{reason: reason, detail: feedDetail(tx.ID)}
		}
		if tx.Direction != bankfeed.DirectionCredit && tx.Direction != bankfeed.DirectionDebit {
			// Fail closed: a transaction the adapter cannot place is never
			// read as a payment, nor passed over as one going out.
			return refuse(refusedDirectionUnknown)
		}
		// One id twice refuses the batch only when an incoming credit — a
		// payment that might be kept — is one of them; two payments out or
		// card lines sharing an id are counted (second review, low).
		incoming := tx.Direction == bankfeed.DirectionCredit && tx.Kind != bankfeed.KindInternal && tx.Kind != bankfeed.KindCard
		if tx.ID != "" {
			before, twice := seen[tx.ID]
			if twice && (before || incoming) {
				return refuse(refusedDuplicateTransaction)
			}
			seen[tx.ID] = before || incoming
		}
		// Not an incoming customer payment — money out, a card, a transfer
		// between the customer's own accounts: counted, whatever else is odd
		// about it, never kept and never a refusal (plan C review, item 6).
		switch {
		case tx.Kind == bankfeed.KindInternal:
			count(bankfile.IgnoredInternalTransfer, booked)
			continue
		case tx.Kind == bankfeed.KindCard:
			count(bankfile.IgnoredCardInformation, booked)
			continue
		case tx.Direction == bankfeed.DirectionDebit:
			count(bankfile.IgnoredDebit, booked)
			continue
		}
		// An incoming credit: every rule holds, or the batch is refused.
		archive := strings.ReplaceAll(tx.ID, "-", "")
		switch {
		case tx.Kind != bankfeed.KindTransfer,
			tx.ID == "" || utf8.RuneCountInString(tx.ID) > feedLineRefSize || utf8.RuneCountInString(archive) > feedArchiveRefSize,
			tx.AmountMinor < 0 || tx.AmountMinor > maxFeedAmountMinor:
			return refuse(refusedTransactionUnreadable)
		case booked.After(today):
			return refuse(refusedBookedInFuture)
		case tx.Currency != "NOK":
			return refuse(refusedCurrencyNotNOK)
		case tx.AmountMinor == 0:
			count(bankfile.IgnoredZeroAmount, booked)
			continue
		}
		kid := strings.TrimSpace(tx.KID)
		if utf8.RuneCountInString(kid) > feedKIDSize {
			// Longer than a KID can be: no KID (the camt.054 rule) — the line
			// is queued no_kid for a person.
			kid = ""
		}
		debtorAccount := feedText(tx.DebtorAccount)
		if utf8.RuneCountInString(debtorAccount) > feedDebtorAccountSize {
			debtorAccount = ""
		}
		kept = append(kept, bankfile.Transaction{
			LineRef: tx.ID, Account: account, BookedOn: booked,
			AmountMinor: tx.AmountMinor, Currency: "NOK", KID: kid,
			RemittanceText: cutRunes(feedText(tx.Text), feedTextSize),
			DebtorName:     cutRunes(feedText(tx.DebtorName), feedNameSize),
			DebtorAccount:  debtorAccount,
			ArchiveRef:     archive,
		})
		raws = append(raws, tx.Raw)
	}
	return kept, raws, ignored, nil
}

// feedDetail is a refusal's detail: the bank's id of the transaction, cut to
// what the column holds — never a name or a text.
func feedDetail(id string) string {
	if id == "" {
		return "A transaction without an id."
	}
	return cutRunes("Transaction "+id+".", 500)
}

// feedText is a text the bank wrote as a line stores it: every control
// character a space, trimmed — the files' rule (bankfile).
func feedText(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7F && r < 0xA0) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, s))
}

// cutRunes is s cut to at most n characters, on a character boundary.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n]))
}

// dropStoredFeedLines is F5 step 4: every kept line whose fingerprint a live
// line of the account has, or whose bank id a feed line of the account has,
// is the same record read again, not a duplicate, and gets no row.
func dropStoredFeedLines(ctx context.Context, q *store.Queries, account string, kept []bankfile.Transaction, raws []json.RawMessage) ([]bankfile.Transaction, []json.RawMessage, *feedRefusal, error) {
	if len(kept) == 0 {
		return nil, nil, nil, nil
	}
	probe := append([]bankfile.Transaction(nil), kept...)
	bankfile.Fingerprints(probe)
	fps, refs := make([]string, len(probe)), make([]string, len(probe))
	for i, tx := range probe {
		fps[i], refs[i] = tx.Fingerprint, tx.LineRef
	}
	stored, err := q.StoredFeedLines(ctx, store.StoredFeedLinesParams{Account: account, Fingerprints: fps, LineRefs: refs})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("invoices: read what account %s's feed stored: %w", account, err)
	}
	byRef := make(map[string]bankfile.Transaction, len(probe))
	for _, tx := range probe {
		byRef[tx.LineRef] = tx
	}
	seenFP, seenRef := make(map[string]bool, len(stored)), make(map[string]bool, len(stored))
	for _, r := range stored {
		seenFP[r.Fingerprint], seenRef[r.LineRef] = true, true
		// The same id with another amount or KID is not the same record:
		// refused, never dropped in silence (plan C review, item 6).
		if tx, ok := byRef[r.LineRef]; ok && r.Fingerprint != tx.Fingerprint {
			amount, err := ratFromNumeric(r.Amount)
			if err != nil {
				return nil, nil, nil, err
			}
			if amount.Cmp(big.NewRat(tx.AmountMinor, 100)) != 0 || r.Kid != tx.KID {
				return nil, nil, &feedRefusal{reason: refusedDuplicateTransaction, detail: feedDetail(tx.LineRef)}, nil
			}
		}
	}
	var outTx []bankfile.Transaction
	var outRaw []json.RawMessage
	for i, tx := range probe {
		if seenFP[tx.Fingerprint] || seenRef[tx.LineRef] {
			continue
		}
		outTx = append(outTx, kept[i])
		outRaw = append(outRaw, raws[i])
	}
	return outTx, outRaw, nil, nil
}

// storeFeedBatch is F5 steps 5 and 6 for the lines left: their bytes as one
// JSON array, in the order the bank answered them, stored once under
// bank-files/<sha256>.json — Exists, then Put, outside any transaction, as
// an upload's file — then the import's transaction with no uploader, the
// bank as provider and the identity folio|<account>|<from>..<to>|<lease>. It answers
// the batch's id, or nil when the import found the account's format no
// longer feed.
func (s *server) storeFeedBatch(ctx context.Context, c feedClaim, from, to time.Time, kept []bankfile.Transaction, raws []json.RawMessage, ignored map[bankfile.IgnoredKind]int) (*int64, error) {
	var doc bytes.Buffer
	doc.WriteByte('[')
	for i, raw := range raws {
		if i > 0 {
			doc.WriteByte(',')
		}
		doc.Write(raw)
	}
	doc.WriteByte(']')
	if !json.Valid(doc.Bytes()) {
		return nil, errors.New("invoices: the bank's transactions do not make a JSON document")
	}
	if doc.Len() > bankfile.MaxBytes {
		return nil, fmt.Errorf("invoices: account %s's batch is %d bytes, more than a bank file may be", c.row.Account, doc.Len())
	}
	if len(kept) > bankfile.MaxTransactions {
		return nil, fmt.Errorf("invoices: account %s's batch holds %d lines, more than a bank file may", c.row.Account, len(kept))
	}
	if !s.storageConfigured {
		return nil, fmt.Errorf("%w: this installation has no object store", errFeedStorage)
	}
	sum := sha256.Sum256(doc.Bytes())
	sha := hex.EncodeToString(sum[:])
	key := bankFileKey(sha, bankfile.FormatFeed)
	exists, err := s.objectExists(ctx, key)
	if err == nil && !exists {
		err = s.objectPut(ctx, key, bytes.NewReader(doc.Bytes()), "application/json")
	}
	if err != nil {
		return nil, fmt.Errorf("%w (%s): %w", errFeedStorage, key, err)
	}

	// Unique per batch: the window's days and the claim's lease, so two
	// windows of one cycle, or two claims of one second, are two files.
	identity := providerFolio + "|" + c.row.Account + "|" + from.Format(time.DateOnly) + ".." + to.Format(time.DateOnly) + "|" + c.lease
	file := bankfile.FeedFile(identity, c.row.Account, kept, ignored)
	result, err := s.storeBankFile(ctx, file, sha, key, doc.Len(), bankFileOrigin{provider: ptr(providerFolio), feedLease: c.lease}, c.now)
	var mismatch *formatMismatch
	switch {
	case errors.As(err, &mismatch):
		return nil, nil
	case err != nil:
		return nil, err
	}
	return &result.file.ID, nil
}
