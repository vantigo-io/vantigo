package agents

// Calibration (virtual employees design V21): a decided reply is a label for its
// score. Over the role's last 200 labelled replies, for each threshold X from 50
// to 100, how many would have run by themselves (score at least X and no rule
// holding them), and of those how many a person had to edit beyond 20 % or
// discard. It is computed here, from the review items, so the screen draws two
// lines and the X marker and the Owner sees what the role's score is worth
// before choosing Auto above X %.

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/agents/gen"
	"github.com/vantigo-io/vantigo/server/internal/agents/store"
)

const (
	// calibrationSample is how many of the newest labelled replies the figures rest on.
	calibrationSample = 200
	// calibrationMinimum is how many labelled replies are needed before they are shown.
	calibrationMinimum = 50
	// largeEdit is the share of a reply a person changed beyond which it counts
	// against the score (V21).
	largeEdit = 0.20
)

// label is one decided reply.
type label struct {
	Score int
	// Held says a rule held the reply, whatever its score.
	Held bool
	// Bad says the reviewer discarded it or changed more than largeEdit of it.
	Bad bool
}

// labelAnsweredOver is the state an Auto reply gets as a label when a person
// answered over it within 24 hours (V21): a reply of a person in the same
// conversation, which contradicts the agent's.
const labelAnsweredOver = "answered_over"

// autoMatures is how long an Auto reply must stand unanswered to count as good.
const autoMatures = 24 * time.Hour

// ruleCodes are the codes of the hard rules and the check; the other codes a
// held_by carries (SEND, TAKEN, MOVED, CHANGE) are the machinery's, not a rule
// holding the score.
var ruleCodes = []string{ruleS1, ruleS2, ruleS3, ruleS4, ruleS5, ruleS6, ruleS7, ruleC1, ruleC2, ruleCheck}

// heldByRule says one of the codes is a hard rule's.
func heldByRule(codes []string) bool {
	return slices.ContainsFunc(codes, func(c string) bool { return slices.Contains(ruleCodes, c) })
}

// labelOf reads a decided reply as a label.
func labelOf(score int16, heldBy []string, state string, ratio *float32) label {
	l := label{Score: int(score), Held: heldByRule(heldBy)}
	switch state {
	case itemDiscarded, labelAnsweredOver:
		l.Bad = true
	case itemEditedSent:
		// The ratio is stored as a float32, so the line is compared as one.
		l.Bad = ratio != nil && *ratio > float32(largeEdit)
	}
	return l
}

// calibrate computes V21's figures from the labels.
func calibrate(labels []label, threshold int) gen.RoleCalibration {
	out := gen.RoleCalibration{
		Labelled: int32(len(labels)), Minimum: calibrationMinimum, Threshold: int32(threshold), Points: []gen.CalibrationPoint{},
	}
	if len(labels) < calibrationMinimum {
		out.NotEnoughHistory = true
		return out
	}
	for _, l := range labels {
		if l.Held {
			out.HeldCount++
		}
	}
	for x := 50; x <= 100; x++ {
		var auto, bad int32
		for _, l := range labels {
			if l.Score >= x && !l.Held {
				auto++
				if l.Bad {
					bad++
				}
			}
		}
		p := gen.CalibrationPoint{X: int32(x), AutoCount: auto, BadCount: bad, AutoShare: float64(auto) / float64(len(labels))}
		if auto > 0 {
			p.BadShare = float64(bad) / float64(auto)
		}
		out.Points = append(out.Points, p)
	}
	return out
}

// GetRoleCalibration (GET /api/v1/agents/roles/{roleId}/calibration)
func (s *server) GetRoleCalibration(ctx context.Context, req gen.GetRoleCalibrationRequestObject) (gen.GetRoleCalibrationResponseObject, error) {
	q := store.New(s.deps.Pool)
	role, err := q.GetRoleByID(ctx, req.RoleId)
	if isNoRows(err) {
		return notFound(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("agents: read the role: %w", err)
	}
	rows, err := q.ListLabelledItems(ctx, store.ListLabelledItemsParams{RoleID: role.ID, Matured: s.deps.Clock().Add(-autoMatures), N: calibrationSample})
	if err != nil {
		return nil, fmt.Errorf("agents: read the labelled items: %w", err)
	}
	labels := make([]label, 0, len(rows))
	for _, r := range rows {
		labels = append(labels, labelOf(r.Score, r.HeldBy, r.State, r.EditRatio))
	}
	out := calibrate(labels, int(role.Threshold))
	out.RoleId = role.ID
	return gen.GetRoleCalibration200JSONResponse(out), nil
}
