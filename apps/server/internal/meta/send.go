package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/meta/store"
)

// This file is Send (Messenger design N7, N12): one part of a message to one
// participant through the Send API, and the table that turns Meta's refusals
// into the outbox's outcomes.

// The Graph error codes the map names.
const (
	codeRecipientUnreachable = 551
	codeTokenRejected        = 190
	codePermission           = 10
	codeCapability           = 200
	subcodeWindowClosed      = 2018278
	codeHumanAgentMissing    = "human_agent_not_permitted"
)

// sendAnswer is what the Send API answers.
type sendAnswer struct {
	RecipientID string `json:"recipient_id"`
	MessageID   string `json:"message_id"`
}

// quickReply is one quick reply of a message.
type quickReply struct {
	ContentType string `json:"content_type"`
	Title       string `json:"title,omitempty"`
	Payload     string `json:"payload,omitempty"`
}

// Send sends one part of a message from the Page address to part.Participant.
func (m *messenger) Send(ctx context.Context, address string, part contracts.MessengerPart) (contracts.MessengerSent, error) {
	page, token, err := m.sendablePage(ctx, address)
	if err != nil {
		return contracts.MessengerSent{}, err
	}
	if part.Participant == "" {
		return contracts.MessengerSent{}, fmt.Errorf("%w: no recipient", contracts.ErrMessengerRejected)
	}
	replies := quickReplies(part)
	recipient, err := json.Marshal(map[string]string{"id": part.Participant})
	if err != nil {
		return contracts.MessengerSent{}, fmt.Errorf("meta: encode the recipient: %w", err)
	}

	var answer sendAnswer
	path := pagePath(page.PageID, "/messages")
	if a := part.Attachment; a != nil {
		if len(a.Content) == 0 {
			return contracts.MessengerSent{}, fmt.Errorf("%w: the attachment is empty", contracts.ErrMessengerRejected)
		}
		if int64(len(a.Content)) > maxAttachmentBytes {
			return contracts.MessengerSent{}, fmt.Errorf("%w: the attachment is over %d bytes", contracts.ErrMessengerRejected, int64(maxAttachmentBytes))
		}
		message := map[string]any{"attachment": map[string]any{
			"type": attachmentType(a.ContentType), "payload": map[string]any{"is_reusable": false},
		}}
		if len(replies) > 0 {
			message["quick_replies"] = replies
		}
		mb, err := json.Marshal(message)
		if err != nil {
			return contracts.MessengerSent{}, fmt.Errorf("meta: encode the message: %w", err)
		}
		fields := [][2]string{{"recipient", string(recipient)}, {"message", string(mb)}, {"messaging_type", messagingType(part)}}
		if part.HumanAgent {
			fields = append(fields, [2]string{"tag", "HUMAN_AGENT"})
		}
		fileName := a.FileName
		if fileName == "" {
			fileName = "file"
		}
		contentType := a.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		err = m.graph.upload(ctx, token, path, fields, "filedata", fileName, contentType, a.Content, &answer)
		if err != nil {
			return contracts.MessengerSent{}, m.sendFailed(ctx, page.PageID, err, part.HumanAgent)
		}
	} else if len(part.Cards) > 0 {
		template, err := genericTemplate(part)
		if err != nil {
			return contracts.MessengerSent{}, err
		}
		message := map[string]any{"attachment": template}
		if len(replies) > 0 {
			message["quick_replies"] = replies
		}
		body := map[string]any{
			"recipient":      json.RawMessage(recipient),
			"messaging_type": messagingType(part),
			"message":        message,
		}
		if part.HumanAgent {
			body["tag"] = "HUMAN_AGENT"
		}
		if err := m.graph.postJSON(ctx, token, path, body, &answer); err != nil {
			return contracts.MessengerSent{}, m.sendFailed(ctx, page.PageID, err, part.HumanAgent)
		}
	} else {
		if part.Text == "" {
			return contracts.MessengerSent{}, fmt.Errorf("%w: the part carries nothing", contracts.ErrMessengerRejected)
		}
		if utf8.RuneCountInString(part.Text) > maxText {
			return contracts.MessengerSent{}, fmt.Errorf("%w: the text is over %d characters", contracts.ErrMessengerRejected, maxText)
		}
		message := map[string]any{"text": part.Text}
		if len(replies) > 0 {
			message["quick_replies"] = replies
		}
		body := map[string]any{
			"recipient":      json.RawMessage(recipient),
			"messaging_type": messagingType(part),
			"message":        message,
		}
		if part.HumanAgent {
			body["tag"] = "HUMAN_AGENT"
		}
		if err := m.graph.postJSON(ctx, token, path, body, &answer); err != nil {
			return contracts.MessengerSent{}, m.sendFailed(ctx, page.PageID, err, part.HumanAgent)
		}
	}
	if answer.MessageID == "" {
		return contracts.MessengerSent{}, errors.New("meta: the Graph API answered a send with no message id")
	}
	return contracts.MessengerSent{PlatformMessageID: answer.MessageID}, nil
}

// templateButton is one button of a card: a postback that carries the payload
// the answer is matched by (N4).
type templateButton struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	Payload string `json:"payload"`
}

// templateElement is one card of the generic template.
type templateElement struct {
	Title    string           `json:"title"`
	Subtitle string           `json:"subtitle,omitempty"`
	ImageURL string           `json:"image_url,omitempty"`
	Buttons  []templateButton `json:"buttons,omitempty"`
}

// genericTemplate is the carousel of a part as the Send API's generic template
// (N12): an element per card, its choices as postback buttons whose payload is
// v1:<messageId>:<choiceId>. What Meta would refuse is refused here, before any
// call: more than 10 cards, more than 3 buttons on one, an empty or over-long
// title, and an image that is not an https address.
func genericTemplate(part contracts.MessengerPart) (map[string]any, error) {
	if len(part.Cards) > maxCards {
		return nil, fmt.Errorf("%w: more than %d cards", contracts.ErrMessengerRejected, maxCards)
	}
	elements := make([]templateElement, 0, len(part.Cards))
	for _, card := range part.Cards {
		if n := utf8.RuneCountInString(card.Title); n < 1 || n > maxCardTitle {
			return nil, fmt.Errorf("%w: a card's title is 1 to %d characters", contracts.ErrMessengerRejected, maxCardTitle)
		}
		if utf8.RuneCountInString(card.Subtitle) > maxCardSubtitle {
			return nil, fmt.Errorf("%w: a card's subtitle is over %d characters", contracts.ErrMessengerRejected, maxCardSubtitle)
		}
		if len(card.Choices) > maxCardChoices {
			return nil, fmt.Errorf("%w: a card has more than %d choices", contracts.ErrMessengerRejected, maxCardChoices)
		}
		if card.ImageURL != "" && !strings.HasPrefix(card.ImageURL, "https://") {
			return nil, fmt.Errorf("%w: a card's image must be an https address", contracts.ErrMessengerRejected)
		}
		e := templateElement{Title: card.Title, Subtitle: card.Subtitle, ImageURL: card.ImageURL}
		for _, c := range card.Choices {
			if n := utf8.RuneCountInString(c.Title); n < 1 || n > maxChoiceTitle {
				return nil, fmt.Errorf("%w: a choice's title is 1 to %d characters", contracts.ErrMessengerRejected, maxChoiceTitle)
			}
			e.Buttons = append(e.Buttons, templateButton{Type: "postback", Title: c.Title, Payload: contracts.MessengerPayload(part.MessageID, c.ID)})
		}
		elements = append(elements, e)
	}
	return map[string]any{"type": "template", "payload": map[string]any{
		"template_type": "generic", "elements": elements,
	}}, nil
}

// sendablePage opens the Page address and its token, or answers
// ErrMessengerUnavailable (wrapped) for an app not configured, an unknown Page
// and one whose token Meta refused, which holds the job.
func (m *messenger) sendablePage(ctx context.Context, address string) (store.MetaPage, string, error) {
	s, err := m.settings(ctx)
	if err != nil {
		return store.MetaPage{}, "", err
	}
	if !configured(s) {
		return store.MetaPage{}, "", fmt.Errorf("%w: %s", contracts.ErrMessengerUnavailable, reasonNotConfigured)
	}
	page, err := m.q.GetPage(ctx, address)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.MetaPage{}, "", fmt.Errorf("%w: the Page is not connected", contracts.ErrMessengerUnavailable)
	}
	if err != nil {
		return store.MetaPage{}, "", fmt.Errorf("meta: read the Page: %w", err)
	}
	if page.State != statePageActive {
		return store.MetaPage{}, "", fmt.Errorf("%w: %s", contracts.ErrMessengerUnavailable, reasonTokenRejected)
	}
	token, err := m.pageToken(page)
	if err != nil {
		return store.MetaPage{}, "", err
	}
	return page, token, nil
}

// messagingType is RESPONSE, or MESSAGE_TAG for a part sent tagged.
func messagingType(part contracts.MessengerPart) string {
	if part.HumanAgent {
		return "MESSAGE_TAG"
	}
	return "RESPONSE"
}

// quickReplies are the part's answer options and its e-mail request.
func quickReplies(part contracts.MessengerPart) []quickReply {
	var out []quickReply
	for _, c := range part.AnswerOptions {
		out = append(out, quickReply{ContentType: "text", Title: c.Title, Payload: contracts.MessengerPayload(part.MessageID, c.ID)})
	}
	if part.ShareEmail {
		out = append(out, quickReply{ContentType: "user_email"})
	}
	return out
}

// attachmentType is the Send API's type of a file by its content type.
func attachmentType(contentType string) string {
	for _, t := range []string{"image", "audio", "video"} {
		if strings.HasPrefix(strings.ToLower(contentType), t+"/") {
			return t
		}
	}
	return "file"
}

// sendFailed turns a failed call into Send's error, with the side effects the
// refusal has: the Page's rejected state and the status line's last Graph
// error. Nothing of the recipient or the text is logged or kept.
func (m *messenger) sendFailed(ctx context.Context, pageID string, err error, tagged bool) error {
	var ge graphError
	if !errors.As(err, &ge) {
		if isTemporaryGraph(err) {
			return contracts.MessengerTemporary(err)
		}
		return err
	}
	out := classifyGraphError(ge, tagged)
	if contracts.IsMessengerTemporary(out) {
		return out
	}
	switch {
	case errors.Is(out, contracts.ErrHumanAgentNotPermitted):
		m.recordGraphError(ctx, codeHumanAgentMissing, ge.Message)
	default:
		m.recordGraphError(ctx, graphCode(ge), ge.Message)
	}
	if ge.Code == codeTokenRejected {
		m.markRejected(ctx, pageID)
	}
	return out
}

// graphCode is a refusal's code as the status line keeps it: "190",
// "10/2018278".
func graphCode(ge graphError) string {
	if ge.Subcode != 0 {
		return strconv.Itoa(ge.Code) + "/" + strconv.Itoa(ge.Subcode)
	}
	return strconv.Itoa(ge.Code)
}

// markRejected sets the Page to token_rejected: later sends hold.
func (m *messenger) markRejected(ctx context.Context, pageID string) {
	now := m.now()
	reason := reasonTokenRejected
	err := m.q.SetPageState(ctx, store.SetPageStateParams{State: statePageRejected, LastError: &reason, Now: &now, PageID: pageID})
	if err != nil {
		m.logger.WarnContext(ctx, "meta: a Page's rejected token could not be recorded", "err", err)
	}
}

// classifyGraphError is Messenger design N12's table: Meta's refusal as the
// outbox's outcome. tagged is whether the part was sent as a Human Agent
// message.
func classifyGraphError(ge graphError, tagged bool) error {
	switch {
	case ge.temporary():
		return contracts.MessengerTemporary(ge)
	case ge.Code == codeTokenRejected:
		return fmt.Errorf("%w: the Page's token was refused: %s", contracts.ErrMessengerRejected, ge.Message)
	case ge.Code == codeRecipientUnreachable:
		return fmt.Errorf("%w: %s", contracts.ErrRecipientUnreachable, ge.Message)
	case ge.Code == codePermission && ge.Subcode == subcodeWindowClosed:
		return fmt.Errorf("%w: %s", contracts.ErrReplyWindowClosed, ge.Message)
	case tagged && (ge.Code == codePermission || ge.Code == codeCapability) && ge.Subcode != subcodeWindowClosed:
		return fmt.Errorf("%w: %s", contracts.ErrHumanAgentNotPermitted, ge.Message)
	}
	return fmt.Errorf("%w: %s", contracts.ErrMessengerRejected, ge.Message)
}
