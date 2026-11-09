package agents

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// This file reads the conversation as Communications answers it, so the
// pipeline and the prompt know what the run is answering. The module imports
// nothing of Communications (module boundaries): it calls the operation as the
// agent and decodes the JSON for the few fields it needs.

// conversation is the part of GET /conversations/{id} the run uses.
type conversation struct {
	ID                        uuid.UUID  `json:"id"`
	ChannelID                 uuid.UUID  `json:"channelId"`
	ChannelType               string     `json:"channelType"`
	ChannelAddress            string     `json:"channelAddress"`
	Status                    string     `json:"status"`
	Subject                   *string    `json:"subject"`
	AssignedUserID            *uuid.UUID `json:"assignedUserId"`
	CustomerID                *int32     `json:"customerId"`
	CustomerAssociationSource *string    `json:"customerAssociationSource"`
	Tags                      []struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	} `json:"tags"`
	Messages []message `json:"messages"`
	Reply    *struct {
		State    string     `json:"state"`
		ClosesAt *time.Time `json:"closesAt"`
	} `json:"replyWindow"`
	Capabilities *capabilities `json:"capabilities"`
}

// message is one message of the conversation.
type message struct {
	ID            uuid.UUID  `json:"id"`
	Direction     string     `json:"direction"`
	OccurredAt    time.Time  `json:"occurredAt"`
	Subject       *string    `json:"subject"`
	AuthorUserID  *uuid.UUID `json:"authorUserId"`
	TextBody      *string    `json:"textBody"`
	AutoSubmitted *bool      `json:"autoSubmitted"`
	Unreadable    *bool      `json:"unreadable"`
	Participant   *struct {
		Address     string  `json:"address"`
		ContactID   *int32  `json:"contactId"`
		DisplayName *string `json:"displayName"`
	} `json:"participant"`
}

// capabilities is what a message on the channel may carry (V13).
type capabilities struct {
	Subject            bool   `json:"subject"`
	HTML               bool   `json:"html"`
	CC                 bool   `json:"cc"`
	Components         bool   `json:"components"`
	ShareEmail         bool   `json:"shareEmail"`
	MaxText            *int32 `json:"maxText"`
	MaxAttachmentBytes *int64 `json:"maxAttachmentBytes"`
}

// parseConversation decodes the conversation detail.
func parseConversation(body []byte) (conversation, error) {
	var c conversation
	if err := json.Unmarshal(body, &c); err != nil {
		return conversation{}, fmt.Errorf("agents: read the conversation: %w", err)
	}
	return c, nil
}

// latestInbound is the newest inbound message that has a sender, nil when none.
func (c conversation) latestInbound() *message {
	var latest *message
	for i := range c.Messages {
		m := &c.Messages[i]
		if m.Direction != "inbound" || m.Participant == nil {
			continue
		}
		if latest == nil || m.OccurredAt.After(latest.OccurredAt) {
			latest = m
		}
	}
	return latest
}

// agentRepliedSince says the agent's user wrote an outbound message after at: its reply
// to the message at is already in the conversation.
func (c conversation) agentRepliedSince(user uuid.UUID, at time.Time) bool {
	for _, m := range c.Messages {
		if m.Direction == "outbound" && m.AuthorUserID != nil && *m.AuthorUserID == user && m.OccurredAt.After(at) {
			return true
		}
	}
	return false
}

// contactIDs are the contacts the conversation's senders are linked to, each once.
func (c conversation) contactIDs() []int32 {
	ids := []int32{}
	for _, m := range c.Messages {
		if m.Participant != nil && m.Participant.ContactID != nil && !slices.Contains(ids, *m.Participant.ContactID) {
			ids = append(ids, *m.Participant.ContactID)
		}
	}
	return ids
}

// inboundCount is how many inbound messages the conversation holds.
func (c conversation) inboundCount() int {
	n := 0
	for _, m := range c.Messages {
		if m.Direction == "inbound" {
			n++
		}
	}
	return n
}

// windowState is a Messenger conversation's reply window, "" for a channel with
// none.
func (c conversation) windowState() string {
	if c.Reply == nil {
		return ""
	}
	return c.Reply.State
}

// hasTag says the conversation carries one of the tags.
func (c conversation) hasTag(ids []uuid.UUID) bool {
	for _, t := range c.Tags {
		if slices.Contains(ids, t.ID) {
			return true
		}
	}
	return false
}

// maxText is the most characters a message holds on the channel, 0 for no limit.
func (c conversation) maxText() int {
	if c.Capabilities == nil || c.Capabilities.MaxText == nil {
		return 0
	}
	return int(*c.Capabilities.MaxText)
}

// contactSet is a customer's contacts as the pipeline matches a sender against.
type contactSet struct {
	Emails []string
	IDs    []int32
	// ByEmail is the contact whose own e-mail address it is.
	ByEmail map[string]int32
}

// parseContacts reads GET /customers/{id}/contacts: every contact's e-mail
// address (its own and the association's) and id, wherever the answer nests them.
func parseContacts(body []byte) contactSet {
	var set contactSet
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			id, isContact := t["id"].(float64)
			isContact = isContact && t["firstName"] != nil
			if isContact {
				set.IDs = append(set.IDs, int32(id))
			}
			if e, ok := t["email"].(string); ok && strings.TrimSpace(e) != "" {
				e = strings.ToLower(strings.TrimSpace(e))
				set.Emails = append(set.Emails, e)
				if isContact {
					if set.ByEmail == nil {
						set.ByEmail = map[string]int32{}
					}
					set.ByEmail[e] = int32(id)
				}
			}
			for _, child := range t {
				walk(child)
			}
		case []any:
			for _, child := range t {
				walk(child)
			}
		}
	}
	var root any
	if json.Unmarshal(body, &root) == nil {
		walk(root)
	}
	return set
}

// includes says the sender is one of the contacts: its address is a contact's
// e-mail address, or the Messenger participant is linked to a contact of the
// customer.
func (s contactSet) includes(address string, contactID *int32) bool {
	if contactID != nil && slices.Contains(s.IDs, *contactID) {
		return true
	}
	a := strings.ToLower(strings.TrimSpace(address))
	return a != "" && slices.Contains(s.Emails, a)
}

// runCustomer is V9b's rule for the run's customer: the conversation's customer
// when a person linked it, or when the customer linker did and the sender is one
// of its contacts; otherwise none. contacts is read only for an automatic link,
// and nil means it could not be read, which reads as no customer.
func runCustomer(c conversation, contacts func() *contactSet) *int32 {
	if c.CustomerID == nil || c.CustomerAssociationSource == nil {
		return nil
	}
	switch *c.CustomerAssociationSource {
	case "manual":
		return c.CustomerID
	case "automatic":
		in := c.latestInbound()
		if in == nil {
			return nil
		}
		set := contacts()
		if set != nil && set.includes(in.Participant.Address, in.Participant.ContactID) {
			return c.CustomerID
		}
	}
	return nil
}
