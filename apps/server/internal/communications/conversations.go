package communications

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	apicommon "github.com/vantigo-io/vantigo/server/internal/apicommon/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the Conversations area's read paths (EP/ConversationEndpoints.cs,
// communications inventory §1.2, §2's ListConversations/GetConversation/
// MarkRead bullets): getCommunicationsConversations,
// getCommunicationsConversationsById and postCommunicationsConversationsByIdRead.
// PATCH lives in conversations_patch.go, notes in conversations_notes.go,
// create in conversations_create.go — response-shape builders shared by more
// than one of those files live here.

// errNoCaller mirrors identity/server.go's callerFrom: every operation in
// this file requires a permission, never RuleAnonymous
// (communications.yaml's x-vantigo-access on each), so module.Router's
// Access.Check already guarantees a real Principal with a non-nil UserID
// before any handler here runs. Its absence is a server error, not a
// reachable 401 — which is also why MarkRead's .NET "missing NameIdentifier
// claim -> 401" branch (inventory §2) has no Go counterpart: the router
// already turned that case into a 401 before a handler could be reached.
var errNoCaller = errors.New("communications: no signed-in caller in the handler context")

// callerUserID is the authenticated caller's id, guaranteed present by the
// router for every operation in this module (see errNoCaller).
func callerUserID(ctx context.Context) (uuid.UUID, error) {
	p, ok := contracts.PrincipalFrom(ctx)
	if !ok || p.UserID == uuid.Nil {
		return uuid.Nil, errNoCaller
	}
	return p.UserID, nil
}

// participantJSON is the participant shape shared by ConversationListItem.Participants,
// ConversationDetailResponse.Participants, ConversationDetailResponse.Messages[i].Participant
// and ConversationDetailResponse.ReplyRecipients.ReplyAllCc — four fields
// spelled out identically (name, type, tag, and alphabetical order, the
// convention oapi-codegen's inline schemas use throughout this contract) so
// Go treats them as the same unnamed struct type and a value built by
// newParticipantJSON is directly assignable to any of the four.
func newParticipantJSON(id, channelID uuid.UUID, address string, displayName *string, contactID *int32) struct {
	Address     string             `json:"address"`
	ChannelId   openapi_types.UUID `json:"channelId"`
	ContactId   *int32             `json:"contactId"`
	DisplayName *string            `json:"displayName"`
	Id          openapi_types.UUID `json:"id"`
} {
	return struct {
		Address     string             `json:"address"`
		ChannelId   openapi_types.UUID `json:"channelId"`
		ContactId   *int32             `json:"contactId"`
		DisplayName *string            `json:"displayName"`
		Id          openapi_types.UUID `json:"id"`
	}{Address: address, ChannelId: channelID, ContactId: contactID, DisplayName: displayName, Id: id}
}

// tagJSON is the tag shape shared by ConversationListItem.Tags and
// ConversationDetailResponse.Tags.
func newTagJSON(t store.CommunicationsTag) struct {
	Color *string            `json:"color"`
	Id    openapi_types.UUID `json:"id"`
	Name  string             `json:"name"`
} {
	return struct {
		Color *string            `json:"color"`
		Id    openapi_types.UUID `json:"id"`
		Name  string             `json:"name"`
	}{Color: t.Color, Id: t.ID, Name: t.Name}
}

// pageValues is PageValues (`:470`): page clamped to a minimum of 1,
// pageSize clamped to [1, 100]. Neither is ever rejected — an out-of-range
// request is silently clamped, never 400 (inventory §2's ListConversations
// bullet).
func pageValues(page, pageSize *int32) (int32, int32) {
	p := int32(1)
	if page != nil {
		p = *page
	}
	if p < 1 {
		p = 1
	}
	size := int32(25)
	if pageSize != nil {
		size = *pageSize
	}
	switch {
	case size < 1:
		size = 1
	case size > 100:
		size = 100
	}
	return p, size
}

// paginationOf is PaginationMetadata.Create, the same formula
// customers/errors.go's paginationMetadata already implements — duplicated
// per-module because depguard forbids importing another business module's
// package for one helper.
func paginationOf(page, pageSize, totalCount int32) apicommon.PaginationMetadata {
	var totalPages int32
	if pageSize > 0 {
		totalPages = int32(math.Ceil(float64(totalCount) / float64(pageSize)))
	}
	return apicommon.PaginationMetadata{
		Page: page, PageSize: pageSize, TotalCount: totalCount, TotalPages: totalPages,
		HasNextPage: page < totalPages, HasPreviousPage: page > 1 && totalCount > 0,
	}
}

// conversationBatches is every per-conversation collection ListConversations
// and GetConversation both need, loaded once for a batch of ids rather than
// once per conversation (the same batch-then-group shape energy's customer
// endpoints use for metering points and supply periods).
type conversationBatches struct {
	tags map[uuid.UUID][]struct {
		Color *string            `json:"color"`
		Id    openapi_types.UUID `json:"id"`
		Name  string             `json:"name"`
	}
	participants map[uuid.UUID][]struct {
		Address     string             `json:"address"`
		ChannelId   openapi_types.UUID `json:"channelId"`
		ContactId   *int32             `json:"contactId"`
		DisplayName *string            `json:"displayName"`
		Id          openapi_types.UUID `json:"id"`
	}
	candidates map[uuid.UUID][]int32
}

// loadConversationBatches loads tags, participants and customer candidates
// for every id in ids, each keyed by conversation id and defaulting to an
// empty (never nil) slice so a conversation with none still serializes `[]`,
// not `null` (every one of these fields is a non-nullable array in the
// contract).
func loadConversationBatches(ctx context.Context, q *store.Queries, ids []uuid.UUID) (conversationBatches, error) {
	b := conversationBatches{
		tags: map[uuid.UUID][]struct {
			Color *string            `json:"color"`
			Id    openapi_types.UUID `json:"id"`
			Name  string             `json:"name"`
		}{},
		participants: map[uuid.UUID][]struct {
			Address     string             `json:"address"`
			ChannelId   openapi_types.UUID `json:"channelId"`
			ContactId   *int32             `json:"contactId"`
			DisplayName *string            `json:"displayName"`
			Id          openapi_types.UUID `json:"id"`
		}{},
		candidates: map[uuid.UUID][]int32{},
	}
	for _, id := range ids {
		b.tags[id] = []struct {
			Color *string            `json:"color"`
			Id    openapi_types.UUID `json:"id"`
			Name  string             `json:"name"`
		}{}
		b.participants[id] = []struct {
			Address     string             `json:"address"`
			ChannelId   openapi_types.UUID `json:"channelId"`
			ContactId   *int32             `json:"contactId"`
			DisplayName *string            `json:"displayName"`
			Id          openapi_types.UUID `json:"id"`
		}{}
		b.candidates[id] = []int32{}
	}

	tagRows, err := q.ListConversationTagsByConversationIDs(ctx, ids)
	if err != nil {
		return conversationBatches{}, fmt.Errorf("communications: list conversation tags: %w", err)
	}
	for _, r := range tagRows {
		b.tags[r.ConversationID] = append(b.tags[r.ConversationID], newTagJSON(store.CommunicationsTag{ID: r.ID, Name: r.Name, Color: r.Color}))
	}

	participantRows, err := q.ListConversationParticipantsByConversationIDs(ctx, ids)
	if err != nil {
		return conversationBatches{}, fmt.Errorf("communications: list conversation participants: %w", err)
	}
	for _, r := range participantRows {
		b.participants[r.ConversationID] = append(b.participants[r.ConversationID],
			newParticipantJSON(r.ID, r.ChannelID, r.Address, r.DisplayName, r.ContactID))
	}

	candidateRows, err := q.ListConversationCandidatesByConversationIDs(ctx, ids)
	if err != nil {
		return conversationBatches{}, fmt.Errorf("communications: list conversation candidates: %w", err)
	}
	for _, r := range candidateRows {
		b.candidates[r.ConversationID] = append(b.candidates[r.ConversationID], r.CustomerID)
	}
	return b, nil
}

// GetCommunicationsConversations List conversations
// (GET /api/v1/communications/conversations)
//
// ListConversations (inventory §1.2, §2): no body validation of any kind;
// page/pageSize silently clamped; status trimmed+lowercased and used as an
// equality filter (an unknown status yields an empty page, never 400);
// unreadOnly only applies when true (the caller always has a user id
// through this router, see errNoCaller); never 404s, even though the
// contract declares one (inventory §6 item 1; task 5 dispatch point 4) —
// ported as-is, not "fixed".
func (s *server) GetCommunicationsConversations(ctx context.Context, req gen.GetCommunicationsConversationsRequestObject) (gen.GetCommunicationsConversationsResponseObject, error) {
	caller, err := callerUserID(ctx)
	if err != nil {
		return nil, err
	}
	page, pageSize := pageValues(req.Params.Page, req.Params.PageSize)

	var status *string
	if req.Params.Status != nil && strings.TrimSpace(*req.Params.Status) != "" {
		v := strings.ToLower(strings.TrimSpace(*req.Params.Status))
		status = &v
	}
	var unreadUserID *uuid.UUID
	if req.Params.UnreadOnly != nil && *req.Params.UnreadOnly {
		unreadUserID = &caller
	}

	q := store.New(s.deps.Pool)
	rows, err := q.ListConversations(ctx, store.ListConversationsParams{
		Status: status, AssignedUserID: req.Params.AssignedUserId, CustomerID: req.Params.CustomerId,
		TagID: req.Params.TagId, UnreadUserID: unreadUserID,
		PageOffset: (page - 1) * pageSize, PageLimit: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("communications: list conversations: %w", err)
	}

	ids := make([]uuid.UUID, 0, len(rows))
	var total int64
	for _, r := range rows {
		ids = append(ids, r.ID)
		total = r.TotalCount
	}
	batches, err := loadConversationBatches(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	readRows, err := q.ListConversationReadStatesByConversationIDsForUser(ctx, store.ListConversationReadStatesByConversationIDsForUserParams{
		ConversationIds: ids, UserID: caller,
	})
	if err != nil {
		return nil, fmt.Errorf("communications: list conversation read states: %w", err)
	}
	lastRead := make(map[uuid.UUID]time.Time, len(readRows))
	for _, r := range readRows {
		lastRead[r.ConversationID] = r.LastReadAt
	}

	data := make([]gen.ConversationListItem, 0, len(rows))
	for _, r := range rows {
		unread := true
		if at, ok := lastRead[r.ID]; ok && !at.Before(r.LastActivityAt) {
			unread = false
		}
		data = append(data, gen.ConversationListItem{
			Id: r.ID, ChannelId: r.ChannelID, Subject: r.Subject, Status: r.Status,
			AssignedUserId: r.AssignedUserID, CustomerId: r.CustomerID,
			CustomerAssociationSource: r.CustomerAssociationSource, SuggestedCustomerId: r.SuggestedCustomerID,
			CandidateCustomerIds: batches.candidates[r.ID], LastActivityAt: r.LastActivityAt, PreviewText: r.PreviewText,
			Participants: batches.participants[r.ID], Unread: unread, Tags: batches.tags[r.ID],
		})
	}
	return gen.GetCommunicationsConversations200JSONResponse{
		Data:       data,
		Pagination: paginationOf(page, pageSize, int32(total)),
	}, nil
}

// replyRecipientsOf is ReplyRecipients (EP/ConversationEndpoints.cs:446-455).
// It always answers the all-negative constant
// (canReply:false, canReplyAll:false, replyTo:nil, replyAllCc:[]) — never
// computed from a query — because its non-constant branch requires a
// direction='inbound' message, and communications.conversation_messages'
// own CHECK constraint (00006_communications_baseline.sql) admits only
// 'outbound' and 'internal_note': this port has no inbound path (design doc
// §1.1), so no row satisfying ReplyRecipients' own precondition can ever
// exist, not even through a raw test fixture. Task 5 dispatch: "canReply is
// false and replyAllCc empty for conversations created through the API...
// pin it rather than working around it" — this is that pin, encoded as the
// only value this function can ever produce rather than as a workaround.
func replyRecipientsOf() struct {
	CanReply    bool `json:"canReply"`
	CanReplyAll bool `json:"canReplyAll"`
	ReplyAllCc  []struct {
		Address     string             `json:"address"`
		ChannelId   openapi_types.UUID `json:"channelId"`
		ContactId   *int32             `json:"contactId"`
		DisplayName *string            `json:"displayName"`
		Id          openapi_types.UUID `json:"id"`
	} `json:"replyAllCc"`
	ReplyTo *string `json:"replyTo"`
} {
	return struct {
		CanReply    bool `json:"canReply"`
		CanReplyAll bool `json:"canReplyAll"`
		ReplyAllCc  []struct {
			Address     string             `json:"address"`
			ChannelId   openapi_types.UUID `json:"channelId"`
			ContactId   *int32             `json:"contactId"`
			DisplayName *string            `json:"displayName"`
			Id          openapi_types.UUID `json:"id"`
		} `json:"replyAllCc"`
		ReplyTo *string `json:"replyTo"`
	}{
		CanReply: false, CanReplyAll: false, ReplyTo: nil,
		ReplyAllCc: []struct {
			Address     string             `json:"address"`
			ChannelId   openapi_types.UUID `json:"channelId"`
			ContactId   *int32             `json:"contactId"`
			DisplayName *string            `json:"displayName"`
			Id          openapi_types.UUID `json:"id"`
		}{},
	}
}

// GetCommunicationsConversationsById Get a conversation
// (GET /api/v1/communications/conversations/{id})
//
// GetConversation (inventory §1.2, §2): lookup -> 404 bare, nothing else.
// replyRecipients is always the all-negative constant (see
// replyRecipientsOf). A message's participant is nullable in the contract
// (openapi/communications.yaml's `participant` schema carries
// `nullable: true`, task 5 fix round 1 item 4) and this port never sets
// participant_id on any message it writes — neither AddNote's note message
// nor CreateConversation's outbound message, matching .NET exactly (only
// the removed inbound processor ever assigned ConversationMessage.ParticipantId)
// — so participant renders `null` for every message, not only notes.
func (s *server) GetCommunicationsConversationsById(ctx context.Context, req gen.GetCommunicationsConversationsByIdRequestObject) (gen.GetCommunicationsConversationsByIdResponseObject, error) {
	caller, err := callerUserID(ctx)
	if err != nil {
		return nil, err
	}
	q := store.New(s.deps.Pool)
	conv, err := q.GetConversationByID(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCommunicationsConversationsById404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("communications: get conversation: %w", err)
	}

	batches, err := loadConversationBatches(ctx, q, []uuid.UUID{req.Id})
	if err != nil {
		return nil, err
	}

	var lastReadAt *time.Time
	readAt, err := q.GetConversationReadState(ctx, store.GetConversationReadStateParams{ConversationID: req.Id, UserID: caller})
	if err == nil {
		lastReadAt = &readAt
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("communications: get conversation read state: %w", err)
	}

	messages, err := s.conversationMessagesOf(ctx, q, req.Id)
	if err != nil {
		return nil, err
	}

	resp := gen.ConversationDetailResponse{
		Id: conv.ID, ChannelId: conv.ChannelID, Subject: conv.Subject, Status: conv.Status,
		AssignedUserId: conv.AssignedUserID, CustomerId: conv.CustomerID,
		CustomerAssociationSource: conv.CustomerAssociationSource, SuggestedCustomerId: conv.SuggestedCustomerID,
		SuggestedCustomerConfidence: conv.SuggestedCustomerConfidence, SuggestedCustomerReasoning: conv.SuggestedCustomerReasoning,
		CandidateCustomerIds: batches.candidates[req.Id], LastActivityAt: conv.LastActivityAt, PreviewText: conv.PreviewText,
		CreatedAt: conv.CreatedAt, Messages: messages, Participants: batches.participants[req.Id],
		Tags: batches.tags[req.Id], LastReadAt: lastReadAt, ReplyRecipients: replyRecipientsOf(),
	}
	return gen.GetCommunicationsConversationsById200JSONResponse(resp), nil
}

// conversationMessagesOf builds GetConversation's Messages array: ordered
// OccurredAt ascending (from the query itself), each with its attachments
// and deliveries batch-loaded by message id (inventory §1.2's `ToMessage`).
func (s *server) conversationMessagesOf(ctx context.Context, q *store.Queries, conversationID uuid.UUID) ([]struct {
	Attachments []struct {
		ContentId         *string            `json:"contentId"`
		ContentType       string             `json:"contentType"`
		CreatedAt         time.Time          `json:"createdAt"`
		DownloadAvailable bool               `json:"downloadAvailable"`
		DownloadPath      string             `json:"downloadPath"`
		FileName          string             `json:"fileName"`
		Id                openapi_types.UUID `json:"id"`
		IsInline          bool               `json:"isInline"`
		ScanStatus        string             `json:"scanStatus"`
		SizeBytes         int64              `json:"sizeBytes"`
	} `json:"attachments"`
	AuthorUserId *openapi_types.UUID `json:"authorUserId"`
	CreatedAt    time.Time           `json:"createdAt"`
	Deliveries   []struct {
		AcceptedAt    *time.Time         `json:"acceptedAt"`
		Attempts      int32              `json:"attempts"`
		Destination   string             `json:"destination"`
		Error         *string            `json:"error"`
		Id            openapi_types.UUID `json:"id"`
		RecipientType string             `json:"recipientType"`
		Status        string             `json:"status"`
	} `json:"deliveries"`
	Direction   string             `json:"direction"`
	HtmlBody    *string            `json:"htmlBody"`
	Id          openapi_types.UUID `json:"id"`
	OccurredAt  time.Time          `json:"occurredAt"`
	Participant *struct {
		Address     string             `json:"address"`
		ChannelId   openapi_types.UUID `json:"channelId"`
		ContactId   *int32             `json:"contactId"`
		DisplayName *string            `json:"displayName"`
		Id          openapi_types.UUID `json:"id"`
	} `json:"participant"`
	Subject  *string `json:"subject"`
	TextBody *string `json:"textBody"`
}, error,
) {
	rows, err := q.GetConversationMessagesByConversationID(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("communications: list conversation messages: %w", err)
	}
	messageIDs := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		messageIDs = append(messageIDs, r.ID)
	}
	attachmentRows, err := q.ListMessageAttachmentsByMessageIDs(ctx, messageIDs)
	if err != nil {
		return nil, fmt.Errorf("communications: list message attachments: %w", err)
	}
	attachmentsByMessage := map[uuid.UUID][]struct {
		ContentId         *string            `json:"contentId"`
		ContentType       string             `json:"contentType"`
		CreatedAt         time.Time          `json:"createdAt"`
		DownloadAvailable bool               `json:"downloadAvailable"`
		DownloadPath      string             `json:"downloadPath"`
		FileName          string             `json:"fileName"`
		Id                openapi_types.UUID `json:"id"`
		IsInline          bool               `json:"isInline"`
		ScanStatus        string             `json:"scanStatus"`
		SizeBytes         int64              `json:"sizeBytes"`
	}{}
	for _, id := range messageIDs {
		attachmentsByMessage[id] = []struct {
			ContentId         *string            `json:"contentId"`
			ContentType       string             `json:"contentType"`
			CreatedAt         time.Time          `json:"createdAt"`
			DownloadAvailable bool               `json:"downloadAvailable"`
			DownloadPath      string             `json:"downloadPath"`
			FileName          string             `json:"fileName"`
			Id                openapi_types.UUID `json:"id"`
			IsInline          bool               `json:"isInline"`
			ScanStatus        string             `json:"scanStatus"`
			SizeBytes         int64              `json:"sizeBytes"`
		}{}
	}
	for _, a := range attachmentRows {
		attachmentsByMessage[a.MessageID] = append(attachmentsByMessage[a.MessageID], struct {
			ContentId         *string            `json:"contentId"`
			ContentType       string             `json:"contentType"`
			CreatedAt         time.Time          `json:"createdAt"`
			DownloadAvailable bool               `json:"downloadAvailable"`
			DownloadPath      string             `json:"downloadPath"`
			FileName          string             `json:"fileName"`
			Id                openapi_types.UUID `json:"id"`
			IsInline          bool               `json:"isInline"`
			ScanStatus        string             `json:"scanStatus"`
			SizeBytes         int64              `json:"sizeBytes"`
		}{
			ContentId: a.ContentID, ContentType: a.ContentType, CreatedAt: a.CreatedAt,
			DownloadAvailable: a.ScanStatus == "clean", DownloadPath: fmt.Sprintf("/api/v1/communications/attachments/%s/download", a.ID),
			FileName: a.FileName, Id: a.ID, IsInline: a.IsInline, ScanStatus: a.ScanStatus, SizeBytes: a.SizeBytes,
		})
	}

	deliveryRows, err := q.ListMessageDeliveriesByMessageIDs(ctx, messageIDs)
	if err != nil {
		return nil, fmt.Errorf("communications: list message deliveries: %w", err)
	}
	deliveriesByMessage := map[uuid.UUID][]struct {
		AcceptedAt    *time.Time         `json:"acceptedAt"`
		Attempts      int32              `json:"attempts"`
		Destination   string             `json:"destination"`
		Error         *string            `json:"error"`
		Id            openapi_types.UUID `json:"id"`
		RecipientType string             `json:"recipientType"`
		Status        string             `json:"status"`
	}{}
	for _, id := range messageIDs {
		deliveriesByMessage[id] = []struct {
			AcceptedAt    *time.Time         `json:"acceptedAt"`
			Attempts      int32              `json:"attempts"`
			Destination   string             `json:"destination"`
			Error         *string            `json:"error"`
			Id            openapi_types.UUID `json:"id"`
			RecipientType string             `json:"recipientType"`
			Status        string             `json:"status"`
		}{}
	}
	for _, d := range deliveryRows {
		deliveriesByMessage[d.MessageID] = append(deliveriesByMessage[d.MessageID], struct {
			AcceptedAt    *time.Time         `json:"acceptedAt"`
			Attempts      int32              `json:"attempts"`
			Destination   string             `json:"destination"`
			Error         *string            `json:"error"`
			Id            openapi_types.UUID `json:"id"`
			RecipientType string             `json:"recipientType"`
			Status        string             `json:"status"`
		}{
			AcceptedAt: d.AcceptedAt, Attempts: d.Attempts, Destination: d.RecipientAddress,
			Error: nil, Id: d.ID, RecipientType: d.RecipientType, Status: d.Status,
		})
	}

	out := make([]struct {
		Attachments []struct {
			ContentId         *string            `json:"contentId"`
			ContentType       string             `json:"contentType"`
			CreatedAt         time.Time          `json:"createdAt"`
			DownloadAvailable bool               `json:"downloadAvailable"`
			DownloadPath      string             `json:"downloadPath"`
			FileName          string             `json:"fileName"`
			Id                openapi_types.UUID `json:"id"`
			IsInline          bool               `json:"isInline"`
			ScanStatus        string             `json:"scanStatus"`
			SizeBytes         int64              `json:"sizeBytes"`
		} `json:"attachments"`
		AuthorUserId *openapi_types.UUID `json:"authorUserId"`
		CreatedAt    time.Time           `json:"createdAt"`
		Deliveries   []struct {
			AcceptedAt    *time.Time         `json:"acceptedAt"`
			Attempts      int32              `json:"attempts"`
			Destination   string             `json:"destination"`
			Error         *string            `json:"error"`
			Id            openapi_types.UUID `json:"id"`
			RecipientType string             `json:"recipientType"`
			Status        string             `json:"status"`
		} `json:"deliveries"`
		Direction   string             `json:"direction"`
		HtmlBody    *string            `json:"htmlBody"`
		Id          openapi_types.UUID `json:"id"`
		OccurredAt  time.Time          `json:"occurredAt"`
		Participant *struct {
			Address     string             `json:"address"`
			ChannelId   openapi_types.UUID `json:"channelId"`
			ContactId   *int32             `json:"contactId"`
			DisplayName *string            `json:"displayName"`
			Id          openapi_types.UUID `json:"id"`
		} `json:"participant"`
		Subject  *string `json:"subject"`
		TextBody *string `json:"textBody"`
	}, 0, len(rows))
	for _, r := range rows {
		// task 5 fix round 1, item 4: nil, not the zero value — participant
		// is nullable in the contract (openapi/communications.yaml) exactly
		// because it is unset for every message this port ever writes, not
		// only notes. Neither AddNote nor CreateConversation's
		// BuildOutboundMessage sets participant_id (see this file's and
		// conversations_create.go's InsertConversationMessage calls, and
		// .NET's own BuildOutboundMessage, which never assigns
		// ConversationMessage.ParticipantId either — only the removed
		// inbound processor ever did). So every row here has
		// r.ParticipantID == nil, and participant stays nil for all of
		// them; the pointer exists so a future inbound producer can set one
		// without another contract change.
		var participant *struct {
			Address     string             `json:"address"`
			ChannelId   openapi_types.UUID `json:"channelId"`
			ContactId   *int32             `json:"contactId"`
			DisplayName *string            `json:"displayName"`
			Id          openapi_types.UUID `json:"id"`
		}
		if r.ParticipantID != nil {
			p := newParticipantJSON(*r.ParticipantID, *r.ParticipantChannelID, *r.ParticipantAddress, r.ParticipantDisplayName, r.ParticipantContactID)
			participant = &p
		}
		out = append(out, struct {
			Attachments []struct {
				ContentId         *string            `json:"contentId"`
				ContentType       string             `json:"contentType"`
				CreatedAt         time.Time          `json:"createdAt"`
				DownloadAvailable bool               `json:"downloadAvailable"`
				DownloadPath      string             `json:"downloadPath"`
				FileName          string             `json:"fileName"`
				Id                openapi_types.UUID `json:"id"`
				IsInline          bool               `json:"isInline"`
				ScanStatus        string             `json:"scanStatus"`
				SizeBytes         int64              `json:"sizeBytes"`
			} `json:"attachments"`
			AuthorUserId *openapi_types.UUID `json:"authorUserId"`
			CreatedAt    time.Time           `json:"createdAt"`
			Deliveries   []struct {
				AcceptedAt    *time.Time         `json:"acceptedAt"`
				Attempts      int32              `json:"attempts"`
				Destination   string             `json:"destination"`
				Error         *string            `json:"error"`
				Id            openapi_types.UUID `json:"id"`
				RecipientType string             `json:"recipientType"`
				Status        string             `json:"status"`
			} `json:"deliveries"`
			Direction   string             `json:"direction"`
			HtmlBody    *string            `json:"htmlBody"`
			Id          openapi_types.UUID `json:"id"`
			OccurredAt  time.Time          `json:"occurredAt"`
			Participant *struct {
				Address     string             `json:"address"`
				ChannelId   openapi_types.UUID `json:"channelId"`
				ContactId   *int32             `json:"contactId"`
				DisplayName *string            `json:"displayName"`
				Id          openapi_types.UUID `json:"id"`
			} `json:"participant"`
			Subject  *string `json:"subject"`
			TextBody *string `json:"textBody"`
		}{
			Attachments: attachmentsByMessage[r.ID], AuthorUserId: r.AuthorUserID, CreatedAt: r.CreatedAt,
			Deliveries: deliveriesByMessage[r.ID], Direction: r.Direction, HtmlBody: r.HtmlBody,
			Id: r.ID, OccurredAt: r.OccurredAt, Participant: participant, Subject: r.Subject, TextBody: r.TextBody,
		})
	}
	return out, nil
}

// PostCommunicationsConversationsByIdRead Mark a conversation read
// (POST /api/v1/communications/conversations/{id}/read)
//
// MarkRead (inventory §1.2, §2, task 5 dispatch point 3): conversation
// existence -> 404 bare; upsert read state; 200 with a genuinely empty body
// (x-vantigo-empty-body: true, SPEC:1753) — PostCommunicationsConversationsByIdRead200Response
// writes only the status line, never a body.
func (s *server) PostCommunicationsConversationsByIdRead(ctx context.Context, req gen.PostCommunicationsConversationsByIdReadRequestObject) (gen.PostCommunicationsConversationsByIdReadResponseObject, error) {
	caller, err := callerUserID(ctx)
	if err != nil {
		return nil, err
	}
	q := store.New(s.deps.Pool)
	if _, err := q.GetConversationByID(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.PostCommunicationsConversationsByIdRead404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("communications: get conversation: %w", err)
	}
	if err := q.UpsertConversationReadState(ctx, store.UpsertConversationReadStateParams{
		ConversationID: req.Id, UserID: caller, LastReadAt: s.deps.Clock(),
	}); err != nil {
		return nil, fmt.Errorf("communications: mark conversation read: %w", err)
	}
	return gen.PostCommunicationsConversationsByIdRead200Response{}, nil
}
