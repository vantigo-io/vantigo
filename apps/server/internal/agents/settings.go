package agents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/agents/gen"
	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

const (
	minRetentionMonths = 1
	maxRetentionMonths = 60
	maxCompanyName     = 120
)

// settings reads the workspace's settings row. The migration writes it, so a
// failure here is a fault.
func (s *server) settings(ctx context.Context) (store.AgentsSetting, error) {
	row, err := store.New(s.deps.Pool).GetSettings(ctx)
	if err != nil {
		return store.AgentsSetting{}, fmt.Errorf("agents: read the settings: %w", err)
	}
	return row, nil
}

// settingsResponse is the row, and what the instance is configured for, as the
// contract answers it.
func (s *server) settingsResponse(row store.AgentsSetting) gen.AgentsSettings {
	out := gen.AgentsSettings{
		CompanyName:        row.CompanyName,
		RetentionMonths:    int32(row.RetentionMonths),
		RetentionCapMonths: int32(s.retentionCapMonths()),
		Version:            row.Version,
		UpdatedAt:          row.UpdatedAt,
		Configured:         s.deps.AI != nil,
		Models:             []string{},
	}
	if s.deps.AI != nil {
		out.Models = s.deps.AI.Models()
	}
	return out
}

// settingsSnapshot is what the audit records of the settings.
func settingsSnapshot(row store.AgentsSetting) map[string]any {
	return map[string]any{"companyName": row.CompanyName, "retentionMonths": row.RetentionMonths}
}

// GetAgentsSettings (GET /api/v1/agents/settings)
func (s *server) GetAgentsSettings(ctx context.Context, _ gen.GetAgentsSettingsRequestObject) (gen.GetAgentsSettingsResponseObject, error) {
	row, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetAgentsSettings200JSONResponse(s.settingsResponse(row)), nil
}

// PutAgentsSettings (PUT /api/v1/agents/settings)
//
// The retention is lowered to what Communications keeps its messages for when it
// asks for longer (V26): a transcript copies message bodies and must not outlive
// them. The answer says what was saved. The version is judged under the row's lock, so two changes at once queue and
// the second is told the first won.
func (s *server) PutAgentsSettings(ctx context.Context, req gen.PutAgentsSettingsRequestObject) (gen.PutAgentsSettingsResponseObject, error) {
	version, ok := parseVersion(req.Params.IfMatch)
	if !ok {
		return badIfMatch(), nil
	}
	if req.Body == nil {
		return invalid("A body is required", nil), nil
	}
	body := *req.Body
	company := strings.TrimSpace(body.CompanyName)
	errs := map[string][]string{}
	if n := utf8.RuneCountInString(company); n < 1 || n > maxCompanyName {
		errs["companyName"] = []string{fmt.Sprintf("companyName must be 1 to %d characters", maxCompanyName)}
	}
	if body.RetentionMonths < minRetentionMonths || body.RetentionMonths > maxRetentionMonths {
		errs["retentionMonths"] = []string{fmt.Sprintf("retentionMonths must be from %d to %d", minRetentionMonths, maxRetentionMonths)}
	}
	if len(errs) > 0 {
		return invalid("The settings are not valid", errs), nil
	}

	actor := callerID(ctx)
	var saved store.AgentsSetting
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		current, err := q.LockSettings(ctx)
		if err != nil {
			return fmt.Errorf("agents: lock the settings: %w", err)
		}
		if current.Version != version {
			return errVersion
		}
		if saved, err = q.UpdateSettings(ctx, store.UpdateSettingsParams{
			CompanyName: company, RetentionMonths: int16(min(int(body.RetentionMonths), s.retentionCapMonths())), Now: s.deps.Clock(), UpdatedBy: actor,
		}); err != nil {
			return fmt.Errorf("agents: change the settings: %w", err)
		}
		return s.audit(ctx, q, actor, "settings.changed", "", settingsSnapshot(current), settingsSnapshot(saved))
	})
	if errors.Is(err, errVersion) {
		return versionConflict(), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PutAgentsSettings200JSONResponse(s.settingsResponse(saved)), nil
}
