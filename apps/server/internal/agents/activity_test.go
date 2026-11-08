package agents_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents"
)

func TestUsage_ThirtyDaysWithZerosForTheDaysWithNothing(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	view := g.h.SignIn(t, "agents:view")
	today := g.h.Now().UTC().Format(time.DateOnly)
	threeAgo := g.h.Now().UTC().AddDate(0, 0, -3).Format(time.DateOnly)
	g.h.Exec(t, `INSERT INTO agents.usage_days (agent_id, day, runs, input_tokens, output_tokens, sent, held, discarded) VALUES
		($1, $2, 4, 1000, 200, 2, 1, 1), ($1, $3, 1, 50, 10, 1, 0, 0)`, g.agent.ID, today, threeAgo)

	var u struct {
		DailyTokenBudget *int `json:"dailyTokenBudget"`
		Days             []struct {
			Day          string `json:"day"`
			Runs         int    `json:"runs"`
			InputTokens  int    `json:"inputTokens"`
			OutputTokens int    `json:"outputTokens"`
			Sent         int    `json:"sent"`
			Held         int    `json:"held"`
			Discarded    int    `json:"discarded"`
		} `json:"days"`
	}
	r := view.Do(http.MethodGet, "/api/v1/agents/agents/"+g.agent.ID+"/usage", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("usage: %d %s", r.Status, r.Body)
	}
	r.JSON(&u)
	if len(u.Days) != 30 || u.Days[29].Day != today || u.Days[0].Day != g.h.Now().UTC().AddDate(0, 0, -29).Format(time.DateOnly) {
		t.Fatalf("days = %d from %s to %s, want the last 30 ending today", len(u.Days), u.Days[0].Day, u.Days[len(u.Days)-1].Day)
	}
	last := u.Days[29]
	if last.Runs != 4 || last.InputTokens != 1000 || last.OutputTokens != 200 || last.Sent != 2 || last.Held != 1 || last.Discarded != 1 {
		t.Errorf("today = %+v", last)
	}
	if d := u.Days[26]; d.Day != threeAgo || d.Runs != 1 {
		t.Errorf("three days ago = %+v", d)
	}
	if d := u.Days[10]; d.Runs != 0 || d.InputTokens != 0 {
		t.Errorf("a day with nothing = %+v, want zeros", d)
	}
	if r := view.Do(http.MethodGet, "/api/v1/agents/agents/"+g.agent.ID+"/usage?days=7", nil); r.Status != http.StatusOK {
		t.Errorf("usage for 7 days = %d", r.Status)
	} else {
		r.JSON(&u)
		if len(u.Days) != 7 {
			t.Errorf("days = %d, want 7", len(u.Days))
		}
	}
	if r := view.Do(http.MethodGet, "/api/v1/agents/agents/"+newID()+"/usage", nil); r.Status != http.StatusNotFound {
		t.Errorf("usage of an unknown agent = %d, want 404", r.Status)
	}
	if r := g.h.SignIn(t).Do(http.MethodGet, "/api/v1/agents/agents/"+g.agent.ID+"/usage", nil); r.Status != http.StatusForbidden {
		t.Errorf("usage without agents:view = %d, want 403", r.Status)
	}
}

func TestActivity_ARunWithItsTimelineVerdictAndTranscript(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	_, reviewerID := g.reviewer(t)
	id, _ := g.seed(t, "reply", map[string]any{}, nil)
	runID := runOf(t, g, id)
	g.h.Exec(t, `UPDATE agents.runs SET verdict = '{"mode":"review","threshold":85,"score":90,"modelScore":95,"sources":["getInvoicesById 2026-0931"],"held":["S4"],"refused":[],"sent":false}', tool_calls = 2, model_turns = 3, input_tokens = 120, output_tokens = 30 WHERE id = $1`, runID)
	g.h.Exec(t, `INSERT INTO agents.actions (run_id, agent_id, operation_id, ref, scoped, mode, status, http_status, duration_ms, approved_by, created_at)
	             VALUES ($1, $2, 'getInvoicesById', 'getInvoicesById 2026-0931', true, 'auto', 'ok', 200, 12, NULL, $4),
	                    ($1, $2, 'postCommunicationsConversationsByIdReply', 'postCommunicationsConversationsByIdReply', false, 'approved', 'ok', 202, 30, $3, $4)`,
		runID, g.agent.ID, reviewerID, g.h.Now())
	g.h.Exec(t, `INSERT INTO agents.run_messages (run_id, role, text, created_at) VALUES ($1, 'user', 'Where is my order?', $2), ($1, 'assistant', 'Let me look.', $2)`, runID, g.h.Now())

	activity := g.h.SignIn(t, append([]string{"agents:activity"}, agents.SeeKeys()...)...)
	var list struct {
		Runs []struct {
			ID         string `json:"id"`
			State      string `json:"state"`
			ToolCalls  int    `json:"toolCalls"`
			ModelTurns int    `json:"modelTurns"`
		} `json:"runs"`
		Total int `json:"total"`
	}
	r := activity.Do(http.MethodGet, "/api/v1/agents/agents/"+g.agent.ID+"/runs?state=waiting_review", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list the runs: %d %s", r.Status, r.Body)
	}
	r.JSON(&list)
	if list.Total != 1 || list.Runs[0].ID != runID || list.Runs[0].ToolCalls != 2 {
		t.Errorf("runs = %+v", list)
	}

	var detail struct {
		Run     struct{ ID, State string } `json:"run"`
		Verdict map[string]any             `json:"verdict"`
		Actions []struct {
			OperationID      string  `json:"operationId"`
			Scoped           bool    `json:"scoped"`
			ApprovedByUserID *string `json:"approvedByUserId"`
		} `json:"actions"`
		Items      []item `json:"items"`
		Transcript []struct {
			Role, Text string
		} `json:"transcript"`
	}
	r = activity.Do(http.MethodGet, "/api/v1/agents/runs/"+runID, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get the run: %d %s", r.Status, r.Body)
	}
	r.JSON(&detail)
	if detail.Verdict["mode"] != "review" || detail.Verdict["score"] != float64(90) {
		t.Errorf("verdict = %v", detail.Verdict)
	}
	if len(detail.Actions) != 2 || !detail.Actions[0].Scoped || detail.Actions[1].ApprovedByUserID == nil || *detail.Actions[1].ApprovedByUserID != reviewerID.String() {
		t.Errorf("actions = %+v, want the read and the approved send", detail.Actions)
	}
	if len(detail.Items) != 1 || detail.Items[0].ID != id.String() || detail.Items[0].Reply == nil {
		t.Errorf("items = %+v", detail.Items)
	}
	if len(detail.Transcript) != 2 || detail.Transcript[0].Text != "Where is my order?" {
		t.Errorf("transcript = %+v", detail.Transcript)
	}

	// A transcript holds customers' data: the permissions above are needed, all of them.
	reviewOnly := g.h.SignIn(t, append([]string{"agents:review"}, agents.SeeKeys()...)...)
	if r := reviewOnly.Do(http.MethodGet, "/api/v1/agents/runs/"+runID, nil); r.Status != http.StatusForbidden {
		t.Errorf("a run with agents:review but not agents:activity = %d, want 403", r.Status)
	}
	noSee := g.h.SignIn(t, "agents:activity")
	if r := noSee.Do(http.MethodGet, "/api/v1/agents/runs/"+runID, nil); r.Status != http.StatusForbidden || problemCode(r) != "reviewer_not_permitted" {
		t.Errorf("a run without what the agent read = %d %s, want 403 reviewer_not_permitted", r.Status, r.Body)
	}
	if r := noSee.Do(http.MethodGet, "/api/v1/agents/agents/"+g.agent.ID+"/runs", nil); r.Status != http.StatusForbidden {
		t.Errorf("the run list without what the agent read = %d, want 403", r.Status)
	}
	if r := activity.Do(http.MethodGet, "/api/v1/agents/runs/"+uuid.NewString(), nil); r.Status != http.StatusNotFound {
		t.Errorf("an unknown run = %d, want 404", r.Status)
	}
	if r := activity.Do(http.MethodGet, "/api/v1/agents/agents/"+newID()+"/runs", nil); r.Status != http.StatusNotFound {
		t.Errorf("the runs of an unknown agent = %d, want 404", r.Status)
	}
}

func runOf(t *testing.T, g *queueRig, itemID uuid.UUID) string {
	t.Helper()
	var run uuid.UUID
	if err := g.h.Pool().QueryRow(t.Context(), `SELECT run_id FROM agents.review_items WHERE id = $1`, itemID).Scan(&run); err != nil {
		t.Fatal(err)
	}
	return run.String()
}

func TestAudit_TheOwnersChangesAreListedWithWhoMadeThem(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	view := g.h.SignIn(t, "agents:view")
	body := roleBody(g.role)
	body["replyMode"] = "auto_above"
	if r := g.owner.Do(http.MethodPut, rolePath(g.agent, g.role), body, ifMatch(g.role.Version)); r.Status != http.StatusOK {
		t.Fatalf("change the mode: %d %s", r.Status, r.Body)
	}
	pb := personaBody("Hanna")
	pb["model"] = "other-model"
	if r := g.owner.Do(http.MethodPut, "/api/v1/agents/agents/"+g.agent.ID, pb, ifMatch(getAgent(t, g.owner, g.agent.ID).Version)); r.Status != http.StatusOK {
		t.Fatalf("change the model: %d %s", r.Status, r.Body)
	}
	var audit struct {
		Events []struct {
			Action      string         `json:"action"`
			ActorUserID string         `json:"actorUserId"`
			Before      map[string]any `json:"before"`
			After       map[string]any `json:"after"`
		} `json:"events"`
		Total int `json:"total"`
	}
	r := view.Do(http.MethodGet, "/api/v1/agents/audit?limit=100", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("audit: %d %s", r.Status, r.Body)
	}
	r.JSON(&audit)
	found := false
	for _, e := range audit.Events {
		if e.Action == "role.mode.changed" && e.Before["replyMode"] == "review" && e.After["replyMode"] == "auto_above" && e.ActorUserID != "" {
			found = true
		}
	}
	if !found || audit.Total < 5 {
		t.Errorf("the mode change is not in the audit (%d events): %+v", audit.Total, audit.Events)
	}
	// The level, the model, the settings and the agent are in it too.
	actions := map[string]bool{}
	for _, e := range audit.Events {
		actions[e.Action] = true
		if e.Action == "agent.changed" && (e.Before["model"] == e.After["model"] || e.After["model"] != "other-model") {
			t.Errorf("the model change is not recorded with before and after: %+v", e)
		}
	}
	for _, want := range []string{"agent.created", "agent.changed", "permission.changed", "role.created", "role.channels.changed", "settings.changed"} {
		if !actions[want] {
			t.Errorf("the audit has no %s: %v", want, actions)
		}
	}
	if r := g.h.SignIn(t).Do(http.MethodGet, "/api/v1/agents/audit", nil); r.Status != http.StatusForbidden {
		t.Errorf("the audit without agents:view = %d, want 403", r.Status)
	}
}
