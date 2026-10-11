package staff

import (
	"context"
	"fmt"

	"github.com/vantigo-io/vantigo/server/internal/secrets"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/ident"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the register's writes as functions of a transaction (plan
// Task 3): a person created and changed, an employment added with its first
// terms row, a national identity number and a bank account set. The
// endpoints call them inside the transaction they open — POST /people in a
// locked one, every other under the person's lock — and so does the import
// (people_import.go), a row at a time, so a row that succeeds is the same
// statements, the same rules and the same events as the edits made by hand.
// None of them calls out of the module: whatever needs the directory or the
// mail is the caller's, before the transaction or after it.

// createPerson is POST /people's write inside txq's transaction: the typed
// employee number, or the settings' next one, the person and their
// person_created event. A number another person took is
// employee_number_taken, as the unique index answers it.
func (s *server) createPerson(ctx context.Context, txq *store.Queries, parsed parsedPerson, who actor) (store.GetPersonRow, error) {
	number := ""
	if parsed.EmployeeNumber != nil {
		number = *parsed.EmployeeNumber
	} else {
		generated, err := generateEmployeeNumber(ctx, txq)
		if err != nil {
			return store.GetPersonRow{}, err
		}
		number = generated
	}
	row := parsed.applyTo(store.GetPersonRow{EmployeeNumber: number})
	id, err := txq.InsertPerson(ctx, store.InsertPersonParams{
		EmployeeNumber: row.EmployeeNumber, GivenName: row.GivenName, FamilyName: row.FamilyName,
		DisplayName: row.DisplayName, BirthDate: row.BirthDate, Gender: row.Gender,
		WorkEmail: row.WorkEmail, PhoneWork: row.PhoneWork, PhoneMobile: row.PhoneMobile,
		PhonePrivate: row.PhonePrivate, PrivateEmail: row.PrivateEmail, Street: row.Street,
		PostalCode: row.PostalCode, City: row.City, Country: row.Country, WorkCountry: row.WorkCountry,
		PreferredLanguage: row.PreferredLanguage, Notes: row.Notes, Now: s.deps.Clock(),
	})
	if err != nil {
		return store.GetPersonRow{}, personWriteError(err, invalidPersonTitle)
	}
	created, err := readPerson(ctx, txq, id)
	if err != nil {
		return store.GetPersonRow{}, err
	}
	return created, s.recordChanges(ctx, txq, id, eventPersonCreated, who, diffPerson(nil, created))
}

// updatePerson is PUT /people/{id}'s write on current, the person's row as
// read under their lock: parsed applied to it, and nothing written — no
// revision, no event — when that changes nothing. A birth date that changes
// is judged against the national identity number on file. It answers the row
// as saved.
func (s *server) updatePerson(ctx context.Context, txq *store.Queries, current store.GetPersonRow, parsed parsedPerson, who actor) (store.GetPersonRow, error) {
	next := parsed.applyTo(current)
	diff := diffPerson(&current, next)
	if len(diff) == 0 {
		return current, nil
	}
	if _, changes := diff["birthDate"]; changes {
		if err := s.checkBirthDateAgainstNationalID(ctx, txq, current.ID, next.BirthDate); err != nil {
			return store.GetPersonRow{}, err
		}
	}
	if err := txq.UpdatePerson(ctx, store.UpdatePersonParams{
		ID: current.ID, GivenName: next.GivenName, FamilyName: next.FamilyName, DisplayName: next.DisplayName,
		BirthDate: next.BirthDate, Gender: next.Gender, WorkEmail: next.WorkEmail, PhoneWork: next.PhoneWork,
		PhoneMobile: next.PhoneMobile, PhonePrivate: next.PhonePrivate, PrivateEmail: next.PrivateEmail,
		Street: next.Street, PostalCode: next.PostalCode, City: next.City, Country: next.Country,
		WorkCountry: next.WorkCountry, PreferredLanguage: next.PreferredLanguage, Notes: next.Notes,
		Now: s.deps.Clock(),
	}); err != nil {
		return store.GetPersonRow{}, personWriteError(err, invalidPersonTitle)
	}
	saved, err := readPerson(ctx, txq, current.ID)
	if err != nil {
		return store.GetPersonRow{}, err
	}
	return saved, s.recordChanges(ctx, txq, current.ID, eventPersonUpdated, who, diff)
}

// newEmployment is one validated POST /people/{id}/employments body: the
// request (its business unit and contract-signed date are read from it), the
// typed employment number or nil for a generated one, the relationship type
// and the first terms row, dated the start.
type newEmployment struct {
	body         gen.StaffEmploymentRequest
	number       *string
	relationship string
	first        store.StaffEmploymentTerm
}

// parseNewEmployment runs every rule of an employment's body that needs no
// database, every failure collected, the first row's fields named
// "terms.<field>". Whether terms.validFrom is the start is the caller's
// (a 409 of its own).
func parseNewEmployment(body gen.StaffEmploymentRequest) (newEmployment, fieldErrors) {
	var f fieldErrors
	out := newEmployment{body: body, relationship: "ordinary"}
	start := body.StartDate.Time
	if start.IsZero() {
		f.add("startDate", "An employment's start date is required")
	}
	var msg string
	out.number, msg = parseEmploymentNumber(body.EmploymentNumber)
	f.add("employmentNumber", msg)
	if v, msg := parseEnum("A relationship type", body.RelationshipType, relationshipTypes); msg != "" {
		f.add("relationshipType", msg)
	} else if v != nil {
		out.relationship = *v
	}
	if body.Terms.Correction != nil {
		f.add("terms.correction", "correction is for changing a terms row, not for a new one")
	}
	first, termsErrors := parseTerms(body.Terms, termsSource{given: func(string) bool { return true }}, start, "terms.")
	for field, messages := range termsErrors.errs {
		for _, m := range messages {
			f.add(field, m)
		}
	}
	if f.code == "" {
		f.code = termsErrors.code
	}
	first.ValidFrom = dayParam(start)
	out.first = first
	return out, f
}

// addEmployment is POST /people/{id}/employments' write under the person's
// lock, which the caller holds: the employment in its business unit — the
// default when it names none — with its number typed or generated, and its
// first terms row; a first row that names a manager or a department takes
// the manager graph's lock and is judged on it from the start on. It records
// employment_created and terms_added.
func (s *server) addEmployment(ctx context.Context, txq *store.Queries, personID int64, e newEmployment, who actor) (store.StaffEmployment, error) {
	start := e.body.StartDate.Time
	first := e.first
	edges := first.ManagerPersonID != nil || first.DepartmentID != nil
	if edges {
		if err := lockManagerGraph(ctx, txq); err != nil {
			return store.StaffEmployment{}, err
		}
	}
	unit, err := resolveUnit(ctx, txq, e.body.BusinessUnitId, 0)
	if err != nil {
		return store.StaffEmployment{}, err
	}
	if err := checkTermsReferences(ctx, txq, personID, first, nil, "terms."); err != nil {
		return store.StaffEmployment{}, err
	}
	employmentNumber := ""
	if e.number != nil {
		taken, err := txq.EmploymentNumberTaken(ctx, *e.number)
		if err != nil {
			return store.StaffEmployment{}, fmt.Errorf("staff: is employment number %s taken: %w", *e.number, err)
		}
		if taken {
			return store.StaffEmployment{}, employmentNumberTaken()
		}
		employmentNumber = *e.number
	} else if employmentNumber, err = generateEmploymentNumber(ctx, txq, personID); err != nil {
		return store.StaffEmployment{}, err
	}
	created, err := txq.InsertEmployment(ctx, store.InsertEmploymentParams{
		PersonID: personID, EmploymentNumber: employmentNumber, RelationshipType: e.relationship, BusinessUnitID: unit,
		StartDate: dayParam(start), ContractSignedAt: pgDate(civilDate(e.body.ContractSignedAt)), Now: s.deps.Clock(),
	})
	if err != nil {
		return store.StaffEmployment{}, employmentWriteError(fmt.Errorf("staff: add an employment to person %d: %w", personID, err))
	}
	first.EmploymentID = created.ID
	row, err := insertTerms(ctx, txq, first, s.deps.Clock())
	if err != nil {
		return store.StaffEmployment{}, err
	}
	if edges {
		if err := checkManagerCycle(ctx, txq, personID, start); err != nil {
			return store.StaffEmployment{}, err
		}
	}
	values, err := employmentValues(&created)
	if err != nil {
		return store.StaffEmployment{}, err
	}
	if err := s.recordRowChange(ctx, txq, personID, eventEmploymentCreated, who, created.ID, 0, diffFields(nil, values)); err != nil {
		return store.StaffEmployment{}, err
	}
	return created, s.recordTermsChange(ctx, txq, personID, eventTermsAdded, who, nil, &row)
}

// sealedNationalID is a checked national identity number ready to be
// written: what may be kept in clear (its kind and a foreign id's country),
// the birth date it encodes, its sealed value and its MAC.
type sealedNationalID struct {
	n           ident.Normalised
	kind        string
	country     *string
	sealed, mac []byte
}

// sealNationalID seals and MACs a checked number — pure computation, no call
// out — so it is done before the transaction the number is written in.
// Neither error names the value.
func (s *server) sealNationalID(n ident.Normalised) (sealedNationalID, error) {
	sealed, err := s.deps.Secrets.Seal(purposeNationalID, []byte(n.Value()))
	if err != nil {
		return sealedNationalID{}, fmt.Errorf("seal the national identity number: %w", err)
	}
	mac, err := s.deps.Secrets.MAC(purposeNationalIDIndex, ident.IndexInput(n))
	if err != nil {
		return sealedNationalID{}, fmt.Errorf("index the national identity number: %w", err)
	}
	out := sealedNationalID{n: n, kind: string(n.Kind()), sealed: sealed, mac: mac}
	if c := n.Country(); c != "" {
		out.country = &c
	}
	return out, nil
}

// setNationalID is PUT /people/{id}/national-id's write under the person's
// lock: the birth date the number encodes judged against the person's, the
// number on file however typed left as it is (no write, no event), and
// otherwise the number written and national_id_set recorded with its kind
// alone. Another person with the number is national_id_duplicate.
func (s *server) setNationalID(ctx context.Context, txq *store.Queries, personID int64, v sealedNationalID, who actor) error {
	current, err := readPerson(ctx, txq, personID)
	if err != nil {
		return err
	}
	if birth, ok := v.n.BirthDate(); ok && current.BirthDate.Valid && !sameDay(current.BirthDate.Time, birth) {
		return refuseInvalid(invalidNationalIDTitle, fieldErrors{
			errs: fieldError("value", "The number encodes another birth date than the person's; correct whichever is wrong"),
			code: codeNationalIDBirthDateMismatch,
		})
	}
	before, err := txq.PersonNationalID(ctx, personID)
	if err != nil {
		return fmt.Errorf("staff: read person %d's national identity number: %w", personID, err)
	}
	// The MAC covers the kind, the country and the value, so an equal one is
	// the number on file, however it was typed: no change, no event.
	if len(before.NationalIDMac) > 0 && secrets.MACEqual(before.NationalIDMac, v.mac) {
		return nil
	}
	if err := txq.SetPersonNationalID(ctx, store.SetPersonNationalIDParams{
		Kind: &v.kind, Country: v.country, Sealed: v.sealed, Mac: v.mac, Now: s.deps.Clock(), ID: personID,
	}); err != nil {
		return nationalIDWriteError(fmt.Errorf("staff: write person %d's national identity number: %w", personID, err))
	}
	return s.recordChanges(ctx, txq, personID, eventNationalIDSet, who, changes{"nationalId": fieldChange{
		From: nationalIDSummary(before.NationalIDKind, before.NationalIDCountry),
		To:   nationalIDSummary(&v.kind, v.country),
	}})
}

// sealedBankAccount is a checked bank account ready to be written: its kind,
// its last four digits, the normalised value (to compare with the one on
// file) and its sealed form.
type sealedBankAccount struct {
	kind, last4, value string
	sealed             []byte
}

// sealBankAccount seals a checked, normalised account — before the
// transaction, as sealNationalID. The error never names the value.
func (s *server) sealBankAccount(kind ident.AccountKind, value string) (sealedBankAccount, error) {
	sealed, err := s.deps.Secrets.Seal(purposeBankAccount, []byte(value))
	if err != nil {
		return sealedBankAccount{}, fmt.Errorf("seal the bank account: %w", err)
	}
	return sealedBankAccount{kind: string(kind), last4: ident.Last4(value), value: value, sealed: sealed}, nil
}

// setBankAccount is PUT /people/{id}/bank-account's write under the person's
// lock: the account on file opened to compare — local work, no call out — so
// the same account again is no change (false: no write, no event and no
// mail), and otherwise the account written and bank_account_set recorded
// with its kind and last four digits. true is the caller's cue to mail the
// person once the transaction has committed (mailBankAccountChanged).
func (s *server) setBankAccount(ctx context.Context, txq *store.Queries, personID int64, v sealedBankAccount, who actor) (bool, error) {
	current, err := readPerson(ctx, txq, personID)
	if err != nil {
		return false, err
	}
	stored, err := txq.PersonBankAccount(ctx, personID)
	if err != nil {
		return false, fmt.Errorf("staff: read person %d's bank account: %w", personID, err)
	}
	if stored.BankAccountKind != nil && *stored.BankAccountKind == v.kind && len(stored.BankAccountSealed) > 0 {
		opened, err := s.deps.Secrets.Open(purposeBankAccount, stored.BankAccountSealed)
		if err != nil {
			return false, fmt.Errorf("staff: open person %d's bank account: %w", personID, err)
		}
		if string(opened) == v.value {
			return false, nil
		}
	}
	if err := txq.SetPersonBankAccount(ctx, store.SetPersonBankAccountParams{
		Kind: &v.kind, Last4: &v.last4, Sealed: v.sealed, Now: s.deps.Clock(), ID: personID,
	}); err != nil {
		return false, fmt.Errorf("staff: write person %d's bank account: %w", personID, err)
	}
	return true, s.recordChanges(ctx, txq, personID, eventBankAccountSet, who, changes{"bankAccount": fieldChange{
		From: bankAccountSummary(current.BankAccountKind, current.BankAccountLast4),
		To:   bankAccountSummary(&v.kind, &v.last4),
	}})
}
