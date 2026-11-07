package communications

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/secrets"
)

// This file is a participant's link to a contact of Customers (Messenger design
// N19-N21, delivery D): the link by hand, the Messenger link a contact taps and
// the ref token it carries. The link itself is participants.contact_id, a
// reference resolved through contracts.CustomerDirectory and never a foreign key;
// what is only suggested or referred sits beside it in
// participant_contact_hints, so it can never be mistaken for the link.

// The ref token (N21): "v1.<contactId>.<channelId>.<expiresAt>.<mac>". The
// channel is in its 32-character hex form and the expiry in Unix seconds, the
// MAC the secrets Box's under refPurpose, base64url without padding, over the
// four fields before it. Every character is URL-safe, and it holds no personal
// data beyond the contact's id. The whole is about 100 characters, far under
// Meta's 2083.
const (
	refPurpose  = "communications/messenger-ref"
	refVersion  = "v1"
	refLifetime = 90 * 24 * time.Hour
)

// refBody is the part of a ref the MAC is made over.
func refBody(contactID int32, channelID uuid.UUID, expiresAt int64) string {
	return refVersion + "." + strconv.FormatInt(int64(contactID), 10) + "." + hexN(channelID) + "." + strconv.FormatInt(expiresAt, 10)
}

// mintRef is the ref of a contact's link for a channel, valid until expiresAt.
func mintRef(box *secrets.Box, contactID int32, channelID uuid.UUID, expiresAt time.Time) (string, error) {
	if box == nil {
		return "", errors.New("communications: no secrets box to sign a ref with")
	}
	body := refBody(contactID, channelID, expiresAt.Unix())
	mac, err := box.MAC(refPurpose, []byte(body))
	if err != nil {
		return "", fmt.Errorf("communications: sign a ref: %w", err)
	}
	return body + "." + base64.RawURLEncoding.EncodeToString(mac), nil
}

// verifyRef is the contact a ref names, ok only when its MAC holds, it has not
// expired and it was made for channelID. Anything else — a ref that is not ours,
// a forged or expired one, one of another channel — is not ok, and the reason is
// the caller's to log, never the person's to see.
func verifyRef(box *secrets.Box, ref string, channelID uuid.UUID, now time.Time) (contactID int32, ok bool) {
	if box == nil {
		return 0, false
	}
	parts := strings.Split(ref, ".")
	if len(parts) != 5 || parts[0] != refVersion {
		return 0, false
	}
	contact, err := strconv.ParseInt(parts[1], 10, 32)
	if err != nil || contact <= 0 {
		return 0, false
	}
	expires, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return 0, false
	}
	got, err := base64.RawURLEncoding.DecodeString(parts[4])
	if err != nil {
		return 0, false
	}
	want, err := box.MAC(refPurpose, []byte(strings.Join(parts[:4], ".")))
	if err != nil || !hmac.Equal(got, want) {
		return 0, false
	}
	if parts[2] != hexN(channelID) || now.Unix() > expires {
		return 0, false
	}
	return int32(contact), true
}

// messengerLinkURL is the m.me link of a Page for a ref.
func messengerLinkURL(pageID, ref string) string {
	return "https://m.me/" + url.PathEscape(pageID) + "?ref=" + url.QueryEscape(ref)
}

// contactExists is whether the customer directory knows the contact. Customers
// off, or a directory that cannot say, is not a contact.
func (s *server) contactExists(ctx context.Context, id int32) (bool, error) {
	entry, err := directoryContact(ctx, s.deps.Directory, id)
	if err != nil {
		return false, fmt.Errorf("communications: look up contact: %w", err)
	}
	return entry != nil, nil
}

// PutCommunicationsParticipantsByIdContact links a participant to a contact.
// (PUT /api/v1/communications/participants/{id}/contact)
//
// N20: the contact must exist (422 contact_unknown) and, when the request names
// a conversation linked to a customer, be one of that customer's contacts (422
// contact_not_of_customer). Setting the contact the participant has is 200. The
// link replaces any other, and clears the suggestion and the referral notice.
func (s *server) PutCommunicationsParticipantsByIdContact(ctx context.Context, req gen.PutCommunicationsParticipantsByIdContactRequestObject) (gen.PutCommunicationsParticipantsByIdContactResponseObject, error) {
	if req.Body == nil || req.Body.ContactId <= 0 {
		return gen.PutCommunicationsParticipantsByIdContact400JSONResponse(validationErrorBody(
			map[string][]string{"contactId": {"The contact is required."}})), nil
	}
	contactID := req.Body.ContactId
	q := store.New(s.deps.Pool)
	if _, err := q.GetParticipantWithChannel(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCommunicationsParticipantsByIdContact404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("communications: get participant: %w", err)
	}

	exists, err := s.contactExists(ctx, contactID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return gen.PutCommunicationsParticipantsByIdContact422JSONResponse(flatErrorBody(
			"contact_unknown", "The selected contact does not exist.")), nil
	}

	if req.Body.ConversationId != nil {
		conv, err := q.GetConversationByID(ctx, *req.Body.ConversationId)
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.PutCommunicationsParticipantsByIdContact404Response{}, nil
		} else if err != nil {
			return nil, fmt.Errorf("communications: get conversation: %w", err)
		}
		if conv.CustomerID != nil {
			contacts, err := directoryContactsForCustomer(ctx, s.deps.Directory, *conv.CustomerID)
			if err != nil {
				return nil, fmt.Errorf("communications: list the customer's contacts: %w", err)
			}
			belongs := false
			for _, c := range contacts {
				if c.ID == contactID {
					belongs = true
					break
				}
			}
			if !belongs {
				return gen.PutCommunicationsParticipantsByIdContact422JSONResponse(flatErrorBody(
					"contact_not_of_customer", "The contact is not one of the conversation's customer's contacts.")), nil
			}
		}
	}

	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if _, err := txq.LockParticipant(ctx, req.Id); err != nil {
			return err
		}
		if err := txq.SetParticipantLinkedContact(ctx, store.SetParticipantLinkedContactParams{ContactID: &contactID, ID: req.Id}); err != nil {
			return err
		}
		return txq.DeleteParticipantContactHints(ctx, req.Id)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCommunicationsParticipantsByIdContact404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("communications: link participant to contact: %w", err)
	}
	return gen.PutCommunicationsParticipantsByIdContact200JSONResponse(gen.ParticipantContactResponse{
		ParticipantId: req.Id, ContactId: &contactID,
	}), nil
}

// DeleteCommunicationsParticipantsByIdContact clears a participant's contact.
// (DELETE /api/v1/communications/participants/{id}/contact)
func (s *server) DeleteCommunicationsParticipantsByIdContact(ctx context.Context, req gen.DeleteCommunicationsParticipantsByIdContactRequestObject) (gen.DeleteCommunicationsParticipantsByIdContactResponseObject, error) {
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if _, err := txq.LockParticipant(ctx, req.Id); err != nil {
			return err
		}
		if err := txq.ClearParticipantContact(ctx, req.Id); err != nil {
			return err
		}
		return txq.DeleteParticipantContactHints(ctx, req.Id)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteCommunicationsParticipantsByIdContact404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("communications: unlink participant from contact: %w", err)
	}
	return gen.DeleteCommunicationsParticipantsByIdContact204Response{}, nil
}

// PostCommunicationsChannelsByIdContactLinks makes a contact's Messenger link.
// (POST /api/v1/communications/channels/{id}/contact-links)
func (s *server) PostCommunicationsChannelsByIdContactLinks(ctx context.Context, req gen.PostCommunicationsChannelsByIdContactLinksRequestObject) (gen.PostCommunicationsChannelsByIdContactLinksResponseObject, error) {
	if req.Body == nil || req.Body.ContactId <= 0 {
		return gen.PostCommunicationsChannelsByIdContactLinks400JSONResponse(validationErrorBody(
			map[string][]string{"contactId": {"The contact is required."}})), nil
	}
	q := store.New(s.deps.Pool)
	channel, err := q.GetChannelByID(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostCommunicationsChannelsByIdContactLinks404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("communications: get channel: %w", err)
	}
	if channel.Type != channelTypeMessenger {
		return gen.PostCommunicationsChannelsByIdContactLinks422JSONResponse(flatErrorBody(
			"not_supported_on_channel", "A Messenger link can only be made for a messenger channel.")), nil
	}
	if !channel.IsActive {
		return gen.PostCommunicationsChannelsByIdContactLinks422JSONResponse(flatErrorBody(
			"channel_inactive", "The channel is inactive.")), nil
	}
	exists, err := s.contactExists(ctx, req.Body.ContactId)
	if err != nil {
		return nil, err
	}
	if !exists {
		return gen.PostCommunicationsChannelsByIdContactLinks422JSONResponse(flatErrorBody(
			"contact_unknown", "The selected contact does not exist.")), nil
	}
	expiresAt := s.deps.Clock().Add(refLifetime).UTC().Truncate(time.Second)
	ref, err := mintRef(s.deps.Secrets, req.Body.ContactId, channel.ID, expiresAt)
	if err != nil {
		return nil, err
	}
	return gen.PostCommunicationsChannelsByIdContactLinks200JSONResponse(gen.ContactLinkResponse{
		Url: messengerLinkURL(channel.Address, ref), ExpiresAt: expiresAt,
	}), nil
}

// messengerLinkOf is the link block of a messenger conversation's detail: its
// participant, the contact it is linked to, the one a shared e-mail address
// suggests and the one whose Messenger link it used while linked to another
// (N20, N21). nil for a conversation with no participant.
func messengerLinkOf(ctx context.Context, q *store.Queries, conversationID uuid.UUID) (*gen.MessengerLink, error) {
	p, err := q.GetConversationMessengerParticipant(ctx, conversationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("communications: get the conversation's participant: %w", err)
	}
	out := &gen.MessengerLink{ParticipantId: p.ID, ContactId: p.ContactID}
	hints, err := q.GetParticipantContactHints(ctx, p.ID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, fmt.Errorf("communications: get the participant's hints: %w", err)
	default:
		// A suggestion is moot once the participant has a contact, and a
		// referral that names the very contact it is linked to says nothing.
		if p.ContactID == nil {
			out.SuggestedContactId = hints.SuggestedContactID
		}
		if hints.ReferralContactID != nil && (p.ContactID == nil || *hints.ReferralContactID != *p.ContactID) {
			out.ReferralContactId = hints.ReferralContactID
		}
	}
	return out, nil
}
