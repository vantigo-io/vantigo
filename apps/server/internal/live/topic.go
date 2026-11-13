// Package live lets the browser hear that something changed without polling
// (live design L1-L16): a module notifies a topic on the transaction that
// writes (Notify), Postgres delivers the notification to every HTTP replica's
// hub when the transaction commits, and each hub hands it to the sockets that
// subscribed to the topic. Events carry identifiers only, never data: a
// screen that hears one refetches through the ordinary, checked API.
//
// It is a platform package: it imports no business module. A module declares
// the topics it serves through module.Module.LiveTopics; Compose collects them
// into a Registry, and cmd/vantigo builds one Hub over it per process that
// serves HTTP.
package live

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

// ErrForbidden is what a Topic's Authorize answers when the person may not
// subscribe — and, so that a subscription is never an existence oracle, also
// when the key names nothing.
var ErrForbidden = errors.New("live: forbidden")

// ErrUnknown is a kind no enabled module registered.
var ErrUnknown = errors.New("live: unknown topic")

// topicPattern is the topic grammar (L8): <module>.<kind>, optionally with a
// key, <module>.<kind>:<key>.
var topicPattern = regexp.MustCompile(`^[a-z]+\.[a-z-]+(:[A-Za-z0-9_-]{1,64})?$`)

// kindPattern is a topic's kind: the grammar without a key.
var kindPattern = regexp.MustCompile(`^[a-z]+\.[a-z-]+$`)

// eventKindPattern is an event's kind: "conversation.changed", "message.added".
var eventKindPattern = regexp.MustCompile(`^[a-z][a-z.-]{0,63}$`)

// Topic is one kind of topic a module serves.
type Topic struct {
	// Kind is <module>.<kind>; its module prefix is the module's name.
	Kind string
	// Presence says whether clients may announce presence on the topic.
	Presence bool
	// Authorize decides whether the person may subscribe to the topic with
	// key ("" for a topic without one). Its context carries
	// contracts.AsUser for the person, and HasPermission answers through
	// it. It returns nil or ErrForbidden; any other error is read as
	// forbidden too.
	Authorize func(ctx context.Context, key string) error
	// Station says whether a shared shop-floor station's socket may subscribe
	// (manufacturing phase 2 §17). A station's socket carries no person: its
	// Authorize gets StationID in the context and no contracts.AsUser, and a
	// topic without Station refuses every station before Authorize runs.
	Station bool
	// Doc, when set, makes the topic a document's (K26): binary frames for it
	// are stored and relayed through the module's DocStore, and the hub
	// answers doc_sync, acks and the rest of the document protocol. Nil for
	// every other topic.
	Doc DocStore
}

// ValidTopic reports whether topic follows the grammar.
func ValidTopic(topic string) bool { return topicPattern.MatchString(topic) }

// SplitTopic splits a valid topic into its kind and its key ("" when it has
// none).
func SplitTopic(topic string) (kind, key string, ok bool) {
	if !ValidTopic(topic) {
		return "", "", false
	}
	kind, key, _ = strings.Cut(topic, ":")
	return kind, key, true
}

// moduleOf is a kind's module prefix.
func moduleOf(kind string) string {
	m, _, _ := strings.Cut(kind, ".")
	return m
}
