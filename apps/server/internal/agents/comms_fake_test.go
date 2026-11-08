package agents_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// fakeComms stands in for Communications' handlers, which a test of this package
// may not compose (module boundaries): it answers the conversation read, the
// reply with the Idempotency-Key and the refusals the module reacts to, the note
// and the conversation patch, so the review queue's approvals run against the
// real dispatcher, gate and grants. The real thing is composed by the
// integration test.
type fakeComms struct {
	mu      sync.Mutex
	convs   map[uuid.UUID]*fakeConv
	replies []sentReply
	patches []json.RawMessage
	notes   []string
}

type fakeConv struct {
	ID          uuid.UUID
	ChannelID   uuid.UUID
	AssignedTo  *uuid.UUID
	WindowState string // "" for a channel with none
	Messages    []fakeMsg
	keys        map[string]sentReply
}

type fakeMsg struct {
	ID      uuid.UUID
	Address string
	Text    string
	At      time.Time
	// Author is set for an outbound message: the user who wrote it.
	Author *uuid.UUID
}

func (m fakeMsg) outbound() bool { return m.Author != nil }

// sentReply is a reply the fake received.
type sentReply struct {
	Conversation uuid.UUID
	Text         string
	Key          string
	InReplyTo    uuid.UUID
	Components   string
	MessageID    uuid.UUID
}

func newFakeComms() *fakeComms { return &fakeComms{convs: map[uuid.UUID]*fakeConv{}} }

// conversation adds a conversation with one inbound message and answers it.
func (f *fakeComms) conversation(channel uuid.UUID, at time.Time) *fakeConv {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := &fakeConv{
		ID: uuid.New(), ChannelID: channel, keys: map[string]sentReply{},
		Messages: []fakeMsg{{ID: uuid.New(), Address: "kari@example.test", Text: "Where is my order?", At: at}},
	}
	f.convs[c.ID] = c
	return c
}

func (f *fakeComms) update(id uuid.UUID, fn func(*fakeConv)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f.convs[id])
}

func (f *fakeComms) sent() []sentReply {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentReply(nil), f.replies...)
}

func (f *fakeComms) patched() []json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]json.RawMessage(nil), f.patches...)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func commsError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": code}})
}

func (c *fakeConv) lastInbound() fakeMsg {
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if !c.Messages[i].outbound() {
			return c.Messages[i]
		}
	}
	return fakeMsg{}
}

func (c *fakeConv) json() map[string]any {
	msgs := make([]map[string]any, 0, len(c.Messages))
	for _, m := range c.Messages {
		if m.outbound() {
			msgs = append(msgs, map[string]any{"id": m.ID, "direction": "outbound", "occurredAt": m.At, "textBody": m.Text, "authorUserId": m.Author})
			continue
		}
		msgs = append(msgs, map[string]any{
			"id": m.ID, "direction": "inbound", "occurredAt": m.At, "textBody": m.Text,
			"participant": map[string]any{"address": m.Address, "displayName": "Kari"},
		})
	}
	out := map[string]any{
		"id": c.ID, "channelId": c.ChannelID, "channelType": "email", "channelAddress": "front@example.test", "status": "open",
		"subject": "Order", "assignedUserId": c.AssignedTo, "tags": []any{}, "messages": msgs,
		"capabilities": map[string]any{"subject": true, "html": true, "cc": true, "components": false},
	}
	if c.WindowState != "" {
		out["replyWindow"] = map[string]any{"state": c.WindowState}
	}
	return out
}

func (f *fakeComms) handler() http.Handler {
	mux := http.NewServeMux()
	conv := func(w http.ResponseWriter, r *http.Request) (*fakeConv, bool) {
		id, err := uuid.Parse(r.PathValue("id"))
		f.mu.Lock()
		c := f.convs[id]
		f.mu.Unlock()
		if err != nil || c == nil {
			commsError(w, http.StatusNotFound, "not_found")
			return nil, false
		}
		return c, true
	}
	mux.HandleFunc("GET /api/v1/communications/conversations/{id}", func(w http.ResponseWriter, r *http.Request) {
		if c, ok := conv(w, r); ok {
			f.mu.Lock()
			defer f.mu.Unlock()
			writeJSON(w, http.StatusOK, c.json())
		}
	})
	mux.HandleFunc("POST /api/v1/communications/conversations/{id}/notes", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := conv(w, r); !ok {
			return
		}
		raw, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.notes = append(f.notes, string(raw))
		f.mu.Unlock()
		writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
	})
	mux.HandleFunc("PATCH /api/v1/communications/conversations/{id}", func(w http.ResponseWriter, r *http.Request) {
		c, ok := conv(w, r)
		if !ok {
			return
		}
		raw, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.patches = append(f.patches, raw)
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, c.json())
	})
	mux.HandleFunc("POST /api/v1/communications/conversations/{id}/reply", func(w http.ResponseWriter, r *http.Request) {
		c, ok := conv(w, r)
		if !ok {
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			TextBody           string          `json:"textBody"`
			InReplyToMessageID uuid.UUID       `json:"inReplyToMessageId"`
			Components         json.RawMessage `json:"components"`
		}
		_ = json.Unmarshal(raw, &body)
		key := r.Header.Get("Idempotency-Key")
		f.mu.Lock()
		defer f.mu.Unlock()
		if key == "" {
			commsError(w, http.StatusBadRequest, "idempotency_key_required")
			return
		}
		if prev, seen := c.keys[key]; seen {
			if prev.Text != body.TextBody {
				commsError(w, http.StatusConflict, "idempotency_key_reused")
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]any{"conversationId": c.ID, "messageId": prev.MessageID, "status": "queued"})
			return
		}
		switch {
		case c.WindowState == "closed":
			commsError(w, http.StatusUnprocessableEntity, "reply_window_closed")
			return
		case c.lastInbound().ID != body.InReplyToMessageID:
			commsError(w, http.StatusConflict, "conversation_moved")
			return
		case c.AssignedTo != nil && !strings.HasPrefix(r.RemoteAddr, "agent-"+c.AssignedTo.String()):
			commsError(w, http.StatusConflict, "conversation_taken")
			return
		}
		sent := sentReply{Conversation: c.ID, Text: body.TextBody, Key: key, InReplyTo: body.InReplyToMessageID, Components: string(body.Components), MessageID: uuid.New()}
		c.keys[key] = sent
		f.replies = append(f.replies, sent)
		if user, err := uuid.Parse(strings.TrimPrefix(r.RemoteAddr, "agent-")); err == nil {
			c.Messages = append(c.Messages, fakeMsg{ID: sent.MessageID, Text: body.TextBody, At: time.Now().AddDate(5, 0, 0), Author: &user})
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"conversationId": c.ID, "messageId": sent.MessageID, "status": "queued"})
	})
	return mux
}

// stubCommunications stands in for Communications: the permissions the module's
// operations are checked against and the handlers above.
func stubCommunications(f *fakeComms) module.Module {
	return module.Module{
		Name: "communications",
		Permissions: []contracts.Permission{
			{Key: "communications:conversations-view", Display: "View conversations", Description: "See conversations.", Category: "Communications"},
			{Key: "communications:conversations-reply", Display: "Reply to conversations", Description: "Answer conversations.", Category: "Communications"},
			{Key: "communications:conversations-manage", Display: "Manage conversations", Description: "Change conversations.", Category: "Communications"},
		},
		Mount: func(module.Deps) (http.Handler, error) { return f.handler(), nil },
	}
}

// checker is the content check's answer, for the test to change.
type checker struct {
	mu     sync.Mutex
	answer string
}

func (c *checker) set(a string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.answer = a
}

func (c *checker) model() *modtest.FakeChatModel {
	return modtest.NewFakeChatModelFunc(func(int, modtest.ChatRequest) modtest.ChatTurn {
		c.mu.Lock()
		defer c.mu.Unlock()
		return modtest.ChatTurn{Text: c.answer}
	})
}

const (
	checkClean = `{"claimsHuman": false, "promises": []}`
	checkHuman = `{"claimsHuman": true, "promises": []}`
)
