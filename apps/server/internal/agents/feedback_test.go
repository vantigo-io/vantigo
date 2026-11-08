package agents_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents"
	"github.com/vantigo-io/vantigo/server/internal/ai"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// Feedback (virtual employees design V28): what the people corrected becomes, once
// a week, at most three proposed edits of a role's instructions, which the Owner
// applies or dismisses; and an Auto reply a person answered over is a label (V21).

// feedbackModel is the check model of a test of the weekly check: it answers the
// rubric of the content check with a clean verdict and the weekly check with the
// answer the test sets, and keeps what the weekly check was shown.
type feedbackModel struct {
	mu     sync.Mutex
	answer string
	shown  []string
	calls  int
}

func (f *feedbackModel) model() *modtest.FakeChatModel {
	return modtest.NewFakeChatModelFunc(func(_ int, req modtest.ChatRequest) modtest.ChatTurn {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, m := range req.Messages {
			if m.Role == "user" && strings.HasPrefix(m.Content, "CURRENT INSTRUCTIONS:") {
				f.calls++
				f.shown = append(f.shown, m.Content)
				return modtest.ChatTurn{Text: f.answer}
			}
		}
		return modtest.ChatTurn{Text: checkClean}
	})
}

func (f *feedbackModel) set(answer string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answer = answer
}

func (f *feedbackModel) seen() (int, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, strings.Join(f.shown, "\n")
}

// feedbackRig is a queue rig whose check model is a feedbackModel, with a role
// whose instructions the suggestions edit.
type feedbackRig struct {
	*queueRig
	fm *feedbackModel
}

const startingInstructions = "Be brief.\n\nNever promise a delivery date."

func newFeedbackRig(t *testing.T) *feedbackRig {
	t.Helper()
	fm := &feedbackModel{answer: `{"suggestions": []}`}
	h, comms, check := newHarnessWith(t, modtest.WithTransport(fm.model()))
	o := owner(t, h)
	setCompany(t, o, "Hansen Rør")
	a := createAgent(t, o, "Hanna")
	r := addRole(t, o, a.ID, "Front desk")
	body := roleBody(r)
	body["instructions"] = startingInstructions
	if resp := o.Do(http.MethodPut, rolePath(a, r), body, ifMatch(r.Version)); resp.Status != http.StatusOK {
		t.Fatalf("set the instructions: %d %s", resp.Status, resp.Body)
	}
	resp := o.Do(http.MethodGet, "/api/v1/agents/agents/"+a.ID, nil)
	var fresh agent
	resp.JSON(&fresh)
	ch := uuid.New()
	return &feedbackRig{queueRig: &queueRig{rig: &rig{h: h, owner: o, agent: fresh, role: fresh.Roles[0], channel: ch}, comms: comms, check: check}, fm: fm}
}

// correct seeds a corrected reply: an edit by ratio with the agent's own text kept, or a
// discard with its reason.
func (g *feedbackRig) correct(t *testing.T, state, reason string) uuid.UUID {
	t.Helper()
	ratio := 0.4
	id, _ := g.seed(t, "reply", map[string]any{
		"text": "Hei Kari, den kommer i morgen.", "agentText": "Hei Kari, den kommer i morgen klokken ni.",
	}, func(o *seedOpts) {
		o.State = state
		if state == "edited_sent" {
			o.EditRatio = &ratio
		}
	})
	if state == "discarded" {
		g.h.Exec(t, `UPDATE agents.review_items SET discard_reason = $2, discard_text = 'Too chatty' WHERE id = $1`, id, reason)
	}
	return id
}

// fixedModel is the one model of the installation, for every task.
type fixedModel struct{}

func (fixedModel) ModelFor(context.Context, string) (string, error) { return "test-model", nil }

// deps are the installation's Deps with the AI client, which the API process's runners
// have from Compose and a test's harness does not hand out.
func (g *feedbackRig) deps() module.Deps {
	d := g.h.Deps()
	d.AI = ai.New(d.Config, d.HTTPTransport, fixedModel{})
	return d
}

func (g *feedbackRig) suggestions(t *testing.T) []suggestion {
	t.Helper()
	r := g.owner.Do(http.MethodGet, "/api/v1/agents/roles/"+g.role.ID+"/suggestions", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list the suggestions: %d %s", r.Status, r.Body)
	}
	var out struct {
		Items []suggestion `json:"items"`
	}
	r.JSON(&out)
	return out.Items
}

type suggestion struct {
	ID        string   `json:"id"`
	OldText   *string  `json:"oldText"`
	NewText   string   `json:"newText"`
	Rationale string   `json:"rationale"`
	ItemIDs   []string `json:"itemIds"`
	State     string   `json:"state"`
}

func answerOf(suggestions ...map[string]any) string {
	raw, _ := json.Marshal(map[string]any{"suggestions": suggestions})
	return "Here you are: " + string(raw)
}

func TestFeedbackWeekStart_IsMondaySixInTheBusinessZone(t *testing.T) {
	t.Parallel()
	oslo, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Skip("no time zone data")
	}
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, oslo)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct{ now, want string }{
		{"2026-11-09 05:59", "2026-11-02 06:00"}, // Monday before six: still last week's
		{"2026-11-09 06:00", "2026-11-09 06:00"},
		{"2026-11-11 15:30", "2026-11-09 06:00"}, // Wednesday
		{"2026-11-15 23:59", "2026-11-09 06:00"}, // Sunday
		{"2026-11-08 12:00", "2026-11-02 06:00"}, // Sunday of the week before
	} {
		if got := agents.FeedbackWeekStart(at(c.now), oslo); !got.Equal(at(c.want)) {
			t.Errorf("week start at %s = %s, want %s", c.now, got.In(oslo).Format("2006-01-02 15:04"), c.want)
		}
	}
}

func TestFeedback_TheWeeklyCheckProposesAtMostThreeEditsThatRestOnTheCorrections(t *testing.T) {
	t.Parallel()
	g := newFeedbackRig(t)
	e1, e2, e3 := g.correct(t, "edited_sent", ""), g.correct(t, "edited_sent", ""), g.correct(t, "edited_sent", "")
	d1, d2 := g.correct(t, "discarded", "wrong_tone"), g.correct(t, "discarded", "wrong_tone")
	untouched, _ := g.seed(t, "reply", map[string]any{}, func(o *seedOpts) { o.State = "sent" }) // a reply sent as it was is no correction
	ids := func(v ...uuid.UUID) []string {
		out := make([]string, len(v))
		for i := range v {
			out[i] = v[i].String()
		}
		return out
	}
	good := func(text string, items ...uuid.UUID) map[string]any {
		return map[string]any{"oldText": nil, "newText": text, "rationale": "People shortened the replies.", "items": ids(items...)}
	}
	g.fm.set(answerOf(
		good("Do not give a time of day.", e1, e2),
		map[string]any{"oldText": "Be brief.", "newText": "Answer in two sentences.", "rationale": "Too chatty.", "items": ids(d1, d2)},
		map[string]any{"oldText": "A passage that is not there", "newText": "x", "rationale": "y", "items": ids(e1, e2)}, // dropped: not in the instructions
		good("Rests on one item only.", e3), // dropped: one correction is no pattern
		map[string]any{"oldText": nil, "newText": "Rests on items that were never shown.", "rationale": "y", "items": ids(uuid.New(), uuid.New())},
		good("A third that fits.", e1, d1),
		good("A fourth, beyond the limit.", e2, e3),
	))

	if err := agents.FeedbackCycle(g.deps()); err != nil {
		t.Fatal(err)
	}
	got := g.suggestions(t)
	if len(got) != 3 {
		t.Fatalf("suggestions = %+v, want the three that hold up", got)
	}
	texts := map[string]suggestion{}
	for _, s := range got {
		texts[s.NewText] = s
		if s.State != "proposed" || len(s.ItemIDs) < 2 {
			t.Errorf("suggestion %+v, want proposed and resting on two or more items", s)
		}
	}
	for _, want := range []string{"Do not give a time of day.", "Answer in two sentences.", "A third that fits."} {
		if _, ok := texts[want]; !ok {
			t.Errorf("no suggestion %q in %v", want, got)
		}
	}
	if s := texts["Answer in two sentences."]; s.OldText == nil || *s.OldText != "Be brief." {
		t.Errorf("the replacing suggestion = %+v, want its passage", s)
	}

	// What the model was shown: the reasons and the texts before and after, never an id of a
	// conversation, a customer or a person.
	calls, shown := g.fm.seen()
	if calls != 1 {
		t.Fatalf("the model was asked %d times, want once", calls)
	}
	for _, want := range []string{"wrong_tone", "Too chatty", "klokken ni", startingInstructions, e1.String(), d1.String()} {
		if !strings.Contains(shown, want) {
			t.Errorf("the model was not shown %q", want)
		}
	}
	for _, never := range []string{untouched.String(), g.agent.UserID, g.channel.String(), "conversationId", "customerId"} {
		if strings.Contains(shown, never) {
			t.Errorf("the model was shown %q", never)
		}
	}

	// The week is claimed: a second pass, and a pass on Friday, ask nothing.
	if err := agents.FeedbackCycle(g.deps()); err != nil {
		t.Fatal(err)
	}
	g.h.Advance(24 * time.Hour)
	if err := agents.FeedbackCycle(g.deps()); err != nil {
		t.Fatal(err)
	}
	if calls, _ := g.fm.seen(); calls != 1 {
		t.Errorf("the model was asked %d times in a week, want once", calls)
	}
	// Nothing changed the instructions by itself.
	if got := modtest.One[string](t, g.h, `SELECT instructions FROM agents.roles WHERE id = $1`, g.role.ID); got != startingInstructions {
		t.Errorf("instructions = %q, want them untouched", got)
	}
}

func TestFeedback_ANextWeekChecksOnlyWhatCameSinceAndNeedsThreeCorrections(t *testing.T) {
	t.Parallel()
	g := newFeedbackRig(t)
	g.correct(t, "edited_sent", "")
	g.correct(t, "edited_sent", "")
	if err := agents.FeedbackCycle(g.deps()); err != nil {
		t.Fatal(err)
	}
	if calls, _ := g.fm.seen(); calls != 0 {
		t.Fatalf("two corrections made %d calls, want none: a pattern is not one or two mistakes", calls)
	}
	// The week is not spent on a check that was not made: the third correction lets it run.
	e3 := g.correct(t, "edited_sent", "")
	g.fm.set(answerOf(map[string]any{"oldText": nil, "newText": "Be exact.", "rationale": "r", "items": []string{e3.String(), g.correct(t, "edited_sent", "").String()}}))
	if err := agents.FeedbackCycle(g.deps()); err != nil {
		t.Fatal(err)
	}
	if calls, _ := g.fm.seen(); calls != 1 || len(g.suggestions(t)) != 1 {
		t.Fatalf("calls = %d, suggestions = %d, want one of each once there are enough corrections", calls, len(g.suggestions(t)))
	}
	// Next week the same corrections are not read again.
	g.h.Advance(8 * 24 * time.Hour)
	if err := agents.FeedbackCycle(g.deps()); err != nil {
		t.Fatal(err)
	}
	if calls, _ := g.fm.seen(); calls != 1 {
		t.Errorf("calls = %d, want the corrections already read left alone", calls)
	}
}

func TestFeedback_ApplyingIsAnOrdinaryRoleEditAndDismissingChangesNothing(t *testing.T) {
	t.Parallel()
	g := newFeedbackRig(t)
	id := func(sql string, args ...any) string {
		i := uuid.NewString()
		g.h.Exec(t, sql, append([]any{i, g.role.ID}, args...)...)
		return i
	}
	items := []uuid.UUID{uuid.New(), uuid.New()}
	appendID := id(`INSERT INTO agents.instruction_suggestions (id, role_id, new_text, rationale, item_ids, created_at) VALUES ($1, $2, 'Offer a callback.', 'r', $3, now())`, items)
	replaceID := id(`INSERT INTO agents.instruction_suggestions (id, role_id, old_text, new_text, rationale, item_ids, created_at) VALUES ($1, $2, 'Be brief.', 'Answer in two sentences.', 'r', $3, now())`, items)
	staleID := id(`INSERT INTO agents.instruction_suggestions (id, role_id, old_text, new_text, rationale, item_ids, created_at) VALUES ($1, $2, 'Gone passage', 'x', 'r', $3, now())`, items)
	dismissID := id(`INSERT INTO agents.instruction_suggestions (id, role_id, new_text, rationale, item_ids, created_at) VALUES ($1, $2, 'Never used.', 'r', $3, now())`, items)
	apply := func(c *modtest.Client, sid string, version any) *modtest.Response {
		return c.Do(http.MethodPost, "/api/v1/agents/suggestions/"+sid+"/apply", nil, ifMatch(version))
	}
	role := func() role { return getAgent(t, g.owner, g.agent.ID).Roles[0] }
	start := role()

	// agents:manage is what instructions need (A1); view alone is not enough.
	if r := apply(g.h.SignIn(t, "agents:view"), appendID, start.Version); r.Status != http.StatusForbidden {
		t.Errorf("apply with agents:view = %d, want 403", r.Status)
	}
	if r := g.h.SignIn(t, "agents:view").Do(http.MethodGet, "/api/v1/agents/roles/"+g.role.ID+"/suggestions", nil); r.Status != http.StatusForbidden {
		t.Errorf("list with agents:view = %d, want 403: they quote the corrections", r.Status)
	}
	m := manager(t, g.h)
	if r := apply(m, appendID, start.Version+7); r.Status != http.StatusConflict || problemCode(r) != "version_conflict" {
		t.Errorf("apply at a stale version = %d %s, want 409 version_conflict", r.Status, r.Body)
	}
	if r := apply(m, appendID, "x"); r.Status != http.StatusBadRequest {
		t.Errorf("apply with a bad If-Match = %d, want 400", r.Status)
	}
	if r := apply(m, uuid.NewString(), start.Version); r.Status != http.StatusNotFound {
		t.Errorf("an unknown suggestion = %d, want 404", r.Status)
	}
	if r := apply(m, staleID, start.Version); r.Status != http.StatusConflict || problemCode(r) != "suggestion_stale" {
		t.Errorf("apply a suggestion whose passage is gone = %d %s, want 409 suggestion_stale", r.Status, r.Body)
	}
	if role().Version != start.Version || role().Instructions != startingInstructions {
		t.Fatal("a refused apply changed the role")
	}

	// A manager applies one: an ordinary edit, the version moves on, the audit records it.
	r := apply(m, appendID, start.Version)
	if r.Status != http.StatusOK {
		t.Fatalf("apply = %d %s", r.Status, r.Body)
	}
	after := role()
	if after.Instructions != startingInstructions+"\n\nOffer a callback." || after.Version != start.Version+1 {
		t.Errorf("role = %q at version %d, want the paragraph appended and the version moved on", after.Instructions, after.Version)
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.events WHERE action = 'role.instructions.suggestion_applied' AND target = $1 AND after::text LIKE '%' || $2 || '%'`, g.role.ID, appendID); n != 1 {
		t.Errorf("audit rows = %d, want one naming the suggestion", n)
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.instruction_suggestions WHERE id = $1 AND state = 'applied' AND decided_by IS NOT NULL AND decided_at IS NOT NULL`, appendID); n != 1 {
		t.Error("the suggestion is not marked applied by the manager")
	}
	if r := apply(m, appendID, after.Version); r.Status != http.StatusConflict || problemCode(r) != "suggestion_decided" {
		t.Errorf("apply twice = %d %s, want 409 suggestion_decided", r.Status, r.Body)
	}
	if r := apply(m, replaceID, after.Version); r.Status != http.StatusOK || role().Instructions != "Answer in two sentences.\n\nNever promise a delivery date.\n\nOffer a callback." {
		t.Errorf("the replacing apply = %d, instructions %q", r.Status, role().Instructions)
	}

	// Dismissing leaves the instructions and the version alone.
	v := role().Version
	if r := m.Do(http.MethodPost, "/api/v1/agents/suggestions/"+dismissID+"/dismiss", nil); r.Status != http.StatusOK {
		t.Fatalf("dismiss = %d %s", r.Status, r.Body)
	}
	if role().Version != v {
		t.Error("dismissing moved the role's version")
	}
	if r := m.Do(http.MethodPost, "/api/v1/agents/suggestions/"+dismissID+"/dismiss", nil); r.Status != http.StatusConflict {
		t.Errorf("dismiss twice = %d, want 409", r.Status)
	}
	if r := apply(m, dismissID, v); r.Status != http.StatusConflict || problemCode(r) != "suggestion_decided" {
		t.Errorf("apply a dismissed one = %d %s, want 409 suggestion_decided", r.Status, r.Body)
	}
	proposed := g.owner.Do(http.MethodGet, "/api/v1/agents/roles/"+g.role.ID+"/suggestions?state=proposed", nil)
	var list struct{ Items []suggestion }
	proposed.JSON(&list)
	if len(list.Items) != 1 || list.Items[0].ID != staleID {
		t.Errorf("proposed = %+v, want only the stale one left", list.Items)
	}
}

func TestFeedback_ASuggestionThatWouldOverflowTheInstructionsIsRefused(t *testing.T) {
	t.Parallel()
	g := newFeedbackRig(t)
	long := strings.Repeat("a", 7990)
	body := roleBody(g.role)
	body["instructions"] = long
	cur := getAgent(t, g.owner, g.agent.ID).Roles[0]
	if r := g.owner.Do(http.MethodPut, rolePath(g.agent, g.role), body, ifMatch(cur.Version)); r.Status != http.StatusOK {
		t.Fatalf("set long instructions: %d %s", r.Status, r.Body)
	}
	sid := uuid.NewString()
	g.h.Exec(t, `INSERT INTO agents.instruction_suggestions (id, role_id, new_text, rationale, created_at) VALUES ($1, $2, $3, 'r', now())`, sid, g.role.ID, strings.Repeat("b", 100))
	cur = getAgent(t, g.owner, g.agent.ID).Roles[0]
	r := g.owner.Do(http.MethodPost, "/api/v1/agents/suggestions/"+sid+"/apply", nil, ifMatch(cur.Version))
	if r.Status != http.StatusBadRequest {
		t.Errorf("apply past the limit = %d %s, want 400", r.Status, r.Body)
	}
}

func TestFeedback_ARoleOfAPausedAgentIsNotChecked(t *testing.T) {
	t.Parallel()
	g := newFeedbackRig(t)
	for range 3 {
		g.correct(t, "discarded", "wrong_facts")
	}
	if r := g.owner.Do(http.MethodPost, "/api/v1/agents/agents/"+g.agent.ID+"/pause", nil); r.Status != http.StatusOK {
		t.Fatal(r.Body)
	}
	if err := agents.FeedbackCycle(g.deps()); err != nil {
		t.Fatal(err)
	}
	if calls, _ := g.fm.seen(); calls != 0 {
		t.Errorf("calls = %d, want none for a paused agent", calls)
	}
}

// ---- Auto replies a person answered over (V21)

// autoRun makes a run that sent its reply by itself, scored score, sent sentAgo ago.
func (g *queueRig) autoRun(t *testing.T, conv uuid.UUID, score int, sentAgo time.Duration) uuid.UUID {
	t.Helper()
	id := uuid.New()
	at := g.h.Now().Add(-sentAgo)
	g.h.Exec(t, `INSERT INTO agents.runs (id, agent_id, role_id, conversation_id, reason, state, outcome, verdict, started_at, ended_at, sent_at)
	             VALUES ($1, $2, $3, $4, 'message', 'done', 'sent', $5, $6, $6, $6)`,
		id, g.agent.ID, g.role.ID, conv, fmt.Sprintf(`{"mode":"auto","score":%d,"sent":true}`, score), at)
	return id
}

func (g *queueRig) answeredOver(t *testing.T, run uuid.UUID) bool {
	t.Helper()
	return g.rows(t, `SELECT count(*) FROM agents.runs WHERE id = $1 AND answered_over_at IS NOT NULL`, run) == 1
}

func TestAnsweredOver_APersonsReplyWithin24HoursLabelsTheAutoReply(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	conv, other, late, byAgent := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	answered := g.autoRun(t, conv, 92, time.Hour)
	elsewhere := g.autoRun(t, other, 92, time.Hour)
	tooOld := g.autoRun(t, late, 92, 25*time.Hour)
	approved := g.autoRun(t, byAgent, 92, time.Hour)

	person := uuid.New()
	g.publish(t, contracts.ConversationReplied{ConversationID: byAgent, MessageID: uuid.New(), ByUserID: g.agentUser(t), ByAgent: true})
	if g.answeredOver(t, approved) {
		t.Error("the agent's own reply answered over its reply")
	}
	g.publish(t, contracts.ConversationReplied{ConversationID: conv, MessageID: uuid.New(), ByUserID: person})
	g.publish(t, contracts.ConversationReplied{ConversationID: late, MessageID: uuid.New(), ByUserID: person})
	if !g.answeredOver(t, answered) {
		t.Error("a person's reply within 24 hours did not label the Auto reply")
	}
	if g.answeredOver(t, elsewhere) {
		t.Error("a reply in another conversation labelled this one")
	}
	if g.answeredOver(t, tooOld) {
		t.Error("a reply 25 hours later labelled the Auto reply")
	}
}

func TestAnsweredOver_CalibrationCountsItAsBadAndALeftAloneReplyAsGood(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	view := g.h.SignIn(t, "agents:view")
	for range 45 {
		g.seed(t, "reply", map[string]any{}, func(o *seedOpts) { o.State = "sent"; o.Score = 90 })
	}
	for range 5 {
		run := g.autoRun(t, uuid.New(), 95, 2*time.Hour)
		g.h.Exec(t, `UPDATE agents.runs SET answered_over_at = $2 WHERE id = $1`, run, g.h.Now().Add(-time.Hour))
	}
	g.autoRun(t, uuid.New(), 95, 30*time.Hour) // left alone for a day: good
	g.autoRun(t, uuid.New(), 95, time.Hour)    // too young to tell: no label

	r := view.Do(http.MethodGet, "/api/v1/agents/roles/"+g.role.ID+"/calibration", nil)
	var c struct {
		Labelled int `json:"labelled"`
		Points   []struct {
			X         int `json:"x"`
			AutoCount int `json:"autoCount"`
			BadCount  int `json:"badCount"`
		} `json:"points"`
	}
	r.JSON(&c)
	if c.Labelled != 51 {
		t.Fatalf("labelled = %d, want 51: 45 decided, 5 answered over, 1 left alone", c.Labelled)
	}
	if p := c.Points[90-50]; p.AutoCount != 51 || p.BadCount != 5 {
		t.Errorf("X=90 = %+v, want 51 automatic of which the 5 answered over are bad", p)
	}
	if p := c.Points[96-50]; p.AutoCount != 0 {
		t.Errorf("X=96 = %+v, want none: the 95s do not reach it", p)
	}
}
