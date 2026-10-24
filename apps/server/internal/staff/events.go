package staff

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the history (design S21, §3.6): every mutation of a person is
// one event, written in the same transaction as the change and attributed to
// an actor snapshotted from the user directory before the transaction opened.
// A payload names the fields a change touched with their values before and
// after; who reads which value is shaping.go's per-field table, applied when
// the history is read. payload_classes, the retention classes the payload
// touches, is for the erase alone (delivery A2).

// The event kinds delivery A1 writes. A sealed field's change names the
// field with its kind (and the bank account's last four digits) before and
// after, never a digit more; a reveal carries no field at all — its actor is
// the viewer, which is what it records.
const (
	eventPersonCreated       = "person_created"
	eventPersonUpdated       = "person_updated"
	eventNextOfKinUpdated    = "next_of_kin_updated"
	eventRolesUpdated        = "roles_updated"
	eventPersonDeleted       = "person_deleted"
	eventNationalIDSet       = "national_id_set"
	eventNationalIDRevealed  = "national_id_revealed"
	eventBankAccountSet      = "bank_account_set"
	eventBankAccountRevealed = "bank_account_revealed"
	eventUserLinked          = "user_linked"
	eventUserUnlinked        = "user_unlinked"

	eventEmploymentCreated  = "employment_created"
	eventEmploymentUpdated  = "employment_updated"
	eventEmploymentEnded    = "employment_ended"
	eventEmploymentReopened = "employment_reopened"
	eventTermsAdded         = "terms_added"
	eventTermsCorrected     = "terms_corrected"
	eventTermsDeleted       = "terms_deleted"
)

// The event kinds delivery A2's document archive writes. Each names the
// document (documentId) and carries its category's key, file name and dates
// as fields; its classes are the category's, which also decide its group
// (shapePayload): the documents group, or the health group for class E.
const (
	eventDocumentAdded   = "document_added"
	eventDocumentUpdated = "document_updated"
	eventDocumentRemoved = "document_removed"
)

// The event kinds of the exports, the legal hold and the erased state
// (delivery A2, Task 2). personal_data_exported is an audited reveal: its
// actor is the viewer and its payload empty, as a sealed field's reveal is.
// The hold's, the reassessment's and the rehire's name their fields —
// legalHold, legalHoldReason, reassessedAt, erasedAt — which the hold group
// reads.
const (
	eventPersonalDataExported = "personal_data_exported"
	eventLegalHoldSet         = "legal_hold_set"
	eventLegalHoldLifted      = "legal_hold_lifted"
	eventReassessed           = "reassessed"
	eventPersonRehired        = "person_rehired"
)

// The register's file's events (plan reading 11): installation events, no
// person's — S21 asks for every export to be an event, and the import is
// recorded the same way, its rows' own events naming each person it wrote.
// Each names its actor and counts its rows, nothing more.
const (
	eventPeopleImported = "people_imported"
	eventPeopleExported = "people_exported"
)

// eventPayrollExported is a month's payroll file (design S18, plan reading
// 13): an installation event naming its actor, the month, the format, the
// rows and the SHA-256 of the file answered, so the very file payroll
// received can be recognised later. It records no payload class: its payload
// names no person and holds no personal data, only the export's lineage —
// pay documentation (S15).
const (
	eventPayrollExported = "payroll_exported"
)

// eventErased is the retention run's record of one person's erase (design
// S15, plan Task 5): one per transaction that erased anything, naming each
// kind and how many items it removed — {kinds: [{kind, count}]}, no fields.
// Its actor is the installation's system actor when the worker ran it, and
// the caller when POST /retention/run did. It touches no class itself (its
// payload_classes are empty, so no later erase rewrites it). It is listed as
// one of the hold's kinds (holdEventKinds), and the hold group reads its
// kinds — the health entry staff:personal-data alone (shapePayload).
const (
	eventErased = "erased"
)

// eventThresholdAcknowledged is a threshold's acknowledgement (design S20,
// plan reading 18): an installation event naming its actor and the key —
// {key} — written once per threshold, in the transaction that records it in
// the settings. Its payload touches no class.
const (
	eventThresholdAcknowledged = "threshold_acknowledged"
)

// The group events (Entra design D9, delivery D): a person joined or left a
// group — a listed group's members written by HR, or a derived group's
// materialised by the accounts worker on the business day. Each names the
// group and how the person came in or went — {groupId, groupName, groupKey,
// source} — class C, as the group's own columns are.
const (
	eventGroupJoined = "group_joined"
	eventGroupLeft   = "group_left"
)

// The competence events (staff competence design S16, delivery A). A
// person's row's event names the row (competenceId) and carries its fields
// before and after — the competence's key, the level, the validity's end,
// the source, the basis, the evidence's document, whether a removal took the
// chain — never the verifier's note. No field of theirs is in fieldClasses:
// each counts as class D, the row's (classOfField), so the D erase blanks
// them, and shapePayload shapes them by the competence groups
// (competenceFieldGroup). The catalogue's two are installation events,
// naming the competence and the fields it changed, as the absence types'.
const (
	eventCompetenceDeclared      = "competence_declared"
	eventCompetenceChanged       = "competence_changed"
	eventCompetenceVerified      = "competence_verified"
	eventCompetenceRenewed       = "competence_renewed"
	eventCompetenceRemoved       = "competence_removed"
	eventCompetenceEvidenceAdded = "competence_evidence_added"
	eventCompetenceCreated       = "competence_created"
	eventCompetenceUpdated       = "competence_updated"
)

// holdEventKinds are the hold's kinds: about the record's retention and who
// exported it rather than about the person's work, so they are listed only to
// an audience that reads the hold group — and to the person themselves, who
// has a right to know a hold is on them and that their data was exported,
// and who reads each as its kind, its fields "changed" (seesHoldEvents). To
// anyone else they are left out of the history and its count, as a health
// event is. The retention run's erased event is one of them: about the
// record's retention, its kinds HR's.
var holdEventKinds = []string{eventPersonalDataExported, eventLegalHoldSet, eventLegalHoldLifted, eventReassessed, eventPersonRehired, eventErased}

const (
	actorKindUser          = "user"
	actorKindSystem        = "system"
	unknownUserDisplay     = "Unknown user"
	systemActorDisplayName = "System"
)

// actor is who an event is attributed to: the three values every event
// carries (actor_kind, actor_display, actor_user_id), resolved once per
// request — the customers module's shape (internal/customers/actor.go),
// copied because no module imports another.
type actor struct {
	Kind    string
	Display string
	UserID  *uuid.UUID
}

// actorFor resolves the request's principal into the actor its events are
// attributed to. It reads the user directory, so it is called before the
// transaction opens (contractscalls.go refuses it inside one). A user the
// directory no longer knows is still the actor, as "Unknown user", so the
// record exists although the account is gone; a lookup that fails fails the
// write, since history that guesses at who wrote it is worse than a 500.
// Every operation of this module is authenticated, so the system actor is
// the answer only to a caller that some later path makes without a user.
func (s *server) actorFor(ctx context.Context) (actor, error) {
	p, ok := contracts.PrincipalFrom(ctx)
	if !ok || p.SCIM || p.UserID == uuid.Nil {
		return actor{Kind: actorKindSystem, Display: systemActorDisplayName}, nil
	}
	user, err := s.usersUser(ctx, p.UserID)
	if err != nil {
		return actor{}, fmt.Errorf("staff: resolve the actor: %w", err)
	}
	id := p.UserID
	if user == nil {
		return actor{Kind: actorKindUser, Display: unknownUserDisplay, UserID: &id}, nil
	}
	return actor{Kind: actorKindUser, Display: user.DisplayName, UserID: &id}, nil
}

// fieldChange is one field's before and after in a payload. Either is absent
// when the field was empty on that side.
type fieldChange struct {
	From any `json:"from,omitempty"`
	To   any `json:"to,omitempty"`
}

// changes is a payload's fields, by the contract's field names.
type changes map[string]fieldChange

// fieldClasses is the retention class (design §3.1, S15) of every field a
// payload can name, for payload_classes: what the erase must reach. It is not
// a visibility table — shaping.go's fieldGroups is.
var fieldClasses = map[string]string{
	"employeeNumber": "C", "givenName": "C", "familyName": "C", "displayName": "C",
	"birthDate": "C", "gender": "C", "workCountry": "C", "preferredLanguage": "C", "roles": "C",
	"workEmail": "D", "phoneWork": "D", "phoneMobile": "D", "phonePrivate": "D", "privateEmail": "D",
	"street": "D", "postalCode": "D", "city": "D", "country": "D", "notes": "D", "nextOfKin": "D",
	"nationalId": "D", "bankAccount": "D", "user": "C",
	// The account flag (design §3.1, S22) is class C, as its columns are;
	// delivery C's.
	"provisionAccount": "C", "provisionFrom": "C", "accountEmailKind": "C",
	// The care-day fields (design §3.1, S11) are class D, the person's
	// administration; delivery B's.
	"childrenUnder12": "D", "soleCarer": "D", "chronicallyIllChildren": "D",

	// An employment and its non-pay terms are class C, kept for good; the
	// pay terms, the seniority date and the pay grade class A, kept five
	// years past the end year; a terms row's note class D (design §3.2, §3.3).
	"employmentNumber": "C", "relationshipType": "C", "businessUnitId": "C", "startDate": "C", "endDate": "C",
	"endReason": "C", "contractSignedAt": "C", "holidaySchemeId": "C", "validFrom": "C", "employmentType": "C", "temporaryGround": "C",
	"expectedEndDate": "C", "appointmentForm": "C", "hiredOutTo": "C", "ftePercent": "C", "workingHoursScheme": "C",
	"fullTimeHoursPerWeek": "C", "occupationCode": "C", "title": "C", "employeeType": "C", "departmentId": "C",
	"locationId": "C", "managerPersonId": "C", "costCentre": "C", "division": "C", "workingTimeText": "C",
	"breakMinutes": "C", "specialHoursText": "C", "overtimeRuleText": "C", "probationEndDate": "C",
	"probationExtensionAgreed": "C", "probationExtendedTo": "C", "probationExtensionReason": "C",
	"noticeEmployee": "C", "noticeEmployer": "C", "noticeUnit": "C", "tariffReference": "C", "holidayTermsText": "C",
	"competenceDevelopmentText": "C", "socialSecurityText": "C",
	"remunerationType": "A", "payComponents": "A", "payMethod": "A", "paydayText": "A", "seniorityDate": "A", "payGrade": "A",
	"note": "D",

	// The hold is class F — it stops every erase and is erased by none; when
	// a person was erased and reassessed is class C, who was employed when
	// (design §3.1).
	"legalHold": "F", "legalHoldReason": "F", "erasedAt": "C", "reassessedAt": "C",
}

// classOfField is a payload field's retention class. A field the table does
// not know counts as D, the class the erase blanks, so an unknown field is
// erased rather than kept.
func classOfField(name string) string {
	if class, ok := fieldClasses[name]; ok {
		return class
	}
	return "D"
}

// classes is the retention classes c's fields belong to, sorted and each
// once (classOfField).
func (c changes) classes() []string {
	var out []string
	for name := range c {
		class := classOfField(name)
		if !slices.Contains(out, class) {
			out = append(out, class)
		}
	}
	slices.Sort(out)
	return out
}

// recordEvent appends one event inside the caller's transaction, so the
// change and its history commit or roll back together. personID nil is an
// installation-level event.
func (s *server) recordEvent(ctx context.Context, txq *store.Queries, personID *int64, kind string, who actor, payload any, classes []string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("staff: encode a %s event: %w", kind, err)
	}
	if classes == nil {
		classes = []string{}
	}
	display := who.Display
	if err := txq.InsertEvent(ctx, store.InsertEventParams{
		PersonID: personID, Kind: kind, ActorKind: who.Kind, ActorUserID: who.UserID, ActorDisplay: &display,
		At: s.deps.Clock(), Payload: body, PayloadClasses: classes,
	}); err != nil {
		return fmt.Errorf("staff: record a %s event: %w", kind, err)
	}
	return nil
}

// recordChanges records kind for personID with c as its fields; nothing when
// c is empty, since a write that changed nothing has nothing to say.
func (s *server) recordChanges(ctx context.Context, txq *store.Queries, personID int64, kind string, who actor, c changes) error {
	if len(c) == 0 {
		return nil
	}
	return s.recordEvent(ctx, txq, &personID, kind, who, map[string]any{"fields": c}, c.classes())
}

// everything is the audience that reads every group. A payload is written
// with every field the change touched — who reads which is decided when the
// history is read (shapePayload) — so the values it is built from are read
// as this audience reads them.
var everything = audience{Self: true, Manage: true, PersonalData: true}

// payloadValues is a response's fields as a payload names them — by the
// contract's names, each value as the response encodes it — less own, the
// response's fields that are not data about the person (ids, revisions).
// An absent field is not in the map.
func payloadValues(response any, own ...string) (map[string]json.RawMessage, error) {
	b, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("staff: encode the values of a change: %w", err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("staff: decode the values of a change: %w", err)
	}
	for _, name := range own {
		delete(out, name)
	}
	return out, nil
}

// diffFields is a change's fields from before to after, each named once with
// its value on either side — absent on the side where it was empty. before
// nil is a creation, after nil a deletion.
func diffFields(before, after map[string]json.RawMessage) changes {
	out := changes{}
	for _, side := range []map[string]json.RawMessage{before, after} {
		for name := range side {
			from, hadFrom := before[name]
			to, hasTo := after[name]
			if hadFrom == hasTo && bytes.Equal(from, to) {
				continue
			}
			var c fieldChange
			if hadFrom {
				c.From = from
			}
			if hasTo {
				c.To = to
			}
			out[name] = c
		}
	}
	return out
}

// recordRowChange records kind for personID with c as its fields and the ids
// of the employment and the terms row it is about (termsID 0 for an
// employment's own event); nothing when c is empty.
func (s *server) recordRowChange(ctx context.Context, txq *store.Queries, personID int64, kind string, who actor, employmentID, termsID int64, c changes) error {
	if len(c) == 0 {
		return nil
	}
	payload := map[string]any{"fields": c, "employmentId": employmentID}
	if termsID != 0 {
		payload["termsId"] = termsID
	}
	return s.recordEvent(ctx, txq, &personID, kind, who, payload, c.classes())
}

// GetStaffPeopleByIdEvents List a person's history
// (GET /api/v1/staff/people/{id}/events)
//
// The person themselves, a manager of theirs today and HR read it; to anyone
// else — a colleague holding staff:view included — the person's history is
// the 404 an unknown id is. A health event — one whose payload touches class E,
// a health document's — is left out of the page and the count for an
// audience without the health group (HR without staff:personal-data, a
// manager), so they do not learn that one happened; the hold's kinds are left
// out the same way for an audience that is neither the person nor HR.
func (s *server) GetStaffPeopleByIdEvents(ctx context.Context, req gen.GetStaffPeopleByIdEventsRequestObject) (gen.GetStaffPeopleByIdEventsResponseObject, error) {
	if f := validatePageParams(req.Params.Page, req.Params.PageSize); f.failed() {
		return gen.GetStaffPeopleByIdEvents400ApplicationProblemPlusJSONResponse(invalid(invalidPagingTitle, f)), nil
	}
	page, pageSize := pageParams(req.Params.Page, req.Params.PageSize)
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var (
		a     audience
		types typeVisibilities
		rows  []store.ListPersonEventsRow
		total int64
	)
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := q.GetPerson(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("staff: read person %d: %w", req.Id, err)
		}
		var err error
		if a, err = personAudience(ctx, q, caller, req.Id); err != nil {
			return err
		}
		if !a.seesEvents() {
			return notFound()
		}
		if types, err = readTypeVisibilities(ctx, q); err != nil {
			return err
		}
		hide, plain := a.hidesLeaveRows(), types.plain()
		if total, err = q.CountPersonEvents(ctx, store.CountPersonEventsParams{
			PersonID: &req.Id, WithHealth: a.reads(groupHealth), WithHold: a.seesHoldEvents(), HoldKinds: holdEventKinds, AbsenceKinds: absenceEventKinds, WithCare: a.reads(groupCareDays), CareKinds: careEventKinds, WithLinks: a.reads(groupLinks), LinkKinds: accountEventKinds,
			HideLeaveRows: hide, PlainTypeIds: plain,
		}); err != nil {
			return fmt.Errorf("staff: count person %d's events: %w", req.Id, err)
		}
		rows, err = q.ListPersonEvents(ctx, store.ListPersonEventsParams{
			PersonID: &req.Id, WithHealth: a.reads(groupHealth), WithHold: a.seesHoldEvents(), HoldKinds: holdEventKinds, AbsenceKinds: absenceEventKinds, WithCare: a.reads(groupCareDays), CareKinds: careEventKinds, WithLinks: a.reads(groupLinks), LinkKinds: accountEventKinds,
			HideLeaveRows: hide, PlainTypeIds: plain, PageSize: pageSize, PageOffset: (page - 1) * pageSize,
		})
		if err != nil {
			return fmt.Errorf("staff: list person %d's events: %w", req.Id, err)
		}
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdEvents404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffPeopleByIdEvents200JSONResponse(gen.PaginatedResponseOfStaffEvent{
		Data:       mapRows(rows, func(r store.ListPersonEventsRow) gen.StaffEvent { return eventResponse(r, a, types) }),
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}), nil
}
