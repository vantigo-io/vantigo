package agents_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/agents"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The trigger, the run queue, the claim and the lapse, the hand-off marker and
// the budget (virtual employees design V13, V14, V23, V24), against the module's
// own tables: Communications is not composed here, so the events it publishes are
// published by the test.

// rig is an installation with an active agent whose front desk role answers a
// channel.
type rig struct {
	h       *modtest.Harness
	owner   *modtest.Client
	agent   agent
	role    role
	channel uuid.UUID
}

func newRig(t *testing.T) *rig {
	t.Helper()
	h := newHarness(t)
	o := owner(t, h)
	setCompany(t, o, "Hansen Rør")
	a := createAgent(t, o, "Hanna")
	r := addRole(t, o, a.ID, "Front desk")
	ch := uuid.New()
	if resp := o.Do(http.MethodPut, rolePath(a, r)+"/channels", map[string]any{"channelIds": []string{ch.String()}}); resp.Status != http.StatusOK {
		t.Fatalf("bind the channel: %d %s", resp.Status, resp.Body)
	}
	return &rig{h: h, owner: o, agent: a, role: r, channel: ch}
}

// publish puts an event on the outbox and delivers it to the module's handler.
func (g *rig) publish(t *testing.T, ev contracts.Event) {
	t.Helper()
	ctx := context.Background()
	tx, err := g.h.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Publish(ctx, tx, "communications", ev, g.h.Now()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	d := events.NewDispatcher(g.h.Pool(), agents.Module().EventHandlers(g.h.Deps()), g.h.Deps().Logger, g.h.Now)
	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("deliver the event: %v", err)
	}
}

func (g *rig) message(t *testing.T, conv uuid.UUID, mutate func(*contracts.ConversationMessageReceived)) {
	t.Helper()
	e := contracts.ConversationMessageReceived{ConversationID: conv, MessageID: uuid.New(), ChannelID: g.channel, ChannelType: "email"}
	if mutate != nil {
		mutate(&e)
	}
	g.publish(t, e)
}

func (g *rig) agentUser(t *testing.T) uuid.UUID {
	t.Helper()
	return uuid.MustParse(g.agent.UserID)
}

type request struct {
	NotBefore, DueBy time.Time
	Reason           string
	Taken            bool
}

func (g *rig) requests(t *testing.T, conv uuid.UUID) []request {
	t.Helper()
	rows, err := g.h.Pool().Query(context.Background(),
		`SELECT not_before, due_by, reason, taken_at IS NOT NULL FROM agents.run_queue WHERE conversation_id = $1 ORDER BY id`, conv)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []request
	for rows.Next() {
		var r request
		if err := rows.Scan(&r.NotBefore, &r.DueBy, &r.Reason, &r.Taken); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func (g *rig) handedOff(t *testing.T, conv uuid.UUID) string {
	t.Helper()
	var reason string
	err := g.h.Pool().QueryRow(context.Background(), `SELECT reason FROM agents.handed_off WHERE conversation_id = $1`, conv).Scan(&reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return reason
}

func TestTrigger_AMessageOnABoundChannelMakesOneRequestTwentySecondsOn(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	conv := uuid.New()
	g.message(t, conv, nil)
	got := g.requests(t, conv)
	if len(got) != 1 || got[0].Reason != "message" || got[0].Taken {
		t.Fatalf("requests = %+v, want one untaken request for a message", got)
	}
	if d := got[0].NotBefore.Sub(g.h.Now()); d != 20*time.Second {
		t.Errorf("not_before is %s after the message, want 20s", d)
	}
	if d := got[0].DueBy.Sub(g.h.Now()); d != 2*time.Minute {
		t.Errorf("due_by is %s after the message, want 2m", d)
	}
}

func TestTrigger_ThreeShortMessagesMakeOneRunAndTheWaitNeverPassesDueBy(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	conv := uuid.New()
	g.message(t, conv, nil)
	first := g.requests(t, conv)[0]
	for range 3 {
		g.h.Advance(30 * time.Second)
		g.message(t, conv, nil)
	}
	got := g.requests(t, conv)
	if len(got) != 1 {
		t.Fatalf("requests = %d, want the messages coalesced into one", len(got))
	}
	if !got[0].DueBy.Equal(first.DueBy) {
		t.Errorf("due_by moved from %s to %s: a chatty customer could postpone the run for ever", first.DueBy, got[0].DueBy)
	}
	if got[0].NotBefore.After(got[0].DueBy) {
		t.Errorf("not_before %s is past due_by %s", got[0].NotBefore, got[0].DueBy)
	}
	g.h.Advance(time.Minute) // now 2m30s after the first message
	g.message(t, conv, nil)
	if got := g.requests(t, conv)[0]; got.NotBefore.After(got.DueBy) {
		t.Errorf("after the wait ran out, not_before %s is past due_by %s", got.NotBefore, got.DueBy)
	}
}

func TestTrigger_WhoIsLeftAlone(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	person := uuid.New()
	tests := []struct {
		name   string
		mutate func(e *contracts.ConversationMessageReceived)
		want   int
	}{
		{"an unbound channel", func(e *contracts.ConversationMessageReceived) { e.ChannelID = uuid.New() }, 0},
		{"a conversation a person has", func(e *contracts.ConversationMessageReceived) { e.AssignedUserID = &person }, 0},
		{"a conversation assigned to the agent", func(e *contracts.ConversationMessageReceived) { u := g.agentUser(t); e.AssignedUserID = &u }, 1},
		{"an auto-reply", func(e *contracts.ConversationMessageReceived) { e.AutoReply = true }, 0},
		{"an ordinary message", nil, 1},
	}
	for _, tt := range tests {
		conv := uuid.New()
		g.message(t, conv, tt.mutate)
		if got := len(g.requests(t, conv)); got != tt.want {
			t.Errorf("%s: %d requests, want %d", tt.name, got, tt.want)
		}
	}
}

func TestTrigger_APausedAgentIsNotWoken(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	if r := g.owner.Do(http.MethodPost, "/api/v1/agents/agents/"+g.agent.ID+"/pause", nil); r.Status != http.StatusOK {
		t.Fatalf("pause: %d %s", r.Status, r.Body)
	}
	conv := uuid.New()
	g.message(t, conv, nil)
	if n := len(g.requests(t, conv)); n != 0 {
		t.Errorf("a paused agent got %d requests", n)
	}
}

func TestTrigger_AnUnreadableMessageIsHandedOffAtOnce(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	conv := uuid.New()
	g.message(t, conv, func(e *contracts.ConversationMessageReceived) { e.Unreadable = true })
	if n := len(g.requests(t, conv)); n != 0 {
		t.Errorf("requests = %d, want none for an unreadable message", n)
	}
	if got := g.handedOff(t, conv); got != "unreadable" {
		t.Errorf("hand-off = %q, want unreadable", got)
	}
	var state string
	var outcome *string
	if err := g.h.Pool().QueryRow(context.Background(), `SELECT state, outcome FROM agents.runs WHERE conversation_id = $1`, conv).Scan(&state, &outcome); err != nil {
		t.Fatal(err)
	}
	if state != "handed_off" || outcome != nil {
		t.Errorf("run = %s/%v, want handed_off with its effects still to do (the API process does them)", state, outcome)
	}
}

// TestHandedOff_SuppressesTheTriggerUntilAPersonActsOrItIsGivenBack is V13.
func TestHandedOff_SuppressesTheTriggerUntilAPersonActsOrItIsGivenBack(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	person := uuid.New()
	conv := uuid.New()
	mark := func() {
		g.h.Exec(t, `INSERT INTO agents.handed_off (conversation_id, role_id, at, reason) VALUES ($1, $2, now(), 'hand_off')
		             ON CONFLICT (conversation_id) DO NOTHING`, conv, g.role.ID)
	}

	mark()
	g.message(t, conv, nil)
	if n := len(g.requests(t, conv)); n != 0 {
		t.Fatalf("a handed-off conversation got %d requests", n)
	}

	// The agent's own hand-off assigns the conversation: that is not a person's act.
	g.publish(t, contracts.ConversationAssigned{ConversationID: conv, ChannelID: g.channel, AssignedUserID: &person, ByUserID: g.agentUser(t)})
	if g.handedOff(t, conv) == "" {
		t.Fatal("the agent's own assignment cleared its hand-off")
	}
	// A reply the agent sent is not a person's either.
	g.publish(t, contracts.ConversationReplied{ConversationID: conv, MessageID: uuid.New(), ByUserID: g.agentUser(t), ByAgent: true})
	if g.handedOff(t, conv) == "" {
		t.Fatal("the agent's own reply cleared its hand-off")
	}

	// A person replying takes the conversation back.
	g.publish(t, contracts.ConversationReplied{ConversationID: conv, MessageID: uuid.New(), ByUserID: person})
	if g.handedOff(t, conv) != "" {
		t.Error("a person's reply left the hand-off in place")
	}
	g.message(t, conv, nil)
	if n := len(g.requests(t, conv)); n != 1 {
		t.Errorf("after the person replied, requests = %d, want 1", n)
	}

	// A person assigning it clears it too.
	other := uuid.New()
	g.h.Exec(t, `DELETE FROM agents.run_queue`)
	mark()
	g.publish(t, contracts.ConversationAssigned{ConversationID: conv, ChannelID: g.channel, AssignedUserID: &person, ByUserID: other})
	if g.handedOff(t, conv) != "" {
		t.Error("a person's assignment left the hand-off in place")
	}

	// Give back.
	mark()
	reader := g.h.SignIn(t, "communications:conversations-view")
	var hand struct {
		HandedOff bool    `json:"handedOff"`
		Reason    *string `json:"reason"`
	}
	if r := reader.Do(http.MethodGet, "/api/v1/agents/conversations/"+conv.String()+"/hand-off", nil); r.Status != http.StatusOK {
		t.Fatalf("read the hand-off: %d %s", r.Status, r.Body)
	} else {
		r.JSON(&hand)
	}
	if !hand.HandedOff || hand.Reason == nil || *hand.Reason != "hand_off" {
		t.Errorf("hand-off = %+v, want handed off for hand_off", hand)
	}
	if r := reader.Do(http.MethodDelete, "/api/v1/agents/conversations/"+conv.String()+"/hand-off", nil); r.Status != http.StatusForbidden {
		t.Errorf("giving back without conversations-manage = %d, want 403", r.Status)
	}
	manager := g.h.SignIn(t, "communications:conversations-manage")
	if r := manager.Do(http.MethodDelete, "/api/v1/agents/conversations/"+conv.String()+"/hand-off", nil); r.Status != http.StatusNoContent {
		t.Fatalf("give back: %d %s", r.Status, r.Body)
	}
	if g.handedOff(t, conv) != "" {
		t.Error("giving back left the marker")
	}
	if r := manager.Do(http.MethodDelete, "/api/v1/agents/conversations/"+conv.String()+"/hand-off", nil); r.Status != http.StatusNoContent {
		t.Errorf("giving back what is not handed off = %d, want 204", r.Status)
	}
	if r := reader.Do(http.MethodGet, "/api/v1/agents/conversations/"+conv.String()+"/hand-off", nil); r.Status == http.StatusOK {
		var h2 struct {
			HandedOff bool `json:"handedOff"`
		}
		r.JSON(&h2)
		if h2.HandedOff {
			t.Error("the hand-off is still read after giving back")
		}
	}
}

func TestTrigger_APersonAssigningTheConversationToTheAgentWakesIt(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	conv := uuid.New()
	me := uuid.New()
	agentUser := g.agentUser(t)
	g.publish(t, contracts.ConversationAssigned{ConversationID: conv, ChannelID: g.channel, AssignedUserID: &agentUser, ByUserID: me})
	got := g.requests(t, conv)
	if len(got) != 1 || got[0].Reason != "assigned" {
		t.Fatalf("requests = %+v, want one for the assignment", got)
	}
	// An assignment to someone else wakes nothing.
	conv2 := uuid.New()
	other := uuid.New()
	g.publish(t, contracts.ConversationAssigned{ConversationID: conv2, ChannelID: g.channel, AssignedUserID: &other, ByUserID: me})
	if n := len(g.requests(t, conv2)); n != 0 {
		t.Errorf("an assignment to a person made %d requests", n)
	}
}

func TestClaim_TakesADueRequestOnceAndOneRunPerConversation(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	conv := uuid.New()
	g.message(t, conv, nil)
	d := g.h.Deps()

	if id, err := agents.ClaimDue(d, uuid.New()); err != nil || id != uuid.Nil {
		t.Fatalf("a request still in its wait was claimed: %v %v", id, err)
	}
	g.h.Advance(21 * time.Second)
	id, err := agents.ClaimDue(d, uuid.New())
	if err != nil || id == uuid.Nil {
		t.Fatalf("claim = %v %v, want the run", id, err)
	}
	var state string
	var until time.Time
	if err := g.h.Pool().QueryRow(context.Background(), `SELECT state, claimed_until FROM agents.runs WHERE id = $1`, id).Scan(&state, &until); err != nil {
		t.Fatal(err)
	}
	if state != "running" || until.Sub(g.h.Now()) != agents.ReplicaClaimFor {
		t.Errorf("run = %s until %s, want running for %s", state, until, agents.ReplicaClaimFor)
	}
	if n := g.h.Count(t, `SELECT count(*) FROM agents.run_queue WHERE conversation_id = $1 AND taken_at IS NOT NULL AND run_id = $2`, conv, id); n != 1 {
		t.Errorf("the request was not marked taken by the run")
	}

	// Two messages while it runs: a new request waits, and does not start a second run.
	g.message(t, conv, nil)
	g.h.Advance(30 * time.Second)
	if id2, err := agents.ClaimDue(d, uuid.New()); err != nil || id2 != uuid.Nil {
		t.Errorf("a second run started on the conversation while one runs: %v %v", id2, err)
	}
	g.h.Exec(t, `UPDATE agents.runs SET state = 'done', ended_at = now() WHERE id = $1`, id)
	if id2, err := agents.ClaimDue(d, uuid.New()); err != nil || id2 == uuid.Nil {
		t.Errorf("the waiting request was not claimed after the run ended: %v %v", id2, err)
	}
}

func TestClaim_APausedAgentsRequestsStayQueued(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	conv := uuid.New()
	g.message(t, conv, nil)
	if r := g.owner.Do(http.MethodPost, "/api/v1/agents/agents/"+g.agent.ID+"/pause", nil); r.Status != http.StatusOK {
		t.Fatalf("pause: %d %s", r.Status, r.Body)
	}
	g.h.Advance(time.Minute)
	if id, _ := agents.ClaimDue(g.h.Deps(), uuid.New()); id != uuid.Nil {
		t.Error("a paused agent's request was claimed")
	}
	if n := len(g.requests(t, conv)); n != 1 {
		t.Errorf("requests = %d, want the queued one kept", n)
	}
}

// TestWorker_ALapsedClaimIsRequeuedOnceThenHandedOff is V14.
func TestWorker_ALapsedClaimIsRequeuedOnceThenHandedOff(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	conv := uuid.New()
	d := g.h.Deps()
	g.message(t, conv, nil)
	g.h.Advance(21 * time.Second)
	first, _ := agents.ClaimDue(d, uuid.New())

	if err := agents.WorkerCycle(d); err != nil {
		t.Fatal(err)
	}
	if st := runState(t, g, first); st != "running" {
		t.Fatalf("a run within its claim was taken over: %s", st)
	}
	g.h.Advance(agents.ReplicaClaimFor + time.Second)
	if err := agents.WorkerCycle(d); err != nil {
		t.Fatal(err)
	}
	if st := runState(t, g, first); st != "stopped" {
		t.Fatalf("the lapsed run is %s, want stopped", st)
	}
	retry := g.requests(t, conv)
	if last := retry[len(retry)-1]; last.Reason != "retry" || last.Taken {
		t.Fatalf("requests = %+v, want a retry waiting", retry)
	}
	if g.handedOff(t, conv) != "" {
		t.Fatal("the first lapse handed the conversation off")
	}

	second, _ := agents.ClaimDue(d, uuid.New())
	if second == uuid.Nil {
		t.Fatal("the retry was not claimed")
	}
	if n := g.h.Count(t, `SELECT attempt FROM agents.runs WHERE id = $1`, second); n != 2 {
		t.Errorf("attempt = %d, want 2", n)
	}
	g.h.Advance(agents.ReplicaClaimFor + time.Second)
	if err := agents.WorkerCycle(d); err != nil {
		t.Fatal(err)
	}
	if st := runState(t, g, second); st != "handed_off" {
		t.Errorf("the second lapse left the run %s, want handed_off", st)
	}
	if got := g.handedOff(t, conv); got != "lapsed" {
		t.Errorf("hand-off = %q, want lapsed", got)
	}
}

func runState(t *testing.T, g *rig, id uuid.UUID) string {
	t.Helper()
	return modtest.One[string](t, g.h, `SELECT state FROM agents.runs WHERE id = $1`, id)
}

func TestWorker_ARequestThatWaitedMoreThanADayIsHandedOff(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	conv := uuid.New()
	g.message(t, conv, nil)
	g.h.Advance(25 * time.Hour)
	if err := agents.WorkerCycle(g.h.Deps()); err != nil {
		t.Fatal(err)
	}
	if got := g.handedOff(t, conv); got != "stale" {
		t.Errorf("hand-off = %q, want stale", got)
	}
	if n := len(g.requests(t, conv)); n != 0 {
		t.Errorf("requests = %d, want it dropped", n)
	}
}

// TestBudget_AtTheBudgetNoNewRunStartsAndQueuedRequestsAreHandedOff is V24.
func TestBudget_AtTheBudgetNoNewRunStartsAndQueuedRequestsAreHandedOff(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	conv := uuid.New()
	g.message(t, conv, nil)
	body := personaBody("Hanna")
	body["dailyTokenBudget"] = 1000
	current := getAgent(t, g.owner, g.agent.ID)
	if r := g.owner.Do(http.MethodPut, "/api/v1/agents/agents/"+g.agent.ID, body, ifMatch(current.Version)); r.Status != http.StatusOK {
		t.Fatalf("set the budget: %d %s", r.Status, r.Body)
	}
	body["dailyTokenBudget"] = 999
	if r := g.owner.Do(http.MethodPut, "/api/v1/agents/agents/"+g.agent.ID, body, ifMatch(current.Version+1)); r.Status != http.StatusBadRequest {
		t.Fatalf("a budget under 1000 = %d, want 400", r.Status)
	}
	d := g.h.Deps()

	if err := agents.HandOffOverBudget(d); err != nil {
		t.Fatal(err)
	}
	if n := len(g.requests(t, conv)); n != 1 {
		t.Fatalf("under budget, requests = %d, want the request kept", n)
	}

	g.h.Exec(t, `INSERT INTO agents.usage_days (agent_id, day, input_tokens, output_tokens) VALUES ($1, $2::date, 900, 100)`, g.agent.ID, g.h.Now())
	if err := agents.HandOffOverBudget(d); err != nil {
		t.Fatal(err)
	}
	if n := len(g.requests(t, conv)); n != 0 {
		t.Errorf("at the budget, requests = %d, want them handed off and deleted", n)
	}
	if got := g.handedOff(t, conv); got != "budget" {
		t.Errorf("hand-off = %q, want budget", got)
	}
	var summary string
	if err := g.h.Pool().QueryRow(context.Background(), `SELECT summary FROM agents.runs WHERE conversation_id = $1`, conv).Scan(&summary); err != nil || summary != "Daily budget reached." {
		t.Errorf("note = %q (%v), want Daily budget reached.", summary, err)
	}
	// The next day the agent starts fresh, and only on new messages.
	g.h.Advance(24 * time.Hour)
	g.h.Exec(t, `DELETE FROM agents.handed_off`)
	conv2 := uuid.New()
	g.message(t, conv2, nil)
	g.h.Advance(time.Minute)
	if err := agents.HandOffOverBudget(d); err != nil {
		t.Fatal(err)
	}
	if n := len(g.requests(t, conv2)); n != 1 {
		t.Errorf("the next day: requests = %d, want the new one kept", n)
	}
}

func TestRoles_ComplaintTagsAreSavedAndKept(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	tag := uuid.NewString()
	body := roleBody(g.role)
	body["complaintTagIds"] = []string{tag, tag}
	r := g.owner.Do(http.MethodPut, rolePath(g.agent, g.role), body, ifMatch(g.role.Version))
	if r.Status != http.StatusOK {
		t.Fatalf("set the complaint tags: %d %s", r.Status, r.Body)
	}
	var got struct {
		Tags    []string `json:"complaintTagIds"`
		Version int      `json:"version"`
	}
	r.JSON(&got)
	if len(got.Tags) != 1 || got.Tags[0] != tag {
		t.Errorf("complaintTagIds = %v, want the tag once", got.Tags)
	}
	// Absent keeps them.
	body = roleBody(g.role)
	body["name"] = "Renamed"
	r = g.owner.Do(http.MethodPut, rolePath(g.agent, g.role), body, ifMatch(got.Version))
	if r.Status != http.StatusOK {
		t.Fatalf("rename: %d %s", r.Status, r.Body)
	}
	var kept struct {
		Tags []string `json:"complaintTagIds"`
	}
	r.JSON(&kept)
	if len(kept.Tags) != 1 {
		t.Errorf("complaintTagIds after a rename = %v, want kept", kept.Tags)
	}
	_ = json.Marshal
}
