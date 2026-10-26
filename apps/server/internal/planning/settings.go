package planning

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// The settings (design S20): one row with a revision, planning:manage's to
// read and change, every field left out kept.

// The settings' bounds (S20).
const (
	minThresholdPercent = 1
	maxThresholdPercent = 100
	minHorizonWeeks     = 1
	maxHorizonWeeks     = 52
	minRequestLeadDays  = 0
	maxRequestLeadDays  = 365
	minEndingLeadWeeks  = 1
	maxEndingLeadWeeks  = 26
)

// The values the two enumerated settings take, as the contract names them.
var (
	overbookingRules = []string{string(gen.Warn), string(gen.Refuse)}
	loadUnits        = []string{loadUnitPercent, loadUnitHours}
)

// settings reads the installation's settings row on q. The migration writes
// it, so a failure here is a fault, never "no settings".
func settings(ctx context.Context, q *store.Queries) (store.PlanningSetting, error) {
	row, err := q.GetSettings(ctx)
	if err != nil {
		return store.PlanningSetting{}, fmt.Errorf("planning: read the settings: %w", err)
	}
	return row, nil
}

// settingsResponse is the row as the contract answers it.
func settingsResponse(row store.PlanningSetting) gen.PlanningSettings {
	return gen.PlanningSettings{
		Overbooking:              gen.PlanningOverbooking(row.Overbooking),
		DefaultLoadUnit:          gen.PlanningLoadUnit(row.DefaultLoadUnit),
		UnbookedThresholdPercent: int32(row.UnbookedThresholdPercent),
		UnbookedHorizonWeeks:     int32(row.UnbookedHorizonWeeks),
		RequestLeadDays:          int32(row.RequestLeadDays),
		EndingLeadWeeks:          int32(row.EndingLeadWeeks),
		Revision:                 row.Revision,
		UpdatedAt:                row.UpdatedAt,
	}
}

// GetPlanningSettings Get the Planning settings
// (GET /api/v1/planning/settings)
//
// planning:manage only, which the contract's access rule enforces.
func (s *server) GetPlanningSettings(ctx context.Context, _ gen.GetPlanningSettingsRequestObject) (gen.GetPlanningSettingsResponseObject, error) {
	row, err := settings(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	return gen.GetPlanningSettings200JSONResponse(settingsResponse(row)), nil
}

// settingsChange is a valid PUT /settings body: each field nil when it was
// left out, which keeps its value.
type settingsChange struct {
	overbooking, defaultLoadUnit                                *string
	thresholdPercent, horizonWeeks, requestLeadDays, endingLead *int16
}

// parseSettings validates a PUT /settings body (S20): every bound is a field
// error on its own field, and every failure is reported at once.
func parseSettings(body gen.PlanningSettingsRequest) (settingsChange, fieldErrors) {
	var (
		c settingsChange
		f fieldErrors
	)
	c.overbooking = enumField(&f, "overbooking", (*string)(body.Overbooking), overbookingRules)
	c.defaultLoadUnit = enumField(&f, "defaultLoadUnit", (*string)(body.DefaultLoadUnit), loadUnits)
	c.thresholdPercent = boundedField(&f, "unbookedThresholdPercent", body.UnbookedThresholdPercent, minThresholdPercent, maxThresholdPercent)
	c.horizonWeeks = boundedField(&f, "unbookedHorizonWeeks", body.UnbookedHorizonWeeks, minHorizonWeeks, maxHorizonWeeks)
	c.requestLeadDays = boundedField(&f, "requestLeadDays", body.RequestLeadDays, minRequestLeadDays, maxRequestLeadDays)
	c.endingLead = boundedField(&f, "endingLeadWeeks", body.EndingLeadWeeks, minEndingLeadWeeks, maxEndingLeadWeeks)
	return c, f
}

// enumField is an optional enumerated value: nil stays nil, anything else
// must be one of values or is a field error on field.
func enumField(f *fieldErrors, field string, raw *string, values []string) *string {
	if raw == nil {
		return nil
	}
	if !slices.Contains(values, *raw) {
		f.add(field, fmt.Sprintf("%s must be one of %s, but was '%s'", field, strings.Join(values, ", "), *raw))
		return nil
	}
	v := *raw
	return &v
}

// boundedField is an optional whole number from lo to hi: nil stays nil,
// anything outside is a field error on field.
func boundedField(f *fieldErrors, field string, raw *int32, lo, hi int32) *int16 {
	if raw == nil {
		return nil
	}
	if *raw < lo || *raw > hi {
		f.add(field, fmt.Sprintf("%s must be from %d to %d", field, lo, hi))
		return nil
	}
	v := int16(*raw)
	return &v
}

// mergedOver is the settings row with c's fields over it: what the change
// writes.
func (c settingsChange) mergedOver(row store.PlanningSetting) store.UpdateSettingsParams {
	p := store.UpdateSettingsParams{
		Overbooking: row.Overbooking, DefaultLoadUnit: row.DefaultLoadUnit,
		UnbookedThresholdPercent: row.UnbookedThresholdPercent, UnbookedHorizonWeeks: row.UnbookedHorizonWeeks,
		RequestLeadDays: row.RequestLeadDays, EndingLeadWeeks: row.EndingLeadWeeks,
	}
	if c.overbooking != nil {
		p.Overbooking = *c.overbooking
	}
	if c.defaultLoadUnit != nil {
		p.DefaultLoadUnit = *c.defaultLoadUnit
	}
	if c.thresholdPercent != nil {
		p.UnbookedThresholdPercent = *c.thresholdPercent
	}
	if c.horizonWeeks != nil {
		p.UnbookedHorizonWeeks = *c.horizonWeeks
	}
	if c.requestLeadDays != nil {
		p.RequestLeadDays = *c.requestLeadDays
	}
	if c.endingLead != nil {
		p.EndingLeadWeeks = *c.endingLead
	}
	return p
}

// PutPlanningSettings Change the Planning settings
// (PUT /api/v1/planning/settings)
//
// Every field left out keeps its value; a property the contract does not name
// is refused as a field error (strictbody.go). The revision is judged under
// the row's lock, so two changes at once queue and the second is told the
// first won.
func (s *server) PutPlanningSettings(ctx context.Context, req gen.PutPlanningSettingsRequestObject) (gen.PutPlanningSettingsResponseObject, error) {
	var body gen.PlanningSettingsRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutPlanningSettings400ApplicationProblemPlusJSONResponse(invalid(invalidSettingsTitle, badIfMatch())), nil
	}
	f := s.unknownProperties(ctx, settingsRequestSchema)
	change, invalidFields := parseSettings(body)
	f.merge(invalidFields)
	if f.failed() {
		return gen.PutPlanningSettings400ApplicationProblemPlusJSONResponse(invalid(invalidSettingsTitle, f)), nil
	}

	userID := callerID(ctx)
	var saved store.PlanningSetting
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		current, err := txq.LockSettings(ctx)
		if err != nil {
			return fmt.Errorf("planning: lock the settings: %w", err)
		}
		if err := checkRevision("settings", current.Revision, revision); err != nil {
			return err
		}
		params := change.mergedOver(current)
		params.Now = s.deps.Clock()
		params.UpdatedByUserID = &userID
		if saved, err = txq.UpdateSettings(ctx, params); err != nil {
			return fmt.Errorf("planning: change the settings: %w", err)
		}
		return nil
	})
	if r, ok := asRefusal(err); ok && r.status == http.StatusConflict {
		return gen.PutPlanningSettings409ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PutPlanningSettings200JSONResponse(settingsResponse(saved)), nil
}
