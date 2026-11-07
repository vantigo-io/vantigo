package communications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is what a Messenger conversation adds to a message beside its text
// (Messenger design N4, N5, N6): the components a reply carries and how they
// are checked against the provider's Capabilities, the shape they are stored
// and read back in, the answer an inbound message can be, and the reply window.

// shareEmailChoiceID is the choice id of a "user_email" quick reply's answer: an
// e-mail address the person shared (N5, N8).
const shareEmailChoiceID = contracts.MessengerShareEmail

// choiceIDPattern is a component id (N4): lower case, digits and hyphens, 1 to
// 40 characters, unique within its message.
var choiceIDPattern = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// The reply window's states (ReplyWindow.state, N6).
const (
	windowOpen       = "open"
	windowHumanAgent = "human_agent"
	windowClosed     = "closed"
)

// defaultReplyWindow is the window of a provider that gave none: Messenger's
// 24 hours.
const defaultReplyWindow = 24 * time.Hour

// storedComponents is conversation_messages.components: the request's
// components as they were sent, with each card's image the reply copied. Its
// keys are MessageComponents', so the column is read back as the response.
type storedComponents struct {
	AnswerOptions []storedChoice `json:"answerOptions,omitempty"`
	ShareEmail    bool           `json:"shareEmail,omitempty"`
	Cards         []storedCard   `json:"cards,omitempty"`
}

type storedChoice struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type storedCard struct {
	Title        string         `json:"title"`
	Subtitle     string         `json:"subtitle,omitempty"`
	AttachmentID *uuid.UUID     `json:"attachmentId,omitempty"`
	Choices      []storedChoice `json:"choices,omitempty"`
}

// empty reports whether there is nothing to store: the message carries no
// components.
func (c storedComponents) empty() bool {
	return len(c.AnswerOptions) == 0 && !c.ShareEmail && len(c.Cards) == 0
}

// storedComponentsOf is the components of a request in the form they are
// stored in, with the nil a request without any answers.
func storedComponentsOf(c *gen.MessageComponents) storedComponents {
	var out storedComponents
	if c == nil {
		return out
	}
	if c.AnswerOptions != nil {
		for _, o := range *c.AnswerOptions {
			out.AnswerOptions = append(out.AnswerOptions, storedChoice{ID: o.Id, Title: o.Title})
		}
	}
	out.ShareEmail = c.ShareEmail != nil && *c.ShareEmail
	if c.Cards != nil {
		for _, card := range *c.Cards {
			stored := storedCard{Title: card.Title}
			if card.Subtitle != nil {
				stored.Subtitle = *card.Subtitle
			}
			if card.AttachmentId != nil {
				id := uuid.UUID(*card.AttachmentId)
				stored.AttachmentID = &id
			}
			if card.Choices != nil {
				for _, ch := range *card.Choices {
					stored.Choices = append(stored.Choices, storedChoice{ID: ch.Id, Title: ch.Title})
				}
			}
			out.Cards = append(out.Cards, stored)
		}
	}
	return out
}

// componentsResponse is the stored components as the thread reads them: nil for
// a message with none — every e-mail message and every one from before
// components —, so the field answers null.
func componentsResponse(raw []byte) (*gen.MessageComponents, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var stored storedComponents
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("communications: decode a message's components: %w", err)
	}
	if stored.empty() {
		return nil, nil
	}
	out := &gen.MessageComponents{}
	if len(stored.AnswerOptions) > 0 {
		options := make([]gen.MessageComponentChoice, 0, len(stored.AnswerOptions))
		for _, o := range stored.AnswerOptions {
			options = append(options, gen.MessageComponentChoice{Id: o.ID, Title: o.Title})
		}
		out.AnswerOptions = &options
	}
	if stored.ShareEmail {
		out.ShareEmail = apicommon.Ptr(true)
	}
	if len(stored.Cards) > 0 {
		cards := make([]gen.MessageComponentCard, 0, len(stored.Cards))
		for _, card := range stored.Cards {
			c := gen.MessageComponentCard{Title: card.Title}
			if card.Subtitle != "" {
				c.Subtitle = apicommon.Ptr(card.Subtitle)
			}
			if card.AttachmentID != nil {
				id := *card.AttachmentID
				c.AttachmentId = &id
			}
			if len(card.Choices) > 0 {
				choices := make([]gen.MessageComponentChoice, 0, len(card.Choices))
				for _, ch := range card.Choices {
					choices = append(choices, gen.MessageComponentChoice{Id: ch.ID, Title: ch.Title})
				}
				c.Choices = &choices
			}
			cards = append(cards, c)
		}
		out.Cards = &cards
	}
	return out, nil
}

// storedAnswer is the answer an inbound message is, in its channel_metadata_json
// under "answer" (N5).
type storedAnswer struct {
	MessageID string `json:"messageId"`
	ChoiceID  string `json:"choiceId"`
	Title     string `json:"title"`
}

// productQuestion is a message of the thread that showed a product in cards
// (N16): the product, and the cards as they were sent, which an answer to one
// of their choices is resolved against (N17).
type productQuestion struct {
	productID int32
	cards     []storedCard
}

// productQuestions are the thread's messages with cards and a product, by id.
func productQuestions(rows []store.GetConversationMessagesByConversationIDRow) (map[uuid.UUID]productQuestion, error) {
	out := map[uuid.UUID]productQuestion{}
	for _, r := range rows {
		if r.ProductID == 0 || len(r.Components) == 0 {
			continue
		}
		var stored storedComponents
		if err := json.Unmarshal(r.Components, &stored); err != nil {
			return nil, fmt.Errorf("communications: decode a message's components: %w", err)
		}
		if len(stored.Cards) > 0 {
			out[r.ID] = productQuestion{productID: r.ProductID, cards: stored.Cards}
		}
	}
	return out, nil
}

// answerResponse is the stored answer as the thread reads it: nil for a message
// that is no answer, or whose answer cannot be read. An answer to a choice of a
// card of a product the question showed carries the product (N17): its id, the
// card's title and the card's image.
func answerResponse(raw []byte, products map[uuid.UUID]productQuestion) *gen.MessageAnswer {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var stored storedAnswer
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil
	}
	id, err := uuid.Parse(stored.MessageID)
	if err != nil {
		return nil
	}
	answer := &gen.MessageAnswer{MessageId: id, ChoiceId: stored.ChoiceID, Title: stored.Title}
	if q, ok := products[id]; ok {
		for _, card := range q.cards {
			if !slices.ContainsFunc(card.Choices, func(c storedChoice) bool { return c.ID == stored.ChoiceID }) {
				continue
			}
			answer.Product = &gen.MessageAnswerProduct{ProductId: q.productID, Name: card.Title, AttachmentId: card.AttachmentID}
			break
		}
	}
	return answer
}

// componentLimitsOf is what a message may carry on a channel whose provider has
// c, as the contract names it; nil when the provider takes no components.
func componentLimitsOf(c contracts.MessengerCapabilities) *gen.MessageComponentLimits {
	if !c.Components() {
		return nil
	}
	return &gen.MessageComponentLimits{
		MaxText: int32(c.MaxText), MaxAnswerOptions: int32(c.MaxAnswerOptions), MaxAnswerTitle: int32(c.MaxAnswerTitle),
		ShareEmail: c.ShareEmail, MaxCards: int32(c.MaxCards), MaxCardChoices: int32(c.MaxCardChoices),
		MaxCardTitle: int32(c.MaxCardTitle), MaxCardSubtitle: int32(c.MaxCardSubtitle), MaxChoiceTitle: int32(c.MaxChoiceTitle),
		MaxAttachmentBytes: attachmentLimit(c.MaxAttachmentBytes),
	}
}

// replyWindow is where a Messenger conversation's reply window stands at now
// (N6): state, and the instant the window of that state closes.
type replyWindow struct {
	state    string
	closesAt *time.Time
}

// replyWindowOf is the window counted from the participant's newest message,
// lastInbound (nil when there is none): open for caps.Window — 24 hours when the
// provider gave none — after it; with the provider's Human Agent permission,
// human_agent until caps.HumanAgentWindow after it; closed after that. A reply
// at the very instant a window closes is still inside it.
func replyWindowOf(now time.Time, lastInbound *time.Time, caps contracts.MessengerCapabilities) replyWindow {
	if lastInbound == nil {
		return replyWindow{state: windowClosed}
	}
	window := caps.Window
	if window <= 0 {
		window = defaultReplyWindow
	}
	open := lastInbound.Add(window)
	if !now.After(open) {
		return replyWindow{state: windowOpen, closesAt: &open}
	}
	if caps.HumanAgentWindow > window {
		extended := lastInbound.Add(caps.HumanAgentWindow)
		if !now.After(extended) {
			return replyWindow{state: windowHumanAgent, closesAt: &extended}
		}
		return replyWindow{state: windowClosed, closesAt: &extended}
	}
	return replyWindow{state: windowClosed, closesAt: &open}
}

// replyWindowResponse is the window as the detail answers it.
func replyWindowResponse(w replyWindow) *gen.ReplyWindow {
	return &gen.ReplyWindow{State: w.state, ClosesAt: w.closesAt}
}

// messengerConversationState is what the detail and the reply read of a
// Messenger conversation's provider: its Capabilities and the window.
// caps is the zero value, which takes no components, when no provider is filled
// under the channel's key.
type messengerConversationState struct {
	caps   contracts.MessengerCapabilities
	filled bool
	window replyWindow
}

// messengerStateOf reads the provider's Capabilities for a conversation's channel
// and the window from the participant's newest message. No network call: both
// are read from the slot and the database.
func (s *server) messengerStateOf(ctx context.Context, q *store.Queries, conversationID uuid.UUID, provider string, now time.Time) (messengerConversationState, error) {
	var out messengerConversationState
	m, err := s.channelMessenger(ctx, provider)
	if err != nil {
		return out, err
	}
	if m != nil {
		if out.caps, err = messengerCapabilities(ctx, m); err != nil {
			return out, err
		}
		out.filled = true
	}
	last, err := latestInbound(ctx, q, conversationID)
	if err != nil {
		return out, err
	}
	out.window = replyWindowOf(now, last, out.caps)
	return out, nil
}

// latestInbound is the occurred_at of the participant's newest message of the
// conversation, nil when it has none.
func latestInbound(ctx context.Context, q *store.Queries, conversationID uuid.UUID) (*time.Time, error) {
	at, err := q.LatestInboundOccurredAt(ctx, conversationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("communications: read the newest inbound message: %w", err)
	}
	return &at, nil
}

// validateComponents checks a reply's components against the provider's
// Capabilities (N4): every limit it breaks is a field of the 422
// components_invalid, keyed by where it is. Cards are checked for their shape;
// that a card's image is a product's belongs to the copy (reply_cards.go).
func validateComponents(c *gen.MessageComponents, caps contracts.MessengerCapabilities) map[string][]string {
	errs := map[string][]string{}
	if c == nil {
		return errs
	}
	// An id is unique within its message, answer options and card choices alike:
	// it is what the payload names (N4). The same choice of a card may stand on
	// several cards (validateChoices).
	seen := map[string]seenChoice{}
	if c.AnswerOptions != nil {
		options := *c.AnswerOptions
		switch {
		case len(options) == 0:
			errs["answerOptions"] = []string{"At least one answer option is required."}
		case len(options)+shareEmailSlots(c) > caps.MaxAnswerOptions:
			// The request for an e-mail address is a quick reply too (N4): it counts.
			errs["answerOptions"] = []string{fmt.Sprintf("At most %d answer options are allowed, the request for an e-mail address counting as one.", caps.MaxAnswerOptions)}
		}
		errs = validateChoices(errs, seen, "answerOptions", options, caps.MaxAnswerTitle, false)
	}
	if c.ShareEmail != nil && *c.ShareEmail && !caps.ShareEmail {
		errs["shareEmail"] = []string{"This channel cannot ask for an e-mail address."}
	}
	if c.Cards != nil {
		cards := *c.Cards
		switch {
		case len(cards) == 0:
			errs["cards"] = []string{"At least one card is required."}
		case len(cards) > caps.MaxCards:
			errs["cards"] = []string{fmt.Sprintf("At most %d cards are allowed.", caps.MaxCards)}
		}
		for i, card := range cards {
			key := fmt.Sprintf("cards[%d]", i)
			if n := utf8.RuneCountInString(strings.TrimSpace(card.Title)); n < 1 || n > caps.MaxCardTitle {
				errs[key+".title"] = []string{fmt.Sprintf("A card's title is 1 to %d characters.", caps.MaxCardTitle)}
			}
			if card.Subtitle != nil && utf8.RuneCountInString(*card.Subtitle) > caps.MaxCardSubtitle {
				errs[key+".subtitle"] = []string{fmt.Sprintf("A card's subtitle is at most %d characters.", caps.MaxCardSubtitle)}
			}
			if card.Choices != nil {
				if len(*card.Choices) > caps.MaxCardChoices {
					errs[key+".choices"] = []string{fmt.Sprintf("A card has at most %d choices.", caps.MaxCardChoices)}
				}
				errs = validateChoices(errs, seen, key+".choices", *card.Choices, caps.MaxChoiceTitle, true)
			}
		}
	}
	return errs
}

// seenChoice is a choice id met so far in a message: the title it carried, and
// whether it was a card's choice.
type seenChoice struct {
	title string
	card  bool
}

// validateChoices checks each choice of one list: an id of the shape N4 names,
// once in the list and not one of the message's answer options or another kind
// of choice (seen holds the ids of the message so far, which it adds to), and a
// title of 1 to maxTitle characters. A card's choice may repeat on another card
// with the same title: it is the same answer, as Buy is on each card of a
// product's carousel (N16), because what the payload names is the message and
// the choice, not the card.
func validateChoices(errs map[string][]string, seen map[string]seenChoice, key string, choices []gen.MessageComponentChoice, maxTitle int, cards bool) map[string][]string {
	inList := map[string]bool{}
	for i, choice := range choices {
		at := fmt.Sprintf("%s[%d]", key, i)
		prev, met := seen[choice.Id]
		switch {
		case !choiceIDPattern.MatchString(choice.Id):
			errs[at+".id"] = []string{"An id is 1 to 40 lower-case letters, digits and hyphens."}
		case inList[choice.Id]:
			errs[at+".id"] = []string{"An id may appear only once."}
		case met && (!cards || !prev.card || prev.title != choice.Title):
			errs[at+".id"] = []string{"An id may appear only once in a message, except the same choice on several cards."}
		}
		inList[choice.Id] = true
		seen[choice.Id] = seenChoice{title: choice.Title, card: cards}
		if n := utf8.RuneCountInString(strings.TrimSpace(choice.Title)); n < 1 || n > maxTitle {
			errs[at+".title"] = []string{fmt.Sprintf("A title is 1 to %d characters.", maxTitle)}
		}
	}
	return errs
}

// shareEmailSlots is how many of a message's quick replies the request for an
// e-mail address takes: one when it is asked, else none.
func shareEmailSlots(c *gen.MessageComponents) int {
	if c != nil && c.ShareEmail != nil && *c.ShareEmail {
		return 1
	}
	return 0
}

// attachmentLimit is a provider's largest file as the contract names it: nil
// when it names none.
func attachmentLimit(n int64) *int64 {
	if n <= 0 {
		return nil
	}
	return &n
}
