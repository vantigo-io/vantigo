package communications_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the assignment of a conversation (virtual employees design V2
// and V3): the assignee's check on PATCH, the list's assignedUserId filter and
// the two events Communications publishes onto the outbox.

// publishedEvents is the payloads of the events named name on the outbox, oldest
// first, with the aggregate each was published for.
func publishedEvents(t *testing.T, h *modtest.Harness, name string) (payloads []map[string]any, aggregates []string) {
	t.Helper()
	rows, err := h.Pool().Query(context.Background(),
		`SELECT payload, aggregate_id FROM platform.events WHERE name = $1 AND module = 'communications' ORDER BY id`, name)
	if err != nil {
		t.Fatalf("read the outbox: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var aggregate string
		if err := rows.Scan(&raw, &aggregate); err != nil {
			t.Fatalf("scan the outbox: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		payloads, aggregates = append(payloads, payload), append(aggregates, aggregate)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the outbox: %v", err)
	}
	return payloads, aggregates
}

func assign(c *modtest.Client, conversation string, assignee any, opts ...modtest.RequestOption) *modtest.Response {
	return c.Do(http.MethodPatch, "/api/v1/communications/conversations/"+conversation, map[string]any{"assignedUserId": assignee}, opts...)
}

// TestPatchConversation_AssigneeMustBeAKnownActiveUser: a user the directory
// does not know, or that is disabled, is a 422 assignee_unknown, and the
// conversation is left as it was.
func TestPatchConversation_AssigneeMustBeAKnownActiveUser(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	reply := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, reply, newConversationBody("x@example.test"))
	c := h.SignIn(t, "communications:conversations-manage", "communications:conversations-view")

	_, disabled := h.SignInUser(t)
	h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, disabled)
	for name, id := range map[string]string{"unknown": uuid.NewString(), "disabled": disabled.String()} {
		r := assign(c, m.ConversationId, id)
		if r.Status != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status %d body %s, want 422", name, r.Status, r.Body)
		}
		var e commErrorJSON
		r.JSON(&e)
		if e.Error.Code != "assignee_unknown" {
			t.Errorf("%s: code = %q, want assignee_unknown", name, e.Error.Code)
		}
	}
	if got := h.Count(t, `SELECT count(*) FROM communications.conversations WHERE id = $1 AND assigned_user_id IS NULL`, m.ConversationId); got != 1 {
		t.Errorf("the conversation was assigned by a refused request")
	}
	if events, _ := publishedEvents(t, h, "communications.conversation.assigned"); len(events) != 0 {
		t.Errorf("%d assigned events after refused requests, want none", len(events))
	}
}

// TestPatchConversation_PublishesAssigned: a change of assignee publishes
// communications.conversation.assigned with the previous assignee and the
// caller; setting the assignee it already has, or changing only the status,
// publishes nothing.
func TestPatchConversation_PublishesAssigned(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	reply := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, reply, newConversationBody("x@example.test"))
	c, caller := h.SignInUser(t, "communications:conversations-manage", "communications:conversations-view")
	_, first := h.SignInUser(t)
	_, second := h.SignInUser(t)

	for _, step := range []any{first.String(), first.String(), second.String(), nil} {
		if r := assign(c, m.ConversationId, step); r.Status != http.StatusOK {
			t.Fatalf("assign %v: status %d body %s", step, r.Status, r.Body)
		}
	}
	if r := c.Do(http.MethodPatch, "/api/v1/communications/conversations/"+m.ConversationId, map[string]any{"status": "closed"}); r.Status != http.StatusOK {
		t.Fatalf("close: status %d body %s", r.Status, r.Body)
	}

	events, aggregates := publishedEvents(t, h, "communications.conversation.assigned")
	want := []struct{ assigned, previous any }{
		{first.String(), nil}, {second.String(), first.String()}, {nil, second.String()},
	}
	if len(events) != len(want) {
		t.Fatalf("%d assigned events, want %d (an unchanged assignee publishes none): %v", len(events), len(want), events)
	}
	for i, w := range want {
		e := events[i]
		if e["conversationId"] != m.ConversationId || e["assignedUserId"] != w.assigned ||
			e["previousAssignedUserId"] != w.previous || e["byUserId"] != caller.String() || aggregates[i] != m.ConversationId {
			t.Errorf("event %d = %v (aggregate %s), want assigned %v, previous %v, by %s", i, e, aggregates[i], w.assigned, w.previous, caller)
		}
		if len(e) != 4 {
			t.Errorf("event %d has %d fields, want the four of the contract: %v", i, len(e), e)
		}
	}
}

// TestConversations_AssigneeFilterAndName: the list takes assignedUserId as a
// user id, "me" or "none"; a row and the detail name their assignee.
func TestConversations_AssigneeFilterAndName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	reply := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	c, me := h.SignInUser(t, "communications:conversations-manage", "communications:conversations-view")
	_, other := h.SignInUser(t)
	mine := createConversation(t, reply, newConversationBody("a@example.test")).ConversationId
	theirs := createConversation(t, reply, newConversationBody("b@example.test")).ConversationId
	nobody := createConversation(t, reply, newConversationBody("c@example.test")).ConversationId
	for conv, user := range map[string]uuid.UUID{mine: me, theirs: other} {
		if r := assign(c, conv, user.String()); r.Status != http.StatusOK {
			t.Fatalf("assign: status %d body %s", r.Status, r.Body)
		}
	}

	ids := func(filter string) map[string]conversationListItemJSON {
		r := c.Do(http.MethodGet, "/api/v1/communications/conversations?assignedUserId="+url.QueryEscape(filter), nil)
		if r.Status != http.StatusOK {
			t.Fatalf("filter %q: status %d body %s", filter, r.Status, r.Body)
		}
		var page paginatedConversationsJSON
		r.JSON(&page)
		out := map[string]conversationListItemJSON{}
		for _, item := range page.Data {
			out[item.Id] = item
		}
		return out
	}
	for filter, want := range map[string]string{"me": mine, me.String(): mine, other.String(): theirs, "none": nobody} {
		got := ids(filter)
		if _, ok := got[want]; !ok || len(got) != 1 {
			t.Errorf("filter %q = %d conversations, want only %s", filter, len(got), want)
		}
	}
	if item := ids("me")[mine]; item.AssignedUserName == nil || *item.AssignedUserName == "" {
		t.Errorf("a row names no assignee: %+v", item)
	}
	if item := ids("none")[nobody]; item.AssignedUserName != nil {
		t.Errorf("an unassigned row names %q", *item.AssignedUserName)
	}

	r := c.Do(http.MethodGet, "/api/v1/communications/conversations?assignedUserId=somebody", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("a bad filter: status %d body %s, want 400", r.Status, r.Body)
	}

	d := c.Do(http.MethodGet, "/api/v1/communications/conversations/"+mine, nil)
	var detail conversationDetailJSON
	d.JSON(&detail)
	if detail.AssignedUserName == nil || *detail.AssignedUserName == "" {
		t.Errorf("the detail names no assignee")
	}
}

// TestAssignableUsers: active users from the directory, for the pickers.
func TestAssignableUsers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, me := h.SignInUser(t, "communications:conversations-view")
	_, gone := h.SignInUser(t)
	h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, gone)

	r := c.Do(http.MethodGet, "/api/v1/communications/assignable-users?limit=20", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s", r.Status, r.Body)
	}
	var raw []map[string]any
	r.JSON(&raw)
	var users []struct {
		UserId      string `json:"userId"`
		DisplayName string `json:"displayName"`
	}
	r.JSON(&users)
	for _, u := range raw {
		if len(u) != 3 || u["kind"] != "person" {
			t.Errorf("a user carries %v, want only userId, displayName and the kind person", u)
		}
	}
	var sawMe bool
	for _, u := range users {
		sawMe = sawMe || u.UserId == me.String()
		if u.UserId == gone.String() {
			t.Errorf("a disabled user is offered")
		}
	}
	if !sawMe {
		t.Errorf("the caller is not offered: %v", users)
	}
	_, named := h.SignInUser(t)
	h.Exec(t, `UPDATE identity.users SET display_name = 'Zebulon Findable' WHERE id = $1`, named)
	var found []struct {
		UserId string `json:"userId"`
	}
	q := c.Do(http.MethodGet, "/api/v1/communications/assignable-users?query=zebulon", nil)
	q.JSON(&found)
	if q.Status != http.StatusOK || len(found) != 1 || found[0].UserId != named.String() {
		t.Errorf("query zebulon = %d %s, want only %s", q.Status, q.Body, named)
	}
	// An agent is offered, marked by its kind (virtual employees design V5).
	agentID := uuid.New()
	h.Exec(t, `INSERT INTO identity.users (id, email, normalized_email, display_name, kind, version, created_at, updated_at)
		VALUES ($1, $2, upper($2), 'Hanna Agentova', 'agent', $3, now(), now())`, agentID, "agent-"+agentID.String()+"@agents.invalid", uuid.New())
	var agents []struct {
		UserId string `json:"userId"`
		Kind   string `json:"kind"`
	}
	a := c.Do(http.MethodGet, "/api/v1/communications/assignable-users?query=agentova", nil)
	a.JSON(&agents)
	if a.Status != http.StatusOK || len(agents) != 1 || agents[0].UserId != agentID.String() || agents[0].Kind != "agent" {
		t.Errorf("query agentova = %d %s, want only the agent, kind agent", a.Status, a.Body)
	}
	if r := c.Do(http.MethodGet, "/api/v1/communications/assignable-users?query="+strings.Repeat("a", 201), nil); r.Status != http.StatusBadRequest {
		t.Errorf("a 201-character query: status %d, want 400", r.Status)
	}
	if r := c.Do(http.MethodGet, "/api/v1/communications/assignable-users?limit=500", nil); r.Status != http.StatusBadRequest {
		t.Errorf("limit 500: status %d, want 400", r.Status)
	}
	if r := h.SignIn(t).Do(http.MethodGet, "/api/v1/communications/assignable-users", nil); r.Status != http.StatusForbidden {
		t.Errorf("without conversations-view: status %d, want 403", r.Status)
	}
}

// TestMailboxSync_PublishesMessageReceived: an imported inbound message
// publishes communications.conversation.message_received on the import's transaction, for a
// new conversation and for a message that joins one, with the conversation's
// assignee, the auto-reply flag and the unreadable flag, and no personal data.
func TestMailboxSync_PublishesMessageReceived(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	at := in.h.Now().Add(time.Minute)

	first := in.plainMail("Can you help?")
	firstID := in.deliver(first, at, "graph-conv-events")
	in.sync(t)
	conv := in.importedMessage(t, firstID).conversationID

	_, assignee := in.h.SignInUser(t)
	in.h.Exec(t, `UPDATE communications.conversations SET assigned_user_id = $2 WHERE id = $1`, conv, assignee)

	in.h.Advance(5 * time.Minute)
	reply := in.plainMail("Out of office")
	reply.From, reply.InReplyTo, reply.References = first.From, first.MessageID, first.MessageID
	reply.Headers = map[string]string{"Auto-Submitted": "auto-replied"}
	replyID := in.deliver(reply, at.Add(time.Minute), "graph-conv-events")
	garbageID := "AAMk-garbage"
	in.fake.AddInbox(in.mailbox.Mailbox, contracts.MailItem{ID: garbageID, ReceivedAt: at.Add(2 * time.Minute)},
		[]byte("this is not a message\r\n"))
	in.sync(t)

	events, aggregates := publishedEvents(t, in.h, "communications.conversation.message_received")
	if len(events) != 3 {
		t.Fatalf("%d events, want one per imported message: %v", len(events), events)
	}
	replyMsg := in.importedMessage(t, replyID)
	if replyMsg.conversationID != conv {
		t.Fatalf("the reply started a conversation of its own; the test needs it in the thread")
	}
	byMessage := map[string]map[string]any{}
	for i, e := range events {
		byMessage[e["messageId"].(string)] = e
		if aggregates[i] != e["conversationId"] {
			t.Errorf("event %d aggregate = %s, want its conversation %v", i, aggregates[i], e["conversationId"])
		}
		if len(e) != 7 {
			t.Errorf("event %d has %d fields, want the seven of the contract: %v", i, len(e), e)
		}
	}
	firstEvent := byMessage[in.importedMessage(t, firstID).id.String()]
	if firstEvent["conversationId"] != conv.String() || firstEvent["channelId"] != in.channelID.String() ||
		firstEvent["channelType"] != "email" || firstEvent["autoReply"] != false || firstEvent["unreadable"] != false ||
		firstEvent["assignedUserId"] != nil {
		t.Errorf("first event = %v", firstEvent)
	}
	replyEvent := byMessage[replyMsg.id.String()]
	if replyEvent["autoReply"] != true || replyEvent["assignedUserId"] != assignee.String() {
		t.Errorf("reply event = %v, want an auto-reply in the assigned conversation", replyEvent)
	}
	garbage := in.importedMessage(t, garbageID)
	if e := byMessage[garbage.id.String()]; e["unreadable"] != true {
		t.Errorf("unreadable event = %v, want unreadable", e)
	}
}

// TestMessengerSync_PublishesMessageReceivedButNotForAnEcho: a participant's
// message publishes the event with the conversation's assignee; the Page's own
// message coming back as an echo publishes none, or an agent would answer
// itself.
func TestMessengerSync_PublishesMessageReceivedButNotForAnEcho(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	f.text("4242", "m1", "Hello", 0)
	f.sync(t)
	conv := f.conversations(t)[0]
	_, assignee := f.h.SignInUser(t)
	f.h.Exec(t, `UPDATE communications.conversations SET assigned_user_id = $2 WHERE id = $1`, conv, assignee)

	f.h.Advance(2 * time.Minute)
	f.text("4242", "m2", "Anyone?", 0)
	f.fake.Push(f.channel.Address, contracts.MessengerEvent{
		Kind: contracts.MessengerEventMessage, Participant: "4242", At: f.h.Now(), PlatformMessageID: "m_suite", Text: "From the Business Suite", Echo: true,
	})
	f.sync(t)

	if got := f.messageCount(t); got != 3 {
		t.Fatalf("messages = %d, want the two of the participant and the echo", got)
	}
	events, _ := publishedEvents(t, f.h, "communications.conversation.message_received")
	if len(events) != 2 {
		t.Fatalf("%d events, want one per participant message and none for the echo: %v", len(events), events)
	}
	for _, e := range events {
		if e["conversationId"] != conv || e["channelId"] != f.channel.Id || e["channelType"] != "messenger" ||
			e["autoReply"] != false || e["unreadable"] != false {
			t.Errorf("event = %v", e)
		}
	}
	if events[0]["assignedUserId"] != nil || events[1]["assignedUserId"] != assignee.String() {
		t.Errorf("assignees = %v and %v, want none then %s", events[0]["assignedUserId"], events[1]["assignedUserId"], assignee)
	}
}

// requireConversationLane fails t unless every event the module published for
// the conversation is on the one outbox lane, communications.conversation, so a
// handler receives a conversation's messages and assignments in order.
func requireConversationLane(t *testing.T, h *modtest.Harness, conversation string, want int) {
	t.Helper()
	rows, err := h.Pool().Query(context.Background(),
		`SELECT DISTINCT aggregate FROM platform.events WHERE module = 'communications' AND aggregate_id = $1`, conversation)
	if err != nil {
		t.Fatalf("read the outbox: %v", err)
	}
	defer rows.Close()
	var lanes []string
	for rows.Next() {
		var lane string
		if err := rows.Scan(&lane); err != nil {
			t.Fatalf("scan the outbox: %v", err)
		}
		lanes = append(lanes, lane)
	}
	if len(lanes) != 1 || lanes[0] != "communications.conversation" {
		t.Errorf("aggregates = %v, want only communications.conversation", lanes)
	}
	if got := h.Count(t, `SELECT count(*) FROM platform.events WHERE module = 'communications' AND aggregate_id = $1`, conversation); got != want {
		t.Errorf("%d events for the conversation, want %d", got, want)
	}
}

// TestEvents_ShareTheConversationLane: both events are on the outbox lane
// communications.conversation, with the conversation as the aggregate id.
func TestEvents_ShareTheConversationLane(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	id := in.deliver(in.plainMail("Hello"), in.h.Now().Add(time.Minute), "graph-lane")
	in.sync(t)
	conv := in.importedMessage(t, id).conversationID
	c := in.h.SignIn(t, "communications:conversations-manage", "communications:conversations-view")
	_, user := in.h.SignInUser(t)
	if r := assign(c, conv.String(), user.String()); r.Status != http.StatusOK {
		t.Fatalf("assign: status %d body %s", r.Status, r.Body)
	}
	requireConversationLane(t, in.h, conv.String(), 2)
}

// failAtCommit makes every commit that touches the row of table matching where
// fail, by a deferred constraint trigger, so the failure comes after the
// handler has published; the trigger is dropped when the test ends.
func failAtCommit(t *testing.T, h *modtest.Harness, table, where string) {
	t.Helper()
	name := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	h.Exec(t, fmt.Sprintf(`CREATE FUNCTION public.%[1]s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF %[3]s THEN RAISE EXCEPTION 'refused at commit'; END IF; RETURN NULL; END $$`, name, table, where))
	h.Exec(t, fmt.Sprintf(`CREATE CONSTRAINT TRIGGER %[1]s AFTER INSERT OR UPDATE ON %[2]s DEFERRABLE INITIALLY DEFERRED
		FOR EACH ROW EXECUTE FUNCTION public.%[1]s()`, name, table))
	t.Cleanup(func() {
		h.Exec(t, fmt.Sprintf(`DROP TRIGGER IF EXISTS %[1]s ON %[2]s`, name, table))
		h.Exec(t, fmt.Sprintf(`DROP FUNCTION IF EXISTS public.%[1]s()`, name))
	})
}

// TestPatchConversation_ARolledBackAssignmentPublishesNothing: a PATCH whose
// transaction fails after the event was published leaves the assignee and the
// outbox as they were.
func TestPatchConversation_ARolledBackAssignmentPublishesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	reply := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, reply, newConversationBody("x@example.test"))
	c := h.SignIn(t, "communications:conversations-manage", "communications:conversations-view")
	_, user := h.SignInUser(t)
	failAtCommit(t, h, "communications.conversations", fmt.Sprintf("NEW.id = '%s' AND NEW.assigned_user_id IS NOT NULL", m.ConversationId))

	r := assign(c, m.ConversationId, user.String(), modtest.SkipContract("the transaction is made to fail at commit"))
	if r.Status != http.StatusInternalServerError {
		t.Fatalf("status %d body %s, want 500", r.Status, r.Body)
	}
	if events, _ := publishedEvents(t, h, "communications.conversation.assigned"); len(events) != 0 {
		t.Errorf("%d events after a rolled-back PATCH, want none", len(events))
	}
	if got := h.Count(t, `SELECT count(*) FROM communications.conversations WHERE id = $1 AND assigned_user_id IS NULL`, m.ConversationId); got != 1 {
		t.Errorf("the rolled-back PATCH assigned the conversation")
	}
}

// TestMailboxSync_ARolledBackImportPublishesNothing: an import whose
// transaction fails at commit publishes no event and stores no message.
func TestMailboxSync_ARolledBackImportPublishesNothing(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	failAtCommit(t, in.h, "communications.inbound_receipts", fmt.Sprintf("NEW.channel_id = '%s'", in.channelID))
	in.deliver(in.plainMail("Hello"), in.h.Now().Add(time.Minute), "graph-rollback")
	in.sync(t)

	if events, _ := publishedEvents(t, in.h, "communications.conversation.message_received"); len(events) != 0 {
		t.Errorf("%d events after a rolled-back import, want none", len(events))
	}
	if n := countInbound(t, in.h, in.channelID); n != 0 {
		t.Errorf("%d messages stored by a rolled-back import, want none", n)
	}
}

// TestPatchConversation_AStatusPatchKeepsAnAssignmentMadeMeanwhile: a PATCH
// that does not mention assignedUserId, held at the conversation's row lock
// while another writer assigns, keeps that assignment and publishes nothing.
func TestPatchConversation_AStatusPatchKeepsAnAssignmentMadeMeanwhile(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	reply := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, reply, newConversationBody("x@example.test"))
	c := h.SignIn(t, "communications:conversations-manage", "communications:conversations-view")
	_, user := h.SignInUser(t)

	ctx := context.Background()
	tx, err := h.Pool().BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM communications.conversations WHERE id = $1 FOR UPDATE`, m.ConversationId); err != nil {
		t.Fatal(err)
	}
	done := make(chan *modtest.Response, 1)
	go func() {
		done <- c.Do(http.MethodPatch, "/api/v1/communications/conversations/"+m.ConversationId, map[string]any{"status": "closed"})
	}()
	// The PATCH has read the conversation and waits for the lock.
	for range 100 {
		if h.Count(t, `SELECT count(*) FROM pg_locks WHERE NOT granted`) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := tx.Exec(ctx, `UPDATE communications.conversations SET assigned_user_id = $2 WHERE id = $1`, m.ConversationId, user); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if r := <-done; r.Status != http.StatusOK {
		t.Fatalf("status %d body %s", r.Status, r.Body)
	}
	if got := h.Count(t, `SELECT count(*) FROM communications.conversations WHERE id = $1 AND status = 'closed' AND assigned_user_id = $2`, m.ConversationId, user); got != 1 {
		t.Errorf("the status PATCH did not keep the assignee made meanwhile")
	}
	if events, _ := publishedEvents(t, h, "communications.conversation.assigned"); len(events) != 0 {
		t.Errorf("%d assigned events for a status change, want none", len(events))
	}
}

// TestPatchConversation_ConcurrentAssignmentsEachPublishOnce: two assignments
// racing each see what the other committed, so the events chain: one real
// change, one event, the second's previous assignee the first's.
func TestPatchConversation_ConcurrentAssignmentsEachPublishOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	reply := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, reply, newConversationBody("x@example.test"))
	c := h.SignIn(t, "communications:conversations-manage", "communications:conversations-view")
	_, a := h.SignInUser(t)
	_, b := h.SignInUser(t)

	var wg sync.WaitGroup
	for _, u := range []uuid.UUID{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := assign(c, m.ConversationId, u.String()); r.Status != http.StatusOK {
				t.Errorf("assign: status %d body %s", r.Status, r.Body)
			}
		}()
	}
	wg.Wait()

	events, _ := publishedEvents(t, h, "communications.conversation.assigned")
	if len(events) != 2 {
		t.Fatalf("%d events, want one per change: %v", len(events), events)
	}
	if events[0]["previousAssignedUserId"] != nil || events[1]["previousAssignedUserId"] != events[0]["assignedUserId"] {
		t.Errorf("events do not chain: %v", events)
	}
	if got := h.Count(t, `SELECT count(*) FROM communications.conversations WHERE id = $1 AND assigned_user_id = $2`,
		m.ConversationId, events[1]["assignedUserId"]); got != 1 {
		t.Errorf("the conversation does not stand at the last event's assignee")
	}
}

// TestPatchConversation_ResendingTheAssigneeDoesNotAskTheDirectory: naming the
// assignee the conversation already has is a no-op even if that user has since
// been disabled.
func TestPatchConversation_ResendingTheAssigneeDoesNotAskTheDirectory(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	reply := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, reply, newConversationBody("x@example.test"))
	c := h.SignIn(t, "communications:conversations-manage", "communications:conversations-view")
	_, user := h.SignInUser(t)
	if r := assign(c, m.ConversationId, user.String()); r.Status != http.StatusOK {
		t.Fatalf("assign: status %d body %s", r.Status, r.Body)
	}
	h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, user)
	if r := assign(c, m.ConversationId, user.String()); r.Status != http.StatusOK {
		t.Fatalf("resend: status %d body %s, want 200", r.Status, r.Body)
	}
	if events, _ := publishedEvents(t, h, "communications.conversation.assigned"); len(events) != 1 {
		t.Errorf("%d events, want only the first assignment", len(events))
	}
}
