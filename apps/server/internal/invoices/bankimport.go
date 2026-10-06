package invoices

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime/multipart"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the bank import (invoices payments and reminders design D3):
// an OCR giro or camt.054 file uploaded, checked all or nothing by package
// bankfile, its accounts judged against the seller's, refused when the same
// bytes or the same file identity came before, stored once in the object
// store, and written in one transaction — the accounts first, in account
// order, then the file row and every line in one insert ordered by
// fingerprint, a line an earlier or overlapping file already brought kept as
// a duplicate row linked to it — and the files' reads. Once the import has
// committed, its lines are matched to invoices (D4, matching.go), each in a
// transaction of its own, the uploader registering the payments; a line
// matching did not reach stays pending for POST …/match.

// The upload's limits: the file itself (bankfile.MaxBytes, 10 MiB), and the
// request around it, which leaves room for the multipart framing — the
// customers import's margin (customers/import.go).
const (
	maxBankFileRequestBytes = bankfile.MaxBytes + 64<<10
	bankFileFormField       = "file"
)

// bankFileBodyLimits raises the router's request-body cap for the upload.
// Every other operation of this module keeps the platform default (1 MiB).
var bankFileBodyLimits = map[string]int64{
	"postInvoicesBankFiles": maxBankFileRequestBytes,
}

// The 400 on 'file' and the 409s of the import.
const (
	invalidBankFileTitle    = "Invalid bank file"
	invalidBankUploadText   = "A bank file is one OCR giro or camt.054 file of at most 10 MiB, sent as the multipart part named 'file'"
	codeBankAccountUnknown  = "bank_account_unknown"
	codeBankFileDuplicate   = "bank_file_duplicate"
	codeBankFormatMismatch  = "bank_import_format_mismatch"
	bankAccountUnknownTitle = "The bank file names an unknown account"
	bankFileDuplicateTitle  = "The bank file was imported before"
	bankFormatMismatchTitle = "The account's bank files come in another format"
)

// bankImportAfterInsert, when a test sets it (export_test.go's
// SetBankImportAfterInsert), runs inside the import's transaction after
// every insert, before the commit, with the file's id; an error rolls the
// import back. A race test parks one import there while another starts. Nil
// in production.
var bankImportAfterInsert func(ctx context.Context, bankFileID int64) error

// importResult is what one import did (D3 step 9): the file row, its lines
// and what became of them. matched, exceptions and their amounts are
// matching's (D4); pending is what matching left, when it stopped early.
type importResult struct {
	file                                                   store.InvoicesBankFile
	transactions, matched, exceptions, duplicates, pending int
	matchedAmount, exceptionsAmount                        *big.Rat
	ignored                                                map[bankfile.IgnoredKind]int
}

// bankFilePart reads the one multipart part named "file" — the customers
// import's importFilePart for a bank file. A missing part, a second one, an
// empty one, one past bankfile.MaxBytes and a read that fails (a malformed
// body, or the router's cap firing) are all ok=false.
func bankFilePart(mr *multipart.Reader) ([]byte, bool) {
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
		if part.FormName() != bankFileFormField {
			_ = part.Close()
			continue
		}
		if found {
			_ = part.Close()
			return nil, false
		}
		found = true
		data, err = io.ReadAll(io.LimitReader(part, bankfile.MaxBytes+1))
		_ = part.Close()
		if err != nil {
			return nil, false
		}
	}
	return data, found && len(data) > 0 && len(data) <= bankfile.MaxBytes
}

// bankFileKey is where a file's bytes are stored, under this module's
// scope: bank-files/<sha256>.<ocr|xml> — keyed by the hash, so the same
// bytes land on the same object and an import that fails after storing them
// leaves nothing a later one does not reuse.
func bankFileKey(sha string, f bankfile.Format) string {
	ext := "xml"
	if f == bankfile.FormatOCR {
		ext = "ocr"
	}
	return "bank-files/" + sha + "." + ext
}

// invalidBankFile is the 400 on 'file'.
func invalidBankFile(message string) gen.PostInvoicesBankFiles400ApplicationProblemPlusJSONResponse {
	return gen.PostInvoicesBankFiles400ApplicationProblemPlusJSONResponse(invalid(invalidBankFileTitle, fieldError(bankFileFormField, message)))
}

// bankFileDuplicate is the 409 naming the earlier import of the same file.
func bankFileDuplicate(id int64, at time.Time, by uuid.UUID) gen.PostInvoicesBankFiles409ApplicationProblemPlusJSONResponse {
	c := conflict(codeBankFileDuplicate, bankFileDuplicateTitle,
		fmt.Sprintf("This file was imported before, as bank file %d on %s. Nothing was imported again.", id, at.In(oslo).Format("2006-01-02 15:04")))
	c.BankFileId, c.UploadedAt, c.UploadedBy = &id, &at, &by
	return gen.PostInvoicesBankFiles409ApplicationProblemPlusJSONResponse(c)
}

// earlierImport is the earlier import of the same bytes, else of the same
// file identity, read on the pool (D3 step 5); found false when neither.
func earlierImport(ctx context.Context, q *store.Queries, sha string, f *bankfile.File) (id int64, at time.Time, by uuid.UUID, found bool, err error) {
	bySHA, err := q.BankFileBySHA(ctx, sha)
	if err == nil {
		return bySHA.ID, bySHA.UploadedAt, bySHA.UploadedByUserID, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, time.Time{}, uuid.Nil, false, fmt.Errorf("invoices: read the bank file by hash: %w", err)
	}
	byIdentity, err := q.BankFileByIdentity(ctx, store.BankFileByIdentityParams{Format: string(f.Format), FileIdentity: f.Identity})
	if err == nil {
		return byIdentity.ID, byIdentity.UploadedAt, byIdentity.UploadedByUserID, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, time.Time{}, uuid.Nil, false, fmt.Errorf("invoices: read the bank file by identity: %w", err)
	}
	return 0, time.Time{}, uuid.Nil, false, nil
}

// PostInvoicesBankFiles Import a bank file
// (POST /api/v1/invoices/bank-files)
//
// D3's steps in order, with one clock read: the part; the format and the
// file's own rules (bankfile.Parse); every account the seller's; the file
// not imported before; its bytes stored once, outside any transaction; then
// one transaction — the accounts, the file row, the lines, the duplicates;
// then, once it has committed, matching (D4) over its lines, the uploader
// registering. A stop in matching is logged and leaves the rest pending; the
// import itself stands.
func (s *server) PostInvoicesBankFiles(ctx context.Context, req gen.PostInvoicesBankFilesRequestObject) (gen.PostInvoicesBankFilesResponseObject, error) {
	now := s.deps.Clock()
	today := businessDay(now)
	by := callerID(ctx)

	body, ok := bankFilePart(req.Body)
	if !ok {
		return invalidBankFile(invalidBankUploadText), nil
	}
	file, err := bankfile.Parse(body, today)
	if err != nil {
		var refusal *bankfile.Error
		if errors.As(err, &refusal) {
			return invalidBankFile(refusal.Error()), nil
		}
		return nil, fmt.Errorf("invoices: parse the bank file: %w", err)
	}

	q := store.New(s.deps.Pool)
	known, err := q.KnownSellerAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the seller's accounts: %w", err)
	}
	for _, account := range file.Accounts {
		if !slices.Contains(known, account) {
			return gen.PostInvoicesBankFiles409ApplicationProblemPlusJSONResponse(conflict(codeBankAccountUnknown, bankAccountUnknownTitle,
				fmt.Sprintf("The file names the account ending %s, which is neither the seller's bank account nor one an issued invoice printed. Nothing was imported.",
					account[len(account)-4:]))), nil
		}
	}

	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	if id, at, uploader, found, err := earlierImport(ctx, q, sha, file); err != nil {
		return nil, err
	} else if found {
		return bankFileDuplicate(id, at, uploader), nil
	}

	if !s.storageConfigured {
		return gen.PostInvoicesBankFiles503ApplicationProblemPlusJSONResponse(storageUnavailable(
			"This installation has no object store, so the bank file could not be kept as the documentation of the payments booked from it. Nothing was imported.")), nil
	}
	key := bankFileKey(sha, file.Format)
	exists, err := s.objectExists(ctx, key)
	if err == nil && !exists {
		err = s.objectPut(ctx, key, bytes.NewReader(body), "application/octet-stream")
	}
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "invoices: a bank file could not be stored", "key", key, "error", err.Error())
		return gen.PostInvoicesBankFiles503ApplicationProblemPlusJSONResponse(storageUnavailable(
			"The object store could not keep the bank file. Nothing was imported; try again.")), nil
	}

	result, refusal, err := s.importBankFile(ctx, file, sha, key, len(body), by, now)
	if err != nil {
		return nil, err
	}
	if refusal != nil {
		return refusal, nil
	}
	// The stop, when matching stops early, is logged by matchFile; what it
	// did not reach stays pending, and the 201 says so.
	counts, _ := s.matchFile(ctx, result.file.ID, by, now)
	result.matched, result.matchedAmount = counts.matched, counts.matchedAmount
	result.exceptions, result.exceptionsAmount = counts.exceptions, counts.exceptionsAmount
	result.pending -= counts.done
	body201, err := importResultResponse(result)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesBankFiles201JSONResponse(body201), nil
}

// importBankFile is D3 step 7, one transaction: the file's accounts
// upserted and read FOR SHARE in account order (lockImportAccounts), a
// format other than the file's refused; the file row; every line in one
// insert ordered by fingerprint; the lines it skipped inserted again as
// duplicate rows; the file's duplicates set. A unique violation on the
// file's hash or identity is an import that committed after the pool read:
// the same 409, naming it.
func (s *server) importBankFile(ctx context.Context, file *bankfile.File, sha, key string, size int, by uuid.UUID, now time.Time) (
	importResult, gen.PostInvoicesBankFilesResponseObject, error,
) {
	ignoredKinds, err := json.Marshal(file.Ignored)
	if err != nil {
		return importResult{}, nil, fmt.Errorf("invoices: encode the ignored kinds: %w", err)
	}
	ignored := 0
	for _, n := range file.Ignored {
		ignored += n
	}
	lines, err := bankLineParams(file)
	if err != nil {
		return importResult{}, nil, err
	}

	var result importResult
	var refusal gen.PostInvoicesBankFilesResponseObject
	var raced bool
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		accounts, err := lockImportAccounts(ctx, txq, file.Accounts, string(file.Format), by, now)
		if err != nil {
			return err
		}
		for _, a := range accounts {
			if a.Format != string(file.Format) {
				refusal = gen.PostInvoicesBankFiles409ApplicationProblemPlusJSONResponse(conflict(codeBankFormatMismatch, bankFormatMismatchTitle,
					fmt.Sprintf("The bank files of account %s are imported as %s; this file is %s. Nothing was imported. A manager can change the account's format.",
						a.Account, a.Format, file.Format)))
				return errRefused
			}
		}
		row, err := txq.InsertBankFile(ctx, store.InsertBankFileParams{
			Format: string(file.Format), Sha256: sha, FileIdentity: file.Identity, ObjectKey: key,
			ByteSize: int32(size), Accounts: file.Accounts, //nolint:gosec // at most bankfile.MaxBytes
			FirstBookedOn: dateOrNull(file.FirstBookedOn), LastBookedOn: dateOrNull(file.LastBookedOn),
			Transactions: int32(len(file.Transactions)), Ignored: int32(ignored), //nolint:gosec // at most bankfile.MaxTransactions
			IgnoredKinds: ignoredKinds, UploadedByUserID: by, UploadedAt: now,
		})
		if db.IsUniqueViolation(err, "uq_bank_files_sha256", "uq_bank_files_identity") {
			raced = true
			return errRefused
		}
		if err != nil {
			return fmt.Errorf("invoices: insert the bank file: %w", err)
		}
		duplicates := 0
		if len(file.Transactions) > 0 {
			lines.BankFileID = row.ID
			live, err := txq.InsertBankTransactions(ctx, lines)
			if err != nil {
				return fmt.Errorf("invoices: insert bank file %d's lines: %w", row.ID, err)
			}
			skipped := skippedLines(lines, live)
			if len(skipped.Fingerprints) > 0 {
				kept, err := txq.InsertDuplicateTransactions(ctx, store.InsertDuplicateTransactionsParams(skipped))
				if err != nil {
					return fmt.Errorf("invoices: insert bank file %d's duplicate lines: %w", row.ID, err)
				}
				if len(kept) != len(skipped.Fingerprints) {
					return fmt.Errorf("invoices: bank file %d: %d lines were skipped but %d found the line they duplicate", row.ID, len(skipped.Fingerprints), len(kept))
				}
			}
			duplicates = len(skipped.Fingerprints)
		}
		if _, err := txq.SetBankFileDuplicates(ctx, store.SetBankFileDuplicatesParams{ID: row.ID, Duplicates: int32(duplicates)}); err != nil { //nolint:gosec // at most bankfile.MaxTransactions
			return fmt.Errorf("invoices: set bank file %d's duplicates: %w", row.ID, err)
		}
		row.Duplicates = ptr(int32(duplicates)) //nolint:gosec // at most bankfile.MaxTransactions
		if hook := bankImportAfterInsert; hook != nil {
			if err := hook(ctx, row.ID); err != nil {
				return err
			}
		}
		result = importResult{
			file: row, transactions: len(file.Transactions), duplicates: duplicates,
			pending: len(file.Transactions) - duplicates, ignored: file.Ignored,
			matchedAmount: new(big.Rat), exceptionsAmount: new(big.Rat),
		}
		return nil
	})
	switch {
	case errors.Is(err, errRefused) && raced:
		id, at, uploader, found, err := earlierImport(ctx, store.New(s.deps.Pool), sha, file)
		if err != nil {
			return importResult{}, nil, err
		}
		if !found {
			return importResult{}, nil, fmt.Errorf("invoices: bank file %s collided with an import that cannot be read", sha)
		}
		return importResult{}, bankFileDuplicate(id, at, uploader), nil
	case errors.Is(err, errRefused):
		return importResult{}, refusal, nil
	case err != nil:
		return importResult{}, nil, err
	}
	return result, nil, nil
}

// bankLineParams is every transaction of file as InsertBankTransactions'
// parallel arrays, in the file's order (the statement orders them).
func bankLineParams(file *bankfile.File) (store.InsertBankTransactionsParams, error) {
	n := len(file.Transactions)
	p := store.InsertBankTransactionsParams{
		Format:   string(file.Format),
		LineRefs: make([]string, 0, n), Accounts: make([]string, 0, n), Directions: make([]string, 0, n),
		Negatives: make([]bool, 0, n), BookedOns: make([]pgtype.Date, 0, n), ValueOns: make([]pgtype.Date, 0, n),
		OrderedOns: make([]pgtype.Date, 0, n), Amounts: make([]pgtype.Numeric, 0, n), Kids: make([]string, 0, n),
		RemittanceTexts: make([]string, 0, n), DebtorNames: make([]string, 0, n), DebtorAccounts: make([]string, 0, n),
		ArchiveRefs: make([]string, 0, n), BankCodes: make([]string, 0, n), Fingerprints: make([]string, 0, n),
		Ordinals: make([]int16, 0, n),
	}
	for _, tx := range file.Transactions {
		direction := "credit"
		if tx.Debit {
			direction = "debit"
		}
		if tx.Ordinal < 1 || tx.Ordinal > 32767 {
			return p, fmt.Errorf("invoices: line %s's ordinal %d does not fit", tx.LineRef, tx.Ordinal)
		}
		p.LineRefs = append(p.LineRefs, tx.LineRef)
		p.Accounts = append(p.Accounts, tx.Account)
		p.Directions = append(p.Directions, direction)
		p.Negatives = append(p.Negatives, tx.Negative)
		p.BookedOns = append(p.BookedOns, pgDate(tx.BookedOn))
		p.ValueOns = append(p.ValueOns, optionalDate(tx.ValueOn))
		p.OrderedOns = append(p.OrderedOns, optionalDate(tx.OrderedOn))
		p.Amounts = append(p.Amounts, pgtype.Numeric{Int: big.NewInt(tx.AmountMinor), Exp: -2, Valid: true})
		p.Kids = append(p.Kids, strings.TrimSpace(tx.KID))
		p.RemittanceTexts = append(p.RemittanceTexts, tx.RemittanceText)
		p.DebtorNames = append(p.DebtorNames, tx.DebtorName)
		p.DebtorAccounts = append(p.DebtorAccounts, tx.DebtorAccount)
		p.ArchiveRefs = append(p.ArchiveRefs, tx.ArchiveRef)
		p.BankCodes = append(p.BankCodes, tx.BankCode)
		p.Fingerprints = append(p.Fingerprints, tx.Fingerprint)
		p.Ordinals = append(p.Ordinals, int16(tx.Ordinal))
	}
	return p, nil
}

// skippedLines is the lines of p whose fingerprint the insert did not
// return — those a live line of their account already had — as the same
// parallel arrays, for InsertDuplicateTransactions.
func skippedLines(p store.InsertBankTransactionsParams, live []store.InsertBankTransactionsRow) store.InsertBankTransactionsParams {
	inserted := make(map[string]bool, len(live))
	for _, r := range live {
		inserted[r.Fingerprint] = true
	}
	out := store.InsertBankTransactionsParams{BankFileID: p.BankFileID, Format: p.Format}
	for i, fp := range p.Fingerprints {
		if inserted[fp] {
			continue
		}
		out.LineRefs = append(out.LineRefs, p.LineRefs[i])
		out.Accounts = append(out.Accounts, p.Accounts[i])
		out.Directions = append(out.Directions, p.Directions[i])
		out.Negatives = append(out.Negatives, p.Negatives[i])
		out.BookedOns = append(out.BookedOns, p.BookedOns[i])
		out.ValueOns = append(out.ValueOns, p.ValueOns[i])
		out.OrderedOns = append(out.OrderedOns, p.OrderedOns[i])
		out.Amounts = append(out.Amounts, p.Amounts[i])
		out.Kids = append(out.Kids, p.Kids[i])
		out.RemittanceTexts = append(out.RemittanceTexts, p.RemittanceTexts[i])
		out.DebtorNames = append(out.DebtorNames, p.DebtorNames[i])
		out.DebtorAccounts = append(out.DebtorAccounts, p.DebtorAccounts[i])
		out.ArchiveRefs = append(out.ArchiveRefs, p.ArchiveRefs[i])
		out.BankCodes = append(out.BankCodes, p.BankCodes[i])
		out.Fingerprints = append(out.Fingerprints, fp)
		out.Ordinals = append(out.Ordinals, p.Ordinals[i])
	}
	return out
}

// dateOrNull is a day, or NULL for the zero time (a file without
// transactions has no booking days).
func dateOrNull(d time.Time) pgtype.Date {
	if d.IsZero() {
		return pgtype.Date{}
	}
	return pgDate(d)
}

// optionalDate is a day the bank may not have written: NULL for nil.
func optionalDate(d *time.Time) pgtype.Date {
	if d == nil {
		return pgtype.Date{}
	}
	return dateOrNull(*d)
}

// GetInvoicesBankFiles List the imported bank files
// (GET /api/v1/invoices/bank-files)
func (s *server) GetInvoicesBankFiles(ctx context.Context, req gen.GetInvoicesBankFilesRequestObject) (gen.GetInvoicesBankFilesResponseObject, error) {
	p := req.Params
	if errs := validatePageParams(p.Page, p.PageSize); len(errs) > 0 {
		return gen.GetInvoicesBankFiles400ApplicationProblemPlusJSONResponse(apicommon.Problem(invalidQueryTitle, strings.Join(errs, " "))), nil
	}
	page, pageSize := pageParams(p.Page, p.PageSize)
	q := store.New(s.deps.Pool)
	total, err := q.CountBankFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: count the bank files: %w", err)
	}
	rows, err := q.ListBankFiles(ctx, store.ListBankFilesParams{PageOffset: (page - 1) * pageSize, PageSize: pageSize})
	if err != nil {
		return nil, fmt.Errorf("invoices: list the bank files: %w", err)
	}
	data := make([]gen.InvoicesBankFile, 0, len(rows))
	for _, r := range rows {
		f, err := bankFileResponse(r.InvoicesBankFile, r.Pending, r.Exceptions, r.Matched)
		if err != nil {
			return nil, err
		}
		data = append(data, f)
	}
	return gen.GetInvoicesBankFiles200JSONResponse(gen.PaginatedResponseOfInvoicesBankFile{
		Data: data, Pagination: apicommon.Pagination(page, pageSize, int32(total)), //nolint:gosec // a count of files
	}), nil
}

// GetInvoicesBankFilesById Get an imported bank file
// (GET /api/v1/invoices/bank-files/{id})
func (s *server) GetInvoicesBankFilesById(ctx context.Context, req gen.GetInvoicesBankFilesByIdRequestObject) (gen.GetInvoicesBankFilesByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetBankFile(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetInvoicesBankFilesById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read bank file %d: %w", req.Id, err)
	}
	file, err := bankFileResponse(row.InvoicesBankFile, row.Pending, row.Exceptions, row.Matched)
	if err != nil {
		return nil, err
	}
	lines, err := q.TransactionsOfFile(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("invoices: read bank file %d's lines: %w", req.Id, err)
	}
	txs, err := bankTransactionsResponse(ctx, q, lines, false)
	if err != nil {
		return nil, err
	}
	return gen.GetInvoicesBankFilesById200JSONResponse(gen.InvoicesBankFileDetail{File: file, Transactions: txs}), nil
}

// importResultResponse is the 201's body.
func importResultResponse(r importResult) (gen.InvoicesBankImportResult, error) {
	file, err := bankFileResponse(r.file, int64(r.pending), int64(r.exceptions), int64(r.matched))
	if err != nil {
		return gen.InvoicesBankImportResult{}, err
	}
	return gen.InvoicesBankImportResult{
		File: file, Transactions: int32(r.transactions), Duplicates: int32(r.duplicates), Pending: int32(r.pending), //nolint:gosec // at most bankfile.MaxTransactions
		Matched: int32(r.matched), MatchedAmount: floatFromRat(r.matchedAmount, 2), //nolint:gosec // at most bankfile.MaxTransactions
		Exceptions: int32(r.exceptions), ExceptionsAmount: floatFromRat(r.exceptionsAmount, 2), //nolint:gosec // at most bankfile.MaxTransactions
		Ignored: ignoredResponse(r.ignored),
	}, nil
}

// ignoredResponse is the four kinds as the wire names them.
func ignoredResponse(kinds map[bankfile.IgnoredKind]int) gen.InvoicesBankIgnored {
	return gen.InvoicesBankIgnored{
		Debit:           int32(kinds[bankfile.IgnoredDebit]),           //nolint:gosec // a count of lines in a 10 MiB file
		NotBooked:       int32(kinds[bankfile.IgnoredNotBooked]),       //nolint:gosec // a count of lines in a 10 MiB file
		CardInformation: int32(kinds[bankfile.IgnoredCardInformation]), //nolint:gosec // a count of lines in a 10 MiB file
		ZeroAmount:      int32(kinds[bankfile.IgnoredZeroAmount]),      //nolint:gosec // a count of lines in a 10 MiB file
	}
}

// bankFileResponse is one file on the wire, with its lines counted by
// status.
func bankFileResponse(f store.InvoicesBankFile, pending, exceptions, matched int64) (gen.InvoicesBankFile, error) {
	var kinds map[bankfile.IgnoredKind]int
	if err := json.Unmarshal(f.IgnoredKinds, &kinds); err != nil {
		return gen.InvoicesBankFile{}, fmt.Errorf("invoices: read bank file %d's ignored kinds: %w", f.ID, err)
	}
	var duplicates int32
	if f.Duplicates != nil {
		duplicates = *f.Duplicates
	}
	return gen.InvoicesBankFile{
		Id: f.ID, Format: f.Format, Sha256: f.Sha256, Accounts: f.Accounts,
		FirstBookedOn: wireDateOf(f.FirstBookedOn), LastBookedOn: wireDateOf(f.LastBookedOn),
		Transactions: f.Transactions, Duplicates: duplicates, Ignored: f.Ignored, IgnoredKinds: ignoredResponse(kinds),
		UploadedBy: f.UploadedByUserID, UploadedAt: f.UploadedAt,
		Pending: int32(pending), Exceptions: int32(exceptions), Matched: int32(matched), //nolint:gosec // counts of one file's lines
	}, nil
}

// bankTransactionResponse is one line on the wire as it stands, without
// what bankTransactionsResponse reads beside it (bankqueue.go): its file,
// what is applied from it, its rest, its twin, its suggestions, its events.
func bankTransactionResponse(l store.InvoicesBankTransaction) (gen.InvoicesBankTransaction, error) {
	amount, err := floatFromNumeric(l.Amount)
	if err != nil {
		return gen.InvoicesBankTransaction{}, err
	}
	var resolution *gen.InvoicesBankTransactionResolution
	if l.Resolution != nil {
		resolution = ptr(gen.InvoicesBankTransactionResolution(*l.Resolution))
	}
	return gen.InvoicesBankTransaction{
		Id: l.ID, LineRef: l.LineRef, Account: l.Account, Direction: l.Direction, Negative: l.Negative,
		BookedOn: wireDate(l.BookedOn.Time), ValueOn: wireDateOf(l.ValueOn), OrderedOn: wireDateOf(l.OrderedOn),
		Amount: amount, Kid: l.Kid, RemittanceText: l.RemittanceText, DebtorName: l.DebtorName,
		DebtorAccount: l.DebtorAccount, ArchiveRef: l.ArchiveRef, Status: gen.InvoicesBankTransactionStatus(l.Status),
		Reason: reasonWire(l.Reason), DuplicateOfId: l.DuplicateOfID, SuggestedInvoiceId: l.SuggestedInvoiceID,
		Resolution: resolution, ResolvedBy: l.ResolvedByUserID, ResolvedAt: l.ResolvedAt, ResolutionNote: l.ResolutionNote,
		Applied: []gen.InvoicesBankTransactionApplied{}, Events: []gen.InvoicesBankTransactionEvent{},
	}, nil
}
