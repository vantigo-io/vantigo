package staff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the person's data in one file (design S16; plan readings 14
// and 15): GET /people/{id}/personal-data, staff:personal-data's complete
// answer to an innsyn request, and GET /me/export, the person's own copy. Both
// read everything in one REPEATABLE READ snapshot, so the parts of the file
// agree with each other, build the file in memory — one person's data is one
// response, never refused for size — and are served as an attachment nobody
// caches (customers' personal-data file, internal/customers/personal_data.go).
//
// The complete file is every field group with the sealed values opened, HR's
// notes, the hold fields, and every event whole. It is an audited reveal: once
// the file has been read, a personal_data_exported event naming the viewer is
// recorded in its own transaction, and a file whose export could not be
// recorded is not answered. The person's own copy is the same shape without
// the sealed values (reading 31), HR's notes and the terms' notes (reading
// 35) and the hold fields, its events shaped as the person reads their
// history; it records nothing, being their own.
//
// The directory is asked after the snapshot, never inside it: the linked
// user, then — for the complete file — the viewer the event is attributed to.

// personalDataSnapshot is what one export reads in its snapshot.
type personalDataSnapshot struct {
	person      store.GetPersonRow
	placement   placement
	nationalID  store.PersonNationalIDRow
	bankAccount store.PersonBankAccountRow
	nextOfKin   []store.ListNextOfKinRow
	roles       []store.ListPersonRolesRow
	employments []store.StaffEmployment
	chains      map[int64][]store.StaffEmploymentTerm
	link        *store.PersonLinkRow
	// account is the account flag (delivery C), carried with the link's
	// state: both files' readers read the links group.
	account   accountPerson
	documents []store.ListPersonDocumentsRow
	events    []store.ListAllPersonEventsRow
	// Delivery B (Task 6): every absence, the adjustments, the cases with
	// their milestones, this year's care days and the standing of
	// egenmelding today — the withdrawal with it, which both files' readers
	// (the person, staff:personal-data) read as care-day readers.
	absences    []store.PersonAbsencesRow
	adjustments []store.StaffHolidayAdjustment
	cases       []store.StaffSickCase
	milestones  []store.StaffSickCaseMilestone
	careDays    gen.StaffCareDays
	selfCert    gen.StaffSelfCertification
	// competences is every competence row the person has had, the
	// superseded ones included, newest first (staff competence design S17),
	// with each competence and evidence document; verifiers is the names of
	// the users who verified them, read from the directory after the
	// snapshot (withVerifiers).
	competences []store.GetPersonCompetenceRow
	verifiers   map[uuid.UUID]string
	// Delivery B: what the person wants — their interests, preferences and
	// availability declaration — and their competence reviews, newest first,
	// each reviewer named in verifiers with the verifiers.
	interests    []store.ListPersonInterestsRow
	preferences  []store.StaffPersonPreference
	availability []store.StaffAvailabilityDeclaration
	reviews      []store.StaffCompetenceReview
	// Delivery C: the profiles that reach the person today — assigned, or
	// their department's default (effectiveProfiles).
	profiles []effectiveProfile
}

// readPersonalData reads one person's file on q, which is a snapshot's: the
// record and where they stand today, the sealed columns as stored, the next
// of kin, the roles, the employments with their chains, the link, every
// document's row — health ones included — and the whole history. An unknown
// person is the 404 refusal.
func readPersonalData(ctx context.Context, q *store.Queries, id int64, today time.Time) (personalDataSnapshot, error) {
	var (
		out personalDataSnapshot
		err error
	)
	if out.person, err = readPerson(ctx, q, id); err != nil {
		return out, err
	}
	placements, err := placementsOf(ctx, q, []int64{id}, today)
	if err != nil {
		return out, err
	}
	out.placement = placements[id]
	if out.nationalID, err = q.PersonNationalID(ctx, id); err != nil {
		return out, fmt.Errorf("staff: read person %d's national identity number: %w", id, err)
	}
	if out.bankAccount, err = q.PersonBankAccount(ctx, id); err != nil {
		return out, fmt.Errorf("staff: read person %d's bank account: %w", id, err)
	}
	if out.nextOfKin, err = q.ListNextOfKin(ctx, id); err != nil {
		return out, fmt.Errorf("staff: list person %d's next of kin: %w", id, err)
	}
	if out.roles, err = q.ListPersonRoles(ctx, id); err != nil {
		return out, fmt.Errorf("staff: list person %d's roles: %w", id, err)
	}
	if out.employments, err = q.ListPersonEmployments(ctx, id); err != nil {
		return out, fmt.Errorf("staff: list person %d's employments: %w", id, err)
	}
	ids := make([]int64, 0, len(out.employments))
	for _, e := range out.employments {
		ids = append(ids, e.ID)
	}
	terms, err := q.ListTermsOfEmployments(ctx, ids)
	if err != nil {
		return out, fmt.Errorf("staff: list person %d's terms: %w", id, err)
	}
	out.chains = map[int64][]store.StaffEmploymentTerm{}
	for _, t := range terms {
		out.chains[t.EmploymentID] = append(out.chains[t.EmploymentID], t)
	}
	if out.link, err = personLink(ctx, q, id); err != nil {
		return out, err
	}
	if out.account, err = readAccountPerson(ctx, q, id); err != nil {
		return out, err
	}
	if out.documents, err = q.ListPersonDocuments(ctx, store.ListPersonDocumentsParams{PersonID: id, WithRestricted: true}); err != nil {
		return out, fmt.Errorf("staff: list person %d's documents: %w", id, err)
	}
	if out.events, err = q.ListAllPersonEvents(ctx, &id); err != nil {
		return out, fmt.Errorf("staff: list person %d's history: %w", id, err)
	}
	if out.absences, err = q.PersonAbsences(ctx, id); err != nil {
		return out, fmt.Errorf("staff: list person %d's absences: %w", id, err)
	}
	if out.adjustments, err = q.ListHolidayAdjustments(ctx, id); err != nil {
		return out, fmt.Errorf("staff: list person %d's holiday adjustments: %w", id, err)
	}
	if out.cases, err = q.PersonCases(ctx, id); err != nil {
		return out, fmt.Errorf("staff: list person %d's cases: %w", id, err)
	}
	ids = ids[:0]
	for _, c := range out.cases {
		ids = append(ids, c.ID)
	}
	if out.milestones, err = q.CaseMilestones(ctx, ids); err != nil {
		return out, fmt.Errorf("staff: list person %d's milestones: %w", id, err)
	}
	row, err := settings(ctx, q)
	if err != nil {
		return out, err
	}
	if out.selfCert, err = selfCertResponse(ctx, q, row, id, today, everything); err != nil {
		return out, err
	}
	if out.careDays, err = careDaysResponse(ctx, q, id, today.Year(), today); err != nil {
		return out, err
	}
	competences, err := q.ListPersonCompetenceHistory(ctx, id)
	if err != nil {
		return out, fmt.Errorf("staff: list person %d's competences: %w", id, err)
	}
	out.competences = mapRows(competences, func(r store.ListPersonCompetenceHistoryRow) store.GetPersonCompetenceRow {
		return store.GetPersonCompetenceRow(r)
	})
	if out.interests, err = q.ListPersonInterests(ctx, id); err != nil {
		return out, fmt.Errorf("staff: list person %d's interests: %w", id, err)
	}
	if out.preferences, err = q.ListPersonPreferences(ctx, id); err != nil {
		return out, fmt.Errorf("staff: list person %d's preferences: %w", id, err)
	}
	if out.availability, err = q.ListAvailabilityDeclarations(ctx, id); err != nil {
		return out, fmt.Errorf("staff: list person %d's availability declaration: %w", id, err)
	}
	if out.reviews, err = q.ListCompetenceReviews(ctx, id); err != nil {
		return out, fmt.Errorf("staff: list person %d's competence reviews: %w", id, err)
	}
	if out.profiles, err = effectiveProfiles(ctx, q, id, today); err != nil {
		return out, err
	}
	return out, nil
}

// withVerifiers is f with the names of its competences' verifiers and its
// reviews' reviewers, as a reads them, read from the user directory after
// the snapshot in one call, each user once.
func (s *server) withVerifiers(ctx context.Context, f personalDataSnapshot, a audience) (personalDataSnapshot, error) {
	var ids []uuid.UUID
	seen := map[uuid.UUID]bool{}
	add := func(id uuid.UUID) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if a.reads(groupCompetenceDetail) {
		for _, r := range f.competences {
			if r.StaffPersonCompetence.VerifiedByUserID != nil {
				add(*r.StaffPersonCompetence.VerifiedByUserID)
			}
		}
	}
	if a.reads(groupReviews) {
		for _, r := range f.reviews {
			add(r.ReviewerUserID)
		}
	}
	users, err := s.usersUsers(ctx, ids)
	if err != nil {
		return f, fmt.Errorf("staff: read the verifiers' and reviewers' names: %w", err)
	}
	f.verifiers = make(map[uuid.UUID]string, len(users))
	for _, u := range users {
		f.verifiers[u.ID] = u.DisplayName
	}
	return f, nil
}

// openedSealed is the sealed values of the complete file, opened.
type openedSealed struct {
	nationalID, bankAccount *string
}

// openSealed opens what the snapshot read sealed — local work, no call out.
func (s *server) openSealed(f personalDataSnapshot) (openedSealed, error) {
	var out openedSealed
	if len(f.nationalID.NationalIDSealed) > 0 {
		value, err := s.deps.Secrets.Open(purposeNationalID, f.nationalID.NationalIDSealed)
		if err != nil {
			return out, fmt.Errorf("staff: open person %d's national identity number: %w", f.person.ID, err)
		}
		v := string(value)
		out.nationalID = &v
	}
	if len(f.bankAccount.BankAccountSealed) > 0 {
		value, err := s.deps.Secrets.Open(purposeBankAccount, f.bankAccount.BankAccountSealed)
		if err != nil {
			return out, fmt.Errorf("staff: open person %d's bank account: %w", f.person.ID, err)
		}
		v := string(value)
		out.bankAccount = &v
	}
	return out, nil
}

// personalDataPerson is the record as the file carries it, read as a reads
// it; opened, when not nil, puts the sealed values — and a foreign id's
// country — beside their kinds.
func personalDataPerson(f personalDataSnapshot, a audience, opened *openedSealed) gen.StaffPersonalDataPerson {
	row := f.person
	out := gen.StaffPersonalDataPerson{
		Id:                row.ID,
		EmployeeNumber:    row.EmployeeNumber,
		GivenName:         row.GivenName,
		FamilyName:        row.FamilyName,
		DisplayName:       row.DisplayName,
		WorkCountry:       row.WorkCountry,
		PreferredLanguage: gen.StaffLanguage(row.PreferredLanguage),
		Status:            f.placement.status,
		WorkEmail:         shown(a, "workEmail", row.WorkEmail),
		PhoneWork:         shown(a, "phoneWork", row.PhoneWork),
		BirthDate:         shown(a, "birthDate", dateOf(row.BirthDate)),
		Gender:            shown(a, "gender", enumOf[gen.StaffGender](row.Gender)),
		PhoneMobile:       shown(a, "phoneMobile", row.PhoneMobile),
		PhonePrivate:      shown(a, "phonePrivate", row.PhonePrivate),
		PrivateEmail:      shown(a, "privateEmail", row.PrivateEmail),
		Street:            shown(a, "street", row.Street),
		PostalCode:        shown(a, "postalCode", row.PostalCode),
		City:              shown(a, "city", row.City),
		Country:           shown(a, "country", row.Country),
		Notes:             shown(a, "notes", row.Notes),
		ErasedAt:          shown(a, "erasedAt", row.ErasedAt),
		// The care-day fields (Task 6): the person's own data, read by both
		// files' audiences — the person and staff:personal-data.
		ChildrenUnder12:        shown(a, "childrenUnder12", int32Of(row.ChildrenUnder12)),
		SoleCarer:              shown(a, "soleCarer", row.SoleCarer),
		ChronicallyIllChildren: shown(a, "chronicallyIllChildren", int32Of(row.ChronicallyIllChildren)),
		ReassessedAt:           shown(a, "reassessedAt", row.ReassessedAt),
		LegalHold:              shown(a, "legalHold", legalHoldOf(row)),
		NationalId: gen.StaffPersonalDataNationalId{
			OnFile: row.NationalIDOnFile,
			Kind:   enumOf[gen.StaffNationalIdKind](row.NationalIDKind),
		},
	}
	if row.BankAccountKind != nil && row.BankAccountLast4 != nil {
		out.BankAccount = &gen.StaffPersonalDataBankAccount{Kind: gen.StaffBankAccountKind(*row.BankAccountKind), Last4: *row.BankAccountLast4}
	}
	if opened != nil {
		out.NationalId.Country = f.nationalID.NationalIDCountry
		out.NationalId.Value = opened.nationalID
		if out.BankAccount != nil {
			out.BankAccount.Value = opened.bankAccount
		}
	}
	return out
}

// personalDataResponse is the file: the snapshot as a reads it, the link's
// user as the directory named them after the snapshot, and each event as
// event makes it.
func personalDataResponse(f personalDataSnapshot, a audience, opened *openedSealed, user *gen.StaffPersonUser,
	today, exportedAt time.Time, event func(store.ListAllPersonEventsRow) gen.StaffEvent, tag absenceTagger,
) gen.StaffPersonalData {
	return gen.StaffPersonalData{
		Absences: mapRows(f.absences, func(r store.PersonAbsencesRow) gen.StaffAbsence {
			return absenceResponse(r.StaffAbsence, r.Kind, r.Visibility, r.Unit, a, tag)
		}),
		HolidayAdjustments: mapRows(f.adjustments, adjustmentResponse),
		Cases: mapRows(f.cases, func(c store.StaffSickCase) gen.StaffCase {
			// Both exports' readers — the person, staff:personal-data — read
			// the health group, so a case's revision, never an etag.
			return caseResponse(c, f.milestones, a, today, func(int64, int32) string { return "" })
		}),
		CareDays:          f.careDays,
		SelfCertification: f.selfCert,
		ExportedAt:        exportedAt,
		Person:            personalDataPerson(f, a, opened),
		Photo:             personalDataPhoto(f.person, a),
		NextOfKin:         mapRows(f.nextOfKin, nextOfKinResponse),
		Roles:             mapRows(f.roles, personRoleResponse),
		Employments: mapRows(f.employments, func(e store.StaffEmployment) gen.StaffPersonalDataEmployment {
			chain := f.chains[e.ID]
			return gen.StaffPersonalDataEmployment{Employment: employmentResponse(e, chain, today, a), Terms: termsListResponse(chain, a)}
		}),
		Link:    linkResponse(f.link, user),
		Account: personalDataAccount(f),
		Documents: mapRows(f.documents, func(r store.ListPersonDocumentsRow) gen.StaffDocument {
			return documentResponse(r.StaffDocument, r.StaffDocumentCategory)
		}),
		Events: mapRows(f.events, event),
		Competences: mapRows(f.competences, func(r store.GetPersonCompetenceRow) gen.StaffPersonCompetence {
			return personCompetenceResponse(r, a, today, f.verifiers)
		}),
		// Delivery B's four sections are required: an empty list to an
		// audience outside their group, never absent.
		Interests:    readList(a.reads(groupWishes), f.interests, interestResponse),
		Preferences:  readList(a.reads(groupWishes), f.preferences, preferenceResponse),
		Availability: readList(a.reads(groupWishes), f.availability, declarationResponse),
		CompetenceReviews: readList(a.reads(groupReviews), f.reviews, func(r store.StaffCompetenceReview) gen.StaffCompetenceReview {
			return competenceReviewResponse(r, f.verifiers)
		}),
		// Delivery C's section is required too: an empty list to an
		// audience outside the Profiles group (reading 33).
		ProfileAssignments: readList(a.reads(groupProfiles), f.profiles, personProfileResponse),
	}
}

// personalDataPhoto is the person's photo as the file carries it (person-glance
// design §4) — its type and when it was set, the bytes being GET
// /people/{id}/photo's — or nil when there is none or a does not read the
// Work group.
func personalDataPhoto(row store.GetPersonRow, a audience) *gen.StaffPersonalDataPhoto {
	if !a.reads(groupWork) || row.PhotoContentType == nil || row.PhotoUpdatedAt == nil {
		return nil
	}
	return &gen.StaffPersonalDataPhoto{ContentType: *row.PhotoContentType, UpdatedAt: *row.PhotoUpdatedAt}
}

// readList is rows through f when the audience reads them, else the empty
// list.
func readList[R, T any](reads bool, rows []R, f func(R) T) []T {
	if !reads {
		return []T{}
	}
	return mapRows(rows, f)
}

// personalDataAccount is the account flag as the file carries it, with the
// link's state when the person has a link — a preparing one, naming no user,
// included.
func personalDataAccount(f personalDataSnapshot) gen.StaffPersonalDataAccount {
	out := gen.StaffPersonalDataAccount{
		ProvisionAccount: f.account.provisionAccount,
		ProvisionFrom:    dateOf(f.account.provisionFrom),
		AccountEmailKind: gen.StaffAccountEmailKind(accountEmailKindOf(f.account)),
	}
	if f.link != nil {
		state := f.link.State
		out.LinkState = &state
	}
	return out
}

// wholeEvent is an event as the complete file carries it: its payload as
// stored, every field and value in it.
func wholeEvent(row store.ListAllPersonEventsRow) gen.StaffEvent {
	out := eventResponse(store.ListPersonEventsRow(row), everything, nil)
	payload := map[string]any{}
	if err := json.Unmarshal(row.Payload, &payload); err == nil {
		out.Payload = payload
	}
	return out
}

// selfEvent is an event as the person reads their own history.
func selfEvent(row store.ListAllPersonEventsRow) gen.StaffEvent {
	return eventResponse(store.ListPersonEventsRow(row), selfAudience, nil)
}

// personalDataDownload writes the file: the generated 200 would be a bare
// application/json that names no file, and this answer has to say what to
// call it and that nobody may cache it — customers' personalDataDownload. The
// body is marshalled before a header is written, so a failure is a 500
// rather than half a file.
type personalDataDownload struct {
	body     gen.StaffPersonalData
	fileName string
}

func (d personalDataDownload) write(w http.ResponseWriter) error {
	body, err := json.Marshal(d.body)
	if err != nil {
		return fmt.Errorf("staff: encode the personal data file: %w", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", d.fileName))
	// A person's whole file: in nobody's cache, least of all a shared one.
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(body)
	return err
}

func (d personalDataDownload) VisitGetStaffPeopleByIdPersonalDataResponse(w http.ResponseWriter) error {
	return d.write(w)
}

func (d personalDataDownload) VisitGetStaffMeExportResponse(w http.ResponseWriter) error {
	return d.write(w)
}

// personalDataFileName is the file's name: the employee number, which is
// letters, digits and hyphens only, so it needs no escaping.
func personalDataFileName(employeeNumber string) string {
	return "staff-" + employeeNumber + "-personal-data.json"
}

// GetStaffPeopleByIdPersonalData Export a person's data
// (GET /api/v1/staff/people/{id}/personal-data)
//
// Order: (1) the file in one snapshot; (2) the sealed values opened, local
// work; (3) the linked user and the viewer from the directory, after the
// snapshot; (4) the export recorded under the person's lock in a transaction
// of its own — an erased person's file is a read, which the read-only rule
// does not reach; (5) the file answered.
func (s *server) GetStaffPeopleByIdPersonalData(ctx context.Context, req gen.GetStaffPeopleByIdPersonalDataRequestObject) (gen.GetStaffPeopleByIdPersonalDataResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	exportedAt := s.deps.Clock()
	var f personalDataSnapshot
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		f, err = readPersonalData(ctx, q, req.Id, caller.Today)
		return err
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdPersonalData404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	opened, err := s.openSealed(f)
	if err != nil {
		return nil, err
	}
	if f, err = s.withVerifiers(ctx, f, everything); err != nil {
		return nil, err
	}
	var user *gen.StaffPersonUser
	if f.link != nil {
		if user, err = s.linkedUser(ctx, f.link.ExternalID); err != nil {
			return nil, err
		}
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withPersonLock(ctx, req.Id, allowErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		return s.recordEvent(ctx, txq, &req.Id, eventPersonalDataExported, who, map[string]any{}, nil)
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.GetStaffPeopleByIdPersonalData404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: record the export of person %d's data: %w", req.Id, err)
	}
	return personalDataDownload{
		body:     personalDataResponse(f, everything, &opened, user, caller.Today, exportedAt, wholeEvent, s.absenceTag),
		fileName: personalDataFileName(f.person.EmployeeNumber),
	}, nil
}

// GetStaffMeExport Export my own data
// (GET /api/v1/staff/me/export)
func (s *server) GetStaffMeExport(ctx context.Context, _ gen.GetStaffMeExportRequestObject) (gen.GetStaffMeExportResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	if caller.Person == nil {
		return gen.GetStaffMeExport404ApplicationProblemPlusJSONResponse(noPerson()), nil
	}
	exportedAt := s.deps.Clock()
	var f personalDataSnapshot
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		f, err = readPersonalData(ctx, q, *caller.Person, caller.Today)
		return err
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffMeExport404ApplicationProblemPlusJSONResponse(noPerson()), nil
	}
	if err != nil {
		return nil, err
	}
	if f, err = s.withVerifiers(ctx, f, selfAudience); err != nil {
		return nil, err
	}
	var user *gen.StaffPersonUser
	if f.link != nil {
		if user, err = s.linkedUser(ctx, f.link.ExternalID); err != nil {
			return nil, err
		}
	}
	return personalDataDownload{
		body:     personalDataResponse(f, selfAudience, nil, user, caller.Today, exportedAt, selfEvent, s.absenceTag),
		fileName: personalDataFileName(f.person.EmployeeNumber),
	}, nil
}
