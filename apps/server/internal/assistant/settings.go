package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/assistant/gen"
	"github.com/vantigo-io/vantigo/server/internal/assistant/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// The workspace modes (S8) and the sensitive data switch's values (S16).
const (
	modeAll       = "all"
	modePerPerson = "per_person"
	modeDisabled  = "disabled"

	sensitiveOff  = "off"
	sensitiveRead = "read"

	minRetentionMonths = 1
	maxRetentionMonths = 60
)

// The reasons the assistant is not available to someone (S8).
const (
	reasonNotConfigured = "not_configured"
	reasonDisabled      = "disabled"
	reasonNotEnabled    = "not_enabled"
)

// settings reads the workspace's settings row. The migration writes it, so a
// failure here is a fault.
func (s *server) settings(ctx context.Context) (store.AssistantSetting, error) {
	row, err := store.New(s.deps.Pool).GetSettings(ctx)
	if err != nil {
		return store.AssistantSetting{}, fmt.Errorf("assistant: read the settings: %w", err)
	}
	return row, nil
}

// available is S8's one function: whether the assistant is available to the
// person, and if not why. It needs the instance to have AI (Deps.AI), the
// workspace mode to allow the person and, in per-person mode, the person's own
// switch. That the module is enabled is Compose's, and that the person's own
// access is current is the router's.
func (s *server) available(ctx context.Context, userID uuid.UUID) (bool, string, error) {
	if s.deps.AI == nil {
		return false, reasonNotConfigured, nil
	}
	settings, err := s.settings(ctx)
	if err != nil {
		return false, "", err
	}
	switch settings.Mode {
	case modeDisabled:
		return false, reasonDisabled, nil
	case modePerPerson:
		person, err := store.New(s.deps.Pool).GetPerson(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, reasonNotEnabled, nil
		}
		if err != nil {
			return false, "", fmt.Errorf("assistant: read the person's switch: %w", err)
		}
		if !person.Enabled {
			return false, reasonNotEnabled, nil
		}
	}
	return true, "", nil
}

// require is available as a gate for the chat endpoints: nil when the caller
// has the assistant, otherwise the 403 assistant_unavailable refusal.
func (s *server) require(ctx context.Context) (*refusal, error) {
	ok, _, err := s.available(ctx, callerID(ctx))
	if err != nil {
		return nil, err
	}
	if !ok {
		r := unavailable()
		return &r, nil
	}
	return nil, nil
}

// GetAssistantAvailability (GET /api/v1/assistant/availability)
func (s *server) GetAssistantAvailability(ctx context.Context, _ gen.GetAssistantAvailabilityRequestObject) (gen.GetAssistantAvailabilityResponseObject, error) {
	ok, reason, err := s.available(ctx, callerID(ctx))
	if err != nil {
		return nil, err
	}
	out := gen.AssistantAvailability{Available: ok, Local: s.deps.Config.AILocal()}
	if reason != "" {
		r := gen.AssistantAvailabilityReason(reason)
		out.Reason = &r
	}
	return gen.GetAssistantAvailability200JSONResponse(out), nil
}

// settingsResponse is the row, and what the instance is configured for, as the
// contract answers it.
func (s *server) settingsResponse(row store.AssistantSetting) gen.AssistantSettings {
	out := gen.AssistantSettings{
		Mode:             gen.AssistantSettingsMode(row.Mode),
		RetentionMonths:  int32(row.RetentionMonths),
		SensitiveDefault: gen.AssistantSettingsSensitiveDefault(row.SensitiveDefault),
		Version:          row.Version,
		UpdatedAt:        row.UpdatedAt,
		Configured:       s.deps.AI != nil,
		Local:            s.deps.Config.AILocal(),
		Models:           []string{},
	}
	if s.deps.AI != nil {
		provider := s.deps.AI.Provider()
		out.Provider = &provider
		out.Models = s.deps.AI.Models()
	}
	return out
}

// GetAssistantSettings (GET /api/v1/assistant/settings)
func (s *server) GetAssistantSettings(ctx context.Context, _ gen.GetAssistantSettingsRequestObject) (gen.GetAssistantSettingsResponseObject, error) {
	row, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetAssistantSettings200JSONResponse(s.settingsResponse(row)), nil
}

// settingsSnapshot is what the audit records of the settings.
func settingsSnapshot(row store.AssistantSetting) map[string]any {
	return map[string]any{"mode": row.Mode, "retentionMonths": row.RetentionMonths, "sensitiveDefault": row.SensitiveDefault}
}

// PutAssistantSettings (PUT /api/v1/assistant/settings)
//
// Every field left out keeps its value. The version is judged under the row's
// lock, so two changes at once queue and the second is told the first won.
func (s *server) PutAssistantSettings(ctx context.Context, req gen.PutAssistantSettingsRequestObject) (gen.PutAssistantSettingsResponseObject, error) {
	version, ok := parseVersion(req.Params.IfMatch)
	if !ok {
		return badIfMatch(), nil
	}
	var body gen.AssistantSettingsRequest
	if req.Body != nil {
		body = *req.Body
	}
	errs := map[string][]string{}
	if body.Mode != nil && !oneOf(string(*body.Mode), modeAll, modePerPerson, modeDisabled) {
		errs["mode"] = []string{"mode must be one of all, per_person, disabled"}
	}
	if body.SensitiveDefault != nil && !oneOf(string(*body.SensitiveDefault), sensitiveOff, sensitiveRead) {
		errs["sensitiveDefault"] = []string{"sensitiveDefault must be off or read"}
	}
	if body.RetentionMonths != nil && (*body.RetentionMonths < minRetentionMonths || *body.RetentionMonths > maxRetentionMonths) {
		errs["retentionMonths"] = []string{fmt.Sprintf("retentionMonths must be from %d to %d", minRetentionMonths, maxRetentionMonths)}
	}
	if len(errs) > 0 {
		return invalid("The settings are not valid", errs), nil
	}

	actor := callerID(ctx)
	var saved store.AssistantSetting
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		current, err := q.LockSettings(ctx)
		if err != nil {
			return fmt.Errorf("assistant: lock the settings: %w", err)
		}
		if current.Version != version {
			return errVersion
		}
		p := store.UpdateSettingsParams{
			Mode: current.Mode, RetentionMonths: current.RetentionMonths, SensitiveDefault: current.SensitiveDefault,
			Now: s.deps.Clock(), UpdatedBy: actor,
		}
		if body.Mode != nil {
			p.Mode = string(*body.Mode)
		}
		if body.RetentionMonths != nil {
			p.RetentionMonths = int16(*body.RetentionMonths)
		}
		if body.SensitiveDefault != nil {
			p.SensitiveDefault = string(*body.SensitiveDefault)
		}
		if saved, err = q.UpdateSettings(ctx, p); err != nil {
			return fmt.Errorf("assistant: change the settings: %w", err)
		}
		return s.audit(ctx, q, actor, "settings.changed", "", settingsSnapshot(current), settingsSnapshot(saved))
	})
	if errors.Is(err, errVersion) {
		return versionConflict(), nil
	}
	if err != nil {
		return nil, err
	}
	s.levels.invalidate()
	return gen.PutAssistantSettings200JSONResponse(s.settingsResponse(saved)), nil
}

// errVersion marks an If-Match version that has moved on.
var errVersion = errors.New("assistant: the version has moved on")

func versionConflict() refusal {
	return refuse(http.StatusConflict, codeVersion, "The settings changed since you read them")
}

func badIfMatch() refusal {
	return invalid("If-Match is not a version", map[string][]string{"If-Match": {"If-Match must carry the version the row was read at, such as \"3\""}})
}

// parseVersion reads an If-Match: a whole number, bare or quoted, zero or more
// (a person who has no row yet is at version 0).
func parseVersion(raw string) (int32, bool) {
	v := strings.TrimSpace(raw)
	v = strings.TrimPrefix(v, "W/")
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil || n < 0 {
		return 0, false
	}
	return int32(n), true
}

func oneOf(v string, set ...string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}

// audit records an Owner's change with before and after, in the transaction of
// the change (assistant.events). Identity's audit is identity's alone, and a
// module may not write another's schema, so the module keeps its own record.
func (s *server) audit(ctx context.Context, q *store.Queries, actor uuid.UUID, action, target string, before, after any) error {
	b, err := json.Marshal(before)
	if err != nil {
		return err
	}
	a, err := json.Marshal(after)
	if err != nil {
		return err
	}
	var t *string
	if target != "" {
		t = &target
	}
	if err := q.InsertEvent(ctx, store.InsertEventParams{At: s.deps.Clock(), ActorUserID: actor, Action: action, Target: t, Before: b, After: a}); err != nil {
		return fmt.Errorf("assistant: record the change: %w", err)
	}
	return nil
}
