package agents

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents/store"
)

func ptrTo[T any](v T) *T { return &v }

func conversationWith(customer *int32, source string, sender string, contactID *int32) conversation {
	c := conversation{CustomerID: customer}
	if source != "" {
		c.CustomerAssociationSource = &source
	}
	m := message{Direction: "inbound"}
	m.Participant = &struct {
		Address     string  `json:"address"`
		ContactID   *int32  `json:"contactId"`
		DisplayName *string `json:"displayName"`
	}{Address: sender, ContactID: contactID}
	c.Messages = []message{m}
	return c
}

// TestRunCustomer is V9b: the conversation's customer counts when a person linked
// it, or when the customer linker did and the sender is the customer's contact.
func TestRunCustomer(t *testing.T) {
	t.Parallel()
	contacts := &contactSet{Emails: []string{"kari@example.test"}, IDs: []int32{7}}
	tests := []struct {
		name string
		c    conversation
		set  *contactSet
		want *int32
	}{
		{"a person linked it", conversationWith(ptrTo(int32(5)), "manual", "stranger@example.test", nil), nil, ptrTo(int32(5))},
		{"the linker, and the sender is a contact", conversationWith(ptrTo(int32(5)), "automatic", "Kari@Example.test", nil), contacts, ptrTo(int32(5))},
		{"the linker, and a Messenger participant linked to a contact", conversationWith(ptrTo(int32(5)), "automatic", "4242", ptrTo(int32(7))), contacts, ptrTo(int32(5))},
		{"the linker, and the sender is not a contact", conversationWith(ptrTo(int32(5)), "automatic", "attacker@example.test", nil), contacts, nil},
		{"the linker, and the contacts cannot be read", conversationWith(ptrTo(int32(5)), "automatic", "kari@example.test", nil), nil, nil},
		{"no customer", conversationWith(nil, "", "kari@example.test", nil), contacts, nil},
		{"a customer with no source", conversationWith(ptrTo(int32(5)), "", "kari@example.test", nil), contacts, nil},
	}
	for _, tt := range tests {
		got := runCustomer(tt.c, func() *contactSet { return tt.set })
		switch {
		case (got == nil) != (tt.want == nil), got != nil && *got != *tt.want:
			t.Errorf("%s: customer = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestParseContacts_ReadsEveryEmailAndId(t *testing.T) {
	t.Parallel()
	body := `{"contacts":[{"contact":{"id":7,"firstName":"Kari","lastName":"N","email":"Kari@Example.test"},"email":"billing@example.test"},{"contact":{"id":9,"firstName":"Ola","lastName":"N","email":null}}]}`
	set := parseContacts([]byte(body))
	if !set.includes("kari@example.test", nil) || !set.includes("BILLING@example.test", nil) {
		t.Errorf("emails = %v, want both addresses", set.Emails)
	}
	if !set.includes("", ptrTo(int32(9))) || set.includes("", ptrTo(int32(1))) {
		t.Errorf("ids = %v, want 7 and 9 only", set.IDs)
	}
	if set.includes("other@example.test", nil) {
		t.Error("a stranger is a contact")
	}
}

// TestPin_EveryOperationOnTheConversationIsTheRunsOwn and the patch is the
// status alone: the carry-over BLOCKER of A1's review.
func TestPin_EveryOperationOnTheConversationIsTheRunsOwn(t *testing.T) {
	t.Parallel()
	own := uuid.New()
	rs := &runState{tpl: &frontDesk, run: store.AgentsRun{ConversationID: own}}
	other := uuid.NewString()

	for _, op := range []string{opGetConversation, opPatch, opTagPut, opTagDelete} {
		if r := rs.pin(op, map[string]any{"id": other}, json.RawMessage(`{"status":"closed"}`)); r == nil || !strings.Contains(r.Content, "own_conversation_only") {
			t.Errorf("%s with another conversation: %+v, want own_conversation_only", op, r)
		}
		if r := rs.pin(op, map[string]any{"id": strings.ToUpper(own.String())}, json.RawMessage(`{"status":"closed"}`)); r != nil {
			t.Errorf("%s with the run's own conversation: refused %s", op, r.Content)
		}
		if r := rs.pin(op, nil, json.RawMessage(`{"status":"closed"}`)); r != nil {
			t.Errorf("%s with no id: refused %s (the tool fills it in)", op, r.Content)
		}
	}
	// A listing names no conversation: the customer scope is its pin.
	if r := rs.pin("getCommunicationsConversations", map[string]any{"id": other}, nil); r != nil {
		t.Errorf("the list was pinned: %s", r.Content)
	}
}

func TestPin_ThePatchTakesTheStatusAlone(t *testing.T) {
	t.Parallel()
	rs := &runState{tpl: &frontDesk, run: store.AgentsRun{ConversationID: uuid.New()}}
	for body, ok := range map[string]bool{
		`{"status":"closed"}`:                           true,
		`{"status":"closed","customerId":"12"}`:         false,
		`{"customerId":"12"}`:                           false,
		`{"assignedUserId":"` + uuid.NewString() + `"}`: false,
		`{"status":"open","assignedUserId":null}`:       false,
		`{}`:   false,
		`null`: false,
		``:     false,
	} {
		r := rs.pin(opPatch, nil, json.RawMessage(body))
		if ok != (r == nil) {
			t.Errorf("patch body %q: refused = %v, want refused = %v", body, r != nil, !ok)
		}
	}
}

func TestCallOperation_TheModulesOwnWritesAreNotForCallOperation(t *testing.T) {
	t.Parallel()
	for _, op := range []string{opReply, opNote, opTagPut, opTagDelete} {
		if !contains(reservedOperations, op) {
			t.Errorf("%s may be called by the model directly and skip the pipeline", op)
		}
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
