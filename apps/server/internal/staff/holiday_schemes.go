package staff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the holiday schemes (design S9, §3.5, §6 Settings): the
// statutory scheme seeded by 00047 and the company's own above it, read by
// every caller and written by HR. A scheme's figures are virkedager, Monday
// to Saturday, as the Holiday Act counts them; every answer carries them
// converted to working days of a five-day week (convertVirkedager), which is
// what the holiday account deducts.
//
// Exactly one scheme is the default — the scheme of an employment that names
// none. A move of the default takes the schemes' default advisory lock (as
// the structure tables' defaults do, locks.go), then the scheme it moves to
// and the default it moves from, each FOR UPDATE; the partial unique index
// is the backstop. The default is never archived, never un-flagged in place
// (structure_default), and an archived scheme never becomes it
// (structure_archived).
//
// A scheme an employment names is never archived (holiday_scheme_in_use,
// plan pre-flight I9): the archive takes the scheme's row FOR UPDATE and
// asks whether any employment names it, while an employment write naming a
// scheme takes its row FOR SHARE under the person's lock (employments.go,
// resolveHolidayScheme) and refuses an archived one. Whichever comes first,
// the other waits on the scheme's row and judges what it then finds. Neither
// locks a person and a scheme the other way round, so the two cannot
// deadlock.

// The installation events of the schemes.
const (
	eventHolidaySchemeCreated = "holiday_scheme_created"
	eventHolidaySchemeUpdated = "holiday_scheme_updated"
)

// holidaySchemeNameIndex is the unique index on a scheme's name, however it
// is cased.
const holidaySchemeNameIndex = "ux_holiday_schemes_name_lower"

// The bounds of a scheme's figures, in virkedager (plan "Exact values"): the
// statutory 25 and 6 are the floors (ferieloven § 5), and so are the main
// period's 18 and the after-September 6, each at most the entitlement; the
// agreed carry-over's default, 12, is also its cap above the statutory 25
// (parseScheme).
const (
	schemeNameMaxLength   = 100
	entitlementMin        = 25
	entitlementMax        = 60
	extraFrom60Min        = 6
	extraFrom60Max        = 30
	mainPeriodDefault     = 18
	afterSeptemberDefault = 6
	maxCarryOverDefault   = 12
)

// holidaySchemeResponse is one scheme with its figures converted.
func holidaySchemeResponse(s store.StaffHolidayScheme) gen.StaffHolidayScheme {
	convert := func(v int16) int32 { return int32(convertVirkedager(int(v))) }
	return gen.StaffHolidayScheme{
		Id:                       s.ID,
		Name:                     s.Name,
		EntitlementVirkedager:    int32(s.EntitlementVirkedager),
		ExtraFrom60Virkedager:    int32(s.ExtraFrom60Virkedager),
		MainPeriodVirkedager:     int32(s.MainPeriodVirkedager),
		AfterSeptemberVirkedager: int32(s.AfterSeptemberVirkedager),
		MaxCarryOverVirkedager:   int32(s.MaxCarryOverVirkedager),
		Converted: gen.StaffHolidaySchemeConverted{
			Entitlement:    convert(s.EntitlementVirkedager),
			ExtraFrom60:    convert(s.ExtraFrom60Virkedager),
			MainPeriod:     convert(s.MainPeriodVirkedager),
			AfterSeptember: convert(s.AfterSeptemberVirkedager),
			MaxCarryOver:   convert(s.MaxCarryOverVirkedager),
		},
		IsDefault:  s.IsDefault,
		Seeded:     s.Seeded,
		ArchivedAt: s.ArchivedAt,
		Revision:   s.Revision,
	}
}

// parsedScheme is one validated scheme body.
type parsedScheme struct {
	Name                                                 string
	Entitlement, ExtraFrom60, MainPeriod, AfterSeptember int16
	MaxCarryOver                                         int16
}

// parseScheme runs a scheme's rules over its fields, every failure collected;
// a part left out takes its default.
func parseScheme(name string, entitlement, extra int32, main, after, carry *int32) (parsedScheme, fieldErrors) {
	var f fieldErrors
	var out parsedScheme
	var msg string
	out.Name, msg = requiredText("A name", name, schemeNameMaxLength)
	f.add("name", msg)
	if entitlement < entitlementMin || entitlement > entitlementMax {
		f.add("entitlementVirkedager", fmt.Sprintf("An entitlement is %d to %d virkedager — the Holiday Act's %d at least", entitlementMin, entitlementMax, entitlementMin))
	}
	if extra < extraFrom60Min || extra > extraFrom60Max {
		f.add("extraFrom60Virkedager", fmt.Sprintf("The extra days from 60 are %d to %d virkedager — the Holiday Act's %d at least", extraFrom60Min, extraFrom60Max, extraFrom60Min))
	}
	out.Entitlement, out.ExtraFrom60 = int16(entitlement), int16(extra)
	part := func(field, label string, v *int32, def int16, low, high int32) int16 {
		if v == nil {
			return def
		}
		if *v < low || *v > high {
			f.add(field, fmt.Sprintf("%s is %d to %d virkedager", label, low, high))
			return def
		}
		return int16(*v)
	}
	// The Holiday Act's floors and the carry-over's cap: 18 days in the main
	// period (ferieloven § 7 (1)), 6 for a start after 30 September (§ 5 nr. 3),
	// and an agreed carry-over of at most 12 days plus the days above the
	// statutory 25 (§ 7 (3)).
	out.MainPeriod = part("mainPeriodVirkedager", "The main period", main, mainPeriodDefault, mainPeriodDefault, entitlement)
	out.AfterSeptember = part("afterSeptemberVirkedager", "The days for a start after 30 September", after, afterSeptemberDefault, afterSeptemberDefault, entitlement)
	out.MaxCarryOver = part("maxCarryOverVirkedager", "The agreed carry-over", carry, maxCarryOverDefault, 0, maxCarryOverDefault+max(entitlement-entitlementMin, 0))
	return out, f
}

// schemeNameTaken is the refusal of a name another scheme has.
func schemeNameTaken() error {
	return refuseInvalid(invalidSchemeTitle, fieldErrors{errs: fieldError("name", "Another holiday scheme already has this name, however it is cased")})
}

// takeDefaultScheme moves the default to keep inside the caller's
// transaction, which holds the default's advisory lock and keep's row: the
// current default is taken FOR UPDATE, then cleared.
func (s *server) takeDefaultScheme(ctx context.Context, txq *store.Queries, keep int64) error {
	if _, err := txq.LockDefaultHolidayScheme(ctx); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("staff: lock the default holiday scheme: %w", err)
	}
	if err := txq.ClearHolidaySchemeDefault(ctx, store.ClearHolidaySchemeDefaultParams{Keep: keep, Now: s.deps.Clock()}); err != nil {
		return fmt.Errorf("staff: move the default holiday scheme: %w", err)
	}
	return nil
}

// GetStaffHolidaySchemes List holiday schemes
// (GET /api/v1/staff/holiday-schemes)
func (s *server) GetStaffHolidaySchemes(ctx context.Context, _ gen.GetStaffHolidaySchemesRequestObject) (gen.GetStaffHolidaySchemesResponseObject, error) {
	rows, err := store.New(s.deps.Pool).ListHolidaySchemes(ctx)
	if err != nil {
		return nil, fmt.Errorf("staff: list the holiday schemes: %w", err)
	}
	return gen.GetStaffHolidaySchemes200JSONResponse(mapRows(rows, holidaySchemeResponse)), nil
}

// PostStaffHolidaySchemes Add a holiday scheme
// (POST /api/v1/staff/holiday-schemes)
func (s *server) PostStaffHolidaySchemes(ctx context.Context, req gen.PostStaffHolidaySchemesRequestObject) (gen.PostStaffHolidaySchemesResponseObject, error) {
	var body gen.StaffHolidaySchemeRequest
	if req.Body != nil {
		body = *req.Body
	}
	parsed, f := parseScheme(body.Name, body.EntitlementVirkedager, body.ExtraFrom60Virkedager,
		body.MainPeriodVirkedager, body.AfterSeptemberVirkedager, body.MaxCarryOverVirkedager)
	if f.failed() {
		return gen.PostStaffHolidaySchemes400ApplicationProblemPlusJSONResponse(invalid(invalidSchemeTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	makeDefault := body.IsDefault != nil && *body.IsDefault
	var created store.StaffHolidayScheme
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if makeDefault {
			if err := advisoryLock(ctx, txq, holidaySchemeDefaultLock); err != nil {
				return err
			}
			if err := s.takeDefaultScheme(ctx, txq, 0); err != nil {
				return err
			}
		}
		created, err = txq.InsertHolidayScheme(ctx, store.InsertHolidaySchemeParams{
			Name: parsed.Name, EntitlementVirkedager: parsed.Entitlement, ExtraFrom60Virkedager: parsed.ExtraFrom60,
			MainPeriodVirkedager: parsed.MainPeriod, AfterSeptemberVirkedager: parsed.AfterSeptember,
			MaxCarryOverVirkedager: parsed.MaxCarryOver, IsDefault: makeDefault, Now: s.deps.Clock(),
		})
		if db.IsUniqueViolation(err, holidaySchemeNameIndex) {
			return schemeNameTaken()
		}
		if err != nil {
			return fmt.Errorf("staff: add a holiday scheme: %w", err)
		}
		return s.recordSchemeChange(ctx, txq, eventHolidaySchemeCreated, who, nil, created)
	})
	if r, ok := asRefusal(err); ok {
		return gen.PostStaffHolidaySchemes400ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PostStaffHolidaySchemes201JSONResponse(holidaySchemeResponse(created)), nil
}

// PutStaffHolidaySchemesById Change a holiday scheme
// (PUT /api/v1/staff/holiday-schemes/{id})
func (s *server) PutStaffHolidaySchemesById(ctx context.Context, req gen.PutStaffHolidaySchemesByIdRequestObject) (gen.PutStaffHolidaySchemesByIdResponseObject, error) {
	var body gen.StaffHolidaySchemeChangeRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffHolidaySchemesById400ApplicationProblemPlusJSONResponse(invalid(invalidSchemeTitle, badIfMatch())), nil
	}
	parsed, f := parseScheme(body.Name, body.EntitlementVirkedager, body.ExtraFrom60Virkedager,
		body.MainPeriodVirkedager, body.AfterSeptemberVirkedager, body.MaxCarryOverVirkedager)
	if f.failed() {
		return gen.PutStaffHolidaySchemesById400ApplicationProblemPlusJSONResponse(invalid(invalidSchemeTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	becomesDefault := body.IsDefault != nil && *body.IsDefault
	var saved store.StaffHolidayScheme
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if becomesDefault {
			if err := advisoryLock(ctx, txq, holidaySchemeDefaultLock); err != nil {
				return err
			}
		}
		row, err := txq.LockHolidayScheme(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("staff: lock holiday scheme %d: %w", req.Id, err)
		}
		if err := checkRevision("holiday scheme", row.Revision, revision); err != nil {
			return err
		}
		switch {
		case becomesDefault && body.Archived:
			return refuseConflict(structureArchived("An archived holiday scheme cannot be the default."))
		case body.IsDefault != nil && !*body.IsDefault && row.IsDefault:
			return refuseConflict(structureDefault("holiday scheme"))
		}
		if body.Archived && row.ArchivedAt == nil {
			if row.IsDefault {
				return refuseConflict(conflict(codeHolidaySchemeInUse, "Holiday scheme in use",
					fmt.Sprintf("%s is the default holiday scheme. Make another scheme the default first.", row.Name)))
			}
			named, err := txq.HolidaySchemeNamed(ctx, row.ID)
			if err != nil {
				return fmt.Errorf("staff: read whether holiday scheme %d is named: %w", row.ID, err)
			}
			if named {
				return refuseConflict(conflict(codeHolidaySchemeInUse, "Holiday scheme in use",
					fmt.Sprintf("Employments follow %s. Move them to another scheme first.", row.Name)))
			}
		}
		makeDefault := becomesDefault && !row.IsDefault
		if makeDefault {
			if err := s.takeDefaultScheme(ctx, txq, row.ID); err != nil {
				return err
			}
		}
		saved, err = txq.UpdateHolidayScheme(ctx, store.UpdateHolidaySchemeParams{
			ID: row.ID, Name: parsed.Name, EntitlementVirkedager: parsed.Entitlement, ExtraFrom60Virkedager: parsed.ExtraFrom60,
			MainPeriodVirkedager: parsed.MainPeriod, AfterSeptemberVirkedager: parsed.AfterSeptember,
			MaxCarryOverVirkedager: parsed.MaxCarryOver, MakeDefault: makeDefault, Archived: body.Archived, Now: s.deps.Clock(),
		})
		if db.IsUniqueViolation(err, holidaySchemeNameIndex) {
			return schemeNameTaken()
		}
		if err != nil {
			return fmt.Errorf("staff: change holiday scheme %d: %w", row.ID, err)
		}
		return s.recordSchemeChange(ctx, txq, eventHolidaySchemeUpdated, who, &row, saved)
	})
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PutStaffHolidaySchemesById404Response{}, nil
		case http.StatusConflict:
			return gen.PutStaffHolidaySchemesById409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PutStaffHolidaySchemesById400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.PutStaffHolidaySchemesById200JSONResponse(holidaySchemeResponse(saved)), nil
}

// recordSchemeChange records a scheme's create (before nil) or change as an
// installation event: the scheme's id and the fields that changed. A default
// moved away from another scheme is that scheme's change too, but not an
// event of its own: the move is this one's isDefault.
func (s *server) recordSchemeChange(ctx context.Context, txq *store.Queries, kind string, who actor, before *store.StaffHolidayScheme, after store.StaffHolidayScheme) error {
	own := []string{"id", "revision", "converted", "seeded"}
	var old map[string]json.RawMessage
	if before != nil {
		var err error
		if old, err = payloadValues(holidaySchemeResponse(*before), own...); err != nil {
			return err
		}
	}
	values, err := payloadValues(holidaySchemeResponse(after), own...)
	if err != nil {
		return err
	}
	return s.recordInstallationChange(ctx, txq, kind, who, "holidaySchemeId", after.ID, diffFields(old, values))
}
