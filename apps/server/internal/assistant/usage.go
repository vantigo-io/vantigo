package assistant

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/assistant/gen"
	"github.com/vantigo-io/vantigo/server/internal/assistant/store"
)

// Cost and limits (the Assistant design S14, S9): what a person used is
// counted per business day as it happens, in assistant.usage_days, so the
// day's token budget survives a person deleting a conversation and the Owner's
// usage view reads counts and never content.

const (
	codeBudgetExhausted = "budget_exhausted"

	// maxUsageDays is the most days the usage view answers at once.
	maxUsageDays = 92
	// defaultUsageDays is the range the view answers when it is given none.
	defaultUsageDays = 30
	// minDailyTokenBudget and maxDailyTokenBudget bound a budget the Owner sets;
	// 0 in a request removes it.
	minDailyTokenBudget = 1000
	maxDailyTokenBudget = 1_000_000_000
)

// businessDay is the civil date of t in the workspace's business time zone, as
// the UTC midnight a date column is compared at. The zone is read through the
// business calendar, which takes a pool connection of its own, so callers hold
// no connection of theirs. A calendar that cannot be read counts the day in UTC.
func (s *server) businessDay(ctx context.Context, t time.Time) time.Time {
	loc := time.UTC
	if s.deps.Calendar != nil {
		if zone, err := s.deps.Calendar.Zone(ctx); err == nil && zone != nil {
			loc = zone
		} else if err != nil {
			s.deps.Logger.Warn("assistant: read the business time zone", "error", err)
		}
	}
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// today is the business day of the module's clock.
func (s *server) today(ctx context.Context) time.Time { return s.businessDay(ctx, s.deps.Clock()) }

// count writes one usage count for the person on today's business day. A count
// that cannot be written is logged and never fails what it counts.
func (s *server) count(ctx context.Context, what string, write func(q *store.Queries, day time.Time) error) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := write(store.New(s.deps.Pool), s.today(cctx)); err != nil {
		s.deps.Logger.Error("assistant: count usage", "what", what, "error", err)
	}
}

func (s *server) countConversation(ctx context.Context, user uuid.UUID) {
	s.count(ctx, "conversation", func(q *store.Queries, day time.Time) error {
		return q.CountConversation(ctx, store.CountConversationParams{UserID: user, Day: day})
	})
}

func (s *server) countRun(ctx context.Context, user uuid.UUID) {
	s.count(ctx, "run", func(q *store.Queries, day time.Time) error {
		return q.CountRun(ctx, store.CountRunParams{UserID: user, Day: day})
	})
}

func (s *server) countTokens(ctx context.Context, user uuid.UUID, input, output *int32) {
	var in, out int64
	if input != nil {
		in = int64(*input)
	}
	if output != nil {
		out = int64(*output)
	}
	if in == 0 && out == 0 {
		return
	}
	s.count(ctx, "tokens", func(q *store.Queries, day time.Time) error {
		return q.CountTokens(ctx, store.CountTokensParams{UserID: user, Day: day, InputTokens: in, OutputTokens: out})
	})
}

func (s *server) countChange(ctx context.Context, user uuid.UUID, ok bool) {
	s.count(ctx, "change", func(q *store.Queries, day time.Time) error {
		return q.CountChange(ctx, store.CountChangeParams{UserID: user, Day: day, Ok: ok})
	})
}

// overBudget reports whether the person has used the Owner's daily token
// budget (S14): the tokens of today's business day, input and output, against
// the budget. No budget set is never over.
func (s *server) overBudget(ctx context.Context, user uuid.UUID) (bool, error) {
	settings, err := s.settings(ctx)
	if err != nil {
		return false, err
	}
	if settings.DailyTokenBudget == nil {
		return false, nil
	}
	used, err := store.New(s.deps.Pool).TokensOnDay(ctx, store.TokensOnDayParams{UserID: user, Day: s.today(ctx)})
	if err != nil {
		return false, fmt.Errorf("assistant: read the day's tokens: %w", err)
	}
	return used >= int64(*settings.DailyTokenBudget), nil
}

// GetAssistantUsage (GET /api/v1/assistant/usage)
//
// Per person and business day: the conversations begun, the runs, the model
// tokens, and the changes confirmed and failed. Names come from the user
// directory. It holds no content, no titles, and not what was changed.
func (s *server) GetAssistantUsage(ctx context.Context, req gen.GetAssistantUsageRequestObject) (gen.GetAssistantUsageResponseObject, error) {
	to := s.today(ctx)
	if req.Params.To != nil {
		to = civil(req.Params.To.Time)
	}
	from := to.AddDate(0, 0, -(defaultUsageDays - 1))
	if req.Params.From != nil {
		from = civil(req.Params.From.Time)
	}
	if from.After(to) {
		return invalid("The range is not valid", map[string][]string{"from": {"from is after to"}}), nil
	}
	if days := int(to.Sub(from).Hours()/24) + 1; days > maxUsageDays {
		return invalid("The range is too long", map[string][]string{"from": {fmt.Sprintf("the range is at most %d days", maxUsageDays)}}), nil
	}
	rows, err := store.New(s.deps.Pool).ListUsage(ctx, store.ListUsageParams{FromDay: from, ToDay: to})
	if err != nil {
		return nil, fmt.Errorf("assistant: read the usage: %w", err)
	}
	names := map[uuid.UUID]string{}
	if len(rows) > 0 && s.deps.Users != nil {
		seen := map[uuid.UUID]bool{}
		var ids []uuid.UUID
		for _, r := range rows {
			if !seen[r.UserID] {
				seen[r.UserID] = true
				ids = append(ids, r.UserID)
			}
		}
		for start := 0; start < len(ids); start += 200 {
			end := min(start+200, len(ids))
			users, err := s.deps.Users.Users(ctx, ids[start:end])
			if err != nil {
				return nil, fmt.Errorf("assistant: read the names: %w", err)
			}
			for _, u := range users {
				names[u.ID] = u.DisplayName
			}
		}
	}
	out := gen.AssistantUsage{From: openapi_types.Date{Time: from}, To: openapi_types.Date{Time: to}, Rows: make([]gen.AssistantUsageRow, 0, len(rows))}
	for _, r := range rows {
		out.Rows = append(out.Rows, gen.AssistantUsageRow{
			UserId: r.UserID, Name: names[r.UserID], Day: openapi_types.Date{Time: r.Day},
			Conversations: r.Conversations, Runs: r.Runs, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
			ConfirmedChanges: r.Confirmed, FailedChanges: r.Failed,
		})
	}
	return gen.GetAssistantUsage200JSONResponse(out), nil
}

// civil is the date of t as the UTC midnight a date column is compared at.
func civil(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
