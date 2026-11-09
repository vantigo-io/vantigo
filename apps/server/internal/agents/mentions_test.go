package agents_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// A note that mentions an agent wakes it (virtual employees design V32): the
// trigger turns communications.conversation.note_mentioned into a run request for
// the agent's role on the conversation's channel, at once, with the note's id and
// not its text.

func (g *rig) mention(t *testing.T, conv uuid.UUID, channel uuid.UUID, by uuid.UUID, mentioned ...uuid.UUID) uuid.UUID {
	t.Helper()
	msg := uuid.New()
	g.publish(t, contracts.ConversationNoteMentioned{ConversationID: conv, MessageID: msg, ChannelID: channel, ByUserID: by, MentionedUserIDs: mentioned})
	return msg
}

func (g *rig) noteOf(t *testing.T, conv uuid.UUID) string {
	t.Helper()
	var note string
	if err := g.h.Pool().QueryRow(context.Background(), `SELECT note FROM agents.run_queue WHERE conversation_id = $1 AND taken_at IS NULL`, conv).Scan(&note); err != nil {
		t.Fatalf("read the request's note: %v", err)
	}
	return note
}

func TestMention_AnAgentMentionedInANoteIsWokenAtOnceUnderItsRoleOnTheChannel(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	conv := uuid.New()
	msg := g.mention(t, conv, g.channel, uuid.New(), uuid.New(), g.agentUser(t))
	got := g.requests(t, conv)
	if len(got) != 1 || got[0].Taken {
		t.Fatalf("requests = %+v, want one untaken request", got)
	}
	if !got[0].NotBefore.Equal(g.h.Now()) {
		t.Errorf("not before %s, want now (%s): a colleague asked, there is no debounce", got[0].NotBefore, g.h.Now())
	}
	if note := g.noteOf(t, conv); note != "mention:"+msg.String() {
		t.Errorf("the request's note = %q, want the id of the note and nothing of its text", note)
	}
}

func TestMention_WakesNothingForAPersonAnotherChannelAPausedAgentOrAHandedOffConversation(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	colleague := uuid.New()

	// A person alone.
	conv := uuid.New()
	g.mention(t, conv, g.channel, uuid.New(), colleague)
	// The agent, on a channel none of its roles is bound to.
	other := uuid.New()
	g.mention(t, other, uuid.New(), uuid.New(), g.agentUser(t))
	// The agent, on a conversation that is handed off.
	handed := uuid.New()
	g.h.Exec(t, `INSERT INTO agents.handed_off (conversation_id, role_id, at, reason) VALUES ($1, $2, now(), 'hand_off')`, handed, g.role.ID)
	g.mention(t, handed, g.channel, uuid.New(), g.agentUser(t))
	// A note an agent wrote: two agents never wake each other.
	byAgent := uuid.New()
	g.mention(t, byAgent, g.channel, g.agentUser(t), g.agentUser(t))
	for name, c := range map[string]uuid.UUID{"a person": conv, "another channel": other, "handed off": handed, "an agent's note": byAgent} {
		if n := len(g.requests(t, c)); n != 0 {
			t.Errorf("%s made %d requests, want none", name, n)
		}
	}

	// A paused agent.
	if r := g.owner.Do(http.MethodPost, "/api/v1/agents/agents/"+g.agent.ID+"/pause", nil); r.Status != http.StatusOK {
		t.Fatalf("pause: %d %s", r.Status, r.Body)
	}
	paused := uuid.New()
	g.mention(t, paused, g.channel, uuid.New(), g.agentUser(t))
	if n := len(g.requests(t, paused)); n != 0 {
		t.Errorf("a paused agent got %d requests", n)
	}
}

func TestMention_MeetsAMessageRequestWaitingInItsDebounceAndMakesItDueNow(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	conv := uuid.New()
	g.message(t, conv, nil)
	if got := g.requests(t, conv); len(got) != 1 || !got[0].NotBefore.After(g.h.Now()) {
		t.Fatalf("requests = %+v, want one still in its wait", got)
	}
	msg := g.mention(t, conv, g.channel, uuid.New(), g.agentUser(t))
	got := g.requests(t, conv)
	if len(got) != 1 || !got[0].NotBefore.Equal(g.h.Now()) || got[0].Reason != "message" {
		t.Fatalf("requests = %+v, want the same request, due now", got)
	}
	if !strings.HasSuffix(g.noteOf(t, conv), msg.String()) {
		t.Errorf("the request's note does not name the note that mentioned the agent")
	}
}
