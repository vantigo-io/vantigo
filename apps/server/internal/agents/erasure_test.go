package agents_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/agents"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// Erasure (virtual employees design V26): a customer's runs, transcripts and
// review items go by customer_id and by every conversation the module saw linked
// to them; a contact's by the conversations they took part in.

// erasureRun makes a run on a conversation, with a transcript, an action, an item and the markers.
func (g *queueRig) erasureRun(t *testing.T, customer *int32, linked *int32, contacts []int32) (conv uuid.UUID, run uuid.UUID) {
	t.Helper()
	c := g.comms.conversation(g.channel, g.h.Now())
	id := g.seedOn(t, c, "reply", map[string]any{}, nil)
	run, _ = uuid.Parse(runOf(t, g, id))
	g.h.Exec(t, `UPDATE agents.runs SET customer_id = $2 WHERE id = $1`, run, customer)
	g.h.Exec(t, `UPDATE agents.review_items SET customer_id = $2 WHERE id = $1`, id, customer)
	g.h.Exec(t, `INSERT INTO agents.run_messages (run_id, role, text, created_at) VALUES ($1, 'user', 'A secret', $2), ($1, 'assistant', 'Noted', $2)`, run, g.h.Now())
	g.h.Exec(t, `INSERT INTO agents.actions (run_id, agent_id, customer_id, operation_id, ref, created_at) VALUES ($1, $2, $3, 'getInvoices', 'getInvoices', $4)`, run, g.agent.ID, customer, g.h.Now())
	g.h.Exec(t, `INSERT INTO agents.handed_off (conversation_id, role_id, at, reason) VALUES ($1, $2, now(), 'hand_off')`, c.ID, g.role.ID)
	g.h.Exec(t, `INSERT INTO agents.seen_senders (channel_id, address, conversation_id, first_seen_at) VALUES ($1, $2, $3, now())`, g.channel, "who-"+c.ID.String()+"@example.test", c.ID)
	if contacts == nil {
		contacts = []int32{}
	}
	g.h.Exec(t, `INSERT INTO agents.conversation_links (conversation_id, customer_id, contact_ids, seen_at) VALUES ($1, $2, $3, now())`, c.ID, linked, contacts)
	return c.ID, run
}

func (g *queueRig) personal(t *testing.T) contracts.CustomerPersonalData {
	t.Helper()
	data := agents.Module().CustomerPersonalData(g.h.Deps())
	if data == nil {
		t.Fatal("the module has no personal data")
	}
	return data
}

func erase(t *testing.T, g *queueRig, fn func(ctx context.Context, tx pgx.Tx) ([]contracts.ErasedData, error)) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	tx, err := g.h.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out, err := fn(ctx, tx)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	for _, e := range out {
		counts[e.Kind] = e.Count
	}
	return counts
}

func TestErasure_ACustomersRunsGoByCustomerAndByEveryConversationOfTheirs(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	data := g.personal(t)
	cust, other := int32(42), int32(7)

	scoped, scopedRun := g.erasureRun(t, &cust, &cust, nil) // linked and in scope
	late, lateRun := g.erasureRun(t, nil, &cust, nil)       // linked after its run: no scope, but seen linked
	stranger, strangerRun := g.erasureRun(t, &other, &other, nil)
	unlinked, unlinkedRun := g.erasureRun(t, nil, nil, nil)
	// An action of an unrelated run that names the customer is blanked, not deleted.
	g.h.Exec(t, `UPDATE agents.actions SET customer_id = $2 WHERE run_id = $1`, strangerRun, cust)

	exported, err := data.ExportCustomerData(context.Background(), cust)
	if err != nil || exported == nil {
		t.Fatalf("export = %v, %v, want a section", exported, err)
	}
	if got, err := data.ExportCustomerData(context.Background(), 999); err != nil || got != nil {
		t.Errorf("export for a customer the module never met = %v, %v, want nil", got, err)
	}

	counts := erase(t, g, func(ctx context.Context, tx pgx.Tx) ([]contracts.ErasedData, error) {
		return data.EraseCustomerData(ctx, tx, cust)
	})
	if counts["agents.runs"] != 2 || counts["agents.runMessages"] != 4 || counts["agents.reviewItems"] != 2 || counts["agents.actions"] != 1 {
		t.Errorf("erased = %v, want 2 runs, 4 transcript messages, 2 items and 1 blanked action", counts)
	}
	for name, run := range map[string]uuid.UUID{"scoped": scopedRun, "late": lateRun} {
		if g.runExists(t, run) {
			t.Errorf("the %s run was kept", name)
		}
	}
	for name, run := range map[string]uuid.UUID{"stranger": strangerRun, "unlinked": unlinkedRun} {
		if !g.runExists(t, run) {
			t.Errorf("the %s run was erased", name)
		}
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.run_messages WHERE text = 'A secret'`); n != 2 {
		t.Errorf("transcripts left = %d, want the two of the others", n)
	}
	for _, c := range []uuid.UUID{scoped, late} {
		if n := g.rows(t, `SELECT (SELECT count(*) FROM agents.handed_off WHERE conversation_id = $1) + (SELECT count(*) FROM agents.seen_senders WHERE conversation_id = $1) + (SELECT count(*) FROM agents.conversation_links WHERE conversation_id = $1)`, c); n != 0 {
			t.Errorf("markers of an erased conversation left: %d", n)
		}
	}
	for _, c := range []uuid.UUID{stranger, unlinked} {
		if n := g.rows(t, `SELECT count(*) FROM agents.handed_off WHERE conversation_id = $1`, c); n != 1 {
			t.Errorf("a marker of another customer's conversation was erased")
		}
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.actions WHERE customer_id = $1`, cust); n != 0 {
		t.Errorf("the customer's id is left in the action log: %d", n)
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.actions WHERE run_id = $1`, strangerRun); n != 1 {
		t.Errorf("the other run's action was deleted, not blanked")
	}

	// Run again for an id it already erased, it finds nothing.
	counts = erase(t, g, func(ctx context.Context, tx pgx.Tx) ([]contracts.ErasedData, error) {
		return data.EraseCustomerData(ctx, tx, cust)
	})
	for kind, n := range counts {
		if n != 0 {
			t.Errorf("a second erasure reports %s = %d, want zeros", kind, n)
		}
	}
	if got, err := data.ExportCustomerData(context.Background(), cust); err != nil || got != nil {
		t.Errorf("export after the erasure = %v, %v, want nil", got, err)
	}
}

func TestErasure_AContactsConversationsGoWithThem(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	data := g.personal(t)
	eraser, ok := data.(contracts.ContactDataEraser)
	if !ok {
		t.Fatal("the module's personal data is no ContactDataEraser")
	}
	cust := int32(42)
	withContact, runA := g.erasureRun(t, &cust, &cust, []int32{5, 6})
	_, runB := g.erasureRun(t, &cust, &cust, []int32{6})
	_ = withContact

	counts := erase(t, g, func(ctx context.Context, tx pgx.Tx) ([]contracts.ErasedData, error) {
		return eraser.EraseContactData(ctx, tx, []int32{5})
	})
	if counts["agents.runs"] != 1 || counts["agents.reviewItems"] != 1 {
		t.Errorf("erased = %v, want one run and one item", counts)
	}
	if g.runExists(t, runA) || !g.runExists(t, runB) {
		t.Errorf("run of the contact kept=%v, run of another contact kept=%v", g.runExists(t, runA), g.runExists(t, runB))
	}
	if counts := erase(t, g, func(ctx context.Context, tx pgx.Tx) ([]contracts.ErasedData, error) {
		return eraser.EraseContactData(ctx, tx, nil)
	}); counts["agents.runs"] != 0 {
		t.Errorf("no contacts erased %v", counts)
	}
}

func TestNotify_ANewHeldItemMailsTheHandOffPersonAndNobodyElse(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	person := uuid.New()
	id := uuid.New()
	agents.NotifyHeldItem(g.h.Deps(), nil, "Hanna", id, "a reply")
	if n := len(g.h.Notices()); n != 0 {
		t.Fatalf("a role with no hand-off person notified %d", n)
	}
	agents.NotifyHeldItem(g.h.Deps(), &person, "Hanna", id, "a reply")
	notices := g.h.Notices()
	if len(notices) != 1 || notices[0].UserID != person {
		t.Fatalf("notices = %+v, want one to the hand-off person", notices)
	}
	en, nb := notices[0].Notice.Texts["en"], notices[0].Notice.Texts["nb"]
	if en.Subject != "Hanna has a reply waiting for review" || nb.Subject != "Hanna har et svar som venter på gjennomgang" {
		t.Errorf("subjects = %q / %q", en.Subject, nb.Subject)
	}
	if want := "/agents/review/" + id.String(); !contains(en.Body, want) || !contains(nb.Body, want) {
		t.Errorf("bodies %q / %q do not link %s", en.Body, nb.Body, want)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestWithdrawal_UnbindingAChannelOrDeletingARoleWithdrawsWhatWaitsOnIt(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	onChannel, convA := g.seed(t, "reply", map[string]any{}, nil)
	otherChannel := uuid.New()
	other := g.comms.conversation(otherChannel, g.h.Now())
	onOther := g.seedOn(t, other, "reply", map[string]any{}, nil)
	g.h.Exec(t, `UPDATE agents.review_items SET channel_id = $2 WHERE id = $1`, onOther, otherChannel)
	g.h.Exec(t, `INSERT INTO agents.role_channels (role_id, channel_id) VALUES ($1, $2)`, g.role.ID, otherChannel)

	state := func(id uuid.UUID) string {
		var s string
		if err := g.h.Pool().QueryRow(context.Background(), `SELECT state FROM agents.review_items WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	// Unbind the first channel only.
	if r := g.owner.Do("PUT", rolePath(g.agent, g.role)+"/channels", map[string]any{"channelIds": []string{otherChannel.String()}}); r.Status != 200 {
		t.Fatalf("unbind: %d %s", r.Status, r.Body)
	}
	if state(onChannel) != "withdrawn" || state(onOther) != "waiting" {
		t.Errorf("after unbinding: %s and %s, want the unbound channel's item withdrawn and the other waiting", state(onChannel), state(onOther))
	}
	if g.handedOff(t, convA.ID) == "" {
		t.Error("the unbound channel's conversation was not handed off")
	}
	// Delete the role: the rest is withdrawn.
	if r := g.owner.Do("DELETE", rolePath(g.agent, g.role), nil); r.Status != 204 {
		t.Fatalf("delete the role: %d %s", r.Status, r.Body)
	}
	if state(onOther) != "withdrawn" || g.handedOff(t, other.ID) == "" {
		t.Errorf("after deleting the role: %s, handed off %q", state(onOther), g.handedOff(t, other.ID))
	}
}
