package accounting

// Statements (A27): a camt.053 file or a CSV file imported into the bank
// accounts' lines. The file is read whole before anything is written — a
// camt.053 file as a stream with no DOCTYPE, a CSV file by the account's
// mapping — and kept in the object store by its SHA-256. Each statement is
// written in its own transaction, the bank account FOR UPDATE: a line the
// account already has is skipped by the bank's id, or without one by its
// fingerprint and its ordinal among the file's lines of that fingerprint, so
// a file imported twice adds nothing and two genuine identical lines of one
// day both stay. A line that looks like one of another origin — the same
// day, amount and KID, counterparty account or text — is written flagged
// as a possible duplicate, kept out of matching until a person says. A file
// imported into an account the feed reads brings only the days before the
// feed's start: the feed brings the rest. A gap or an overlap against the
// previous statement's balances is flagged, never refused.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/accounting/bankstmt"
	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// The sources of a statement.
const (
	statementCamt053 = "camt053"
	statementCSV     = "csv"
	statementFeed    = "feed"
)

// statementFile is the file a statement came from, as kept.
type statementFile struct {
	name, key, sha string
}

// storedStatement is what writing one statement came to.
type storedStatement struct {
	id                 int64
	imported, skipped  int
	coveredByFeed      int
	possibleDuplicates int
	continuity, detail string
	repeated           bool
}

// writeStatement writes one statement's lines on the account in one
// transaction (A27). ordinals counts, per fingerprint, the lines of the same
// file written before, so the file's identical lines keep their ordinals. by
// is nil for the feed, whose lines carry the bank's transaction id.
func (s *server) writeStatement(ctx context.Context, bankAccountID int64, account string, st bankstmt.Statement, source string,
	file *statementFile, ordinals map[string]int, by, lease *uuid.UUID) (storedStatement, error) {
	var out storedStatement
	read := st
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		out, st = storedStatement{}, read
		if lease != nil {
			// The feed's write lands only while its claim holds.
			if _, err := q.HoldsFeedLease(ctx, store.HoldsFeedLeaseParams{ID: bankAccountID, LeaseID: lease}); noRows(err) {
				return errFeedLeaseLost
			} else if err != nil {
				return err
			}
		} else if acc, err := q.LockBankAccount(ctx, bankAccountID); err != nil {
			return err
		} else if acc.Source == "feed" && acc.FeedStartDay != nil {
			// The feed brings its days; a file brings only those before.
			kept := st.Lines[:0:0]
			for _, l := range st.Lines {
				if l.BookedOn.Before(*acc.FeedStartDay) {
					kept = append(kept, l)
				} else {
					out.coveredByFeed++
				}
			}
			st.Lines = kept
			if !st.To.Before(*acc.FeedStartDay) {
				// The statement runs into the feed's days: its balances are
				// not the kept lines' alone, and its period ends before them.
				st.Opening, st.Closing = nil, nil
				st.To = acc.FeedStartDay.AddDate(0, 0, -1)
				if st.To.Before(st.From) {
					st.To = st.From
				}
			}
		}
		if file != nil {
			prior, err := q.StatementOfFile(ctx, store.StatementOfFileParams{BankAccountID: bankAccountID, FileSha256: &file.sha,
				StatementRef: st.Ref})
			if err == nil {
				out = storedStatement{id: prior, skipped: len(st.Lines), continuity: "unknown", repeated: true}
				return nil
			}
			if !noRows(err) {
				return err
			}
		}
		out.continuity, out.detail = "unknown", ""
		if st.Opening != nil {
			out.continuity = "ok"
			prev, err := q.PreviousBalancedStatement(ctx, store.PreviousBalancedStatementParams{BankAccountID: bankAccountID,
				Before: st.From})
			switch {
			case noRows(err):
			case err != nil:
				return err
			case !st.From.After(prev.PeriodTo):
				out.continuity = "overlap"
				out.detail = fmt.Sprintf("The statement starts on %s, within the previous statement, which runs to %s.",
					st.From.Format(time.DateOnly), prev.PeriodTo.Format(time.DateOnly))
			case st.From.After(prev.PeriodTo.AddDate(0, 0, 1)):
				out.continuity = "gap"
				out.detail = fmt.Sprintf("No statement covers %s to %s.", prev.PeriodTo.AddDate(0, 0, 1).Format(time.DateOnly),
					st.From.AddDate(0, 0, -1).Format(time.DateOnly))
			case mustRat(prev.ClosingBalance).Cmp(st.Opening) != 0:
				out.continuity = "mismatch"
				out.detail = fmt.Sprintf("The opening balance %s is not the previous statement's closing balance %s.",
					money(st.Opening), money(mustRat(prev.ClosingBalance)))
			}
		}
		p := store.InsertStatementParams{BankAccountID: bankAccountID, Source: source, StatementRef: st.Ref, PeriodFrom: st.From,
			PeriodTo: st.To, Continuity: out.continuity, ContinuityDetail: truncate(out.detail, 500), Now: s.now(), UserID: by}
		if st.Opening != nil && st.Closing != nil {
			p.OpeningBalance, p.ClosingBalance = ptr(money(st.Opening)), ptr(money(st.Closing))
		}
		if file != nil {
			p.FileName, p.ObjectKey, p.FileSha256 = ptr(cleanFileName(file.name)), &file.key, &file.sha
		}
		id, err := q.InsertStatement(ctx, p)
		if err != nil {
			return err
		}
		out.id = id
		for _, l := range st.Lines {
			fp := bankstmt.Fingerprint(account, l)
			ordinals[fp]++
			lp := store.InsertStatementLineParams{StatementID: id, BankAccountID: bankAccountID, BookedOn: l.BookedOn,
				ValueOn: l.ValueOn, Amount: money(l.Amount), Text: l.Text, Reference: l.Reference,
				CounterpartyName: l.CounterpartyName, CounterpartyAccount: l.CounterpartyAccount, Fingerprint: fp,
				Ordinal: int16(min(ordinals[fp], 32767)), Kind: l.Kind, Reversal: l.Reversal, TxReference: l.TxReference, Now: s.now()}
			if lp.Kind == "" {
				lp.Kind = bankstmt.KindTransfer
			}
			if l.KID != "" {
				lp.Kid = ptr(l.KID)
			}
			if l.BankID != "" {
				lp.BankID = ptr(l.BankID)
			}
			if len(l.Batch) > 0 {
				items := make([]gen.AccountingBankBatchItem, 0, len(l.Batch))
				for _, b := range l.Batch {
					item := gen.AccountingBankBatchItem{Amount: money(b.Amount), Reference: b.Reference, Name: b.Name, Text: b.Text}
					if b.KID != "" {
						item.Kid = ptr(b.KID)
					}
					if b.BankID != "" {
						item.BankId = ptr(b.BankID)
					}
					items = append(items, item)
				}
				if lp.Batch, err = json.Marshal(items); err != nil {
					return err
				}
			}
			dup, err := q.ProbableDuplicate(ctx, store.ProbableDuplicateParams{BankAccountID: bankAccountID, BookedOn: l.BookedOn,
				Amount: lp.Amount, Source: source, Kid: l.KID, CounterpartyAccount: l.CounterpartyAccount, TextKey: textKey(l.Text)})
			switch {
			case err == nil:
				lp.PossibleDuplicateOf = &dup
			case !noRows(err):
				return err
			}
			lineID, err := q.InsertStatementLine(ctx, lp)
			if err != nil {
				return err
			}
			switch {
			case lineID == 0:
				out.skipped++
			case lp.PossibleDuplicateOf != nil:
				out.imported++
				out.possibleDuplicates++
			default:
				out.imported++
			}
		}
		return q.SetStatementCounts(ctx, store.SetStatementCountsParams{ID: id, LinesImported: int32(out.imported),
			LinesSkipped: int32(out.skipped)})
	})
	return out, err
}

// textKey is a line's text as the cross-origin duplicate check compares it:
// lower case, letters and digits only — as ProbableDuplicate reads the
// stored line's.
func textKey(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// keepStatementFile writes the file to the object store under its hash, once.
func (s *server) keepStatementFile(ctx context.Context, data []byte, ext string) (string, string, error) {
	st, err := s.objectStore()
	if err != nil || !s.storageConfigured {
		if err == nil {
			err = storage.ErrNotConfigured
		}
		return "", "", storageRefusal(err)
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	key := "bank-statements/" + sha + ext
	exists, err := st.Exists(ctx, key)
	if err != nil {
		return "", "", storageRefusal(err)
	}
	if !exists {
		contentType := "text/csv"
		if ext == ".xml" {
			contentType = "application/xml"
		}
		if err := st.Put(ctx, key, bytes.NewReader(data), contentType); err != nil {
			s.logger().ErrorContext(ctx, "accounting: a statement file could not be written to the store", "error", err.Error())
			return "", "", storageRefusal(err)
		}
	}
	return key, sha, nil
}

// ImportAccountingBankStatement imports a camt.053 or a CSV file (A27).
func (s *server) ImportAccountingBankStatement(ctx context.Context, req gen.ImportAccountingBankStatementRequestObject) (gen.ImportAccountingBankStatementResponseObject, error) {
	up, err := readStatementUpload(req.Body)
	if err != nil {
		return nil, err
	}
	out, err := s.importStatement(ctx, up, req.Params.BankAccountId, callerID(ctx))
	if err != nil {
		return nil, err
	}
	return gen.ImportAccountingBankStatement201JSONResponse(out), nil
}

// importStatement reads a statement file and writes its statements, as by;
// bankAccountID names the bank account a CSV file is of.
func (s *server) importStatement(ctx context.Context, up statementUpload, bankAccountID *int64, by uuid.UUID) (gen.AccountingBankImportResult, error) {
	var err error
	q := store.New(s.deps.Pool)
	var statements []bankstmt.Statement
	source, ext := statementCamt053, ".xml"
	if bankstmt.IsXML(up.data) {
		if statements, err = bankstmt.ReadCamt053(bytes.NewReader(up.data), bankstmt.DefaultLimits); err != nil {
			return gen.AccountingBankImportResult{}, statementRefusal(err)
		}
	} else {
		source, ext = statementCSV, ".csv"
		if bankAccountID == nil {
			return gen.AccountingBankImportResult{}, invalidField("The file cannot be imported", "bankAccountId", codeBankStatementAccountReq,
				"A CSV file names no account: choose the bank account it is of.")
		}
		row, err := q.GetBankAccount(ctx, *bankAccountID)
		if noRows(err) {
			return gen.AccountingBankImportResult{}, notFound()
		}
		if err != nil {
			return gen.AccountingBankImportResult{}, err
		}
		m, err := accountMapping(row, up.mapping)
		if err != nil {
			return gen.AccountingBankImportResult{}, err
		}
		rows, err := bankstmt.ReadCSV(up.data, m, bankstmt.DefaultLimits)
		if err != nil {
			return gen.AccountingBankImportResult{}, statementRefusal(err)
		}
		st := bankstmt.Statement{Account: row.AccountNumber, Currency: currency}
		for _, r := range rows {
			if r.Err != nil {
				return gen.AccountingBankImportResult{}, invalidField("The file cannot be imported", attachmentFormField, codeBankStatementInvalid,
					fmt.Sprintf("Row %d cannot be read: %s. Check the CSV mapping with a preview.", r.N, r.Err.Error()))
			}
			st.Lines = append(st.Lines, r.Line)
			if st.From.IsZero() || r.Line.BookedOn.Before(st.From) {
				st.From = r.Line.BookedOn
			}
			if r.Line.BookedOn.After(st.To) {
				st.To = r.Line.BookedOn
			}
		}
		if len(st.Lines) == 0 {
			return gen.AccountingBankImportResult{}, invalidField("The file cannot be imported", attachmentFormField, codeBankStatementInvalid,
				"The file holds no line after its header rows.")
		}
		statements = []bankstmt.Statement{st}
	}
	accounts, err := q.ListBankAccounts(ctx)
	if err != nil {
		return gen.AccountingBankImportResult{}, err
	}
	byNumber := map[string]store.ListBankAccountsRow{}
	for _, a := range accounts {
		byNumber[a.AccountNumber] = a
	}
	out := gen.AccountingBankImportResult{Statements: []gen.AccountingBankImportStatement{}, UnknownAccounts: []string{}}
	known := 0
	seen := map[string]bool{}
	for _, st := range statements {
		out.NotBooked += int32(st.NotBooked)
		if _, ok := byNumber[st.Account]; ok && st.Account != "" {
			known++
			continue
		}
		name := st.Account
		if name == "" {
			name = st.Raw
		}
		if !seen[name] {
			seen[name] = true
			out.UnknownAccounts = append(out.UnknownAccounts, name)
		}
	}
	if known == 0 {
		return gen.AccountingBankImportResult{}, refuseWith(invalidField("The file cannot be imported", attachmentFormField, codeBankStatementAccount,
			"No bank account here has the file's account: add it under Bank accounts first."), out.UnknownAccounts)
	}
	key, sha, err := s.keepStatementFile(ctx, up.data, ext)
	if err != nil {
		return gen.AccountingBankImportResult{}, err
	}
	file := &statementFile{name: up.name, key: key, sha: sha}
	ordinals := map[int64]map[string]int{}
	for _, st := range statements {
		acc, ok := byNumber[st.Account]
		if !ok || st.Account == "" {
			continue
		}
		if ordinals[acc.ID] == nil {
			ordinals[acc.ID] = map[string]int{}
		}
		stored, err := s.writeStatement(ctx, acc.ID, acc.AccountNumber, st, source, file, ordinals[acc.ID], &by, nil)
		if err != nil {
			return gen.AccountingBankImportResult{}, err
		}
		out.Statements = append(out.Statements, gen.AccountingBankImportStatement{BankAccountId: acc.ID, BankAccountName: acc.Name,
			StatementId: stored.id, Source: source, PeriodFrom: apiDate(st.From), PeriodTo: apiDate(st.To),
			Imported: int32(stored.imported), Skipped: int32(stored.skipped), Continuity: stored.continuity,
			ContinuityDetail: stored.detail, Repeated: stored.repeated, CoveredByFeed: int32(stored.coveredByFeed),
			PossibleDuplicates: int32(stored.possibleDuplicates)})
	}
	s.logger().InfoContext(ctx, "accounting: a bank statement was imported", "user_id", by, "source", source, "sha256", sha,
		"statements", len(out.Statements), "unknown_accounts", len(out.UnknownAccounts))
	return out, nil
}

// refuseWith is a 400 refusal whose detail also names the accounts.
func refuseWith(err error, accounts []string) error {
	var r *refusal
	if errors.As(err, &r) && len(accounts) > 0 {
		r.problem.Detail = ptr(fmt.Sprintf("The file's accounts: %s.", joinList(accounts)))
	}
	return err
}

// joinList is the items joined with commas.
func joinList(items []string) string {
	out := ""
	for i, it := range items {
		if i > 0 {
			out += ", "
		}
		out += it
	}
	return out
}

// statementView is a statement as the contract answers it.
func statementView(r store.ListStatementsRow) gen.AccountingBankStatement {
	out := gen.AccountingBankStatement{Id: r.ID, BankAccountId: r.BankAccountID, Source: r.Source, StatementRef: r.StatementRef,
		PeriodFrom: apiDate(r.PeriodFrom), PeriodTo: apiDate(r.PeriodTo), Continuity: r.Continuity,
		ContinuityDetail: r.ContinuityDetail, FileName: r.FileName, HasFile: r.HasFile, LinesImported: r.LinesImported,
		LinesSkipped: r.LinesSkipped, ImportedAt: r.ImportedAt}
	if r.OpeningBalance != "" {
		out.OpeningBalance, out.ClosingBalance = ptr(money(mustRat(r.OpeningBalance))), ptr(money(mustRat(r.ClosingBalance)))
	}
	if r.ImportedByUserID != nil {
		out.ImportedByUserId = ptr(openapi_types.UUID(*r.ImportedByUserID))
	}
	return out
}

// ListAccountingBankStatements is the statements imported, newest first.
func (s *server) ListAccountingBankStatements(ctx context.Context, req gen.ListAccountingBankStatementsRequestObject) (gen.ListAccountingBankStatementsResponseObject, error) {
	rows, err := store.New(s.deps.Pool).ListStatements(ctx, store.ListStatementsParams{BankAccountID: req.Params.BankAccountId,
		RowLimit: 200})
	if err != nil {
		return nil, err
	}
	out := gen.ListAccountingBankStatements200JSONResponse{Items: []gen.AccountingBankStatement{}}
	for _, r := range rows {
		out.Items = append(out.Items, statementView(r))
	}
	return out, nil
}

// statementDownload is a statement's file streamed as it was uploaded, never
// cached.
type statementDownload struct {
	name string
	body io.ReadCloser
}

func (r statementDownload) VisitDownloadAccountingBankStatementFileResponse(w http.ResponseWriter) error {
	defer func() { _ = r.body.Close() }()
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": r.name}))
	return gen.DownloadAccountingBankStatementFile200ApplicationoctetStreamResponse{Body: r.body}.
		VisitDownloadAccountingBankStatementFileResponse(w)
}

// DownloadAccountingBankStatementFile streams a statement's file as uploaded.
func (s *server) DownloadAccountingBankStatementFile(ctx context.Context, req gen.DownloadAccountingBankStatementFileRequestObject) (gen.DownloadAccountingBankStatementFileResponseObject, error) {
	row, err := store.New(s.deps.Pool).GetStatement(ctx, req.Id)
	if noRows(err) || (err == nil && row.ObjectKey == nil) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	st, err := s.objectStore()
	if err != nil {
		return nil, storageRefusal(err)
	}
	body, err := st.Get(ctx, *row.ObjectKey)
	if err != nil {
		return nil, storageRefusal(err)
	}
	return statementDownload{name: cleanFileName(deref(row.FileName)), body: body}, nil
}
