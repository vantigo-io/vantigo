package communications_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the linker (M15; delivery D, reading 19): the sender's address
// looked up in the customer directory before the import's transaction; one
// customer links the conversation automatically, several become candidates,
// none links nothing; one contact links the participant; a conversation with a
// customer is never linked again.

// linkDirectory is the package's directory answering ContactsByEmail from a
// map by address in lower case, or failing; it records the addresses asked.
type linkDirectory struct {
	fakeDirectory
	mu      sync.Mutex
	matches map[string][]contracts.ContactMatch
	err     error
	asked   []string
}

func (d *linkDirectory) ContactsByEmail(_ context.Context, email string) ([]contracts.ContactMatch, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.asked = append(d.asked, email)
	if d.err != nil {
		return nil, d.err
	}
	return d.matches[strings.ToLower(email)], nil
}

// senderOf is the bare, lower-cased address of m's From.
func senderOf(m testMail) string {
	return strings.ToLower(strings.TrimSuffix(strings.SplitN(m.From, "<", 2)[1], ">"))
}

// linkState is a conversation's customer, its source and its candidates, and
// the sender participant's contact.
type linkState struct {
	customer   *int32
	source     *string
	candidates int
	contact    *int32
}

func readLink(t *testing.T, h *modtest.Harness, m inboundMessage) linkState {
	t.Helper()
	var s linkState
	if err := h.Pool().QueryRow(context.Background(),
		`SELECT c.customer_id, c.customer_association_source,
		        (SELECT count(*) FROM communications.conversation_customer_candidates x WHERE x.conversation_id = c.id),
		        p.contact_id
		 FROM communications.conversations c
		 JOIN communications.conversation_messages m ON m.conversation_id = c.id
		 JOIN communications.participants p ON p.id = m.participant_id
		 WHERE m.id = $1`, m.id).Scan(&s.customer, &s.source, &s.candidates, &s.contact); err != nil {
		t.Fatalf("read link: %v", err)
	}
	return s
}

// TestLinker_Outcomes: one contact of one customer links both; two customers
// become candidates and no customer; two contacts of one customer link the
// customer but no contact; an unknown sender links nothing.
func TestLinker_Outcomes(t *testing.T) {
	t.Parallel()
	directory := &linkDirectory{matches: map[string][]contracts.ContactMatch{}}
	in := newInbox(t, nil, nil, modtest.WithDirectory(directory))

	one := in.plainMail("one")
	directory.matches[senderOf(one)] = []contracts.ContactMatch{{ContactID: 2001, CandidateCustomerIDs: []int32{1001}}}
	several := in.plainMail("several")
	directory.matches[senderOf(several)] = []contracts.ContactMatch{
		{ContactID: 2001, CandidateCustomerIDs: []int32{1001}}, {ContactID: 2002, CandidateCustomerIDs: []int32{1002, 1001}},
	}
	sameCustomer := in.plainMail("same customer")
	directory.matches[senderOf(sameCustomer)] = []contracts.ContactMatch{
		{ContactID: 2001, CandidateCustomerIDs: []int32{1001}}, {ContactID: 2002, CandidateCustomerIDs: []int32{1001}},
	}
	unknown := in.plainMail("unknown")

	got := readLink(t, in.h, in.importOne(t, one, ""))
	if got.customer == nil || *got.customer != 1001 || got.source == nil || *got.source != "automatic" ||
		got.contact == nil || *got.contact != 2001 || got.candidates != 0 {
		t.Errorf("one contact, one customer: %+v, want 1001 automatic and contact 2001", got)
	}
	got = readLink(t, in.h, in.importOne(t, several, ""))
	if got.customer != nil || got.candidates != 2 || got.contact != nil {
		t.Errorf("two customers: %+v, want two candidates, no customer, no contact", got)
	}
	got = readLink(t, in.h, in.importOne(t, sameCustomer, ""))
	if got.customer == nil || *got.customer != 1001 || got.contact != nil {
		t.Errorf("two contacts of one customer: %+v, want 1001 and no contact", got)
	}
	got = readLink(t, in.h, in.importOne(t, unknown, ""))
	if got.customer != nil || got.candidates != 0 || got.contact != nil {
		t.Errorf("unknown sender: %+v, want nothing linked", got)
	}
	for _, asked := range directory.asked {
		if asked != strings.ToLower(asked) {
			t.Errorf("the directory was asked %q, want the address in lower case", asked)
		}
	}
}

// TestLinker_NeverRelinks: a conversation with a customer — set by hand or by
// an earlier message — keeps it when another sender of another customer
// writes in the thread.
func TestLinker_NeverRelinks(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"manual", "automatic"} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			directory := &linkDirectory{matches: map[string][]contracts.ContactMatch{}}
			in := newInbox(t, nil, nil, modtest.WithDirectory(directory))
			first := in.importOne(t, in.plainMail("first"), "")
			in.h.Exec(t, `UPDATE communications.conversations SET customer_id = 1002, customer_association_source = $2 WHERE id = $1`,
				first.conversationID, source)

			second := in.plainMail("second sender")
			second.InReplyTo = *first.rfc
			directory.matches[senderOf(second)] = []contracts.ContactMatch{{ContactID: 2001, CandidateCustomerIDs: []int32{1001}}}
			got := readLink(t, in.h, in.importOne(t, second, ""))
			if got.customer == nil || *got.customer != 1002 || *got.source != source || got.candidates != 0 {
				t.Errorf("%+v, want 1002 %s kept and no candidates", got, source)
			}
			if got.contact == nil || *got.contact != 2001 {
				t.Errorf("the second sender's contact = %v, want 2001 linked all the same", got.contact)
			}
		})
	}
}

// TestLinker_NoOrAFailingDirectory: Customers off (no directory) or a
// directory that fails imports the message unlinked.
func TestLinker_NoOrAFailingDirectory(t *testing.T) {
	t.Parallel()
	for name, directory := range map[string]contracts.CustomerDirectory{
		"none":    nil,
		"failing": &linkDirectory{err: errors.New("customers is down")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			in := newInbox(t, nil, nil, modtest.WithDirectory(directory))
			m := in.importOne(t, in.plainMail("x"), "")
			if got := readLink(t, in.h, m); got.customer != nil || got.candidates != 0 || got.contact != nil {
				t.Errorf("%+v, want the message imported unlinked", got)
			}
			if m.id == uuid.Nil {
				t.Error("no message")
			}
		})
	}
}
