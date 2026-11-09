package communications_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is what an internal note's mentions need of Communications (virtual
// employees design V32): the ids stored with the note, the one event published in
// its transaction with no text in it, the mail a person mentioned gets, and the
// refusals.

func addNote(c *modtest.Client, conversation string, body map[string]any) *modtest.Response {
	return c.Do(http.MethodPost, "/api/v1/communications/conversations/"+conversation+"/notes", body)
}

type mentionRig struct {
	h            *modtest.Harness
	c            *modtest.Client
	author       uuid.UUID
	conversation string
}

func newMentionRig(t *testing.T) mentionRig {
	t.Helper()
	h := newHarness(t)
	setupChannel(t, h)
	reply := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	m := createConversation(t, reply, newConversationBody("x@example.test"))
	c, author := h.SignInUser(t, "communications:conversations-manage", "communications:conversations-view", "agents:view")
	return mentionRig{h: h, c: c, author: author, conversation: m.ConversationId}
}

// nameOf is the display name a user is mentioned by.
func nameOf(t *testing.T, h *modtest.Harness, id uuid.UUID) string {
	t.Helper()
	return modtest.One[string](t, h, `SELECT display_name FROM identity.users WHERE id = $1`, id)
}

func TestNoteMentions_AreStoredWithTheNoteAndPublishedOnceWithoutItsText(t *testing.T) {
	t.Parallel()
	g := newMentionRig(t)
	_, colleague := g.h.SignInUser(t)
	_, agent := g.h.AgentContext(t, nil)

	r := addNote(g.c, g.conversation, map[string]any{
		"textBody": "@" + nameOf(t, g.h, colleague) + " and @" + nameOf(t, g.h, agent) + " please look at 4711, the customer is angry",
		// The author, a repeat and the agent: the author is left out, a repeat counts once.
		"mentions": []string{colleague.String(), agent.String(), colleague.String(), g.author.String()},
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("add a note with mentions: %d %s", r.Status, r.Body)
	}
	var created conversationMutationJSON
	r.JSON(&created)

	if got := g.h.Count(t, `SELECT count(*) FROM communications.conversation_message_mentions WHERE message_id = $1`, created.MessageId); got != 2 {
		t.Errorf("%d mentions stored, want the colleague and the agent", got)
	}
	events, aggregates := publishedEvents(t, g.h, "communications.conversation.note_mentioned")
	if len(events) != 1 || aggregates[0] != g.conversation {
		t.Fatalf("events = %v (aggregates %v), want one on the conversation", events, aggregates)
	}
	e := events[0]
	ids, _ := e["mentionedUserIds"].([]any)
	if e["messageId"] != *created.MessageId || e["byUserId"] != g.author.String() || e["channelId"] == nil || len(ids) != 2 {
		t.Errorf("event = %v, want the message, the author, the channel and two users", e)
	}
	if len(e) != 5 {
		t.Errorf("the event has %d fields, want the five of the contract: %v", len(e), e)
	}
	for k, v := range e {
		if s, ok := v.(string); ok && strings.Contains(s, "angry") {
			t.Errorf("the note's text is in the event (%s)", k)
		}
	}
}

func TestNoteMentions_APersonIsMailedAndAnAgentIsNot(t *testing.T) {
	t.Parallel()
	g := newMentionRig(t)
	_, colleague := g.h.SignInUser(t, "communications:conversations-view")
	_, agent := g.h.AgentContext(t, nil)

	r := addNote(g.c, g.conversation, map[string]any{
		"textBody": "@" + nameOf(t, g.h, colleague) + " @" + nameOf(t, g.h, agent) + ", the invoice is wrong",
		"mentions": []string{colleague.String(), agent.String()},
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("add a note: %d %s", r.Status, r.Body)
	}
	notices := g.h.Notices()
	if len(notices) != 1 || notices[0].UserID != colleague {
		t.Fatalf("notices = %+v, want one, to the person", notices)
	}
	for lang, text := range notices[0].Notice.Texts {
		if strings.Contains(text.Body, "invoice") || strings.Contains(text.Subject, "invoice") {
			t.Errorf("the %s notice holds the note's text: %q", lang, text.Body)
		}
	}
}

func TestNoteMentions_APersonWhoCannotReadConversationsIsNotMailed(t *testing.T) {
	t.Parallel()
	g := newMentionRig(t)
	_, blind := g.h.SignInUser(t)
	r := addNote(g.c, g.conversation, map[string]any{"textBody": "@" + nameOf(t, g.h, blind) + " look", "mentions": []string{blind.String()}})
	if r.Status != http.StatusCreated {
		t.Fatalf("add a note: %d %s", r.Status, r.Body)
	}
	if n := len(g.h.Notices()); n != 0 {
		t.Errorf("%d notices to a person who cannot read conversations, want none", n)
	}
	if got := g.h.Count(t, `SELECT count(*) FROM communications.conversation_message_mentions`); got != 1 {
		t.Errorf("%d mentions stored, want the one", got)
	}
}

func TestNoteMentions_AMentionMustBeNamedInTheTextAndAnAgentNeedsAgentsView(t *testing.T) {
	t.Parallel()
	g := newMentionRig(t)
	_, colleague := g.h.SignInUser(t)
	_, agent := g.h.AgentContext(t, nil)
	if r := addNote(g.c, g.conversation, map[string]any{"textBody": "no name here", "mentions": []string{colleague.String()}}); r.Status != http.StatusBadRequest {
		t.Errorf("a mention whose @Name is not in the text = %d %s, want 400", r.Status, r.Body)
	}
	noView, _ := g.h.SignInUser(t, "communications:conversations-manage", "communications:conversations-view")
	if r := addNote(noView, g.conversation, map[string]any{"textBody": "@" + nameOf(t, g.h, agent) + " help", "mentions": []string{agent.String()}}); r.Status != http.StatusBadRequest {
		t.Errorf("mentioning an agent without agents:view = %d %s, want 400", r.Status, r.Body)
	}
	if r := addNote(noView, g.conversation, map[string]any{"textBody": "@" + nameOf(t, g.h, colleague) + " help", "mentions": []string{colleague.String()}}); r.Status != http.StatusCreated {
		t.Errorf("mentioning a person without agents:view = %d %s, want 201", r.Status, r.Body)
	}
}

func TestNoteMentions_AreRefusedForAnUnknownOrDisabledUserOrTooMany(t *testing.T) {
	t.Parallel()
	g := newMentionRig(t)
	_, disabled := g.h.SignInUser(t)
	g.h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, disabled)

	many := make([]string, 11)
	for i := range many {
		many[i] = uuid.NewString()
	}
	for name, mentions := range map[string][]string{"unknown": {uuid.NewString()}, "disabled": {disabled.String()}, "too many": many} {
		r := addNote(g.c, g.conversation, map[string]any{"textBody": "a note", "mentions": mentions})
		if r.Status != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", name, r.Status, r.Body)
		}
	}
	if got := g.h.Count(t, `SELECT count(*) FROM communications.conversation_messages WHERE conversation_id = $1 AND direction = 'internal_note'`, g.conversation); got != 0 {
		t.Errorf("%d notes stored by refused requests", got)
	}
	if events, _ := publishedEvents(t, g.h, "communications.conversation.note_mentioned"); len(events) != 0 {
		t.Errorf("%d events after refused requests", len(events))
	}
}

func TestNoteMentions_ANoteWithoutThemPublishesNothingAndAMissingConversationIs404(t *testing.T) {
	t.Parallel()
	g := newMentionRig(t)
	if r := addNote(g.c, g.conversation, map[string]any{"textBody": "just a note"}); r.Status != http.StatusCreated {
		t.Fatalf("add a note: %d %s", r.Status, r.Body)
	}
	if events, _ := publishedEvents(t, g.h, "communications.conversation.note_mentioned"); len(events) != 0 {
		t.Errorf("%d events for a note that mentions no one", len(events))
	}
	_, colleague := g.h.SignInUser(t)
	if r := addNote(g.c, uuid.NewString(), map[string]any{"textBody": "@" + nameOf(t, g.h, colleague), "mentions": []string{colleague.String()}}); r.Status != http.StatusNotFound {
		t.Errorf("a missing conversation: %d, want 404", r.Status)
	}
	if got := g.h.Count(t, `SELECT count(*) FROM communications.conversation_message_mentions`); got != 0 {
		t.Errorf("%d mentions stored", got)
	}
}

func TestNoteMentions_ANoteAnAgentWritesMentionsNoOne(t *testing.T) {
	t.Parallel()
	g := newMentionRig(t)
	_, colleague := g.h.SignInUser(t)
	ctx, _ := asAgent(t, g.h, uuid.New())
	status, body, err := g.h.AgentCall(t, ctx, "postCommunicationsConversationsByIdNotes", map[string]any{"id": g.conversation}, nil,
		map[string]any{"textBody": "@colleague, over to you", "mentions": []string{colleague.String()}})
	if err != nil || status != http.StatusCreated {
		t.Fatalf("an agent's note: %d %s %v", status, body, err)
	}
	if got := g.h.Count(t, `SELECT count(*) FROM communications.conversation_message_mentions`); got != 0 {
		t.Errorf("%d mentions stored for an agent's note, want none", got)
	}
	if events, _ := publishedEvents(t, g.h, "communications.conversation.note_mentioned"); len(events) != 0 {
		t.Errorf("an agent's note published %d mention events, want none", len(events))
	}
	if n := len(g.h.Notices()); n != 0 {
		t.Errorf("%d notices for an agent's note", n)
	}
}
