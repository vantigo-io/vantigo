package timetracking

import (
	"context"
	"fmt"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/time/gen"
	"github.com/vantigo-io/vantigo/server/internal/time/store"
)

// This file is the installation's time settings: so far only the period lock
// (D9), which every save, submit and approval reads through callerFor.

// settingsResponse is the settings as they stand in q.
func settingsResponse(ctx context.Context, q *store.Queries) (gen.TimeSettingsResponse, error) {
	lock, err := lockedBefore(ctx, q)
	if err != nil {
		return gen.TimeSettingsResponse{}, err
	}
	var resp gen.TimeSettingsResponse
	if lock != nil {
		resp.LockedBefore = &openapi_types.Date{Time: *lock}
	}
	return resp, nil
}

// GetTimeSettings Get the time settings
// (GET /api/v1/time/settings)
//
// Anyone with time:access reads them: the lock decides what they may still
// change, and the client shows it.
func (s *server) GetTimeSettings(ctx context.Context, _ gen.GetTimeSettingsRequestObject) (gen.GetTimeSettingsResponseObject, error) {
	resp, err := settingsResponse(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	return gen.GetTimeSettings200JSONResponse(resp), nil
}

// PutTimeSettings Change the time settings
// (PUT /api/v1/time/settings)
//
// time:manage only, which the contract's access rule enforces. A full
// replace: a lockedBefore sets or moves the lock, and null — or leaving it
// out — lifts it. Any date is accepted, one in the future included: the lock
// is an administrator's statement about which period is closed.
func (s *server) PutTimeSettings(ctx context.Context, req gen.PutTimeSettingsRequestObject) (gen.PutTimeSettingsResponseObject, error) {
	var lock *openapi_types.Date
	if req.Body != nil {
		lock = req.Body.LockedBefore
	}
	q := store.New(s.deps.Pool)
	if lock == nil {
		if err := q.DeleteSetting(ctx, settingLockedBefore); err != nil {
			return nil, fmt.Errorf("time: lift the lock: %w", err)
		}
	} else if err := q.SetSetting(ctx, store.SetSettingParams{
		Key: settingLockedBefore, Value: lock.Format(time.DateOnly),
	}); err != nil {
		return nil, fmt.Errorf("time: set the lock: %w", err)
	}
	resp, err := settingsResponse(ctx, q)
	if err != nil {
		return nil, err
	}
	return gen.PutTimeSettings200JSONResponse(resp), nil
}
