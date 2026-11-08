package agents_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/agents"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// What an agent remembers (virtual employees design V27): the notes on the
// customer's page, their deletion, their counts, their erasure with the customer,
// the contact and the agent, and their retention.

// note keeps a note of the agent about a subject.
func (g *queueRig) note(t *testing.T, kind, subject, text string, at time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	g.h.Exec(t, `INSERT INTO agents.memory (id, agent_id, subject_type, subject_id, text, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		id, g.agent.ID, kind, subject, text, at)
	return id
}

func TestMemory_TheContractAnswersOneCustomersNotesNewestFirstPerAgent(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	now := g.h.Now()
	older := g.note(t, "customer", "42", "Prefers e-mail", now.Add(-48*time.Hour))
	newer := g.note(t, "customer", "42", "Has a dog", now.Add(-time.Hour))
	g.note(t, "customer", "7", "Another customer's note", now)
	g.note(t, "conversation", uuid.NewString(), "A conversation's note", now)

	slot := agents.Module().AgentMemory(g.h.Deps())
	got, err := slot.AboutCustomer(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].AgentName != "Hanna" || len(got[0].Notes) != 2 || got[0].Notes[0].ID != newer || got[0].Notes[1].ID != older {
		t.Fatalf("notes = %+v, want Hanna's two about customer 42, the newer first", got)
	}
	if got[0].Full {
		t.Error("two notes are a full memory")
	}
	for i := range 48 {
		g.note(t, "customer", "42", fmt.Sprintf("more %d", i), now.Add(-time.Duration(i+3)*time.Hour))
	}
	if full, err := slot.AboutCustomer(context.Background(), 42); err != nil || len(full) != 1 || !full[0].Full {
		t.Errorf("fifty notes = %+v, %v, want the memory full", full, err)
	}
	if none, err := slot.AboutCustomer(context.Background(), 999); err != nil || len(none) != 0 {
		t.Errorf("a customer nothing is remembered about = %v, %v, want none", none, err)
	}
}

func TestMemory_DeletingANoteNeedsManageAndIsAudited(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	id := g.note(t, "customer", "42", "Secret preference", g.h.Now())
	path := "/api/v1/agents/memory/" + id.String()

	if r := g.h.SignIn(t, "agents:view").Do(http.MethodDelete, path, nil); r.Status != http.StatusForbidden {
		t.Errorf("delete with agents:view = %d, want 403", r.Status)
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.memory WHERE id = $1`, id); n != 1 {
		t.Fatal("a refused delete removed the note")
	}
	m := manager(t, g.h)
	if r := m.Do(http.MethodDelete, path, nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete = %d %s, want 204", r.Status, r.Body)
	}
	if r := m.Do(http.MethodDelete, path, nil); r.Status != http.StatusNotFound {
		t.Errorf("delete again = %d, want 404", r.Status)
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.memory WHERE id = $1`, id); n != 0 {
		t.Error("the note is still there")
	}
	// The record names the note and who it was about, never what it said.
	if n := g.rows(t, `SELECT count(*) FROM agents.events WHERE action = 'memory.deleted' AND target = $1 AND before::text LIKE '%42%'`, id.String()); n != 1 {
		t.Errorf("audit rows = %d, want one naming the subject", n)
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.events WHERE before::text LIKE '%Secret preference%' OR after::text LIKE '%Secret preference%'`); n != 0 {
		t.Error("the audit holds the text of the note")
	}
}

func TestMemory_TheAgentPageCountsWhatItKeeps(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	g.note(t, "customer", "42", "a", g.h.Now())
	g.note(t, "customer", "42", "b", g.h.Now())
	g.note(t, "customer", "7", "c", g.h.Now())
	g.note(t, "conversation", uuid.NewString(), "d", g.h.Now())
	v := g.h.SignIn(t, "agents:view")
	r := v.Do(http.MethodGet, "/api/v1/agents/agents/"+g.agent.ID+"/memory", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("counts = %d %s", r.Status, r.Body)
	}
	var c struct{ CustomerNotes, ConversationNotes, Customers int }
	r.JSON(&c)
	if c.CustomerNotes != 3 || c.ConversationNotes != 1 || c.Customers != 2 {
		t.Errorf("counts = %+v, want 3 customer notes about 2 customers and 1 conversation note", c)
	}
	if r := v.Do(http.MethodGet, "/api/v1/agents/agents/"+uuid.NewString()+"/memory", nil); r.Status != http.StatusNotFound {
		t.Errorf("an unknown agent = %d, want 404", r.Status)
	}
	if r := g.h.SignIn(t).Do(http.MethodGet, "/api/v1/agents/agents/"+g.agent.ID+"/memory", nil); r.Status != http.StatusForbidden {
		t.Errorf("counts without agents:view = %d, want 403", r.Status)
	}
}

func TestMemory_ADeletedAgentForgetsAndIsNotShown(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	g.note(t, "customer", "42", "x", g.h.Now())
	if r := manager(t, g.h).Do(http.MethodDelete, "/api/v1/agents/agents/"+g.agent.ID, nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete the agent = %d %s", r.Status, r.Body)
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.memory`); n != 0 {
		t.Errorf("notes of a deleted agent = %d, want none", n)
	}
}

func TestMemory_ErasedWithTheCustomerTheContactAndTheirConversations(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	data := g.personal(t)
	cust, other := int32(42), int32(7)
	now := g.h.Now()

	conv, run := g.erasureRun(t, &cust, &cust, nil)
	lateConv, _ := g.erasureRun(t, nil, &cust, nil)
	strangerConv, strangerRun := g.erasureRun(t, &other, &other, nil)
	g.note(t, "customer", "42", "Customer note", now)
	g.note(t, "conversation", conv.String(), "Note about the customer's conversation", now)
	g.note(t, "conversation", lateConv.String(), "Note about a conversation linked later", now)
	runNote := g.note(t, "customer", "999", "Written in the customer's run, whatever it named", now)
	g.h.Exec(t, `UPDATE agents.memory SET run_id = $2 WHERE id = $1`, runNote, run)
	g.note(t, "customer", "7", "Another customer's note", now)
	g.note(t, "conversation", strangerConv.String(), "Another customer's conversation", now)
	strangerNote := g.note(t, "customer", "888", "Written in another customer's run", now)
	g.h.Exec(t, `UPDATE agents.memory SET run_id = $2 WHERE id = $1`, strangerNote, strangerRun)
	// A suggestion that rests on an item of the customer's goes with it.
	var itemID uuid.UUID
	if err := g.h.Pool().QueryRow(context.Background(), `SELECT id FROM agents.review_items WHERE conversation_id = $1`, conv).Scan(&itemID); err != nil {
		t.Fatal(err)
	}
	g.h.Exec(t, `INSERT INTO agents.instruction_suggestions (id, role_id, new_text, rationale, item_ids, created_at) VALUES ($1, $2, 'x', 'y', $3, now()), ($4, $2, 'x2', 'y2', '{}', now())`,
		uuid.New(), g.role.ID, []uuid.UUID{itemID}, uuid.New())

	exported, err := data.ExportCustomerData(context.Background(), cust)
	if err != nil || exported == nil || !strings.Contains(fmt.Sprintf("%+v", exported), "Customer note") {
		t.Fatalf("export = %+v, %v, want the customer's notes in it", exported, err)
	}
	counts := erase(t, g, func(ctx context.Context, tx pgx.Tx) ([]contracts.ErasedData, error) {
		return data.EraseCustomerData(ctx, tx, cust)
	})
	if counts["agents.memory"] != 4 || counts["agents.instructionSuggestions"] != 1 {
		t.Errorf("erased = %v, want 4 notes and 1 suggestion", counts)
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.memory`); n != 3 {
		t.Errorf("notes left = %d, want the other customer's three", n)
	}
	if n := g.rows(t, `SELECT count(*) FROM agents.instruction_suggestions`); n != 1 {
		t.Errorf("suggestions left = %d, want the one that rests on no erased item", n)
	}

	// A contact: the notes of the conversations they took part in, and what a run of those wrote.
	contactConv, contactRun := g.erasureRun(t, nil, nil, []int32{55})
	g.note(t, "conversation", contactConv.String(), "About the contact's conversation", now)
	contactNote := g.note(t, "customer", "777", "Written in the contact's run", now)
	g.h.Exec(t, `UPDATE agents.memory SET run_id = $2 WHERE id = $1`, contactNote, contactRun)
	before := g.rows(t, `SELECT count(*) FROM agents.memory`)
	counts = erase(t, g, func(ctx context.Context, tx pgx.Tx) ([]contracts.ErasedData, error) {
		return data.(contracts.ContactDataEraser).EraseContactData(ctx, tx, []int32{55})
	})
	if counts["agents.memory"] != 2 || g.rows(t, `SELECT count(*) FROM agents.memory`) != before-2 {
		t.Errorf("contact erasure = %v, want the two notes of the contact's conversation and run", counts)
	}
}

func TestMemory_ExpiresWithTheRetention(t *testing.T) {
	t.Parallel()
	g := newQueueRig(t)
	day := 24 * time.Hour
	old := g.note(t, "customer", "42", "old", g.h.Now().Add(-200*day))
	recent := g.note(t, "customer", "42", "recent", g.h.Now().Add(-100*day))
	if err := agents.RetentionCycle(g.h.Deps()); err != nil {
		t.Fatal(err)
	}
	if g.rows(t, `SELECT count(*) FROM agents.memory WHERE id = $1`, old) != 0 || g.rows(t, `SELECT count(*) FROM agents.memory WHERE id = $1`, recent) != 1 {
		t.Error("retention kept the old note or removed the recent one")
	}
}
