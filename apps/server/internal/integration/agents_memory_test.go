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

// toolResults are what the run's tools answered the model, from the transcript.
func (r *runRig) toolResults(t *testing.T, conv uuid.UUID) string {
	t.Helper()
	rows, err := r.h.Pool().Query(t.Context(), `SELECT m.text FROM agents.run_messages m JOIN agents.runs ru ON ru.id = m.run_id
		WHERE ru.conversation_id = $1 AND m.role = 'tool' ORDER BY m.id`, conv)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return strings.Join(out, "\n")
}

func TestAgentMemory_ARunWithNoCustomerKeepsNothingAndSaysWhy(t *testing.T) {
	t.Parallel()
	r := newRunRig(t, "review")
	r.script.setTurns(
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{remember("Asked who we are; wants a quote.")}},
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{replyCall("Hello! Who are you?", []string{}, 50)}},
		modtest.ChatTurn{Text: "Noted."},
	)
	conv := r.mail(t, "Someone <someone@unknown.test>", "", "Quote", "Can I get a quote?", "")
	r.await(t, conv)
	if notes := r.notesOf(t); len(notes) != 0 {
		t.Fatalf("notes = %+v, want none: a run with no customer keeps no note", notes)
	}
	if got := r.toolResults(t, conv); !strings.Contains(got, "no_customer") {
		t.Errorf("the tool answered %s, want a refusal that says no customer is known", got)
	}
}

func TestAgentMemory_TheCapsHold(t *testing.T) {
	t.Parallel()
	r := newRunRig(t, "review")
	a := r.customer(t, "Acme AS", "kari@acme.test")
	r.script.setTurns(
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{remember(strings.Repeat("x", 501))}}, // too long: refused
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{remember("fresh 1")}},
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{remember("fresh 1")}}, // already held
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{remember("fresh 2"), remember("fresh 3")}},
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{remember("fresh 4")}}, // the run's fourth: refused
		modtest.ChatTurn{Text: "Done."},
	)
	conv := r.mail(t, "Kari Nordmann <kari@acme.test>", "", "Hi", "Hello", "")
	r.link(t, conv, a)
	r.await(t, conv)
	if n := len(r.notesOf(t)); n != 3 {
		t.Fatalf("notes = %d, want 3: three a run, the duplicate and the long one not kept", n)
	}
	got := r.toolResults(t, conv)
	for _, want := range []string{"note_too_long", "already_remembered", "too_many_notes"} {
		if !strings.Contains(got, want) {
			t.Errorf("no tool answer %q in %s", want, got)
		}
	}
}

func TestAgentMemory_AFullMemoryRefusesAndNeverEvicts(t *testing.T) {
	t.Parallel()
	r := newRunRig(t, "review")
	a := r.customer(t, "Acme AS", "kari@acme.test")
	// Forty-nine notes about Acme exist; the oldest is "note 0".
	for i := range 49 {
		r.h.Exec(t, `INSERT INTO agents.memory (id, agent_id, subject_type, subject_id, text, created_at) VALUES ($1, $2, 'customer', $3, $4, $5)`,
			uuid.New(), r.agentID, fmt.Sprint(a), fmt.Sprintf("note %d", i), r.h.Now().Add(-time.Duration(60-i)*time.Hour))
	}
	r.script.setTurns(
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{remember("fresh 1")}}, // the fiftieth
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{remember("fresh 2")}}, // refused: full
		modtest.ChatTurn{Text: "Done."},
	)
	conv := r.mail(t, "Kari Nordmann <kari@acme.test>", "", "Hi", "Hello", "")
	r.link(t, conv, a)
	r.await(t, conv)
	notes := r.notesOf(t)
	if len(notes) != 50 {
		t.Fatalf("notes = %d, want 50", len(notes))
	}
	joined := ""
	for _, n := range notes {
		joined += n.Text + "|"
	}
	if !strings.Contains(joined, "note 0|") || !strings.Contains(joined, "fresh 1|") || strings.Contains(joined, "fresh 2") {
		t.Errorf("notes = %s, want nothing evicted and the fifty-first refused", joined)
	}
	if got := r.toolResults(t, conv); !strings.Contains(got, "memory_full") || !strings.Contains(got, "a person must delete a note") {
		t.Errorf("the tool answered %s, want memory_full", got)
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
	if strings.Index(seed, "fresh 1") > strings.Index(seed, "note 40") || strings.Contains(seed, `"note 20"`) {
		t.Errorf("the notes are not the newest twenty, newest first: %s", seed)
	}
}
