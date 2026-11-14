package invoices

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

// syncOutcome is what a cycle that did not fail did: the batch it stored,
// nil when nothing new was read, or the refusal of the batch.
type syncOutcome struct {
	bankFileID *int64
	refused    *feedRefusal
}

// feedClaim is one claimed account: the row as the claim saw it, its lease
// and the claim's one clock read.
type feedClaim struct {
	row   store.InvoicesBankFeedAccount
	lease string
	now   time.Time
}

// syncAccount is one cycle of F5 for the claimed account. A failure is
// answered as an error — the bank's typed ones (bankfeed.ErrUnauthorized,
// *bankfeed.ErrRateLimited) among them — and a refusal as the outcome;
// either way nothing of the batch is stored. Matching stopping early is not
// a failure: the batch stands and the lines it did not reach stay pending,
// matched by the next cycle.
func (s *server) syncAccount(ctx context.Context, c feedClaim) (syncOutcome, error) {
	account := c.row.Account
	loc, err := s.businessZone(ctx)
	if err != nil {
		return syncOutcome{}, err
	}
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

	// 1. The days to read.
	from := utcDay(c.row.StartDay.Time)
	latest, err := q.LatestBookedInFormat(ctx, store.LatestBookedInFormatParams{Account: account, Format: bankFeedFormatFeed})
	if err != nil {
		return syncOutcome{}, fmt.Errorf("invoices: read account %s's latest feed day: %w", account, err)
	}
	if latest.Valid {
		if back := utcDay(latest.Time).Add(-bankFeedOverlap); back.After(from) {
			from = back
		}
	}
	if from.After(today) {
		from = today
	}

	feed, err := s.bankFeed(ctx)
	if err != nil {
		return syncOutcome{}, err
	}
	txs, err := feed.Transactions(ctx, account, from, today)
	if err != nil {
		return syncOutcome{}, err
	}

	// 2 and 3. Refuse the whole batch, or keep the incoming credit transfers.
	kept, raws, ignored, refusal := judgeFeedBatch(txs, account, today, utcDay(c.row.StartDay.Time))
	if refusal != nil {
		return syncOutcome{refused: refusal}, nil
	}

	// 4. Drop what an earlier cycle stored.
	kept, raws, err = dropStoredFeedLines(ctx, q, account, kept, raws)
	if err != nil {
		return syncOutcome{}, err
	}
	var out syncOutcome
	if len(kept) > 0 {
		id, err := s.storeFeedBatch(ctx, c, kept, raws, ignored)
		if err != nil {
			return syncOutcome{}, err
		}
		if id == nil {
			return syncOutcome{refused: &feedRefusal{reason: refusedFormatMismatch, detail: "The account's format is no longer feed."}}, nil
		}
		out.bankFileID = id
		if hook := bankFeedSyncAfterStore; hook != nil {
			if err := hook(ctx, *id); err != nil {
				return syncOutcome{}, err
			}
		}
	}

	// 7. Matching, by no user, of every feed batch of the account with a
	// line still pending — this cycle's, and any an earlier cycle stored and
	// did not get to match.
	files, err := q.PendingFeedFilesOfAccount(ctx, account)
	if err != nil {
		return syncOutcome{}, fmt.Errorf("invoices: read account %s's pending feed batches: %w", account, err)
	}
	for _, id := range files {
		if _, err := s.matchFile(ctx, id, nil, c.now, loc); err != nil {
			// Logged by matchFile; the lines stay pending for the next cycle.
			break
		}
	}
	return out, nil
}

// judgeFeedBatch is F5 steps 2 and 3 over everything the bank answered, in
// the order it answered: the batch refused whole on the first transaction
// that breaks a rule, else the incoming credit transfers above zero built as
// lines (findings 1 and 12) with the bytes the bank sent for each, and the
// rest counted by kind. A transaction booked before the start day is left
// out uncounted: a person chose to read nothing before it.
func judgeFeedBatch(txs []bankfeed.Transaction, account string, today, startDay time.Time) ([]bankfile.Transaction, []json.RawMessage, map[bankfile.IgnoredKind]int, *feedRefusal) {
	ignored := map[bankfile.IgnoredKind]int{}
	seen := make(map[string]bool, len(txs))
	var kept []bankfile.Transaction
	var raws []json.RawMessage
	for _, tx := range txs {
		archive := strings.ReplaceAll(tx.ID, "-", "")
		switch {
		case tx.ID == "" || utf8.RuneCountInString(tx.ID) > feedLineRefSize || utf8.RuneCountInString(archive) > feedArchiveRefSize ||
			tx.AmountMinor < 0 || tx.AmountMinor > maxFeedAmountMinor:
			return nil, nil, nil, &feedRefusal{reason: refusedTransactionUnreadable, detail: feedDetail(tx.ID)}
		case seen[tx.ID]:
			return nil, nil, nil, &feedRefusal{reason: refusedDuplicateTransaction, detail: feedDetail(tx.ID)}
		case utcDay(tx.BookedOn).After(today):
			return nil, nil, nil, &feedRefusal{reason: refusedBookedInFuture, detail: feedDetail(tx.ID)}
		case tx.Currency != "NOK":
			return nil, nil, nil, &feedRefusal{reason: refusedCurrencyNotNOK, detail: feedDetail(tx.ID)}
		case tx.Direction != bankfeed.DirectionCredit && tx.Direction != bankfeed.DirectionDebit:
			// Fail closed: a transaction the adapter cannot place is never
			// read as a payment, nor passed over as one going out.
			return nil, nil, nil, &feedRefusal{reason: refusedDirectionUnknown, detail: feedDetail(tx.ID)}
		}
		seen[tx.ID] = true
		booked := utcDay(tx.BookedOn)
		if booked.Before(startDay) {
			continue
		}
		switch {
		case tx.Kind == bankfeed.KindInternal:
			ignored[bankfile.IgnoredInternalTransfer]++
			continue
		case tx.Kind == bankfeed.KindCard:
			ignored[bankfile.IgnoredCardInformation]++
			continue
		case tx.Kind != bankfeed.KindTransfer:
			// A kind the port does not name is no transfer: counted with the
			// card transactions, never kept.
			ignored[bankfile.IgnoredCardInformation]++
			continue
		case tx.Direction == bankfeed.DirectionDebit:
			ignored[bankfile.IgnoredDebit]++
			continue
		case tx.AmountMinor == 0:
			ignored[bankfile.IgnoredZeroAmount]++
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
func dropStoredFeedLines(ctx context.Context, q *store.Queries, account string, kept []bankfile.Transaction, raws []json.RawMessage) ([]bankfile.Transaction, []json.RawMessage, error) {
	if len(kept) == 0 {
		return nil, nil, nil
	}
	probe := append([]bankfile.Transaction(nil), kept...)
	bankfile.Fingerprints(probe)
	fps, refs := make([]string, len(probe)), make([]string, len(probe))
	for i, tx := range probe {
		fps[i], refs[i] = tx.Fingerprint, tx.LineRef
	}
	stored, err := q.StoredFeedLines(ctx, store.StoredFeedLinesParams{Account: account, Fingerprints: fps, LineRefs: refs})
	if err != nil {
		return nil, nil, fmt.Errorf("invoices: read what account %s's feed stored: %w", account, err)
	}
	seenFP, seenRef := make(map[string]bool, len(stored)), make(map[string]bool, len(stored))
	for _, r := range stored {
		seenFP[r.Fingerprint], seenRef[r.LineRef] = true, true
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
	return outTx, outRaw, nil
}

// storeFeedBatch is F5 steps 5 and 6 for the lines left: their bytes as one
// JSON array, in the order the bank answered them, stored once under
// bank-files/<sha256>.json — Exists, then Put, outside any transaction, as
// an upload's file — then the import's transaction with no uploader, the
// bank as provider and the identity folio|<account>|<claim time>. It answers
// the batch's id, or nil when the import found the account's format no
// longer feed.
func (s *server) storeFeedBatch(ctx context.Context, c feedClaim, kept []bankfile.Transaction, raws []json.RawMessage, ignored map[bankfile.IgnoredKind]int) (*int64, error) {
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

	identity := providerFolio + "|" + c.row.Account + "|" + c.now.UTC().Format(time.RFC3339)
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
