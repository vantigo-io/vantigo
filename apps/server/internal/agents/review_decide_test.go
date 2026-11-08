package agents_test

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

const draft = "Your order ships tomorrow."

// finishHandOffs runs the runner's step with the dispatcher and the module's gate.
func (g *queueRig) finishHandOffs(t *testing.T) {
	t.Helper()
	d := g.h.AgentDeps(t)
	d.Operations.SetGate(contracts.GrantAgent, agents.GateFor(d))
	if err := agents.FinishHandOffs(d); err != nil {
		t.Fatal(err)
	}
}

func (g *queueRig) allNotes() string {
	g.comms.mu.Lock()
	defer g.comms.mu.Unlock()
	return strings.Join(g.comms.notes, "\n")
}

func (g *queueRig) reassigned() bool {
	for _, p := range g.comms.patched() {
		if strings.Contains(string(p), "assignedUserId") {
			return true
		}
	}
	return false
}

// A hand-off never takes a conversation from a person who has it, whichever path made it.
func TestHandOff_NeverTakesAConversationFromAPerson(t *testing.T) {
	t.Parallel()
	person := uuid.New()
	for name, run := range map[string]func(g *queueRig, rev interface{}, id uuid.UUID){
		"a reply withdrawn because a colleague took it": func(g *queueRig, _ interface{}, id uuid.UUID) {
			rev, _ := g.reviewer(t)
			if r := rev.Do(http.MethodPost, reviewPath(id, "/approve"), nil, ifMatch(1)); r.Status != http.StatusOK {
				t.Fatalf("approve: %d %s", r.Status, r.Body)
			}
		},
		"a change withdrawn because a colleague took it": func(g *queueRig, _ interface{}, id uuid.UUID) {
			rev, _ := g.reviewer(t)
			if r := rev.Do(http.MethodPost, reviewPath(id, "/approve"), nil, ifMatch(1)); r.Status != http.StatusOK {
				t.Fatalf("approve: %d %s", r.Status, r.Body)
			}
		},
		"an item that expired": func(g *queueRig, _ interface{}, id uuid.UUID) {
			g.h.Advance(25 * time.Hour)
			if err := agents.ReviewsCycle(g.h.Deps()); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			g := newQueueRig(t)
			var id uuid.UUID
			var conv *fakeConv
			if strings.Contains(name, "change") {
				id, conv = g.seed(t, "change", map[string]any{"operationId": "patchCommunicationsConversationsById", "summary": "Close it", "explain": "x"}, nil)
			} else {
				id, conv = g.seed(t, "reply", map[string]any{}, nil)
			}
			g.comms.update(conv.ID, func(c *fakeConv) { c.AssignedTo = &person })
			run(g, nil, id)
			g.finishHandOffs(t)
			if g.reassigned() {
				t.Errorf("the hand-off took the conversation from the person: %s", g.comms.patched())
			}
			notes := g.allNotes()
			if !strings.Contains(notes, "stopped working") || strings.Contains(notes, "to a colleague") {
				t.Errorf("the note should say the agent stopped, not that it handed over: %q", notes)
			}
		})
	}
}

// The agent's own assignment is still given up, to the hand-off person or the Inbox.
func TestHandOff_GivesUpTheAgentsOwnAssignment(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	id, conv := g.seed(t, "reply", map[string]any{}, nil)
	agentUser := uuid.MustParse(g.agent.UserID)
	g.comms.update(conv.ID, func(c *fakeConv) { c.AssignedTo = &agentUser })
	rev, _ := g.reviewer(t)
	if r := rev.Do(http.MethodPost, reviewPath(id, "/discard"), map[string]any{"reason": "other"}, ifMatch(1)); r.Status != http.StatusOK {
		t.Fatalf("discard: %d %s", r.Status, r.Body)
	}
	g.finishHandOffs(t)
	if !g.reassigned() {
		t.Error("the agent kept a conversation it handed off")
	}
	if !strings.Contains(g.allNotes(), "handed this conversation to a colleague") {
		t.Errorf("notes = %q", g.allNotes())
	}
}

// The note links the gated review item and never quotes the draft.
func TestHandOff_TheNoteNeverQuotesTheDraft(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	rev, _ := g.reviewer(t)
	discarded, _ := g.seed(t, "reply", map[string]any{}, nil)
	rev.Do(http.MethodPost, reviewPath(discarded, "/discard"), map[string]any{"reason": "wrong_tone"}, ifMatch(1))
	closed, convClosed := g.seed(t, "reply", map[string]any{}, nil)
	g.comms.update(convClosed.ID, func(c *fakeConv) { c.WindowState = "closed" })
	rev.Do(http.MethodPost, reviewPath(closed, "/approve"), nil, ifMatch(1))
	lapsing, _ := g.seed(t, "reply", map[string]any{}, nil)
	g.h.Advance(25 * time.Hour)
	if err := agents.ReviewsCycle(g.h.Deps()); err != nil {
		t.Fatal(err)
	}
	g.finishHandOffs(t)
	notes := g.allNotes()
	if strings.Contains(notes, draft) || strings.Contains(notes, "The agent wrote") {
		t.Errorf("a note quotes the draft: %q", notes)
	}
	for _, id := range []uuid.UUID{discarded, closed, lapsing} {
		if !strings.Contains(notes, "/agents/review/"+id.String()) {
			t.Errorf("no note links item %s: %q", id, notes)
		}
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.runs WHERE summary LIKE '%' || $1 || '%'`, draft); n != 0 {
		t.Errorf("a hand-off summary keeps the draft: %d", n)
	}
}

// Two reviewers approving a change act once; the loser is told it was decided.
func TestReview_ConcurrentApprovalsOfAChangeMakeItOnce(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	id, conv := g.seed(t, "change", map[string]any{
		"operationId": "patchCommunicationsConversationsById", "summary": "Close it", "explain": "x", "body": map[string]any{"status": "closed"},
	}, nil)
	g.h.Exec(t, `UPDATE agents.review_items SET item = jsonb_set(item, '{pathParams}', jsonb_build_object('id', $2::text)) WHERE id = $1`, id, conv.ID.String())
	var wg sync.WaitGroup
	statuses := make([]int, 6)
	for i := range statuses {
		c, _ := g.reviewer(t)
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = c.Do(http.MethodPost, reviewPath(id, "/approve"), nil, ifMatch(1)).Status
		}()
	}
	wg.Wait()
	ok, lost := 0, 0
	for _, s := range statuses {
		switch s {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			lost++
		default:
			t.Errorf("status %d", s)
		}
	}
	if ok != 1 || lost != len(statuses)-1 || len(g.comms.patched()) != 1 {
		t.Errorf("statuses %v, changes made %d: want one winner, the rest 409, one change", statuses, len(g.comms.patched()))
	}
}

func TestReview_AClaimedItemIsNeitherExpiredNorDiscardedUnderTheReviewerAndStaleClaimsReturn(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	rev, _ := g.reviewer(t)
	id, _ := g.seed(t, "reply", map[string]any{}, func(o *seedOpts) { o.Expires = g.h.Now().Add(time.Minute) })
	g.h.Exec(t, `UPDATE agents.review_items SET state = 'deciding', claimed_at = $2 WHERE id = $1`, id, g.h.Now())
	g.h.Advance(5 * time.Minute)
	if err := agents.ReviewsCycle(g.h.Deps()); err != nil {
		t.Fatal(err)
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.review_items WHERE id = $1 AND state = 'deciding'`, id); n != 1 {
		t.Fatal("the expiry worker touched an item being decided")
	}
	if r := rev.Do(http.MethodPost, reviewPath(id, "/discard"), map[string]any{"reason": "other"}, ifMatch(1)); r.Status != http.StatusConflict {
		t.Errorf("discard of an item being decided = %d, want 409", r.Status)
	}
	if it := readItem(t, rev, id); it.State != "waiting" {
		t.Errorf("an item being decided reads %q, want waiting", it.State)
	}
	g.h.Advance(11 * time.Minute) // past the lease: the claim was never finished
	if err := agents.ReviewsCycle(g.h.Deps()); err != nil {
		t.Fatal(err)
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.review_items WHERE id = $1 AND state = 'expired'`, id); n != 1 {
		t.Errorf("the stale claim did not go back and lapse")
	}
}

// A reply key used before with other text: finish when the agent's reply is there, else say so.
func TestReview_AKeyUsedBeforeIsFinishedOrExplained(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	rev, _ := g.reviewer(t)
	agentUser := uuid.MustParse(g.agent.UserID)

	id, conv := g.seed(t, "reply", map[string]any{}, nil)
	key := fmt.Sprintf("agents-%s-%s-%s", g.agent.ID, conv.ID, conv.Messages[0].ID)
	g.comms.update(conv.ID, func(c *fakeConv) {
		c.keys[key] = sentReply{Text: "an earlier text", MessageID: uuid.New()}
		c.Messages = append(c.Messages, fakeMsg{ID: uuid.New(), Text: "an earlier text", At: time.Now().AddDate(5, 0, 0), Author: &agentUser})
	})
	var it item
	r := rev.Do(http.MethodPost, reviewPath(id, "/send"), map[string]any{"text": "Edited"}, ifMatch(1))
	if r.Status != http.StatusOK {
		t.Fatalf("edit after a send that was made: %d %s", r.Status, r.Body)
	}
	r.JSON(&it)
	if it.State != "sent" || it.Outcome == nil || *it.Outcome != "already_sent" {
		t.Errorf("item = %+v, want sent as already_sent", it)
	}

	id2, conv2 := g.seed(t, "reply", map[string]any{}, nil)
	key2 := fmt.Sprintf("agents-%s-%s-%s", g.agent.ID, conv2.ID, conv2.Messages[0].ID)
	g.comms.update(conv2.ID, func(c *fakeConv) { c.keys[key2] = sentReply{Text: "an earlier text", MessageID: uuid.New()} })
	r = rev.Do(http.MethodPost, reviewPath(id2, "/send"), map[string]any{"text": "Edited"}, ifMatch(1))
	if r.Status != http.StatusConflict || problemCode(r) != "reply_key_reused" {
		t.Errorf("edit after an unseen earlier send = %d %s, want 409 reply_key_reused", r.Status, r.Body)
	}
	if got := readItem(t, rev, id2); got.State != "waiting" {
		t.Errorf("the claim was not given back: %q", got.State)
	}
}

func TestReview_TheDecidedFilterPagesOnTheServer(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	rev, _ := g.reviewer(t)
	g.seed(t, "reply", map[string]any{}, nil) // waiting
	for _, st := range []string{"sent", "discarded", "withdrawn", "expired", "edited_sent"} {
		g.seed(t, "reply", map[string]any{}, func(o *seedOpts) { o.State = st })
	}
	var list struct {
		Items []item `json:"items"`
		Total int    `json:"total"`
	}
	r := rev.Do(http.MethodGet, "/api/v1/agents/review?state=decided&limit=2&offset=2", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("decided: %d %s", r.Status, r.Body)
	}
	r.JSON(&list)
	if list.Total != 5 || len(list.Items) != 2 {
		t.Errorf("total %d, page %d: want 5 decided, a page of 2", list.Total, len(list.Items))
	}
	for _, it := range list.Items {
		if it.State == "waiting" {
			t.Errorf("a waiting item in the decided list")
		}
	}
}

func TestRetention_KeepsARunWithAnItemStillWaiting(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	old := g.oldRun(t, 400*24*time.Hour)
	g.h.Exec(t, `UPDATE agents.review_items SET state = 'waiting', decided_at = NULL, expires_at = now() + interval '1 day' WHERE run_id = $1`, old)
	if err := agents.RetentionCycle(g.h.Deps()); err != nil {
		t.Fatal(err)
	}
	if !g.runExists(t, old) {
		t.Error("retention deleted a run whose item still waits")
	}
}
