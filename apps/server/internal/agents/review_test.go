package agents_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The review queue (virtual employees design V20): seeing an item needs what the
// agent read, deciding needs the reviewer to satisfy the operation's rule, an
// approval sends once with the disclosure, and every re-check of the pipeline
// holds.

const disclosureLine = "«Hanna» is «Hansen Rør»'s virtual assistant (AI)."

// queueRig is a rig whose Communications is the fake one, with the agent's levels
// raised for the conversation operations.
type queueRig struct {
	*rig
	comms *fakeComms
	check *checker
}

func newQueueRig(t *testing.T) *queueRig {
	t.Helper()
	h, comms, check := newHarnessWith(t)
	o := owner(t, h)
	setCompany(t, o, "Hansen Rør")
	a := createAgent(t, o, "Hanna")
	r := addRole(t, o, a.ID, "Front desk")
	ch := uuid.New()
	if resp := o.Do(http.MethodPut, rolePath(a, r)+"/channels", map[string]any{"channelIds": []string{ch.String()}}); resp.Status != http.StatusOK {
		t.Fatalf("bind the channel: %d %s", resp.Status, resp.Body)
	}
	for _, key := range []string{"communications:conversations-view", "communications:conversations-reply", "communications:conversations-manage"} {
		if resp := o.Do(http.MethodPut, "/api/v1/agents/agents/"+a.ID+"/permissions/"+key, map[string]any{"level": "change"}); resp.Status != http.StatusOK {
			t.Fatalf("raise %s: %d %s", key, resp.Status, resp.Body)
		}
	}
	return &queueRig{rig: &rig{h: h, owner: o, agent: a, role: r, channel: ch}, comms: comms, check: check}
}

// reviewer is a person who may review, see what the front desk read and send a
// reply and change a conversation.
func (g *queueRig) reviewer(t *testing.T, extra ...string) (*modtest.Client, uuid.UUID) {
	t.Helper()
	keys := append([]string{"agents:review", "communications:conversations-reply", "communications:conversations-manage"}, agents.SeeKeys()...)
	return g.h.SignInUser(t, append(keys, extra...)...)
}

// seed makes a conversation of the fake Communications and a review item on it.
func (g *queueRig) seed(t *testing.T, kind string, item map[string]any, mutate func(*seedOpts)) (itemID uuid.UUID, conv *fakeConv) {
	t.Helper()
	conv = g.comms.conversation(g.channel, g.h.Now())
	return g.seedOn(t, conv, kind, item, mutate), conv
}

type seedOpts struct {
	Score     int
	HeldBy    []string
	State     string
	Expires   time.Time
	CreatedAt time.Time
	Decided   bool
	EditRatio *float64
}

func (g *queueRig) seedOn(t *testing.T, conv *fakeConv, kind string, item map[string]any, mutate func(*seedOpts)) uuid.UUID {
	t.Helper()
	o := seedOpts{Score: 90, HeldBy: []string{}, State: "waiting", Expires: g.h.Now().Add(24 * time.Hour), CreatedAt: g.h.Now()}
	if mutate != nil {
		mutate(&o)
	}
	if kind == "reply" {
		if _, ok := item["text"]; !ok {
			item["text"] = "Your order ships tomorrow."
		}
		item["disclosure"] = disclosureLine
		item["inReplyToMessageId"] = conv.Messages[len(conv.Messages)-1].ID
	}
	raw, _ := json.Marshal(item)
	runID, itemID := uuid.New(), uuid.New()
	g.h.Exec(t, `INSERT INTO agents.runs (id, agent_id, role_id, conversation_id, reason, state, outcome, started_at, ended_at)
	             VALUES ($1, $2, $3, $4, 'message', 'waiting_review', 'review', $5, $5)`, runID, g.agent.ID, g.role.ID, conv.ID, o.CreatedAt)
	g.h.Exec(t, `INSERT INTO agents.review_items (id, run_id, agent_id, role_id, conversation_id, channel_id, kind, item, score, sources, model_score, held_by, state, decided_at, edit_ratio, expires_at, created_at)
	             VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, '["getInvoicesById 2026-0931"]', $9, $10, $11::varchar, CASE WHEN $11::varchar = 'waiting' THEN NULL ELSE $12::timestamptz END, $13, $14, $12)`,
		itemID, runID, g.agent.ID, g.role.ID, conv.ID, g.channel, kind, raw, o.Score, o.HeldBy, o.State, o.CreatedAt, o.EditRatio, o.Expires)
	return itemID
}

type item struct {
	ID              string   `json:"id"`
	RunID           string   `json:"runId"`
	Kind            string   `json:"kind"`
	State           string   `json:"state"`
	Version         int      `json:"version"`
	Score           int      `json:"score"`
	HeldBy          []string `json:"heldBy"`
	Outcome         *string  `json:"outcome"`
	DecidedByUserID *string  `json:"decidedByUserId"`
	DiscardReason   *string  `json:"discardReason"`
	EditRatio       *float64 `json:"editRatio"`
	AgentName       string   `json:"agentName"`
	Reply           *struct {
		Text       string `json:"text"`
		Disclosure string `json:"disclosure"`
	} `json:"reply"`
	Change *struct {
		OperationID string `json:"operationId"`
	} `json:"change"`
}

func reviewPath(id uuid.UUID, tail string) string {
	return "/api/v1/agents/review/" + id.String() + tail
}

func readItem(t *testing.T, c *modtest.Client, id uuid.UUID) item {
	t.Helper()
	r := c.Do(http.MethodGet, reviewPath(id, ""), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("read the item: %d %s", r.Status, r.Body)
	}
	var it item
	r.JSON(&it)
	return it
}

func problemCode(r *modtest.Response) string {
	var p struct {
		Code  string   `json:"code"`
		Rules []string `json:"rules"`
	}
	r.JSON(&p)
	return p.Code
}

func (g *queueRig) rows(t *testing.T, sql string, args ...any) int { return g.h.Count(t, sql, args...) }

func TestReview_SeeingAnItemNeedsWhatTheAgentRead(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	id, _ := g.seed(t, "reply", map[string]any{}, nil)

	// agents:review alone is not enough: the queue would let a person read
	// conversations and customers through the agent.
	bare := g.h.SignIn(t, "agents:review")
	if r := bare.Do(http.MethodGet, "/api/v1/agents/review", nil); r.Status != http.StatusForbidden || problemCode(r) != "reviewer_not_permitted" {
		t.Errorf("list with agents:review alone = %d %s, want 403 reviewer_not_permitted", r.Status, r.Body)
	}
	if r := bare.Do(http.MethodGet, reviewPath(id, ""), nil); r.Status != http.StatusForbidden {
		t.Errorf("get with agents:review alone = %d, want 403", r.Status)
	}
	// The count holds no content.
	if r := bare.Do(http.MethodGet, "/api/v1/agents/review/count", nil); r.Status != http.StatusOK {
		t.Errorf("count with agents:review = %d, want 200", r.Status)
	}
	// Each read permission of the template counts: leave one out and it is refused.
	keys := agents.SeeKeys()
	for _, missing := range keys {
		var others []string
		for _, k := range keys {
			if k != missing {
				others = append(others, k)
			}
		}
		c := g.h.SignIn(t, append([]string{"agents:review"}, others...)...)
		if r := c.Do(http.MethodGet, reviewPath(id, ""), nil); r.Status != http.StatusForbidden {
			t.Errorf("get without %s = %d, want 403", missing, r.Status)
		}
	}
	// Nobody without agents:review gets in at all.
	noReview := g.h.SignIn(t, keys...)
	if r := noReview.Do(http.MethodGet, "/api/v1/agents/review", nil); r.Status != http.StatusForbidden {
		t.Errorf("list without agents:review = %d, want 403", r.Status)
	}
	if r := noReview.Do(http.MethodGet, "/api/v1/agents/review/count", nil); r.Status != http.StatusForbidden {
		t.Errorf("count without agents:review = %d, want 403", r.Status)
	}

	rev, _ := g.reviewer(t)
	it := readItem(t, rev, id)
	if it.Kind != "reply" || it.State != "waiting" || it.AgentName != "Hanna" || it.Reply == nil || it.Reply.Disclosure != disclosureLine || it.Score != 90 {
		t.Errorf("item = %+v", it)
	}
	if r := rev.Do(http.MethodGet, reviewPath(uuid.New(), ""), nil); r.Status != http.StatusNotFound {
		t.Errorf("an unknown item = %d, want 404", r.Status)
	}
}

func TestReview_TheQueueListsTheWaitingFirstAndFilters(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	rev, _ := g.reviewer(t)
	later, _ := g.seed(t, "reply", map[string]any{}, func(o *seedOpts) { o.Expires = g.h.Now().Add(20 * time.Hour) })
	sooner, _ := g.seed(t, "reply", map[string]any{}, func(o *seedOpts) { o.Expires = g.h.Now().Add(2 * time.Hour) })
	done, _ := g.seed(t, "reply", map[string]any{}, func(o *seedOpts) { o.State = "sent" })

	var list struct {
		Items []item `json:"items"`
		Total int    `json:"total"`
	}
	r := rev.Do(http.MethodGet, "/api/v1/agents/review", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list: %d %s", r.Status, r.Body)
	}
	r.JSON(&list)
	var order []string
	for _, it := range list.Items {
		order = append(order, it.ID)
	}
	if want := []string{sooner.String(), later.String(), done.String()}; !slices.Equal(order, want) || list.Total != 3 {
		t.Errorf("order = %v (total %d), want the waiting by expiry, then the decided %v", order, list.Total, want)
	}

	r = rev.Do(http.MethodGet, "/api/v1/agents/review?state=waiting&agentId="+g.agent.ID+"&limit=1", nil)
	list.Items = nil
	r.JSON(&list)
	if len(list.Items) != 1 || list.Total != 2 || list.Items[0].ID != sooner.String() {
		t.Errorf("waiting, one at a time = %+v (total %d)", list.Items, list.Total)
	}
	if r := rev.Do(http.MethodGet, "/api/v1/agents/review?agentId="+newID(), nil); r.Status != http.StatusOK {
		t.Errorf("another agent's filter = %d", r.Status)
	}
	if r := rev.Do(http.MethodGet, "/api/v1/agents/review?state=nonsense", nil, modtest.SkipContract("a filter the contract refuses before the handler sees it")); r.Status != http.StatusBadRequest {
		t.Errorf("a state that is none = %d, want 400", r.Status)
	}
	var count struct {
		Waiting int `json:"waiting"`
	}
	rev.Do(http.MethodGet, "/api/v1/agents/review/count", nil).JSON(&count)
	if count.Waiting != 2 {
		t.Errorf("waiting = %d, want 2", count.Waiting)
	}
}

func TestReview_AnApprovalSendsTheFullTextWithTheDisclosureOnce(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	rev, reviewerID := g.reviewer(t)
	id, conv := g.seed(t, "reply", map[string]any{}, nil)

	r := rev.Do(http.MethodPost, reviewPath(id, "/approve"), nil, ifMatch(1))
	if r.Status != http.StatusOK {
		t.Fatalf("approve: %d %s", r.Status, r.Body)
	}
	var it item
	r.JSON(&it)
	if it.State != "sent" || it.Version != 2 || it.DecidedByUserID == nil || *it.DecidedByUserID != reviewerID.String() {
		t.Errorf("item = %+v, want sent at version 2 by the reviewer", it)
	}
	sent := g.comms.sent()
	if len(sent) != 1 {
		t.Fatalf("replies sent = %d, want 1", len(sent))
	}
	if want := disclosureLine + "\n\nYour order ships tomorrow."; sent[0].Text != want {
		t.Errorf("text sent = %q, want the stored full text %q", sent[0].Text, want)
	}
	if want := fmt.Sprintf("agents-%s-%s-%s", g.agent.ID, conv.ID, conv.Messages[0].ID); sent[0].Key != want {
		t.Errorf("Idempotency-Key = %q, want %q", sent[0].Key, want)
	}
	// The action log names the reviewer (V25); the day counts the send.
	if n := g.rows(t, `SELECT count(*) FROM agents.actions WHERE run_id = $1 AND approved_by = $2 AND operation_id = 'postCommunicationsConversationsByIdReply'`, it.RunID, reviewerID); n != 1 {
		t.Errorf("approved actions = %d, want 1", n)
	}
	if n := g.rows(t, `SELECT coalesce(sum(sent), 0) FROM agents.usage_days WHERE agent_id = $1`, g.agent.ID); n != 1 {
		t.Errorf("sent today = %d, want 1", n)
	}
	if n := g.rows(t, `SELECT count(*) FROM identity.agent_grants WHERE approved_by_user_id = $1 AND revoked_at IS NULL`, reviewerID); n != 0 {
		t.Errorf("the approval's grant outlived it: %d live", n)
	}

	// A second approval of the same item is refused, and sends nothing.
	if r := rev.Do(http.MethodPost, reviewPath(id, "/approve"), nil, ifMatch(2)); r.Status != http.StatusConflict || problemCode(r) != "item_decided" {
		t.Errorf("approve again = %d %s, want 409 item_decided", r.Status, r.Body)
	}
	if n := len(g.comms.sent()); n != 1 {
		t.Errorf("replies after the second approval = %d, want 1", n)
	}
}

func TestReview_TwoReviewersApprovingAtOnceSendOnce(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	id, _ := g.seed(t, "reply", map[string]any{}, nil)
	var wg sync.WaitGroup
	statuses := make([]int, 4)
	for i := range statuses {
		c, _ := g.reviewer(t)
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = c.Do(http.MethodPost, reviewPath(id, "/approve"), nil, ifMatch(1)).Status
		}()
	}
	wg.Wait()
	for _, s := range statuses {
		if s != http.StatusOK && s != http.StatusConflict {
			t.Errorf("a concurrent approval answered %d, want 200 or 409", s)
		}
	}
	if n := len(g.comms.sent()); n != 1 {
		t.Fatalf("replies sent by %v = %d, want exactly 1", statuses, n)
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.review_items WHERE id = $1 AND state = 'sent' AND version = 2`, id); n != 1 {
		t.Errorf("the item is not sent at version 2")
	}
}

func TestReview_ADecisionIsGuardedByIfMatch(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	rev, _ := g.reviewer(t)
	id, _ := g.seed(t, "reply", map[string]any{}, nil)
	if r := rev.Do(http.MethodPost, reviewPath(id, "/approve"), nil, ifMatch(7)); r.Status != http.StatusConflict || problemCode(r) != "version_conflict" {
		t.Errorf("approve at a stale version = %d %s, want 409 version_conflict", r.Status, r.Body)
	}
	if r := rev.Do(http.MethodPost, reviewPath(id, "/approve"), nil, modtest.Header("If-Match", "x")); r.Status != http.StatusBadRequest {
		t.Errorf("approve with a bad If-Match = %d, want 400", r.Status)
	}
	if n := len(g.comms.sent()); n != 0 {
		t.Errorf("a refused approval sent %d replies", n)
	}
}

// The laundering checks: an approval must not grant more than the reviewer could
// do, and nothing is sent when it is refused.
func TestReview_AReviewerWhoMayNotReplyCannotApprove(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	id, _ := g.seed(t, "reply", map[string]any{}, nil)
	keys := append([]string{"agents:review"}, agents.SeeKeys()...)
	cannotReply := g.h.SignIn(t, keys...)
	r := cannotReply.Do(http.MethodPost, reviewPath(id, "/approve"), nil, ifMatch(1))
	if r.Status != http.StatusForbidden || problemCode(r) != "reviewer_not_permitted" {
		t.Errorf("approve without conversations-reply = %d %s, want 403 reviewer_not_permitted", r.Status, r.Body)
	}
	if r := cannotReply.Do(http.MethodPost, reviewPath(id, "/send"), map[string]any{"text": "Hi"}, ifMatch(1)); r.Status != http.StatusForbidden {
		t.Errorf("edit and send without conversations-reply = %d, want 403", r.Status)
	}
	// Seeing is not enough: the reviewer must be able to see, too.
	cannotSee := g.h.SignIn(t, "agents:review", "communications:conversations-reply", "communications:conversations-manage")
	if r := cannotSee.Do(http.MethodPost, reviewPath(id, "/approve"), nil, ifMatch(1)); r.Status != http.StatusForbidden {
		t.Errorf("approve without seeing what the agent read = %d, want 403", r.Status)
	}
	if r := cannotSee.Do(http.MethodPost, reviewPath(id, "/discard"), map[string]any{"reason": "other"}, ifMatch(1)); r.Status != http.StatusForbidden {
		t.Errorf("discard without seeing what the agent read = %d, want 403", r.Status)
	}
	if n := len(g.comms.sent()); n != 0 {
		t.Errorf("a refused reviewer sent %d replies", n)
	}
	if it := readItem(t, g.mustReviewer(t), id); it.State != "waiting" || it.Version != 1 {
		t.Errorf("the item moved: %+v", it)
	}
}

func (g *queueRig) mustReviewer(t *testing.T) *modtest.Client {
	c, _ := g.reviewer(t)
	return c
}

func TestReview_EditAndSendKeepsTheDisclosureAndChecksTheEditedText(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	rev, _ := g.reviewer(t)
	id, _ := g.seed(t, "reply", map[string]any{"text": "Your order ships tomorrow."}, nil)

	// The reviewer's text claims to be a person: C1 holds it in the queue.
	g.check.set(checkHuman)
	r := rev.Do(http.MethodPost, reviewPath(id, "/send"), map[string]any{"text": "This is Kari from the shop, a real person."}, ifMatch(1))
	if r.Status != http.StatusUnprocessableEntity || problemCode(r) != "held" {
		t.Fatalf("send a text that claims to be a person = %d %s, want 422 held", r.Status, r.Body)
	}
	var p struct {
		Rules []string `json:"rules"`
	}
	r.JSON(&p)
	if !slices.Equal(p.Rules, []string{"C1"}) {
		t.Errorf("rules = %v, want [C1]", p.Rules)
	}
	if n := len(g.comms.sent()); n != 0 {
		t.Fatalf("a held text was sent: %d", n)
	}
	held := readItem(t, rev, id)
	if held.State != "waiting" || !slices.Contains(held.HeldBy, "C1") || held.Version != 2 {
		t.Errorf("after the hold: %+v, want waiting at version 2 held by C1", held)
	}

	// A clean text goes out, with the disclosure above it whatever was typed.
	g.check.set(checkClean)
	r = rev.Do(http.MethodPost, reviewPath(id, "/send"), map[string]any{"text": "Your order ships on Friday."}, ifMatch(2))
	if r.Status != http.StatusOK {
		t.Fatalf("send the edited text: %d %s", r.Status, r.Body)
	}
	var it item
	r.JSON(&it)
	if it.State != "edited_sent" || it.EditRatio == nil || *it.EditRatio <= 0 || it.Reply == nil || it.Reply.Text != "Your order ships on Friday." {
		t.Errorf("item = %+v, want edited_sent with the reviewer's text and a ratio", it)
	}
	sent := g.comms.sent()
	if len(sent) != 1 || sent[0].Text != disclosureLine+"\n\nYour order ships on Friday." {
		t.Errorf("sent = %+v, want the reviewer's text under the disclosure", sent)
	}
	if n := g.rows(t, `SELECT coalesce(sum(sent), 0) FROM agents.usage_days WHERE agent_id = $1`, g.agent.ID); n != 1 {
		t.Errorf("sent today = %d, want 1", n)
	}

	// A change cannot be edited, and an empty text is no text.
	chg, _ := g.seed(t, "change", map[string]any{"operationId": "patchCommunicationsConversationsById", "summary": "Close it", "explain": "The customer is satisfied"}, nil)
	if r := rev.Do(http.MethodPost, reviewPath(chg, "/send"), map[string]any{"text": "x"}, ifMatch(1)); r.Status != http.StatusBadRequest {
		t.Errorf("edit a change = %d, want 400", r.Status)
	}
	id2, _ := g.seed(t, "reply", map[string]any{}, nil)
	if r := rev.Do(http.MethodPost, reviewPath(id2, "/send"), map[string]any{"text": "   "}, ifMatch(1), modtest.SkipContract("a body the handler refuses")); r.Status != http.StatusBadRequest {
		t.Errorf("edit to an empty text = %d, want 400", r.Status)
	}
}

func TestReview_AnItemAlreadyHeldByARuleIsTheReviewersToDecide(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	rev, _ := g.reviewer(t)
	// S1 held it in the first place: the reviewer is the one who decides.
	id, _ := g.seed(t, "reply", map[string]any{}, func(o *seedOpts) { o.HeldBy = []string{"S1", "S4"} })
	if r := rev.Do(http.MethodPost, reviewPath(id, "/approve"), nil, ifMatch(1)); r.Status != http.StatusOK {
		t.Fatalf("approve an item S1 held: %d %s", r.Status, r.Body)
	}
	if n := len(g.comms.sent()); n != 1 {
		t.Errorf("sent = %d, want 1", n)
	}
}

func TestReview_AConversationTheStateChangedUnderIsWithdrawnAndHandedOff(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		change  func(g *queueRig, c *fakeConv)
		outcome string
		rerun   bool
		handOff bool
	}{
		"a person took it": {func(g *queueRig, c *fakeConv) {
			other := uuid.New()
			g.comms.update(c.ID, func(c *fakeConv) { c.AssignedTo = &other })
		}, "conversation_taken", false, true},
		"a newer message": {func(g *queueRig, c *fakeConv) {
			g.comms.update(c.ID, func(c *fakeConv) {
				c.Messages = append(c.Messages, fakeMsg{ID: uuid.New(), Address: "kari@example.test", Text: "Also: when?", At: g.h.Now().Add(time.Minute)})
			})
		}, "conversation_moved", true, false},
		"a closed window": {func(g *queueRig, c *fakeConv) {
			g.comms.update(c.ID, func(c *fakeConv) { c.WindowState = "closed" })
		}, "window_closed", false, true},
		"only the human-agent window left": {func(g *queueRig, c *fakeConv) {
			g.comms.update(c.ID, func(c *fakeConv) { c.WindowState = "human_agent" })
		}, "window_closed", false, true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			g := newQueueRig(t)
			rev, _ := g.reviewer(t)
			id, conv := g.seed(t, "reply", map[string]any{}, nil)
			tc.change(g, conv)
			r := rev.Do(http.MethodPost, reviewPath(id, "/approve"), nil, ifMatch(1))
			if r.Status != http.StatusOK {
				t.Fatalf("approve: %d %s", r.Status, r.Body)
			}
			var it item
			r.JSON(&it)
			if it.State != "withdrawn" || it.Outcome == nil || *it.Outcome != tc.outcome {
				t.Errorf("item = %+v, want withdrawn with %s", it, tc.outcome)
			}
			if n := len(g.comms.sent()); n != 0 {
				t.Errorf("sent = %d, want nothing", n)
			}
			if got := g.handedOff(t, conv.ID) != ""; got != tc.handOff {
				t.Errorf("handed off = %v, want %v", got, tc.handOff)
			}
			if got := len(g.requests(t, conv.ID)) == 1; got != tc.rerun {
				t.Errorf("a new run asked for = %v, want %v", got, tc.rerun)
			}
		})
	}
}

func TestReview_AnAgentThatCanNoLongerActWithdrawsWhatItWrote(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	rev, _ := g.reviewer(t)
	id, conv := g.seed(t, "reply", map[string]any{}, nil)
	// The Owner lowers the level: the gate says never.
	if r := g.owner.Do(http.MethodPut, "/api/v1/agents/agents/"+g.agent.ID+"/permissions/communications:conversations-reply", map[string]any{"level": "off"}); r.Status != http.StatusOK {
		t.Fatalf("lower: %d %s", r.Status, r.Body)
	}
	var it item
	rev.Do(http.MethodPost, reviewPath(id, "/approve"), nil, ifMatch(1)).JSON(&it)
	if it.State != "withdrawn" || len(g.comms.sent()) != 0 || g.handedOff(t, conv.ID) == "" {
		t.Errorf("item %+v, sent %d, handed off %q: want withdrawn, nothing sent, handed off", it, len(g.comms.sent()), g.handedOff(t, conv.ID))
	}

	// A deleted agent withdraws too.
	id2, conv2 := g.seed(t, "reply", map[string]any{}, nil)
	_ = id2
	if r := g.owner.Do(http.MethodDelete, "/api/v1/agents/agents/"+g.agent.ID, nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete the agent: %d %s", r.Status, r.Body)
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.review_items WHERE id = $1 AND state = 'withdrawn'`, id2); n != 1 {
		t.Errorf("deleting the agent left its waiting item waiting")
	}
	if g.handedOff(t, conv2.ID) == "" {
		t.Error("deleting the agent did not hand off what waited")
	}
}

func TestReview_AChangeIsPinnedAndCheckedAgainstTheGateAgain(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	rev, _ := g.reviewer(t)
	patch := func(body map[string]any) map[string]any {
		return map[string]any{"operationId": "patchCommunicationsConversationsById", "summary": "Update a conversation", "explain": "Close it", "body": body}
	}

	// A change that is still allowed is made, as the agent.
	id, conv := g.seed(t, "change", patch(map[string]any{"status": "closed"}), nil)
	g.h.Exec(t, `UPDATE agents.review_items SET item = jsonb_set(item, '{pathParams}', jsonb_build_object('id', $2::text)) WHERE id = $1`, id, conv.ID.String())
	r := rev.Do(http.MethodPost, reviewPath(id, "/approve"), nil, ifMatch(1))
	if r.Status != http.StatusOK {
		t.Fatalf("approve the change: %d %s", r.Status, r.Body)
	}
	var it item
	r.JSON(&it)
	if it.State != "sent" || len(g.comms.patched()) != 1 {
		t.Errorf("item %+v, patches %d: want made once", it, len(g.comms.patched()))
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.actions WHERE run_id = $1 AND approved_by IS NOT NULL`, it.RunID); n != 1 {
		t.Errorf("approved actions = %d, want 1", n)
	}

	// A change that reaches beyond the status is not one an agent may make.
	id2, conv2 := g.seed(t, "change", patch(map[string]any{"assignedUserId": uuid.NewString()}), nil)
	g.h.Exec(t, `UPDATE agents.review_items SET item = jsonb_set(item, '{pathParams}', jsonb_build_object('id', $2::text)) WHERE id = $1`, id2, conv2.ID.String())
	rev.Do(http.MethodPost, reviewPath(id2, "/approve"), nil, ifMatch(1)).JSON(&it)
	if it.State != "withdrawn" || len(g.comms.patched()) != 1 {
		t.Errorf("a patch of the assignee: item %+v, patches %d, want withdrawn and not made", it, len(g.comms.patched()))
	}

	// A change that names another conversation is pinned back to the run's own: it is refused.
	id3, _ := g.seed(t, "change", patch(map[string]any{"status": "closed"}), nil)
	g.h.Exec(t, `UPDATE agents.review_items SET item = jsonb_set(item, '{pathParams}', jsonb_build_object('id', $2::text)) WHERE id = $1`, id3, uuid.NewString())
	rev.Do(http.MethodPost, reviewPath(id3, "/approve"), nil, ifMatch(1)).JSON(&it)
	if it.State != "withdrawn" || len(g.comms.patched()) != 1 {
		t.Errorf("another conversation: item %+v, patches %d, want withdrawn and not made", it, len(g.comms.patched()))
	}

	// The Owner lowers the level to read: the change is now never, and withdrawn.
	id4, conv4 := g.seed(t, "change", patch(map[string]any{"status": "closed"}), nil)
	g.h.Exec(t, `UPDATE agents.review_items SET item = jsonb_set(item, '{pathParams}', jsonb_build_object('id', $2::text)) WHERE id = $1`, id4, conv4.ID.String())
	if r := g.owner.Do(http.MethodPut, "/api/v1/agents/agents/"+g.agent.ID+"/permissions/communications:conversations-manage", map[string]any{"level": "read"}); r.Status != http.StatusOK {
		t.Fatalf("lower: %d %s", r.Status, r.Body)
	}
	rev.Do(http.MethodPost, reviewPath(id4, "/approve"), nil, ifMatch(1)).JSON(&it)
	if it.State != "withdrawn" || len(g.comms.patched()) != 1 {
		t.Errorf("after the level fell: item %+v, patches %d, want withdrawn and not made", it, len(g.comms.patched()))
	}

	// A reviewer who may not manage conversations cannot approve a change that does.
	g.owner.Do(http.MethodPut, "/api/v1/agents/agents/"+g.agent.ID+"/permissions/communications:conversations-manage", map[string]any{"level": "change"})
	id5, conv5 := g.seed(t, "change", patch(map[string]any{"status": "closed"}), nil)
	g.h.Exec(t, `UPDATE agents.review_items SET item = jsonb_set(item, '{pathParams}', jsonb_build_object('id', $2::text)) WHERE id = $1`, id5, conv5.ID.String())
	weak := g.h.SignIn(t, append([]string{"agents:review"}, agents.SeeKeys()...)...)
	if r := weak.Do(http.MethodPost, reviewPath(id5, "/approve"), nil, ifMatch(1)); r.Status != http.StatusForbidden || problemCode(r) != "reviewer_not_permitted" {
		t.Errorf("approve a change without conversations-manage = %d %s, want 403", r.Status, r.Body)
	}
	if len(g.comms.patched()) != 1 {
		t.Errorf("a refused reviewer made a change")
	}
}

func TestReview_DiscardNeedsAReasonAndHandsAReplyOff(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	rev, reviewerID := g.reviewer(t)
	id, conv := g.seed(t, "reply", map[string]any{}, nil)

	if r := rev.Do(http.MethodPost, reviewPath(id, "/discard"), map[string]any{"reason": "bored"}, ifMatch(1), modtest.SkipContract("a reason the contract refuses")); r.Status != http.StatusBadRequest {
		t.Errorf("a reason that is none = %d, want 400", r.Status)
	}
	if r := rev.Do(http.MethodPost, reviewPath(id, "/discard"), map[string]any{"reason": "wrong_tone"}, ifMatch(3)); r.Status != http.StatusConflict {
		t.Errorf("discard at a stale version = %d, want 409", r.Status)
	}
	r := rev.Do(http.MethodPost, reviewPath(id, "/discard"), map[string]any{"reason": "wrong_tone", "text": "Too cold."}, ifMatch(1))
	if r.Status != http.StatusOK {
		t.Fatalf("discard: %d %s", r.Status, r.Body)
	}
	var it item
	r.JSON(&it)
	if it.State != "discarded" || it.DiscardReason == nil || *it.DiscardReason != "wrong_tone" || it.DecidedByUserID == nil || *it.DecidedByUserID != reviewerID.String() {
		t.Errorf("item = %+v", it)
	}
	if reason := g.handedOff(t, conv.ID); reason != "discarded" {
		t.Errorf("hand-off = %q, want discarded", reason)
	}
	if n := g.rows(t, `SELECT coalesce(sum(discarded), 0) FROM agents.usage_days WHERE agent_id = $1`, g.agent.ID); n != 1 {
		t.Errorf("discarded today = %d, want 1", n)
	}
	if n := len(g.comms.sent()); n != 0 {
		t.Errorf("a discard sent %d replies", n)
	}
	if r := rev.Do(http.MethodPost, reviewPath(id, "/discard"), map[string]any{"reason": "other"}, ifMatch(2)); r.Status != http.StatusConflict || problemCode(r) != "item_decided" {
		t.Errorf("discard twice = %d %s, want 409 item_decided", r.Status, r.Body)
	}

	// Discarding a change hands nothing off.
	chg, convChg := g.seed(t, "change", map[string]any{"operationId": "patchCommunicationsConversationsById", "summary": "Close", "explain": "x"}, nil)
	if r := rev.Do(http.MethodPost, reviewPath(chg, "/discard"), map[string]any{"reason": "should_not_answer"}, ifMatch(1)); r.Status != http.StatusOK {
		t.Fatalf("discard a change: %d %s", r.Status, r.Body)
	}
	if g.handedOff(t, convChg.ID) != "" {
		t.Error("discarding a change handed the conversation off")
	}
}

func TestReviews_AnItemExpiresAfterADayOrBeforeTheWindowClosesAndHandsOff(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	day, dayConv := g.seed(t, "reply", map[string]any{}, nil)
	window, windowConv := g.seed(t, "reply", map[string]any{}, func(o *seedOpts) { o.Expires = g.h.Now().Add(45 * time.Minute) })
	change, changeConv := g.seed(t, "change", map[string]any{"operationId": "patchCommunicationsConversationsById", "summary": "x", "explain": "x"}, nil)
	waiting := func(id uuid.UUID) bool {
		return g.rows(t, `SELECT count(*) FROM agents.review_items WHERE id = $1 AND state = 'waiting'`, id) == 1
	}

	if err := agents.ReviewsCycle(g.h.Deps()); err != nil {
		t.Fatal(err)
	}
	if !waiting(day) || !waiting(window) || !waiting(change) {
		t.Fatal("an item expired before its time")
	}
	g.h.Advance(46 * time.Minute) // past the Messenger reply's window less 15 minutes
	if err := agents.ReviewsCycle(g.h.Deps()); err != nil {
		t.Fatal(err)
	}
	if waiting(window) || !waiting(day) || !waiting(change) {
		t.Errorf("after 46 minutes: window waiting=%v day waiting=%v, want only the window's expired", waiting(window), waiting(day))
	}
	if g.handedOff(t, windowConv.ID) != "expired" || g.handedOff(t, dayConv.ID) != "" {
		t.Errorf("hand-offs: window %q, day %q", g.handedOff(t, windowConv.ID), g.handedOff(t, dayConv.ID))
	}
	g.h.Advance(24 * time.Hour)
	if err := agents.ReviewsCycle(g.h.Deps()); err != nil {
		t.Fatal(err)
	}
	if waiting(day) || waiting(change) {
		t.Error("an item waited more than a day")
	}
	if g.handedOff(t, dayConv.ID) != "expired" || g.handedOff(t, changeConv.ID) != "expired" {
		t.Error("an expired item did not hand its conversation off")
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.review_items WHERE state = 'expired' AND version = 2`); n != 3 {
		t.Errorf("expired items = %d, want 3", n)
	}
	// A run is recorded for each hand-off, for the runner to finish.
	if n := g.rows(t, `SELECT count(*) FROM agents.runs WHERE state = 'handed_off' AND outcome IS NULL AND error_code = 'expired'`); n != 3 {
		t.Errorf("hand-offs to finish = %d, want 3", n)
	}
}
