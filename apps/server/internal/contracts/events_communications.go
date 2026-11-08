package contracts

import "github.com/google/uuid"

// The events the communications module publishes onto the platform outbox
// (virtual employees design V3). Each is a versioned wire form; a payload
// carries ids and states and never personal data: no names, addresses or
// message text. Both are published under the conversation's row lock, and
// AggregateID is the conversation. Both are named communications.conversation.*
// so they share the outbox lane of that aggregate, and are delivered in order
// per conversation.

// ConversationMessageReceived is communications.conversation.message_received v1: an inbound message
// was imported on a channel, by the mailbox import or the messenger sync. A
// Messenger echo, the Page's own outgoing message coming back, is not one.
//
// ChannelType is the type of the conversation's channel, "email" or
// "messenger". AutoReply is true for an automatic message (an out-of-office
// answer, a bounce, a list); Unreadable for one the import could not read and
// stored as placeholder text. AssignedUserID is the conversation's assignee as
// it stands after the insert.
type ConversationMessageReceived struct {
	ConversationID uuid.UUID  `json:"conversationId"`
	MessageID      uuid.UUID  `json:"messageId"`
	ChannelID      uuid.UUID  `json:"channelId"`
	ChannelType    string     `json:"channelType"`
	AutoReply      bool       `json:"autoReply"`
	Unreadable     bool       `json:"unreadable"`
	AssignedUserID *uuid.UUID `json:"assignedUserId"`
}

func (ConversationMessageReceived) EventName() string {
	return "communications.conversation.message_received"
}
func (ConversationMessageReceived) EventVersion() int     { return 1 }
func (e ConversationMessageReceived) AggregateID() string { return e.ConversationID.String() }

// ConversationAssigned is communications.conversation.assigned v1: a
// conversation's assignee changed. AssignedUserID is null when it was cleared,
// PreviousAssignedUserID null when it had none, and ByUserID is who changed it.
type ConversationAssigned struct {
	ConversationID         uuid.UUID  `json:"conversationId"`
	AssignedUserID         *uuid.UUID `json:"assignedUserId"`
	PreviousAssignedUserID *uuid.UUID `json:"previousAssignedUserId"`
	ByUserID               uuid.UUID  `json:"byUserId"`
}

func (ConversationAssigned) EventName() string     { return "communications.conversation.assigned" }
func (ConversationAssigned) EventVersion() int     { return 1 }
func (e ConversationAssigned) AggregateID() string { return e.ConversationID.String() }
