package staff

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the thresholds' acknowledgement (design S20, plan reading 18):
// HR acknowledges each of 5, 30, 50 and otp once and permanently, and its
// attention item is not shown again. The settings row's
// thresholds_acknowledged column records when — {"5": at, …} (design §3.6) —
// and a threshold_acknowledged event of the installation records who.

// thresholdKeys are the thresholds there are.
var thresholdKeys = []string{"5", "30", "50", thresholdOTP}

// parseAcknowledged reads the settings' thresholds_acknowledged column: when
// each threshold was acknowledged, by key.
func parseAcknowledged(raw []byte) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("staff: read the acknowledged thresholds: %w", err)
	}
	return out, nil
}

// thresholdsResponse is the acknowledgements as the contract carries them.
func thresholdsResponse(acknowledged map[string]time.Time) gen.StaffThresholdsAcknowledged {
	at := func(key string) *time.Time {
		if v, ok := acknowledged[key]; ok {
			return &v
		}
		return nil
	}
	return gen.StaffThresholdsAcknowledged{N5: at("5"), N30: at("30"), N50: at("50"), Otp: at(thresholdOTP)}
}

// PostStaffThresholdsByKeyAcknowledge Acknowledge a threshold
// (POST /api/v1/staff/thresholds/{key}/acknowledge)
//
// staff:manage, which the contract's access rule enforces. Under the settings
// row's lock — the lock PUT /settings takes — the key is added to the column
// and the event recorded, in one transaction, so two acknowledgements at once
// queue and the second finds the first's: an acknowledged threshold changes
// nothing and records nothing, and answers the same.
func (s *server) PostStaffThresholdsByKeyAcknowledge(ctx context.Context, req gen.PostStaffThresholdsByKeyAcknowledgeRequestObject) (gen.PostStaffThresholdsByKeyAcknowledgeResponseObject, error) {
	if !slices.Contains(thresholdKeys, req.Key) {
		return gen.PostStaffThresholdsByKeyAcknowledge404ApplicationProblemPlusJSONResponse(thresholdUnknown(req.Key)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var acknowledged map[string]time.Time
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		row, err := txq.LockSettings(ctx)
		if err != nil {
			return fmt.Errorf("staff: lock the settings: %w", err)
		}
		if acknowledged, err = parseAcknowledged(row.ThresholdsAcknowledged); err != nil {
			return err
		}
		if _, done := acknowledged[req.Key]; done {
			return nil
		}
		now := s.deps.Clock()
		acknowledged[req.Key] = now.UTC()
		body, err := json.Marshal(acknowledged)
		if err != nil {
			return fmt.Errorf("staff: encode the acknowledged thresholds: %w", err)
		}
		if err := txq.SetThresholdsAcknowledged(ctx, store.SetThresholdsAcknowledgedParams{ThresholdsAcknowledged: body, Now: now}); err != nil {
			return fmt.Errorf("staff: record threshold %s's acknowledgement: %w", req.Key, err)
		}
		return s.recordEvent(ctx, txq, nil, eventThresholdAcknowledged, who, map[string]any{"key": req.Key}, nil)
	})
	if err != nil {
		return nil, err
	}
	return gen.PostStaffThresholdsByKeyAcknowledge200JSONResponse(thresholdsResponse(acknowledged)), nil
}
