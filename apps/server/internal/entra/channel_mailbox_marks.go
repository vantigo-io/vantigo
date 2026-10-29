package entra

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the channel mailboxes' message state writes (Outlook connection
// and Microsoft 365 mail design M11, M12, M17; delivery E): EnsureFolder and
// Mark, behind Send's gate (a mailbox Check recorded in
// entra.channel_mailboxes, then the provider ready). Every message call sends
// Prefer: IdType="ImmutableId" so a message keeps its id across moves.

const (
	channelMaxFolderRunes = 120
	channelMaxCategories  = 25
	channelMaxCategoryLen = 120
)

// folderExistsCodes are the codes Graph answers on a 409 when a mail folder
// with that displayName already exists.
var folderExistsCodes = []string{"ErrorFolderExists"}

// validFolderName reports whether name is a trimmed display name EnsureFolder
// may send to Graph: 1..120 runes, no control characters.
func validFolderName(name string) bool {
	if name == "" || strings.TrimSpace(name) != name || utf8.RuneCountInString(name) > channelMaxFolderRunes {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

type graphMailFolder struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

// EnsureFolder answers the id of the top-level folder named name in mailbox,
// creating it when it does not yet exist. If a concurrent create wins with 409
// ErrorFolderExists, EnsureFolder re-reads and answers the existing folder's id.
func (m *channelMailbox) EnsureFolder(ctx context.Context, mailbox, name string) (string, error) {
	oid, err := m.recorded(ctx, mailbox)
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(name)
	if !validFolderName(trimmed) {
		return "", errors.New("entra: not a valid mail folder name")
	}
	cred, err := m.ready(ctx)
	if err != nil {
		return "", err
	}
	id, err := m.findMailFolder(ctx, cred, oid, trimmed)
	if err != nil || id != "" {
		return id, err
	}
	var created graphMailFolder
	err = m.client.post(ctx, cred, userPath(oid, "/mailFolders"), map[string]string{"displayName": trimmed}, &created, noRetry)
	if isStatus(err, http.StatusConflict) && isCode(err, folderExistsCodes...) {
		id, err := m.findMailFolder(ctx, cred, oid, trimmed)
		if err != nil {
			return "", err
		}
		if id == "" {
			return "", fmt.Errorf("%w: the mail folder reported as existing was not found", errTenantUnreachable)
		}
		return id, nil
	}
	if err != nil {
		return "", channelRefusal(err)
	}
	if !validID(created.ID) {
		return "", fmt.Errorf("%w: the created mail folder's id", errTenantUnreachable)
	}
	return created.ID, nil
}

func (m *channelMailbox) findMailFolder(ctx context.Context, cred graphCredential, oid uuid.UUID, name string) (string, error) {
	escaped := strings.ReplaceAll(name, "'", "''")
	path := userPath(oid, "/mailFolders") + "?" + query(url.Values{
		"$filter": {"displayName eq '" + escaped + "'"},
		"$select": {"id,displayName"},
		"$top":    {"10"},
	})
	var found struct {
		Value []graphMailFolder `json:"value"`
	}
	if err := m.client.get(ctx, cred, path, &found, noWords); err != nil {
		return "", channelRefusal(err)
	}
	for _, f := range found.Value {
		if strings.EqualFold(strings.TrimSpace(f.DisplayName), name) && validID(f.ID) {
			return f.ID, nil
		}
	}
	return "", nil
}

// Mark changes a message's state in mailbox: marks it read (PATCH
// …/messages/{id} {"isRead": true}), sets its categories (PATCH
// …/messages/{id} {"categories": …}), or moves it to op.FolderID (POST
// …/messages/{id}/move {"destinationId": …}). A 404 ErrorItemNotFound answers
// contracts.ErrMailItemGone.
func (m *channelMailbox) Mark(ctx context.Context, mailbox, messageID string, op contracts.MailMark) error {
	oid, err := m.recorded(ctx, mailbox)
	if err != nil {
		return err
	}
	if !validID(messageID) {
		return errors.New("entra: not a message id")
	}
	var (
		method string
		path   string
		body   any
		opts   []callOption
	)
	msgPath := userPath(oid, "/messages/", url.PathEscape(messageID))
	switch op.Kind {
	case contracts.MailMarkRead:
		method = http.MethodPatch
		path = msgPath
		body = map[string]any{"isRead": true}
		opts = []callOption{noWords, immutableID}
	case contracts.MailMarkCategory:
		if len(op.Categories) > channelMaxCategories {
			return errors.New("entra: too many categories")
		}
		cats := make([]string, 0, len(op.Categories))
		for _, c := range op.Categories {
			trimmed := strings.TrimSpace(c)
			if trimmed == "" || len(trimmed) > channelMaxCategoryLen {
				return errors.New("entra: invalid category")
			}
			cats = append(cats, trimmed)
		}
		method = http.MethodPatch
		path = msgPath
		body = map[string]any{"categories": cats}
		opts = []callOption{noWords, immutableID}
	case contracts.MailMarkMove:
		if !validID(op.FolderID) {
			return errors.New("entra: not a destination folder id")
		}
		method = http.MethodPost
		path = msgPath + "/move"
		body = map[string]string{"destinationId": op.FolderID}
		opts = []callOption{noRetry, immutableID}
	default:
		return fmt.Errorf("entra: unknown mail mark kind %q", op.Kind)
	}
	cred, err := m.ready(ctx)
	if err != nil {
		return err
	}
	err = m.client.do(ctx, cred, method, path, body, nil, opts...)
	switch {
	case err == nil:
		return nil
	case isStatus(err, http.StatusNotFound) && isCode(err, itemGoneCodes...):
		return fmt.Errorf("%w: %w", contracts.ErrMailItemGone, err)
	}
	return channelRefusal(err)
}
