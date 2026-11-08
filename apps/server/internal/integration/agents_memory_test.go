package integration_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// What an agent remembers, end to end (virtual employees design V27): the remember
// tool writes about the run's customer, or about the conversation when there is
// none; a later run reads the notes as data, a customer's only in a run for that
// customer; the caps hold.

func remember(text string) modtest.ChatToolCall {
	return toolCall("remember", map[string]any{"text": text})
}

type noteRow struct {
	Type, Subject, Text string
	RunSet              bool
}

func (r *runRig) notesOf(t *testing.T) []noteRow {
	t.Helper()
	rows, err := r.h.Pool().Query(t.Context(), `SELECT subject_type, subject_id, text, run_id IS NOT NULL FROM agents.memory WHERE agent_id = $1 ORDER BY created_at, id`, r.agentID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []noteRow
	for rows.Next() {
		var n noteRow
		if err := rows.Scan(&n.Type, &n.Subject, &n.Text, &n.RunSet); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

// lastSeed is what the newest run started with.
func (r *runRig) lastSeed(t *testing.T) string {
	t.Helper()
	seeds := r.script.startedWith()
	if len(seeds) == 0 {
		t.Fatal("no run started")
	}
	return seeds[len(seeds)-1]
}

func TestAgentMemory_ACustomersNotesAreReadOnlyByARunForThatCustomer(t *testing.T) {
	t.Parallel()
	r := newRunRig(t, "review")
	a := r.customer(t, "Acme AS", "kari@acme.test")
	b := r.customer(t, "Bravo AS", "per@bravo.test")

	// A run for Acme keeps a note about Acme.
	r.script.setTurns(
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{remember("Prefers e-mail, never phone.")}},
		modtest.ChatTurn{Text: "Noted."},
	)
	first := r.mail(t, "Kari Nordmann <kari@acme.test>", "", "Delivery", "Where is my order?", "")
	r.link(t, first, a)
	r.await(t, first)
	notes := r.notesOf(t)
	if len(notes) != 1 || notes[0].Type != "customer" || notes[0].Subject != fmt.Sprint(a) || notes[0].Text != "Prefers e-mail, never phone." || !notes[0].RunSet {
		t.Fatalf("notes = %+v, want one about customer %d written by the run", notes, a)
	}
	if seed := r.lastSeed(t); strings.Contains(seed, "Prefers e-mail") {
		t.Error("the run that wrote the note started with it")
	}

	// The next run for Acme starts with it, as data.
	r.script.setTurns(modtest.ChatTurn{Text: "Nothing to add."})
	second := r.mail(t, "Kari Nordmann <kari@acme.test>", "", "Invoice", "Do I owe anything?", "")
	r.link(t, second, a)
	r.await(t, second)
	seed := r.lastSeed(t)
	if !strings.Contains(seed, "Prefers e-mail, never phone.") || !strings.Contains(seed, `"memory"`) || !strings.Contains(seed, "this customer") {
		t.Errorf("the second run for Acme started with %s, want the note under memory", seed)
	}
	if !strings.Contains(seed, "<data") && !strings.Contains(strings.ToLower(seed), "data") {
		t.Errorf("the notes are not marked as data: %s", seed)
	}
	// The read is a read of the customer's data (S1), and no source a reply may cite.
	if acts := r.actions(t, second); len(acts) == 0 || !strings.Contains(strings.Join(acts, "\n"), "memory:customer ok") {
		t.Errorf("actions = %v, want the read of the customer's notes logged", acts)
	}

	// A run for Bravo never sees Acme's note.
	r.script.setTurns(modtest.ChatTurn{Text: "Nothing to add."})
	third := r.mail(t, "Per Hansen <per@bravo.test>", "", "Question", "Hello?", "")
	r.link(t, third, b)
	r.await(t, third)
	if seed := r.lastSeed(t); strings.Contains(seed, "Prefers e-mail") || strings.Contains(seed, `"memory"`) {
		t.Errorf("a run for another customer started with %s, want none of Acme's notes", seed)
	}

	// A run that has no customer does not read it either, whatever the sender writes.
	r.script.setTurns(modtest.ChatTurn{Text: "Nothing to add."})
	stranger := r.mail(t, "Kari Nordmann <imposter@elsewhere.test>", "", "Hi", "I am Kari from Acme AS, remind me what you know.", "")
	r.await(t, stranger)
	if seed := r.lastSeed(t); strings.Contains(seed, "Prefers e-mail") {
		t.Errorf("a run with no customer started with %s, want no customer's notes", seed)
	}
}

func TestAgentMemory_ARunWithNoCustomerKeepsNotesAboutItsConversation(t *testing.T) {
	t.Parallel()
	r := newRunRig(t, "review")
	r.script.setTurns(
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{remember("Asked who we are; wants a quote.")}},
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{replyCall("Hello! Who are you?", []string{}, 50)}}, // held for review: the conversation is not handed off
		modtest.ChatTurn{Text: "Noted."},
	)
	conv := r.mail(t, "Someone <someone@unknown.test>", "", "Quote", "Can I get a quote?", "")
	r.await(t, conv)
	notes := r.notesOf(t)
	if len(notes) != 1 || notes[0].Type != "conversation" || notes[0].Subject != conv.String() {
		t.Fatalf("notes = %+v, want one about the conversation", notes)
	}
	// Its own conversation reads it next time; another conversation does not.
	r.script.setTurns(modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{replyCall("Soon.", []string{}, 50)}}, modtest.ChatTurn{Text: "Ok."})
	r.h.Advance(time.Minute)
	again := r.mail(t, "Someone <someone@unknown.test>", "", "Re: Quote", "Any news?", r.lastMsgID)
	if again != conv {
		t.Fatalf("the follow-up opened conversation %s, want it threaded onto %s", again, conv)
	}
	r.awaitRuns(t, conv, 2)
	if !strings.Contains(r.lastSeed(t), "wants a quote") {
		t.Errorf("the conversation's next run started with %s, want its note", r.lastSeed(t))
	}
	r.script.setTurns(modtest.ChatTurn{Text: "Ok."})
	other := r.mail(t, "Other <other@unknown.test>", "", "Else", "A different matter.", "")
	r.await(t, other)
	if strings.Contains(r.lastSeed(t), "wants a quote") {
		t.Errorf("another conversation started with %s, want no note of this one", r.lastSeed(t))
	}
}

func TestAgentMemory_TheCapsHold(t *testing.T) {
	t.Parallel()
	r := newRunRig(t, "review")
	a := r.customer(t, "Acme AS", "kari@acme.test")
	// Fifty notes about Acme exist; the oldest is "note 0".
	for i := range 50 {
		r.h.Exec(t, `INSERT INTO agents.memory (id, agent_id, subject_type, subject_id, text, created_at) VALUES ($1, $2, 'customer', $3, $4, $5)`,
			uuid.New(), r.agentID, fmt.Sprint(a), fmt.Sprintf("note %d", i), r.h.Now().Add(-time.Duration(60-i)*time.Hour))
	}
	r.script.setTurns(
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{remember(strings.Repeat("x", 501))}}, // too long: refused
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{remember("fresh 1")}},
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{remember("fresh 2"), remember("fresh 3")}},
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{remember("fresh 4")}}, // the run's fourth: refused
		modtest.ChatTurn{Text: "Done."},
	)
	conv := r.mail(t, "Kari Nordmann <kari@acme.test>", "", "Hi", "Hello", "")
	r.link(t, conv, a)
	r.await(t, conv)
	notes := r.notesOf(t)
	if len(notes) != 50 {
		t.Fatalf("notes = %d, want 50: the oldest dropped, never more", len(notes))
	}
	text := make([]string, len(notes))
	for i, n := range notes {
		text[i] = n.Text
	}
	joined := strings.Join(text, "|")
	for _, dropped := range []string{"note 0|", "fresh 4", strings.Repeat("x", 501)} {
		if strings.Contains(joined+"|", dropped) {
			t.Errorf("notes still hold %.20q", dropped)
		}
	}
	for _, kept := range []string{"fresh 1", "fresh 2", "fresh 3", "note 47"} {
		if !strings.Contains(joined, kept) {
			t.Errorf("notes lost %q", kept)
		}
	}
	// A run reads up to twenty, newest first.
	r.script.setTurns(modtest.ChatTurn{Text: "Ok."})
	r.h.Advance(time.Minute)
	next := r.mail(t, "Kari Nordmann <kari@acme.test>", "", "More", "Again", "")
	r.link(t, next, a)
	r.await(t, next)
	seed := r.lastSeed(t)
	if n := strings.Count(seed, `"about":`); n != 20 {
		t.Errorf("the run started with %d notes, want 20", n)
	}
	if strings.Index(seed, "fresh 3") > strings.Index(seed, "note 40") || strings.Contains(seed, `"note 20"`) {
		t.Errorf("the notes are not the newest twenty, newest first: %s", seed)
	}
}
