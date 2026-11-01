package assistant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/assistant/gen"
	"github.com/vantigo-io/vantigo/server/internal/assistant/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// permissionView is one catalog permission with its level under table.
func permissionView(p contracts.Permission, table *levelTable) gen.AssistantPermission {
	out := gen.AssistantPermission{
		Key:            p.Key,
		Display:        p.Display,
		Sensitive:      p.Sensitive,
		DefaultLevel:   gen.AssistantPermissionLevel(table.defaultLevel(p)),
		EffectiveLevel: gen.AssistantPermissionLevel(table.effective(p)),
	}
	if p.Description != "" {
		d := p.Description
		out.Description = &d
	}
	if level, ok := table.overrides[p.Key]; ok {
		l := gen.AssistantPermissionLevel(level)
		out.Override = &l
	}
	return out
}

// moduleOf is the module a permission key belongs to: the part before ':'.
func moduleOf(key string) string {
	module, _, _ := strings.Cut(key, ":")
	return module
}

// GetAssistantPermissions (GET /api/v1/assistant/settings/permissions)
//
// The catalog of the enabled modules (Deps.Catalog is composed from them),
// grouped by module and category in the catalog's own order of key.
func (s *server) GetAssistantPermissions(ctx context.Context, _ gen.GetAssistantPermissionsRequestObject) (gen.GetAssistantPermissionsResponseObject, error) {
	s.levels.invalidate()
	table, err := s.levelTable(ctx)
	if err != nil {
		return nil, fmt.Errorf("assistant: read the permission levels: %w", err)
	}
	keys := make([]string, 0, len(s.deps.Catalog))
	for key := range s.deps.Catalog {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	type groupKey struct{ module, category string }
	index := map[groupKey]int{}
	groups := []gen.AssistantPermissionGroup{}
	for _, key := range keys {
		p := s.deps.Catalog[key]
		gk := groupKey{moduleOf(key), p.Category}
		i, ok := index[gk]
		if !ok {
			i = len(groups)
			index[gk] = i
			groups = append(groups, gen.AssistantPermissionGroup{Module: gk.module, Category: gk.category, Permissions: []gen.AssistantPermission{}})
		}
		groups[i].Permissions = append(groups[i].Permissions, permissionView(p, table))
	}
	return gen.GetAssistantPermissions200JSONResponse(gen.AssistantPermissions{Version: table.version, Groups: groups}), nil
}

// PutAssistantPermission (PUT /api/v1/assistant/settings/permissions/{key})
//
// Sets the permission's level, or resets it with a null level. The settings'
// version guards it and moves on with it, which is what makes the gate's cache
// notice.
func (s *server) PutAssistantPermission(ctx context.Context, req gen.PutAssistantPermissionRequestObject) (gen.PutAssistantPermissionResponseObject, error) {
	version, ok := parseVersion(req.Params.IfMatch)
	if !ok {
		return badIfMatch(), nil
	}
	perm, known := s.deps.Catalog[req.Key]
	if !known {
		return notFound(), nil
	}
	if req.Body == nil {
		return invalid("A body is required", nil), nil
	}
	// A null level is a reset.
	level := ""
	if req.Body.Level != nil {
		level = string(*req.Body.Level)
	}
	if level != "" && !oneOf(level, levelOff, levelRead, levelChange) {
		return invalid("The level is not valid", map[string][]string{"level": {"level must be off, read or change, or null to reset it"}}), nil
	}

	actor := callerID(ctx)
	var result gen.AssistantPermissionResult
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		current, err := q.LockSettings(ctx)
		if err != nil {
			return fmt.Errorf("assistant: lock the settings: %w", err)
		}
		if current.Version != version {
			return errVersion
		}
		before := map[string]any{"level": nil}
		if row, err := q.GetPermissionLevel(ctx, req.Key); err == nil {
			before["level"] = row.Level
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("assistant: read the permission's level: %w", err)
		}
		after := map[string]any{"level": nil}
		if level == "" {
			err = q.DeletePermissionLevel(ctx, req.Key)
		} else {
			after["level"] = level
			err = q.UpsertPermissionLevel(ctx, store.UpsertPermissionLevelParams{PermissionKey: req.Key, Level: level, Now: s.deps.Clock(), UpdatedBy: actor})
		}
		if err != nil {
			return fmt.Errorf("assistant: change the permission's level: %w", err)
		}
		saved, err := q.BumpSettingsVersion(ctx, store.BumpSettingsVersionParams{Now: s.deps.Clock(), UpdatedBy: actor})
		if err != nil {
			return fmt.Errorf("assistant: move the settings' version on: %w", err)
		}
		if err := s.audit(ctx, q, actor, "permission.changed", req.Key, before, after); err != nil {
			return err
		}
		levels, err := q.ListPermissionLevels(ctx)
		if err != nil {
			return fmt.Errorf("assistant: read the permission levels: %w", err)
		}
		table := &levelTable{version: saved.Version, sensitive: saved.SensitiveDefault, overrides: map[string]string{}}
		for _, l := range levels {
			table.overrides[l.PermissionKey] = l.Level
		}
		result = gen.AssistantPermissionResult{Version: saved.Version, Permission: permissionView(perm, table)}
		return nil
	})
	if errors.Is(err, errVersion) {
		return refuse(http.StatusConflict, codeVersion, "The settings changed since you read them"), nil
	}
	if err != nil {
		return nil, err
	}
	s.levels.invalidate()
	return gen.PutAssistantPermission200JSONResponse(result), nil
}
