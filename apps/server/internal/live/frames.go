package live

import "encoding/json"

// The server's frames (L4), JSON text.
var (
	// frameResyncReconnect is the listener's return: the one resync that
	// means the socket is live again.
	frameResyncReconnect = []byte(`{"type":"resync","reason":"reconnect"}`)
	// frameResyncSlow is a cleared queue: events were dropped, refetch; it
	// says nothing about whether the hub is live.
	frameResyncSlow = []byte(`{"type":"resync","reason":"slow_consumer"}`)
	frameDegraded   = []byte(`{"type":"degraded"}`)
	framePong       = []byte(`{"type":"pong"}`)
)

type topicFrame struct {
	Type   string `json:"type"`
	Topic  string `json:"topic"`
	Reason string `json:"reason,omitempty"`
}

type eventFrame struct {
	Type  string `json:"type"`
	Topic string `json:"topic"`
	Kind  string `json:"kind"`
	ID    string `json:"id,omitempty"`
}

// Member is one person in a topic's presence set.
type Member struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	State  string `json:"state"`
	Since  string `json:"since"`
}

type presenceFrame struct {
	Type    string   `json:"type"`
	Topic   string   `json:"topic"`
	Members []Member `json:"members"`
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic("live: marshal a frame: " + err.Error()) // plain strings only
	}
	return b
}

func frameOK(topic string) []byte {
	return mustJSON(topicFrame{Type: "ok", Topic: topic})
}

func frameDenied(topic, reason string) []byte {
	return mustJSON(topicFrame{Type: "denied", Topic: topic, Reason: reason})
}

func frameEvent(topic, kind, id string) []byte {
	return mustJSON(eventFrame{Type: "event", Topic: topic, Kind: kind, ID: id})
}

func framePresence(topic string, members []Member) []byte {
	if members == nil {
		members = []Member{}
	}
	return mustJSON(presenceFrame{Type: "presence", Topic: topic, Members: members})
}
