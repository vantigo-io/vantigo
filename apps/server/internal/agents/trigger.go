package agents

// The trigger (virtual employees design V13; module boundaries rule 23): the
// agents.trigger handler of the domain-events outbox receives what Communications
// publishes about a conversation and, when an agent should answer it, writes a
// run request into agents.run_queue. It runs on the delivery's own transaction,
// writes only this module's schema, reads no contract and leaves the process
// never; the API process's runner takes the request later.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

const triggerHandler = "agents.trigger"

// The waits of V13: a request runs 20 seconds after the newest message, and never
// later than 2 minutes after the first.
const (
	debounce   = 20 * time.Second
	maxWait    = 2 * time.Minute
	staleAfter = 24 * time.Hour
	// answeredOverWindow is how long after an Auto reply a person's reply in the
	// same conversation answers it over (V21).
	answeredOverWindow = 24 * time.Hour
)

// eventHandlers is the module's handlers (module.Module.EventHandlers), built
// from what a disabled module's Deps has too.
func eventHandlers(d module.Deps) []events.Handler {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	t := &trigger{clock: d.Clock, logger: logger, enabled: func() bool {
		return d.Config == nil || slices.Contains(d.Config.Modules, "agents")
	}}
	return []events.Handler{{
		Name: triggerHandler,
		Events: []events.EventRef{
			{Name: contracts.ConversationMessageReceived{}.EventName(), Version: 1},
			{Name: contracts.ConversationAssigned{}.EventName(), Version: 1},
			{Name: contracts.ConversationReplied{}.EventName(), Version: 1},
			{Name: contracts.QuoteSent{}.EventName(), Version: 1},
			{Name: contracts.QuoteAccepted{}.EventName(), Version: 1},
			{Name: contracts.WeekUnsubmitted{}.EventName(), Version: 1},
		},
		Handle: t.handle,
	}}
}

type trigger struct {
	clock   func() time.Time
	logger  *slog.Logger
	enabled func() bool
}

func (t *trigger) handle(ctx context.Context, tx pgx.Tx, ev events.Delivered) error {
	if !t.enabled() {
		return nil
	}
	q := store.New(tx)
	switch ev.Name {
	case contracts.ConversationMessageReceived{}.EventName():
		var e contracts.ConversationMessageReceived
		if err := json.Unmarshal(ev.Payload, &e); err != nil {
			return fmt.Errorf("agents: decode event %d: %w", ev.ID, err)
		}
		return t.messageReceived(ctx, q, e)
	case contracts.ConversationAssigned{}.EventName():
		var e contracts.ConversationAssigned
		if err := json.Unmarshal(ev.Payload, &e); err != nil {
			return fmt.Errorf("agents: decode event %d: %w", ev.ID, err)
		}
		return t.assigned(ctx, q, e)
	case contracts.QuoteSent{}.EventName():
		var e contracts.QuoteSent
		if err := json.Unmarshal(ev.Payload, &e); err != nil {
			return fmt.Errorf("agents: decode event %d: %w", ev.ID, err)
		}
		return t.quoteSent(ctx, q, e)
	case contracts.QuoteAccepted{}.EventName():
		var e contracts.QuoteAccepted
		if err := json.Unmarshal(ev.Payload, &e); err != nil {
			return fmt.Errorf("agents: decode event %d: %w", ev.ID, err)
		}
		// What waited to look at the quote again is moot.
		return q.DeleteWakeupsOfSubject(ctx, store.DeleteWakeupsOfSubjectParams{SubjectType: subjectQuote, SubjectID: fmt.Sprint(e.QuoteID)})
	case contracts.WeekUnsubmitted{}.EventName():
		var e contracts.WeekUnsubmitted
		if err := json.Unmarshal(ev.Payload, &e); err != nil {
			return fmt.Errorf("agents: decode event %d: %w", ev.ID, err)
		}
		return t.weekUnsubmitted(ctx, q, e)
	case contracts.ConversationReplied{}.EventName():
		var e contracts.ConversationReplied
		if err := json.Unmarshal(ev.Payload, &e); err != nil {
			return fmt.Errorf("agents: decode event %d: %w", ev.ID, err)
		}
		// A person answering takes the conversation back from the hand-off.
		if !e.ByAgent {
			if _, err := q.DeleteHandedOff(ctx, e.ConversationID); err != nil {
				return err
			}
			// And answers over an Auto reply the agent sent in the last 24 hours (V21).
			now := ev.OccurredAt
			if now.IsZero() {
				now = t.clock()
			}
			_, err := q.MarkAnsweredOver(ctx, store.MarkAnsweredOverParams{ConversationID: e.ConversationID, Now: now, Since: now.Add(-answeredOverWindow)})
			return err
		}
	}
	return nil
}

// answers is the role that answers a channel's conversation, when one should: the
// channel is bound to a role of an active agent, and the conversation is
// unassigned or assigned to that agent.
func (t *trigger) answers(ctx context.Context, q *store.Queries, channelID uuid.UUID, assignedUserID *uuid.UUID) (store.RoleOfChannelRow, bool, error) {
	role, err := q.RoleOfChannel(ctx, channelID)
	if isNoRows(err) {
		return role, false, nil
	}
	if err != nil {
		return role, false, fmt.Errorf("agents: find the role of a channel: %w", err)
	}
	if role.AgentState != stateActive {
		return role, false, nil
	}
	// Only a template that answers conversations is woken by a message: a reply to
	// a conversation the sales follow-up started waits in the Inbox for a person.
	if tpl, ok := templates[role.Template]; !ok || !tpl.answersConversations() {
		return role, false, nil
	}
	if assignedUserID != nil && *assignedUserID != role.AgentUserID {
		return role, false, nil
	}
	return role, true, nil
}

func (t *trigger) messageReceived(ctx context.Context, q *store.Queries, e contracts.ConversationMessageReceived) error {
	role, ok, err := t.answers(ctx, q, e.ChannelID, e.AssignedUserID)
	if err != nil || !ok {
		return err
	}
	if _, err := q.GetHandedOff(ctx, e.ConversationID); err == nil {
		return nil
	} else if !isNoRows(err) {
		return fmt.Errorf("agents: read the hand-off: %w", err)
	}
	if e.AutoReply {
		return nil
	}
	now := t.clock()
	if e.Unreadable {
		return handOffAtOnce(ctx, q, role, e.ConversationID, "unreadable", "An unreadable message arrived: the import could not read it.", now)
	}
	return q.UpsertRunRequest(ctx, store.UpsertRunRequestParams{
		RoleID: role.RoleID, ConversationID: e.ConversationID, Reason: reasonMessage,
		NotBefore: now.Add(debounce), DueBy: now.Add(maxWait), Now: now,
	})
}

func (t *trigger) assigned(ctx context.Context, q *store.Queries, e contracts.ConversationAssigned) error {
	// The agent's own hand-off assigns the conversation; it is not a person's act.
	if _, err := q.GetAgentByUser(ctx, e.ByUserID); err == nil {
		return nil
	} else if !isNoRows(err) {
		return fmt.Errorf("agents: read the assigner: %w", err)
	}
	if _, err := q.DeleteHandedOff(ctx, e.ConversationID); err != nil {
		return fmt.Errorf("agents: clear the hand-off: %w", err)
	}
	if e.AssignedUserID == nil {
		return nil
	}
	// A person's assignment to the agent wakes it, on its own channel.
	role, ok, err := t.answers(ctx, q, e.ChannelID, e.AssignedUserID)
	if err != nil || !ok || role.AgentUserID != *e.AssignedUserID {
		return err
	}
	now := t.clock()
	return q.UpsertRunRequest(ctx, store.UpsertRunRequestParams{
		RoleID: role.RoleID, ConversationID: e.ConversationID, Reason: reasonAssigned,
		NotBefore: now, DueBy: now.Add(maxWait), Now: now,
	})
}

// quoteSent wakes each sales follow-up role about a quote N days after it was sent
// (V30): the wakeup is written now, for the role's wait, and the worker turns it
// into a run when it comes due. A quote revised and sent again moves it. The
// customer is the event's, which Orders published; no message names it.
func (t *trigger) quoteSent(ctx context.Context, q *store.Queries, e contracts.QuoteSent) error {
	roles, err := q.ListRolesOfTemplate(ctx, templateSalesFollowup)
	if err != nil {
		return fmt.Errorf("agents: find the sales follow-up roles: %w", err)
	}
	now := t.clock()
	customer := e.CustomerID
	for _, r := range roles {
		if err := q.UpsertWakeup(ctx, store.UpsertWakeupParams{
			ID: uuid.New(), RoleID: r.ID, SubjectType: subjectQuote, SubjectID: fmt.Sprint(e.QuoteID), CustomerID: &customer,
			At: e.SentAt.Add(time.Duration(r.WaitDays) * 24 * time.Hour), Reason: "The quote was sent " + fmt.Sprint(r.WaitDays) + " days ago.", Now: now,
		}); err != nil {
			return fmt.Errorf("agents: wake the sales follow-up: %w", err)
		}
	}
	return nil
}

// weekUnsubmitted makes a run request for each timesheet reminder role: one run
// about one person's week. A paused agent's request waits, and lapses after a day.
func (t *trigger) weekUnsubmitted(ctx context.Context, q *store.Queries, e contracts.WeekUnsubmitted) error {
	roles, err := q.ListRolesOfTemplate(ctx, templateTimesheetReminders)
	if err != nil {
		return fmt.Errorf("agents: find the timesheet reminder roles: %w", err)
	}
	now := t.clock()
	for _, r := range roles {
		if err := q.InsertSubjectRequest(ctx, store.InsertSubjectRequestParams{
			RoleID: r.ID, SubjectType: subjectPersonWeek, SubjectID: e.AggregateID(), Reason: reasonEvent,
			Note: "Hours registered in the week: " + e.Hours + ".", Now: now,
		}); err != nil {
			return fmt.Errorf("agents: queue a timesheet reminder: %w", err)
		}
	}
	return nil
}
