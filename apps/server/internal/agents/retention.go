package agents

// Retention (virtual employees design V26): a run, with its transcript, actions
// and review items, is kept for the workspace's retention_months and no longer
// than Communications keeps its messages, because a transcript copies message
// bodies and must not outlive them. A retention longer than Communications' is
// lowered when it is saved and every pass of the agents-retention worker. The
// notes an agent remembers (V27) and the suggestions the weekly check made (V28)
// expire with the runs they came from. The day's usage holds counts only and is
// kept for 24 months.

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

const (
	retentionWorkerName     = "agents-retention"
	retentionWorkerInterval = time.Hour
	// usageMonths is how long the day's usage is kept.
	usageMonths = 24
	// retentionBatch is how many runs one delete takes, and retentionPasses how
	// many deletes a pass makes, so a first pass over a long history is spread
	// over the passes that follow.
	retentionBatch  = 500
	retentionPasses = 100
	// defaultCommunicationsRetentionDays is Communications' own default, for an
	// installation whose configuration is not at hand.
	defaultCommunicationsRetentionDays = 365
	daysPerMonth                       = 30
)

// communicationsRetentionDays is how long Communications keeps its messages
// (COMMUNICATIONS_RETENTION_DAYS).
func (s *server) communicationsRetentionDays() int {
	if s.deps.Config != nil && s.deps.Config.CommunicationsRetentionDays > 0 {
		return s.deps.Config.CommunicationsRetentionDays
	}
	return defaultCommunicationsRetentionDays
}

// retentionCapMonths is the longest retention in months that does not outlive
// Communications' messages: whole 30-day months, at least 1 and at most 60.
func (s *server) retentionCapMonths() int {
	return min(max(s.communicationsRetentionDays()/daysPerMonth, minRetentionMonths), maxRetentionMonths)
}

// retentionCutoff is the moment before which a run is deleted: the retention, and
// never older than Communications' messages are.
func (s *server) retentionCutoff(now time.Time, months int) time.Time {
	byMonths := now.AddDate(0, -months, 0)
	byMessages := now.AddDate(0, 0, -s.communicationsRetentionDays())
	if byMessages.After(byMonths) {
		return byMessages
	}
	return byMonths
}

type retentionWorker struct{ s *server }

func (w *retentionWorker) Name() string            { return retentionWorkerName }
func (w *retentionWorker) Interval() time.Duration { return retentionWorkerInterval }

func (w *retentionWorker) Run(ctx context.Context) error {
	t := time.NewTicker(retentionWorkerInterval)
	defer t.Stop()
	for {
		if err := w.cycle(ctx); err != nil && ctx.Err() == nil {
			w.s.warn("agents: the retention worker failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// cycle lowers a retention that outlives Communications', then deletes what is
// older than it.
func (w *retentionWorker) cycle(ctx context.Context) error {
	s := w.s
	now := s.deps.Clock()
	q := store.New(s.deps.Pool)
	cfg, err := q.GetSettings(ctx)
	if err != nil {
		return fmt.Errorf("agents: read the settings: %w", err)
	}
	months := int(cfg.RetentionMonths)
	if limit := s.retentionCapMonths(); months > limit {
		err := withTx(ctx, s.deps.Pool, func(q *store.Queries) error {
			lowered, err := q.LowerRetention(ctx, store.LowerRetentionParams{Months: int16(limit), Now: now})
			if isNoRows(err) {
				return nil // another replica did
			}
			if err != nil {
				return err
			}
			// The system made this change, not a person: the nil user says so.
			return s.audit(ctx, q, uuid.Nil, "settings.retention_lowered", "", settingsSnapshot(cfg), settingsSnapshot(lowered))
		})
		if err != nil {
			return fmt.Errorf("agents: lower the retention: %w", err)
		}
		months = limit
	}

	cutoff := s.retentionCutoff(now, months)
	for range retentionPasses {
		n, err := q.DeleteRunsBefore(ctx, store.DeleteRunsBeforeParams{Cutoff: cutoff, N: retentionBatch})
		if err != nil {
			return fmt.Errorf("agents: delete old runs: %w", err)
		}
		if n < retentionBatch {
			break
		}
	}
	if _, err := q.DeleteHandedOffBefore(ctx, cutoff); err != nil {
		return fmt.Errorf("agents: delete old hand-off markers: %w", err)
	}
	if _, err := q.DeleteTakenRequestsBefore(ctx, cutoff); err != nil {
		return fmt.Errorf("agents: delete old run requests: %w", err)
	}
	if _, err := q.DeleteSeenSendersBefore(ctx, cutoff); err != nil {
		return fmt.Errorf("agents: delete old senders: %w", err)
	}
	if _, err := q.DeleteConversationLinksBefore(ctx, cutoff); err != nil {
		return fmt.Errorf("agents: delete old conversation links: %w", err)
	}
	if _, err := q.DeleteMemoryBefore(ctx, cutoff); err != nil {
		return fmt.Errorf("agents: delete old notes: %w", err)
	}
	if _, err := q.DeleteSuggestionsBefore(ctx, cutoff); err != nil {
		return fmt.Errorf("agents: delete old suggestions: %w", err)
	}
	if _, err := q.DeleteUsageBefore(ctx, s.day(now).AddDate(0, -usageMonths, 0)); err != nil {
		return fmt.Errorf("agents: delete old usage: %w", err)
	}
	return nil
}

var _ worker.Worker = (*retentionWorker)(nil)
