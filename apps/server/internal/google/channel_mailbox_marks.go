package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the channel mailboxes' message state writes (Google Workspace
// design G34; delivery F): EnsureFolder and Mark, behind Send's gate (a
// mailbox Check recorded, then the provider ready). Gmail has labels where
// Exchange has folders and categories: a folder is a user label, a move adds
// it and takes INBOX off, a category is a label added, read takes UNREAD off.

const (
	channelMaxLabelRunes  = 120
	channelMaxCategories  = 25
	channelMaxCategoryLen = 120
)

// reservedLabelNames are the names of Gmail's system labels, by id and as
// Gmail shows them, lower case: no user label may take one, and a folder or
// a category named so is refused before any call (errReservedLabel).
var reservedLabelNames = []string{
	"inbox", "sent", "sent mail", "trash", "bin", "spam", "draft", "drafts",
	"starred", "important", "unread", "chat", "chats",
}

// errReservedLabel is a folder or a category named as one of Gmail's system
// labels: refused for good (ErrMailboxDenied), as no retry makes it a name
// Gmail takes.
var errReservedLabel = fmt.Errorf("%w: a name Gmail keeps for a system label", contracts.ErrMailboxDenied)

// reservedLabel reports whether name is one of reservedLabelNames, ignoring
// case.
func reservedLabel(name string) bool {
	return slices.Contains(reservedLabelNames, strings.ToLower(name))
}

// userLabelPrefix begins the id of every user label Gmail makes; a system
// label's id is its name (INBOX, TRASH).
const userLabelPrefix = "Label_"

// validLabelName reports whether name is a trimmed label name EnsureFolder
// may send to Gmail: 1..120 runes, no control characters, no slash at
// either end (Gmail nests a label under a slash).
func validLabelName(name string) bool {
	if name == "" || strings.TrimSpace(name) != name || utf8.RuneCountInString(name) > channelMaxLabelRunes ||
		strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// gmailLabel is a label as labels.list answers it.
type gmailLabel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// EnsureFolder answers the id of the user label named name in mailbox,
// creating it when it does not yet exist. A concurrent create that wins with
// 409 is re-read and its label answered. A system label is never answered,
// and a name Gmail keeps for one is errReservedLabel with no call.
func (m *channelMailbox) EnsureFolder(ctx context.Context, mailbox, name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if !validLabelName(trimmed) {
		return "", errors.New("google: not a valid label name")
	}
	if reservedLabel(trimmed) {
		return "", errReservedLabel
	}
	s, err := m.open(ctx, mailbox, false)
	if err != nil {
		return "", err
	}
	return m.ensureLabel(ctx, s, trimmed)
}

// ensureLabel is EnsureFolder's work on an open mailbox.
func (m *channelMailbox) ensureLabel(ctx context.Context, s gmailSession, name string) (string, error) {
	id, err := m.findLabel(ctx, s, name)
	if err != nil || id != "" {
		return id, err
	}
	var created gmailLabel
	err = m.keys.c.send(ctx, s.modify, http.MethodPost, s.gmailURL("/labels", nil), map[string]string{
		"name": name, "labelListVisibility": "labelShow", "messageListVisibility": "show",
	}, &created, noRetry())
	if isStatus(err, http.StatusConflict) {
		id, err := m.findLabel(ctx, s, name)
		if err != nil {
			return "", err
		}
		if id == "" {
			return "", fmt.Errorf("%w: the label reported as existing was not found", errGoogleUnreachable)
		}
		return id, nil
	}
	if err != nil {
		return "", gmailRefusal(err)
	}
	if !validGmailID(created.ID) {
		return "", fmt.Errorf("%w: the created label's id", errGoogleUnreachable)
	}
	return created.ID, nil
}

// findLabel is the id of the user label named name, ignoring case; "" for
// none.
func (m *channelMailbox) findLabel(ctx context.Context, s gmailSession, name string) (string, error) {
	var found struct {
		Labels []gmailLabel `json:"labels"`
	}
	if err := m.keys.c.get(ctx, s.modify, s.gmailURL("/labels", url.Values{"fields": {"labels(id,name,type)"}}), &found); err != nil {
		return "", gmailRefusal(err)
	}
	for _, l := range found.Labels {
		if l.Type == "user" && strings.EqualFold(strings.TrimSpace(l.Name), name) && validGmailID(l.ID) {
			return l.ID, nil
		}
	}
	return "", nil
}

// Mark changes a message's state in mailbox (messages.modify): read takes
// UNREAD off; category adds the labels named by the categories, each found
// or created (a system label's name is errReservedLabel, with no call); move
// adds the label op.FolderID — a user label's id (Label_…), one EnsureFolder
// answered, never a system label's — and takes INBOX off. A 404 is
// contracts.ErrMailItemGone.
func (m *channelMailbox) Mark(ctx context.Context, mailbox, messageID string, op contracts.MailMark) error {
	if !validGmailID(messageID) {
		return errors.New("google: not a message id")
	}
	var cats []string
	switch op.Kind {
	case contracts.MailMarkRead:
	case contracts.MailMarkCategory:
		if len(op.Categories) > channelMaxCategories {
			return errors.New("google: too many categories")
		}
		for _, c := range op.Categories {
			trimmed := strings.TrimSpace(c)
			if len(trimmed) > channelMaxCategoryLen || !validLabelName(trimmed) {
				return errors.New("google: invalid category")
			}
			if reservedLabel(trimmed) {
				return errReservedLabel
			}
			cats = append(cats, trimmed)
		}
	case contracts.MailMarkMove:
		if !validGmailID(op.FolderID) || !strings.HasPrefix(op.FolderID, userLabelPrefix) {
			return errors.New("google: not a user label's id")
		}
	default:
		return fmt.Errorf("google: unknown mail mark kind %q", op.Kind)
	}
	s, err := m.open(ctx, mailbox, false)
	if err != nil {
		return err
	}
	body := map[string][]string{}
	switch op.Kind {
	case contracts.MailMarkRead:
		body["removeLabelIds"] = []string{labelUnread}
	case contracts.MailMarkCategory:
		if len(cats) == 0 {
			return nil
		}
		for _, c := range cats {
			id, err := m.ensureLabel(ctx, s, c)
			if err != nil {
				return err
			}
			body["addLabelIds"] = append(body["addLabelIds"], id)
		}
	case contracts.MailMarkMove:
		body["addLabelIds"] = []string{op.FolderID}
		body["removeLabelIds"] = []string{labelInbox}
	}
	err = m.keys.c.send(ctx, s.modify, http.MethodPost,
		s.gmailURL("/messages/"+url.PathEscape(messageID)+"/modify", url.Values{"fields": {"id"}}), body, nil,
		shownAs(gmailShownBase+"/messages/{id}/modify"))
	return itemRefusal(err)
}
