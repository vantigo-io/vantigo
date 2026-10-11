package staff

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/ident"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the sealed identifiers (design S4, §4 Sealed fields): the
// national identity number and the bank account, written only under
// staff:personal-data, checked by staff/ident, sealed by the platform's
// secrets box and — the national id — indexed by a keyed MAC in a unique
// column, so the same number on two people is refused without opening
// anything. Only what may be shown is kept in clear: the national id's kind
// (and a foreign id's country), the bank account's kind and last four
// digits. A value is opened only by its reveal, answered once with
// Cache-Control no-store and recorded as an event naming the viewer — or by
// the personal-data export (personal_data.go), which is recorded the same way.
//
// No digit of either ever reaches a response other than its reveal, an event
// payload, a problem detail or a log line: the refusals carry fixed
// sentences, the errors wrapped on the way to a 500 name the person's id and
// never the value, and the bank-change mail names the last four digits.

// The purposes the secrets box keys these values with (platform-packages.md:
// a purpose seals or MACs, never both, and a MAC purpose ends in -index).
// Each is part of every value stored under it: renaming one orphans them all.
const (
	purposeNationalID      = "staff/national-id"
	purposeNationalIDIndex = "staff/national-id-index"
	purposeBankAccount     = "staff/bank-account"
)

// nationalIDMACIndex is the unique constraint on national_id_mac, Postgres'
// own name for the column's UNIQUE.
const nationalIDMACIndex = "people_national_id_mac_key"

// noStore is a reveal's Cache-Control: the value is answered once and kept by
// no cache between the server and the person who asked.
const noStore = "no-store"

// bankChangeMailTimeout bounds the bank-change mail's send. The change has
// committed by then, so a slow mail server delays the answer and nothing else.
const bankChangeMailTimeout = 30 * time.Second

// identMessage is ident's refusal as a sentence for a field error. ident
// promises its errors name no digit; the check here keeps that promise from
// resting on one package alone — a message that carries a digit is replaced
// by fallback, since the refusal must never echo the value it refused.
func identMessage(err error, fallback string) string {
	msg := err.Error()
	if msg == "" || strings.ContainsAny(msg, "0123456789") {
		return fallback
	}
	return strings.ToUpper(msg[:1]) + msg[1:]
}

// nationalIDWriteError is the national id write's own database refusal: the
// MAC index's unique violation, which settles two writers of one number, is
// national_id_duplicate — naming nobody, since who already has the number is
// itself personal data. Anything else is the error it was.
func nationalIDWriteError(err error) error {
	if db.IsUniqueViolation(err, nationalIDMACIndex) {
		return refuseInvalid(invalidNationalIDTitle, fieldErrors{
			errs: fieldError("value", "Another person already has this national identity number"),
			code: codeNationalIDDuplicate,
		})
	}
	return err
}

// nationalIDSummary is a national id as an event payload names it: its kind
// and a foreign id's country, nil when none is on file.
func nationalIDSummary(kind, country *string) any {
	if kind == nil {
		return nil
	}
	out := map[string]any{"kind": *kind}
	if country != nil {
		out["country"] = *country
	}
	return out
}

// bankAccountSummary is a bank account as an event payload names it: its
// kind and last four digits, nil when none is on file.
func bankAccountSummary(kind, last4 *string) any {
	if kind == nil || last4 == nil {
		return nil
	}
	return map[string]any{"kind": *kind, "last4": *last4}
}

// sameDay reports whether a stored birth date is the civil date birth.
func sameDay(stored time.Time, birth time.Time) bool {
	return stored.Format(time.DateOnly) == birth.Format(time.DateOnly)
}

// checkBirthDateAgainstNationalID is reading 17 on HR's path: a birth date
// set on a person whose fødselsnummer or d-nummer encodes another is refused
// national_id_birth_date_mismatch on birthDate, as the national id's own
// write refuses it the other way round. The number is opened under the
// person's lock — local work, no call out — and never leaves this function.
// A foreign id encodes no date; a cleared birth date agrees with anything.
func (s *server) checkBirthDateAgainstNationalID(ctx context.Context, txq *store.Queries, personID int64, birth pgtype.Date) error {
	if !birth.Valid {
		return nil
	}
	row, err := txq.PersonNationalID(ctx, personID)
	if err != nil {
		return fmt.Errorf("staff: read person %d's national identity number: %w", personID, err)
	}
	if row.NationalIDKind == nil || *row.NationalIDKind == string(ident.Foreign) || len(row.NationalIDSealed) == 0 {
		return nil
	}
	value, err := s.deps.Secrets.Open(purposeNationalID, row.NationalIDSealed)
	if err != nil {
		return fmt.Errorf("staff: open person %d's national identity number: %w", personID, err)
	}
	n, err := ident.ParseNationalID(ident.NationalIDKind(*row.NationalIDKind), "", string(value))
	if err != nil {
		return fmt.Errorf("staff: person %d's stored national identity number no longer parses: %w", personID, ident.ErrInvalidNationalID)
	}
	if encoded, ok := n.BirthDate(); ok && !sameDay(birth.Time, encoded) {
		return refuseInvalid(invalidPersonTitle, fieldErrors{
			errs: fieldError("birthDate", "The person's national identity number encodes another birth date; correct whichever is wrong"),
			code: codeNationalIDBirthDateMismatch,
		})
	}
	return nil
}

// PutStaffPeopleByIdNationalId Set a person's national identity number
// (PUT /api/v1/staff/people/{id}/national-id)
//
// The number is checked, sealed and MAC'd before the transaction — pure
// computation, no call out — and written under the person's lock, where the
// birth date it encodes is judged against the person's.
func (s *server) PutStaffPeopleByIdNationalId(ctx context.Context, req gen.PutStaffPeopleByIdNationalIdRequestObject) (gen.PutStaffPeopleByIdNationalIdResponseObject, error) {
	var body gen.StaffNationalIdRequest
	if req.Body != nil {
		body = *req.Body
	}
	country := ""
	if body.Country != nil {
		country = *body.Country
	}
	n, err := ident.ParseNationalID(ident.NationalIDKind(body.Kind), country, body.Value)
	if err != nil {
		return gen.PutStaffPeopleByIdNationalId400ApplicationProblemPlusJSONResponse(invalid(invalidNationalIDTitle, fieldErrors{
			errs: fieldError("value", identMessage(err, "Not a valid national identity number of this kind")),
			code: codeNationalIDInvalid,
		})), nil
	}
	sealed, err := s.sealNationalID(n)
	if err != nil {
		return nil, fmt.Errorf("staff: person %d: %w", req.Id, err)
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}

	var (
		saved store.GetPersonRow
		link  *string
	)
	err = s.withPersonLock(ctx, req.Id, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := s.setNationalID(ctx, txq, req.Id, sealed, who); err != nil {
			return err
		}
		var err error
		if saved, err = readPerson(ctx, txq, req.Id); err != nil {
			return err
		}
		link, err = linkedUserID(ctx, txq, req.Id)
		return err
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PutStaffPeopleByIdNationalId404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PutStaffPeopleByIdNationalId404Response{}, nil
		case http.StatusConflict:
			return gen.PutStaffPeopleByIdNationalId409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PutStaffPeopleByIdNationalId400ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	out, err := s.respondPerson(ctx, caller, saved, link)
	if err != nil {
		return nil, err
	}
	return gen.PutStaffPeopleByIdNationalId200JSONResponse(out), nil
}

// PostStaffPeopleByIdNationalIdReveal Reveal a person's national identity number
// (POST /api/v1/staff/people/{id}/national-id/reveal)
//
// The value is opened and the reveal recorded in one transaction under the
// person's lock: a reveal that could not be recorded answers nothing.
func (s *server) PostStaffPeopleByIdNationalIdReveal(ctx context.Context, req gen.PostStaffPeopleByIdNationalIdRevealRequestObject) (gen.PostStaffPeopleByIdNationalIdRevealResponseObject, error) {
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var out gen.StaffNationalIdReveal
	err = s.withPersonLock(ctx, req.Id, allowErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		row, err := txq.PersonNationalID(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("staff: read person %d's national identity number: %w", req.Id, err)
		}
		if row.NationalIDKind == nil || len(row.NationalIDSealed) == 0 {
			return notFound()
		}
		value, err := s.deps.Secrets.Open(purposeNationalID, row.NationalIDSealed)
		if err != nil {
			return fmt.Errorf("staff: open person %d's national identity number: %w", req.Id, err)
		}
		out = gen.StaffNationalIdReveal{
			Kind: gen.StaffNationalIdKind(*row.NationalIDKind), Country: row.NationalIDCountry, Value: string(value),
		}
		return s.recordEvent(ctx, txq, &req.Id, eventNationalIDRevealed, who, map[string]any{}, nil)
	})
	if _, ok := asRefusal(err); ok || errors.Is(err, errPersonNotFound) {
		return gen.PostStaffPeopleByIdNationalIdReveal404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	cacheControl := noStore
	return gen.PostStaffPeopleByIdNationalIdReveal200JSONResponse{
		Body:    out,
		Headers: gen.PostStaffPeopleByIdNationalIdReveal200ResponseHeaders{CacheControl: &cacheControl},
	}, nil
}

// PutStaffPeopleByIdBankAccount Set a person's bank account
// (PUT /api/v1/staff/people/{id}/bank-account)
//
// The account is checked and sealed before the transaction and written under
// the person's lock; the person is mailed once it has committed, never under
// the lock (design S12).
func (s *server) PutStaffPeopleByIdBankAccount(ctx context.Context, req gen.PutStaffPeopleByIdBankAccountRequestObject) (gen.PutStaffPeopleByIdBankAccountResponseObject, error) {
	var body gen.StaffBankAccountRequest
	if req.Body != nil {
		body = *req.Body
	}
	value, err := ident.ValidateBankAccount(ident.AccountKind(body.Kind), body.Value)
	if err != nil {
		return gen.PutStaffPeopleByIdBankAccount400ApplicationProblemPlusJSONResponse(invalid(invalidBankAccountTitle, fieldErrors{
			errs: fieldError("value", identMessage(err, "Not a valid bank account number of this kind")),
			code: codeBankAccountInvalid,
		})), nil
	}
	sealed, err := s.sealBankAccount(ident.AccountKind(body.Kind), value)
	if err != nil {
		return nil, fmt.Errorf("staff: person %d: %w", req.Id, err)
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}

	var (
		saved   store.GetPersonRow
		link    *string
		changed bool
	)
	err = s.withPersonLock(ctx, req.Id, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		var err error
		if changed, err = s.setBankAccount(ctx, txq, req.Id, sealed, who); err != nil {
			return err
		}
		if saved, err = readPerson(ctx, txq, req.Id); err != nil {
			return err
		}
		link, err = linkedUserID(ctx, txq, req.Id)
		return err
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PutStaffPeopleByIdBankAccount404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PutStaffPeopleByIdBankAccount404Response{}, nil
		case http.StatusConflict:
			return gen.PutStaffPeopleByIdBankAccount409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PutStaffPeopleByIdBankAccount400ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	if changed {
		s.mailBankAccountChanged(ctx, saved)
	}
	out, err := s.respondPerson(ctx, caller, saved, link)
	if err != nil {
		return nil, err
	}
	return gen.PutStaffPeopleByIdBankAccount200JSONResponse(out), nil
}

// PostStaffPeopleByIdBankAccountReveal Reveal a person's bank account
// (POST /api/v1/staff/people/{id}/bank-account/reveal)
func (s *server) PostStaffPeopleByIdBankAccountReveal(ctx context.Context, req gen.PostStaffPeopleByIdBankAccountRevealRequestObject) (gen.PostStaffPeopleByIdBankAccountRevealResponseObject, error) {
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var out gen.StaffBankAccountReveal
	err = s.withPersonLock(ctx, req.Id, allowErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		row, err := txq.PersonBankAccount(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("staff: read person %d's bank account: %w", req.Id, err)
		}
		if row.BankAccountKind == nil || len(row.BankAccountSealed) == 0 {
			return notFound()
		}
		value, err := s.deps.Secrets.Open(purposeBankAccount, row.BankAccountSealed)
		if err != nil {
			return fmt.Errorf("staff: open person %d's bank account: %w", req.Id, err)
		}
		out = gen.StaffBankAccountReveal{Kind: gen.StaffBankAccountKind(*row.BankAccountKind), Value: string(value)}
		return s.recordEvent(ctx, txq, &req.Id, eventBankAccountRevealed, who, map[string]any{}, nil)
	})
	if _, ok := asRefusal(err); ok || errors.Is(err, errPersonNotFound) {
		return gen.PostStaffPeopleByIdBankAccountReveal404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	cacheControl := noStore
	return gen.PostStaffPeopleByIdBankAccountReveal200JSONResponse{
		Body:    out,
		Headers: gen.PostStaffPeopleByIdBankAccountReveal200ResponseHeaders{CacheControl: &cacheControl},
	}, nil
}

// mailBankAccountChanged tells the person their bank account was changed
// (design S4, S12; plan reading 11): at their private email when set, else
// their work email, in their preferred language, naming the last four digits
// and nothing more — so a change somebody else made is noticed by the one
// person whose pay it redirects. It runs after the change has committed and
// outside every transaction; the request's cancellation does not reach the
// send, which may already be accepted by the server. It never fails the
// change: with no address, no SMTP driver, or a send that fails, it says so
// in a log line that names the person's id and not their address.
func (s *server) mailBankAccountChanged(ctx context.Context, person store.GetPersonRow) {
	logger := s.deps.Logger
	if !s.mailAvailable() {
		logger.InfoContext(ctx, "staff: bank-account change mail skipped: the mail driver is not smtp", "person_id", person.ID)
		return
	}
	to := person.PrivateEmail
	if to == nil {
		to = person.WorkEmail
	}
	if to == nil {
		logger.InfoContext(ctx, "staff: bank-account change mail skipped: the person has no email", "person_id", person.ID)
		return
	}
	last4 := ""
	if person.BankAccountLast4 != nil {
		last4 = *person.BankAccountLast4
	}
	subject, body := bankChangeMail(person.PreferredLanguage, person.DisplayName, last4)
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bankChangeMailTimeout)
	defer cancel()
	if err := s.smtpSend(sendCtx, s.deps.Config.Mail, mail.Outbound{To: []string{*to}, Subject: subject, TextBody: body}); err != nil {
		// The error with the address taken out, however it is cased: a mail
		// server's refusal often quotes it, upper-cased as often as not, and
		// it is the person's.
		redact := regexp.MustCompile("(?i)" + regexp.QuoteMeta(*to))
		logger.WarnContext(ctx, "staff: the bank-account change mail could not be sent", "person_id", person.ID,
			"error", redact.ReplaceAllLiteralString(err.Error(), "<recipient>"))
	}
}

// bankChangeMail is the bank-change mail's subject and text in language —
// en or nb, Norwegian for anything else, as the column defaults — naming the
// person and the new account's last four digits.
func bankChangeMail(language, name, last4 string) (string, string) {
	if language == "en" {
		return "Your bank account in Vantigo was changed",
			fmt.Sprintf("Hi %s,\n\nThe bank account your pay is paid to was changed in Vantigo. It is now the account ending in %s.\n\nIf you did not ask for this change, tell HR at once.\n", name, last4)
	}
	return "Kontonummeret ditt i Vantigo er endret",
		fmt.Sprintf("Hei %s,\n\nKontonummeret lønnen din utbetales til, er endret i Vantigo. Det er nå kontoen som slutter på %s.\n\nHvis du ikke har bedt om denne endringen, si fra til HR med en gang.\n", name, last4)
}
