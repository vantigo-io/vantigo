package staff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the absence types (design S8, §3.5, §6 Settings): the
// statutory vocabulary seeded by 00047 and the company's own, read by every
// caller — the type is public, who takes it is not (plan reading 19) — and
// added and changed by HR. A type's key, kind and unit never change, since
// the rules key on the kind and an absence counts in the unit. A seed changes
// only its names, its order and whether it is hidden (seed_immutable
// otherwise): its flags and its a-melding mapping are the vocabulary's, not
// the installation's. The reserved absent, which the health erase writes in
// place of a sick or child-sick type (plan reading 12), is never shown.
//
// The a-melding descriptions a type maps to are the contract's enum,
// StaffAmeldingLeave: the kodeverk PermisjonsOgPermitteringsBeskrivelse's six
// leave descriptions and permittering in NAV's spellings (plan Revision 2,
// Q4a), so the expired velferdspermisjon and the unsplit utdanningspermisjon
// are refused before a handler runs. Each write is an installation event —
// absence_type_created, absence_type_updated — naming the type and the
// fields it changed; it is about no person and touches no retention class.

// The installation events of the types.
const (
	eventAbsenceTypeCreated = "absence_type_created"
	eventAbsenceTypeUpdated = "absence_type_updated"
)

// absenceTypeKeyIndex is the unique index on a type's key.
const absenceTypeKeyIndex = "ux_absence_types_key"

// reservedAbsenceType is the key of the type the health erase writes.
const reservedAbsenceType = "absent"

// absenceTypeKeyPattern is a company type's key: 1 to 40 of a–z, 0–9 and the
// underscore, the alphabet of the seeds' own keys.
var absenceTypeKeyPattern = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)

// absenceTypeNameMaxLength is the names' columns' width.
const absenceTypeNameMaxLength = 100

// absenceTypeSortMax bounds a type's place in the list.
const absenceTypeSortMax = 1000

// ameldingLeaveDescriptions are the a-melding leave descriptions a type may
// be reported as — the kodeverk PermisjonsOgPermitteringsBeskrivelse's six
// leave descriptions and permittering, in NAV's spellings (plan Revision 2,
// Q4a; the contract's StaffAmeldingLeave). The codes velferdspermisjon and
// utdanningspermisjon expired on 31 December 2022 (Q4b) and are none of
// them. The request is not judged against the contract's enums before it
// reaches a handler, so this list is the rule.
var ameldingLeaveDescriptions = []string{
	"permisjonMedForeldrepenger", "permisjonVedMilitaertjeneste", "utdanningspermisjonLovfestet",
	"utdanningspermisjonIkkeLovfestet", "andreLovfestedePermisjoner", "andreIkkeLovfestedePermisjoner", "permittering",
}

// absenceUnits and absenceVisibilities are a type's unit and visibility.
var (
	absenceUnits        = []string{"days", "hours"}
	absenceVisibilities = []string{"absent", "hr_only"}
)

// companyAbsenceKinds are the kinds a company type may have; the sick kinds
// and child_sick are the seeds' alone, since the case, egenmelding and the
// care days key on them.
var companyAbsenceKinds = []string{"holiday", "leave", "time_off", "other"}

// absenceTypeResponse is one type in full.
func absenceTypeResponse(a store.StaffAbsenceType) gen.StaffAbsenceType {
	return gen.StaffAbsenceType{
		Id:            a.ID,
		Key:           a.Key,
		Names:         gen.StaffAbsenceTypeNames{En: a.NameEn, Nb: a.NameNb},
		Kind:          gen.StaffAbsenceKind(a.Kind),
		Unit:          gen.StaffAbsenceUnit(a.Unit),
		Paid:          a.Paid,
		DrawsHoliday:  a.DrawsHoliday,
		AllowsPercent: a.AllowsPercent,
		NeedsDecision: a.NeedsDecision,
		Visibility:    gen.StaffAbsenceVisibility(a.Visibility),
		AmeldingLeave: enumOf[gen.StaffAmeldingLeave](a.AmeldingLeave),
		Seeded:        a.Seeded,
		Hidden:        a.Hidden,
		Reserved:      a.Seeded && a.Key == reservedAbsenceType,
		Sort:          a.Sort,
		Revision:      a.Revision,
	}
}

// absenceTypeFlags is what a type's create and change share: its names, its
// flags, its visibility, its mapping, hidden and its place.
type absenceTypeFlags struct {
	NameEn, NameNb string
	Paid           bool
	DrawsHoliday   bool
	AllowsPercent  bool
	NeedsDecision  bool
	Visibility     string
	AmeldingLeave  *string
	Hidden         bool
	Sort           *int32
}

// parseAbsenceTypeFlags runs the shared rules over a type in unit: names of
// 1 to 100 characters, a sort of 1 to 1000, and a percentage and the holiday
// account only on a type counted in days — an hour registration counts no
// day (design §4 Days). The enums are the contract's to have judged.
func parseAbsenceTypeFlags(names gen.StaffAbsenceTypeNames, unit string, in absenceTypeFlags, f *fieldErrors) absenceTypeFlags {
	out := in
	var msg string
	out.NameEn, msg = requiredText("An English name", names.En, absenceTypeNameMaxLength)
	f.add("names.en", msg)
	out.NameNb, msg = requiredText("A Norwegian name", names.Nb, absenceTypeNameMaxLength)
	f.add("names.nb", msg)
	if out.Sort != nil && (*out.Sort < 1 || *out.Sort > absenceTypeSortMax) {
		f.add("sort", fmt.Sprintf("A type's place is 1 to %d", absenceTypeSortMax))
	}
	if _, msg := parseEnum("A visibility", &out.Visibility, absenceVisibilities); msg != "" {
		f.add("visibility", msg)
	}
	if _, msg := parseEnum("An a-melding leave description", out.AmeldingLeave, ameldingLeaveDescriptions); msg != "" {
		f.add("ameldingLeave", msg)
	}
	if unit == "hours" {
		if out.AllowsPercent {
			f.add("allowsPercent", "A type counted in hours takes no percentage: an hour registration counts no day")
		}
		if out.DrawsHoliday {
			f.add("drawsHoliday", "A type counted in hours does not draw on the holiday account, which counts days")
		}
	}
	return out
}

// GetStaffAbsenceTypes List absence types
// (GET /api/v1/staff/absence-types)
//
// Every caller reads the types that are not hidden; staff:manage reads the
// hidden ones too, to show them again.
func (s *server) GetStaffAbsenceTypes(ctx context.Context, _ gen.GetStaffAbsenceTypesRequestObject) (gen.GetStaffAbsenceTypesResponseObject, error) {
	rows, err := store.New(s.deps.Pool).ListAbsenceTypes(ctx, s.has(ctx, permManage))
	if err != nil {
		return nil, fmt.Errorf("staff: list the absence types: %w", err)
	}
	return gen.GetStaffAbsenceTypes200JSONResponse(mapRows(rows, absenceTypeResponse)), nil
}

// PostStaffAbsenceTypes Add an absence type
// (POST /api/v1/staff/absence-types)
func (s *server) PostStaffAbsenceTypes(ctx context.Context, req gen.PostStaffAbsenceTypesRequestObject) (gen.PostStaffAbsenceTypesResponseObject, error) {
	var body gen.StaffAbsenceTypeRequest
	if req.Body != nil {
		body = *req.Body
	}
	var f fieldErrors
	if !absenceTypeKeyPattern.MatchString(body.Key) {
		f.add("key", "A key is 1 to 40 characters of a–z, 0–9 and underscores")
	}
	kind := string(body.Kind)
	if !slices.Contains(companyAbsenceKinds, kind) {
		f.add("kind", fmt.Sprintf("A company type's kind is holiday, leave, time_off or other, but was '%s'; the sick kinds are the seeds'", kind))
	}
	unit := string(body.Unit)
	if _, msg := parseEnum("A unit", &unit, absenceUnits); msg != "" {
		f.add("unit", msg)
	}
	hidden := body.Hidden != nil && *body.Hidden
	flags := parseAbsenceTypeFlags(body.Names, unit, absenceTypeFlags{
		Paid: body.Paid, DrawsHoliday: body.DrawsHoliday, AllowsPercent: body.AllowsPercent, NeedsDecision: body.NeedsDecision,
		Visibility: string(body.Visibility), AmeldingLeave: stringOf(body.AmeldingLeave), Hidden: hidden, Sort: body.Sort,
	}, &f)
	if f.failed() {
		return gen.PostStaffAbsenceTypes400ApplicationProblemPlusJSONResponse(invalid(invalidAbsenceTypeTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var created store.StaffAbsenceType
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		sort := flags.Sort
		if sort == nil {
			next, err := txq.NextAbsenceTypeSort(ctx)
			if err != nil {
				return fmt.Errorf("staff: read the next absence type's place: %w", err)
			}
			sort = &next
		}
		created, err = txq.InsertAbsenceType(ctx, store.InsertAbsenceTypeParams{
			Key: body.Key, NameEn: flags.NameEn, NameNb: flags.NameNb, Kind: kind, Unit: unit, Paid: flags.Paid,
			DrawsHoliday: flags.DrawsHoliday, AllowsPercent: flags.AllowsPercent, NeedsDecision: flags.NeedsDecision,
			Visibility: flags.Visibility, AmeldingLeave: flags.AmeldingLeave, Hidden: flags.Hidden, Sort: *sort, Now: s.deps.Clock(),
		})
		if db.IsUniqueViolation(err, absenceTypeKeyIndex) {
			return refuseInvalid(invalidAbsenceTypeTitle, fieldErrors{errs: fieldError("key", "Another absence type already has this key")})
		}
		if err != nil {
			return fmt.Errorf("staff: add an absence type: %w", err)
		}
		return s.recordAbsenceTypeChange(ctx, txq, eventAbsenceTypeCreated, who, nil, created)
	})
	if r, ok := asRefusal(err); ok {
		return gen.PostStaffAbsenceTypes400ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PostStaffAbsenceTypes201JSONResponse(absenceTypeResponse(created)), nil
}

// PutStaffAbsenceTypesById Change an absence type
// (PUT /api/v1/staff/absence-types/{id})
func (s *server) PutStaffAbsenceTypesById(ctx context.Context, req gen.PutStaffAbsenceTypesByIdRequestObject) (gen.PutStaffAbsenceTypesByIdResponseObject, error) {
	var body gen.StaffAbsenceTypeChangeRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffAbsenceTypesById400ApplicationProblemPlusJSONResponse(invalid(invalidAbsenceTypeTitle, badIfMatch())), nil
	}
	if f := s.unknownProperties(ctx, absenceTypeChangeSchema); f.failed() {
		return gen.PutStaffAbsenceTypesById400ApplicationProblemPlusJSONResponse(invalid(invalidAbsenceTypeTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var saved store.StaffAbsenceType
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		row, err := txq.LockAbsenceType(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("staff: lock absence type %d: %w", req.Id, err)
		}
		var f fieldErrors
		sort := body.Sort
		flags := parseAbsenceTypeFlags(body.Names, row.Unit, absenceTypeFlags{
			Paid: body.Paid, DrawsHoliday: body.DrawsHoliday, AllowsPercent: body.AllowsPercent, NeedsDecision: body.NeedsDecision,
			Visibility: string(body.Visibility), AmeldingLeave: stringOf(body.AmeldingLeave), Hidden: body.Hidden, Sort: &sort,
		}, &f)
		if f.failed() {
			return refuseInvalid(invalidAbsenceTypeTitle, f)
		}
		if err := checkRevision("absence type", row.Revision, revision); err != nil {
			return err
		}
		if row.Seeded && !seedChangeAllowed(row, flags) {
			return refuseConflict(conflict(codeSeedImmutable, "Seeded absence type",
				fmt.Sprintf("%s is one of the types every installation starts with: its names, its place and whether it is hidden change, nothing else.", row.NameEn)))
		}
		saved, err = txq.UpdateAbsenceType(ctx, store.UpdateAbsenceTypeParams{
			ID: row.ID, NameEn: flags.NameEn, NameNb: flags.NameNb, Paid: flags.Paid, DrawsHoliday: flags.DrawsHoliday,
			AllowsPercent: flags.AllowsPercent, NeedsDecision: flags.NeedsDecision, Visibility: flags.Visibility,
			AmeldingLeave: flags.AmeldingLeave, Hidden: flags.Hidden, Sort: sort, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("staff: change absence type %d: %w", row.ID, err)
		}
		return s.recordAbsenceTypeChange(ctx, txq, eventAbsenceTypeUpdated, who, &row, saved)
	})
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PutStaffAbsenceTypesById404Response{}, nil
		case http.StatusConflict:
			return gen.PutStaffAbsenceTypesById409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PutStaffAbsenceTypesById400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.PutStaffAbsenceTypesById200JSONResponse(absenceTypeResponse(saved)), nil
}

// seedChangeAllowed is whether a change of a seed keeps everything but its
// names, its place and hidden as they are — and the reserved absent hidden.
func seedChangeAllowed(row store.StaffAbsenceType, next absenceTypeFlags) bool {
	if row.Key == reservedAbsenceType && !next.Hidden {
		return false
	}
	return next.Paid == row.Paid && next.DrawsHoliday == row.DrawsHoliday && next.AllowsPercent == row.AllowsPercent &&
		next.NeedsDecision == row.NeedsDecision && next.Visibility == row.Visibility && textEqual(next.AmeldingLeave, row.AmeldingLeave)
}

// recordAbsenceTypeChange records a type's create (before nil) or change as
// an installation event: the type's id and the fields that changed.
func (s *server) recordAbsenceTypeChange(ctx context.Context, txq *store.Queries, kind string, who actor, before *store.StaffAbsenceType, after store.StaffAbsenceType) error {
	var old map[string]json.RawMessage
	if before != nil {
		var err error
		if old, err = payloadValues(absenceTypeResponse(*before), "id", "revision"); err != nil {
			return err
		}
	}
	values, err := payloadValues(absenceTypeResponse(after), "id", "revision")
	if err != nil {
		return err
	}
	return s.recordInstallationChange(ctx, txq, kind, who, "absenceTypeId", after.ID, diffFields(old, values))
}

// recordInstallationChange records kind as an installation event — about no
// person, touching no retention class — naming the row it is about by idName
// and the fields that changed; nothing when none did.
func (s *server) recordInstallationChange(ctx context.Context, txq *store.Queries, kind string, who actor, idName string, id int64, c changes) error {
	if len(c) == 0 {
		return nil
	}
	return s.recordEvent(ctx, txq, nil, kind, who, map[string]any{idName: id, "fields": c}, nil)
}
