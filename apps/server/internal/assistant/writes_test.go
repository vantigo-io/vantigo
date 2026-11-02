package assistant_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/assistant"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The proposed changes, their decisions, the daily budget and the usage view
// (the Assistant design S12, S14, S9). What needs a real write — a confirm
// operation of another module — is proved in internal/integration, where the
// modules are composed; here are the rules that do not: who may decide a card,
// what a decided one answers, how a waiting run ends, and the counts.

type pendingSeed struct {
	conv, run, action, toolMessage uuid.UUID
}

// seedPending writes what a run that proposed a change leaves behind: a
// conversation, a run awaiting_confirmation, the waiting tool result and the
// pending action.
func seedPending(t *testing.T, h *modtest.Harness, user uuid.UUID, status string, expires time.Time) pendingSeed {
	t.Helper()
	s := pendingSeed{conv: uuid.New(), run: uuid.New(), action: uuid.New(), toolMessage: uuid.New()}
	now := h.Now()
	h.Exec(t, `INSERT INTO assistant.conversations (id, user_id, title, origin_app, origin_route, created_at, last_activity_at)
	           VALUES ($1, $2, 'Rename it', 'projects', '/projects', $3, $3)`, s.conv, user, now)
	state := "awaiting_confirmation"
	if status != "pending" {
		state = "done"
	}
	h.Exec(t, `INSERT INTO assistant.runs (id, conversation_id, user_id, state, started_at) VALUES ($1, $2, $3, $4, $5)`, s.run, s.conv, user, state, now)
	h.Exec(t, `INSERT INTO assistant.messages (id, conversation_id, run_id, role, content, created_at)
	           VALUES ($1, $2, $3, 'tool', '[{"type":"tool_result","callId":"c1","name":"call_operation","status":"ok","content":"{}"}]', $4)`, s.toolMessage, s.conv, s.run, now)
	h.Exec(t, `INSERT INTO assistant.pending_actions (id, run_id, conversation_id, user_id, operation_id, call_id, tool_message_id, request, preview, status, created_at, expires_at)
	           VALUES ($1, $2, $3, $4, 'putThing', 'c1', $5,
	                   '{"operationId":"putThing","pathParams":{"id":"1"},"body":{"name":"New"},"fields":["name"]}',
	                   '{"summary":"Change a thing","method":"PUT","fields":[{"name":"name","current":"Old","next":"New"}]}',
	                   $6, $7, $8)`, s.action, s.run, s.conv, user, s.toolMessage, status, now, expires)
	return s
}

type problem struct {
	Code string `json:"code"`
}

func decide(c *modtest.Client, action uuid.UUID, verb string) (int, string) {
	r := c.Do(http.MethodPost, "/api/v1/assistant/pending-actions/"+action.String()+"/"+verb, nil)
	var p problem
	_ = json.Unmarshal(r.Body, &p)
	return r.Status, p.Code
}

func TestPending_AnotherPersonsActionIsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel())
	_, user := person(t, h)
	other, _ := person(t, h)
	seed := seedPending(t, h, user, "pending", h.Now().Add(time.Hour))

	for _, verb := range []string{"confirm", "cancel"} {
		if status, code := decide(other, seed.action, verb); status != http.StatusNotFound || code != "not_found" {
			t.Errorf("another person's %s = %d %s, want 404 not_found", verb, status, code)
		}
		if status, _ := decide(other, uuid.New(), verb); status != http.StatusNotFound {
			t.Errorf("%s of an action that does not exist = %d, want 404", verb, status)
		}
	}
	if got := modtest.One[string](t, h, `SELECT status FROM assistant.pending_actions WHERE id = $1`, seed.action); got != "pending" {
		t.Errorf("status = %s, another person decided the action", got)
	}
	// And the other person's conversation does not list it.
	if r := other.Do(http.MethodGet, "/api/v1/assistant/conversations/"+seed.conv.String(), nil); r.Status != http.StatusNotFound {
		t.Errorf("another person's conversation = %d, want 404", r.Status)
	}
}

func TestPending_ADecidedOrExpiredActionIsAConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel())
	c, user := person(t, h)

	for _, status := range []string{"confirmed", "cancelled", "failed"} {
		seed := seedPending(t, h, user, status, h.Now().Add(time.Hour))
		for _, verb := range []string{"confirm", "cancel"} {
			if got, code := decide(c, seed.action, verb); got != http.StatusConflict || code != "action_decided" {
				t.Errorf("%s of a %s action = %d %s, want 409 action_decided", verb, status, got, code)
			}
		}
	}
	// One that expired, written or not yet by the worker.
	written := seedPending(t, h, user, "expired", h.Now().Add(-time.Hour))
	waiting := seedPending(t, h, user, "pending", h.Now().Add(-time.Minute))
	for _, seed := range []pendingSeed{written, waiting} {
		for _, verb := range []string{"confirm", "cancel"} {
			if got, code := decide(c, seed.action, verb); got != http.StatusConflict || code != "action_expired" {
				t.Errorf("%s of an expired action = %d %s, want 409 action_expired", verb, got, code)
			}
		}
	}
	// The waiting card reads as expired in the conversation before the worker runs.
	var d struct {
		PendingActions []struct {
			Status string `json:"status"`
		} `json:"pendingActions"`
	}
	r := c.Do(http.MethodGet, "/api/v1/assistant/conversations/"+waiting.conv.String(), nil)
	r.JSON(&d)
	if len(d.PendingActions) != 1 || d.PendingActions[0].Status != "expired" {
		t.Errorf("pendingActions = %+v, want one that reads expired", d.PendingActions)
	}
}

func TestPending_TheConversationShowsTheCardAndBlocksAnotherMessage(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel())
	c, user := person(t, h)
	seed := seedPending(t, h, user, "pending", h.Now().Add(time.Hour))

	var d struct {
		ActiveRun *struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"activeRun"`
		PendingActions []struct {
			ID      string `json:"id"`
			Status  string `json:"status"`
			Summary string `json:"summary"`
			Fields  []struct {
				Name    string `json:"name"`
				Current string `json:"current"`
				Next    string `json:"next"`
			} `json:"fields"`
		} `json:"pendingActions"`
	}
	c.Do(http.MethodGet, "/api/v1/assistant/conversations/"+seed.conv.String(), nil).JSON(&d)
	if d.ActiveRun == nil || d.ActiveRun.State != "awaiting_confirmation" {
		t.Fatalf("activeRun = %+v, want the run that waits", d.ActiveRun)
	}
	if len(d.PendingActions) != 1 || d.PendingActions[0].Status != "pending" || d.PendingActions[0].Summary != "Change a thing" ||
		d.PendingActions[0].Fields[0].Current != "Old" || d.PendingActions[0].Fields[0].Next != "New" {
		t.Errorf("pendingActions = %+v", d.PendingActions)
	}

	// One run per conversation, and a waiting run is one.
	r := ask(t, c, seed.conv.String(), "While it waits")
	var p problem
	r.JSON(&p)
	if r.Status != http.StatusConflict || p.Code != "run_in_progress" {
		t.Errorf("a message while a change waits = %d %s, want 409 run_in_progress", r.Status, r.Body)
	}

	// Stopping the run cancels the change it proposed, and ends the run.
	if r := c.Do(http.MethodPost, "/api/v1/assistant/runs/"+d.ActiveRun.ID+"/stop", nil); r.Status != http.StatusOK {
		t.Fatalf("stop: %d %s", r.Status, r.Body)
	}
	if got := modtest.One[string](t, h, `SELECT status FROM assistant.pending_actions WHERE id = $1`, seed.action); got != "cancelled" {
		t.Errorf("status after the stop = %s, want cancelled", got)
	}
	if got := modtest.One[string](t, h, `SELECT state FROM assistant.runs WHERE id = $1`, seed.run); got != "stopped" {
		t.Errorf("run after the stop = %s, want stopped", got)
	}
	if got := modtest.One[string](t, h, `SELECT request::text FROM assistant.pending_actions WHERE id = $1`, seed.action); strings.Contains(got, "New") || strings.Contains(got, `"body"`) {
		t.Errorf("a decided action kept its request: %s", got)
	}
	if got := modtest.One[string](t, h, `SELECT preview::text FROM assistant.pending_actions WHERE id = $1`, seed.action); strings.Contains(got, "Old") || strings.Contains(got, `"next"`) || !strings.Contains(got, `"name": "name"`) {
		t.Errorf("a decided action kept its values or lost its field names: %s", got)
	}
	// The conversation is free again.
	if r := ask(t, c, seed.conv.String(), "Now?"); r.Status != http.StatusOK {
		t.Errorf("a message after the stop = %d %s", r.Status, r.Body)
	}
}

func TestPending_TheWorkerExpiresWhatNobodyDecidedOnAPoolOfOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel(), modtest.WithPoolMaxConns(1))
	_, user := person(t, h)
	old := seedPending(t, h, user, "pending", h.Now().Add(-time.Minute))
	fresh := seedPending(t, h, user, "pending", h.Now().Add(time.Hour))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	n, err := assistant.StaleRunsCycle(ctx, h.Deps())
	if err != nil || n != 1 {
		t.Fatalf("StaleRunsCycle on a pool of one = %d, %v, want the one run it ended and no error", n, err)
	}
	if got := modtest.One[string](t, h, `SELECT status FROM assistant.pending_actions WHERE id = $1`, old.action); got != "expired" {
		t.Errorf("old action = %s, want expired", got)
	}
	if got := modtest.One[string](t, h, `SELECT state FROM assistant.runs WHERE id = $1`, old.run); got != "expired" {
		t.Errorf("old run = %s, want expired", got)
	}
	if got := modtest.One[string](t, h, `SELECT request::text FROM assistant.pending_actions WHERE id = $1`, old.action); strings.Contains(got, `"body"`) {
		t.Errorf("an expired action kept its request body: %s", got)
	}
	if got := modtest.One[string](t, h, `SELECT status FROM assistant.pending_actions WHERE id = $1`, fresh.action); got != "pending" {
		t.Errorf("a card still inside its day = %s, want pending", got)
	}
	if got := modtest.One[string](t, h, `SELECT state FROM assistant.runs WHERE id = $1`, fresh.run); got != "awaiting_confirmation" {
		t.Errorf("its run = %s, want awaiting_confirmation", got)
	}
}

func TestPending_AClaimedButUnrecordedConfirmationIsClosedAsInterrupted(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel())
	_, user := person(t, h)
	seed := seedPending(t, h, user, "pending", h.Now().Add(time.Hour))
	h.Exec(t, `UPDATE assistant.pending_actions SET status = 'confirmed', decided_at = $2 WHERE id = $1`, seed.action, h.Now().Add(-time.Hour))
	h.Exec(t, `UPDATE assistant.runs SET state = 'running', started_at = $2 WHERE id = $1`, seed.run, h.Now().Add(-time.Hour))

	if _, err := assistant.StaleRunsCycle(context.Background(), h.Deps()); err != nil {
		t.Fatal(err)
	}
	if got := modtest.One[string](t, h, `SELECT status || ':' || (outcome->>'reason') FROM assistant.pending_actions WHERE id = $1`, seed.action); got != "failed:interrupted" {
		t.Errorf("action = %s, want failed:interrupted", got)
	}
	if got := modtest.One[string](t, h, `SELECT request::text FROM assistant.pending_actions WHERE id = $1`, seed.action); strings.Contains(got, `"body"`) {
		t.Errorf("an interrupted action kept its request body: %s", got)
	}
}

func TestPending_ErasureAndRetentionTakeThePendingActionsToo(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel())
	_, living := person(t, h)
	gone := uuid.New()
	seedPending(t, h, living, "pending", h.Now().Add(time.Hour))
	seedPending(t, h, gone, "pending", h.Now().Add(time.Hour))
	h.Exec(t, `INSERT INTO assistant.usage_days (user_id, day, runs) VALUES ($1, $2, 3)`, gone, h.Now())

	if _, erased, err := assistant.RetentionCycle(context.Background(), h.Deps()); err != nil || erased != 1 {
		t.Fatalf("RetentionCycle erased = %d, %v, want the one erased person", erased, err)
	}
	for _, table := range []string{"pending_actions", "usage_days"} {
		if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.`+table+` WHERE user_id = $1`, gone); n != 0 {
			t.Errorf("%d rows of the erased person left in %s", n, table)
		}
	}
	if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.pending_actions WHERE user_id = $1`, living); n != 1 {
		t.Errorf("the living person's action = %d rows, want 1", n)
	}

	// A conversation that ages out takes its actions with it.
	old := seedPending(t, h, living, "pending", h.Now().Add(time.Hour))
	h.Exec(t, `UPDATE assistant.conversations SET last_activity_at = $2 WHERE id = $1`, old.conv, h.Now().AddDate(0, -13, 0))
	if expired, _, err := assistant.RetentionCycle(context.Background(), h.Deps()); err != nil || expired != 1 {
		t.Fatalf("RetentionCycle expired = %d, %v, want 1", expired, err)
	}
	if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.pending_actions WHERE id = $1`, old.action); n != 0 {
		t.Error("the pending action outlived its conversation")
	}
}

// --- the daily token budget (S14) ---

func today(h *modtest.Harness) time.Time {
	y, m, d := h.Now().UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func setBudget(t *testing.T, o *modtest.Client, version int, budget int) {
	t.Helper()
	r := o.Do(http.MethodPut, "/api/v1/assistant/settings", map[string]any{"dailyTokenBudget": budget}, ifMatch(version))
	if r.Status != http.StatusOK {
		t.Fatalf("set the budget to %d: %d %s", budget, r.Status, r.Body)
	}
}

func TestBudget_TheOwnerSetsAndClearsIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel())
	o := owner(t, h)

	var s struct {
		DailyTokenBudget *int `json:"dailyTokenBudget"`
		Version          int  `json:"version"`
	}
	o.Do(http.MethodGet, "/api/v1/assistant/settings", nil).JSON(&s)
	if s.DailyTokenBudget != nil {
		t.Fatalf("default budget = %d, want none", *s.DailyTokenBudget)
	}
	setBudget(t, o, s.Version, 50000)
	o.Do(http.MethodGet, "/api/v1/assistant/settings", nil).JSON(&s)
	if s.DailyTokenBudget == nil || *s.DailyTokenBudget != 50000 {
		t.Fatalf("budget = %v, want 50000", s.DailyTokenBudget)
	}
	// Left out, it is kept; the change is audited with before and after.
	if r := o.Do(http.MethodPut, "/api/v1/assistant/settings", map[string]any{"retentionMonths": 6}, ifMatch(s.Version)); r.Status != http.StatusOK {
		t.Fatalf("change the retention: %d %s", r.Status, r.Body)
	}
	if got := modtest.One[string](t, h, `SELECT after->>'dailyTokenBudget' FROM assistant.events ORDER BY id DESC LIMIT 1`); got != "50000" {
		t.Errorf("the budget in the audit = %s, want it kept", got)
	}
	if got := modtest.One[string](t, h, `SELECT jsonb_typeof(before->'dailyTokenBudget') FROM assistant.events WHERE action = 'settings.changed' ORDER BY id LIMIT 1`); got != "null" {
		t.Errorf("the budget before is a %s, want null: no limit", got)
	}
	o.Do(http.MethodGet, "/api/v1/assistant/settings", nil).JSON(&s)
	setBudget(t, o, s.Version, 0)
	s.DailyTokenBudget = nil // a key left out of the answer is not reset by decoding into the old value
	o.Do(http.MethodGet, "/api/v1/assistant/settings", nil).JSON(&s)
	if s.DailyTokenBudget != nil {
		t.Errorf("budget after 0 = %d, want none", *s.DailyTokenBudget)
	}
	for _, bad := range []int{1, 999, 2_000_000_000} {
		if r := o.Do(http.MethodPut, "/api/v1/assistant/settings", map[string]any{"dailyTokenBudget": bad}, ifMatch(s.Version), modtest.SkipContract("a budget out of range on purpose")); r.Status != http.StatusBadRequest {
			t.Errorf("budget %d = %d, want 400", bad, r.Status)
		}
	}
}

func TestBudget_ARunIsRefusedAtTheStartOnceTheDayIsUsed(t *testing.T) {
	t.Parallel()
	model := modtest.NewFakeChatModel()
	h := newHarness(t, model)
	o := owner(t, h)
	c, user := person(t, h)
	setBudget(t, o, 1, 1000)
	conv := startConversation(t, c)

	// Yesterday's tokens do not count against today.
	h.Exec(t, `INSERT INTO assistant.usage_days (user_id, day, input_tokens, output_tokens) VALUES ($1, $2, 5000, 5000)`, user, today(h).AddDate(0, 0, -1))
	if r := ask(t, c, conv.ID, "Yesterday was heavy"); r.Status != http.StatusOK {
		t.Fatalf("with yesterday's tokens used: %d %s", r.Status, r.Body)
	}

	h.Exec(t, `INSERT INTO assistant.usage_days (user_id, day, input_tokens, output_tokens) VALUES ($1, $2, 900, 100)
	           ON CONFLICT (user_id, day) DO UPDATE SET input_tokens = 900, output_tokens = 100`, user, today(h))
	before := modtest.One[int](t, h, `SELECT count(*) FROM assistant.runs`)
	r := ask(t, c, conv.ID, "One more")
	var p problem
	r.JSON(&p)
	if r.Status != http.StatusTooManyRequests || p.Code != "budget_exhausted" {
		t.Fatalf("a run over the budget = %d %s, want 429 budget_exhausted", r.Status, r.Body)
	}
	if after := modtest.One[int](t, h, `SELECT count(*) FROM assistant.runs`); after != before {
		t.Errorf("the refused message started a run (%d -> %d)", before, after)
	}
	// Someone else has their own budget.
	other, _ := person(t, h)
	if r := ask(t, other, startConversation(t, other).ID, "Mine"); r.Status != http.StatusOK {
		t.Errorf("another person with their own budget = %d %s", r.Status, r.Body)
	}
	// No limit again.
	setBudget(t, o, 2, 0)
	if r := ask(t, c, conv.ID, "Now without a limit"); r.Status != http.StatusOK {
		t.Errorf("after the limit was removed = %d %s", r.Status, r.Body)
	}
}

func TestBudget_ARunThatUsesTheDayMidWayEndsWithTheCode(t *testing.T) {
	t.Parallel()
	model := modtest.NewFakeChatModel(
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{{ID: "c1", Name: "search_docs", Args: map[string]any{"query": "invoice"}}}},
		modtest.ChatTurn{Text: "Never said."},
	)
	h := newHarness(t, model)
	o := owner(t, h)
	c, user := person(t, h)
	setBudget(t, o, 1, 1000)
	h.Exec(t, `INSERT INTO assistant.usage_days (user_id, day, input_tokens) VALUES ($1, $2, 900)`, user, today(h))
	conv := startConversation(t, c)

	r := ask(t, c, conv.ID, "Tell me about invoices")
	if r.Status != http.StatusOK {
		t.Fatalf("send: %d %s", r.Status, r.Body)
	}
	events := parseStream(r.Body)
	if last := events[len(events)-1]; last.Name != "error" || !strings.Contains(string(last.Data), "budget_exhausted") {
		t.Errorf("last event = %s %s, want error budget_exhausted", last.Name, last.Data)
	}
	if state, code := modtest.One[string](t, h, `SELECT state FROM assistant.runs`), modtest.One[string](t, h, `SELECT error_code FROM assistant.runs`); state != "failed" || code != "budget_exhausted" {
		t.Errorf("run = %s %s, want failed budget_exhausted", state, code)
	}
	if n := len(model.Requests()); n != 1 {
		t.Errorf("%d model calls, want only the one before the budget ran out", n)
	}
	if got := modtest.One[int](t, h, `SELECT input_tokens FROM assistant.usage_days WHERE user_id = $1 AND day = $2`, user, today(h)); got != 1000 {
		t.Errorf("input tokens counted = %d, want 900 + the 100 of the call", got)
	}
	if d := getConversation(t, c, conv.ID); d.LastRun == nil || d.LastRun.ErrorCode != "budget_exhausted" {
		t.Errorf("lastRun = %+v, want failed budget_exhausted", d.LastRun)
	}
}

// --- the Owner's usage view (S9) ---

func TestUsage_CountsPerPersonAndDayAndNeverContent(t *testing.T) {
	t.Parallel()
	model := modtest.NewFakeChatModel(modtest.ChatTurn{Text: "The reply."})
	h := newHarness(t, model)
	o := owner(t, h)
	c, user := person(t, h)
	conv := startConversation(t, c)
	if r := ask(t, c, conv.ID, "SECRETWORD about a customer"); r.Status != http.StatusOK {
		t.Fatalf("send: %d %s", r.Status, r.Body)
	}
	h.Exec(t, `INSERT INTO assistant.usage_days (user_id, day, confirmed, failed) VALUES ($1, $2, 2, 1)
	           ON CONFLICT (user_id, day) DO UPDATE SET confirmed = 2, failed = 1`, user, today(h))

	r := o.Do(http.MethodGet, "/api/v1/assistant/usage", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("usage: %d %s", r.Status, r.Body)
	}
	var u struct {
		From string `json:"from"`
		To   string `json:"to"`
		Rows []struct {
			UserID           string `json:"userId"`
			Name             string `json:"name"`
			Day              string `json:"day"`
			Conversations    int    `json:"conversations"`
			Runs             int    `json:"runs"`
			InputTokens      int    `json:"inputTokens"`
			OutputTokens     int    `json:"outputTokens"`
			ConfirmedChanges int    `json:"confirmedChanges"`
			FailedChanges    int    `json:"failedChanges"`
		} `json:"rows"`
	}
	r.JSON(&u)
	day := today(h).Format("2006-01-02")
	if u.To != day || u.From != today(h).AddDate(0, 0, -29).Format("2006-01-02") {
		t.Errorf("range = %s..%s, want the 30 days to %s", u.From, u.To, day)
	}
	var mine = -1
	for i, row := range u.Rows {
		if row.UserID == user.String() {
			mine = i
		}
	}
	if mine < 0 {
		t.Fatalf("rows = %+v, want the person's", u.Rows)
	}
	row := u.Rows[mine]
	if row.Day != day || row.Conversations != 1 || row.Runs != 1 || row.InputTokens != 100 || row.OutputTokens != 20 || row.ConfirmedChanges != 2 || row.FailedChanges != 1 || row.Name == "" {
		t.Errorf("row = %+v, want 1 conversation, 1 run, 100/20 tokens, 2 confirmed, 1 failed and a name", row)
	}
	for _, secret := range []string{"SECRETWORD", "The reply.", "title", "text"} {
		if strings.Contains(string(r.Body), secret) {
			t.Errorf("the usage view holds %q: it must never show content", secret)
		}
	}

	// Only the Owner; at most 92 days; from not after to.
	if r := c.Do(http.MethodGet, "/api/v1/assistant/usage", nil); r.Status != http.StatusForbidden {
		t.Errorf("a person's usage view = %d, want 403", r.Status)
	}
	to := today(h)
	if r := o.Do(http.MethodGet, "/api/v1/assistant/usage?from="+to.AddDate(0, 0, -91).Format("2006-01-02")+"&to="+day, nil); r.Status != http.StatusOK {
		t.Errorf("92 days = %d %s, want 200", r.Status, r.Body)
	}
	if r := o.Do(http.MethodGet, "/api/v1/assistant/usage?from="+to.AddDate(0, 0, -92).Format("2006-01-02")+"&to="+day, nil); r.Status != http.StatusBadRequest {
		t.Errorf("93 days = %d, want 400", r.Status)
	}
	if r := o.Do(http.MethodGet, "/api/v1/assistant/usage?from="+day+"&to="+to.AddDate(0, 0, -1).Format("2006-01-02"), nil); r.Status != http.StatusBadRequest {
		t.Errorf("from after to = %d, want 400", r.Status)
	}
	// Deleting the conversation does not give the day back.
	if r := c.Do(http.MethodDelete, "/api/v1/assistant/conversations/"+conv.ID, nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d", r.Status)
	}
	if got := modtest.One[int](t, h, `SELECT input_tokens FROM assistant.usage_days WHERE user_id = $1 AND day = $2`, user, today(h)); got != 100 {
		t.Errorf("tokens after the delete = %d, want the day's count kept", got)
	}
}

func TestPending_CancelWritesNothingAndTheModelAnswersInOneLineWithoutTools(t *testing.T) {
	t.Parallel()
	model := modtest.NewFakeChatModel(modtest.ChatTurn{Text: "Understood, I left it as it is."})
	h := newHarness(t, model)
	c, user := person(t, h)
	seed := seedPending(t, h, user, "pending", h.Now().Add(time.Hour))

	r := c.Do(http.MethodPost, "/api/v1/assistant/pending-actions/"+seed.action.String()+"/cancel", nil)
	if r.Status != http.StatusOK || r.Header("Content-Type") != "text/event-stream" {
		t.Fatalf("cancel: %d %s %s", r.Status, r.Header("Content-Type"), r.Body)
	}
	events := parseStream(r.Body)
	got := names(events)
	if got[0] != "run" || got[1] != "confirmation" || got[len(got)-1] != "done" {
		t.Fatalf("events = %v, want run, confirmation … done", got)
	}
	if !strings.Contains(string(events[1].Data), `"cancelled"`) {
		t.Errorf("the decided card = %s, want it cancelled", events[1].Data)
	}

	if st := modtest.One[string](t, h, `SELECT status FROM assistant.pending_actions WHERE id = $1`, seed.action); st != "cancelled" {
		t.Errorf("status = %s, want cancelled", st)
	}
	if st := modtest.One[string](t, h, `SELECT state FROM assistant.runs WHERE id = $1`, seed.run); st != "done" {
		t.Errorf("run = %s, want done", st)
	}
	// The model was told the person cancelled, was offered no tools, and answered once.
	reqs := model.Requests()
	if len(reqs) != 1 || len(reqs[0].Tools) != 0 {
		t.Fatalf("model calls = %+v, want one without tools", reqs)
	}
	var sawCancelled bool
	for _, m := range reqs[0].Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "cancelled") {
			sawCancelled = true
		}
	}
	if !sawCancelled {
		t.Errorf("the model was not told the person cancelled: %+v", reqs[0].Messages)
	}
	d := getConversation(t, c, seed.conv.String())
	if last := d.Messages[len(d.Messages)-1]; last.Role != "assistant" || last.Parts[0].Text != "Understood, I left it as it is." {
		t.Errorf("the answer = %+v", last)
	}
	// The grant of the resumed run ended with it, and cancelling again is a conflict.
	if live := modtest.One[int](t, h, `SELECT count(*) FROM identity.assistant_grants WHERE revoked_at IS NULL`); live != 0 {
		t.Errorf("%d grants still live", live)
	}
	if status, code := decide(c, seed.action, "cancel"); status != http.StatusConflict || code != "action_decided" {
		t.Errorf("a second cancel = %d %s, want 409 action_decided", status, code)
	}
}
