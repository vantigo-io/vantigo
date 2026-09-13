package communications

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is the Tags area (EP/TagEndpoints.cs, communications inventory
// §1.4, §2's CreateTag/AddTag/RemoveTag bullets): getCommunicationsTags,
// postCommunicationsTags, putCommunicationsConversationsByIdTagsByTagId and
// deleteCommunicationsConversationsByIdTagsByTagId.
//
// Permission pairing (task 5 dispatch point 6): the tag-link routes (PUT and
// DELETE) require communications:conversations-manage ALONE — no +view,
// unlike notes and PATCH on the same conversation resource, which require
// manage+view. GET /tags and POST /tags pair conversations-view and
// conversations-manage respectively, each alone. module.Router enforces all
// of this from communications.yaml's x-vantigo-access; no handler here
// re-checks it — this comment exists so the asymmetry is visible beside the
// code it governs, and conversations_test.go pins it with tests that would
// fail if +view were ever added to either tag-link route.

// tagResponseOf is ToTagResponse's implicit shape (TagEndpoints.cs's direct
// `new TagResponse(tag.Id, tag.Name, tag.Color)` construction).
func tagResponseOf(t store.CommunicationsTag) gen.TagResponse {
	return gen.TagResponse{Id: t.ID, Name: t.Name, Color: t.Color}
}

// GetCommunicationsTags List tags
// (GET /api/v1/communications/tags)
//
// ListTags (inventory §1.4): a bare array, ordered by Name ascending.
func (s *server) GetCommunicationsTags(ctx context.Context, _ gen.GetCommunicationsTagsRequestObject) (gen.GetCommunicationsTagsResponseObject, error) {
	q := store.New(s.deps.Pool)
	rows, err := q.ListTags(ctx)
	if err != nil {
		return nil, fmt.Errorf("communications: list tags: %w", err)
	}
	data := make([]gen.TagResponse, 0, len(rows))
	for _, row := range rows {
		data = append(data, tagResponseOf(row))
	}
	return gen.GetCommunicationsTags200JSONResponse(data), nil
}

// PostCommunicationsTags Create a tag
// (POST /api/v1/communications/tags)
//
// CreateTag (inventory §2): (1) inline name check -> 400 invalid_request,
// flat (no fields) — inventory §2's own text: "A tag name is required."; (2)
// insert; a unique violation on name -> 409 tag_exists.
func (s *server) PostCommunicationsTags(ctx context.Context, req gen.PostCommunicationsTagsRequestObject) (gen.PostCommunicationsTagsResponseObject, error) {
	var name, color string
	var hasColor bool
	if req.Body != nil {
		if req.Body.Name != nil {
			name = *req.Body.Name
		}
		if req.Body.Color != nil {
			color = strings.TrimSpace(*req.Body.Color)
			hasColor = true
		}
	}
	if strings.TrimSpace(name) == "" || len(name) > 100 {
		return gen.PostCommunicationsTags400JSONResponse(flatErrorBody(
			"invalid_request", "A tag name is required.")), nil
	}
	name = strings.TrimSpace(name)

	var colorPtr *string
	if hasColor {
		colorPtr = &color
	}

	q := store.New(s.deps.Pool)
	tag, err := q.InsertTag(ctx, store.InsertTagParams{ID: uuid.New(), Name: name, Color: colorPtr})
	if err != nil {
		if db.IsUniqueViolation(err, "ux_tags_name") {
			return gen.PostCommunicationsTags409JSONResponse(flatErrorBody(
				"tag_exists", "A tag with this name already exists.")), nil
		}
		return nil, fmt.Errorf("communications: create tag: %w", err)
	}
	return gen.PostCommunicationsTags201JSONResponse(tagResponseOf(tag)), nil
}

// PutCommunicationsConversationsByIdTagsByTagId Add a tag to a conversation
// (PUT /api/v1/communications/conversations/{id}/tags/{tagId})
//
// AddTag (inventory §2, §1.4): both existence checks together (conversation
// OR tag missing -> 404 bare); then an idempotent insert -> 204, whether or
// not the link already existed.
func (s *server) PutCommunicationsConversationsByIdTagsByTagId(ctx context.Context, req gen.PutCommunicationsConversationsByIdTagsByTagIdRequestObject) (gen.PutCommunicationsConversationsByIdTagsByTagIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	exists, err := q.ConversationAndTagExist(ctx, store.ConversationAndTagExistParams{ConversationID: req.Id, TagID: req.TagId})
	if err != nil {
		return nil, fmt.Errorf("communications: check conversation and tag existence: %w", err)
	}
	if !exists.ConversationExists || !exists.TagExists {
		return gen.PutCommunicationsConversationsByIdTagsByTagId404Response{}, nil
	}
	if err := q.InsertConversationTagIfAbsent(ctx, store.InsertConversationTagIfAbsentParams{
		ConversationID: req.Id, TagID: req.TagId,
	}); err != nil {
		return nil, fmt.Errorf("communications: add conversation tag: %w", err)
	}
	return gen.PutCommunicationsConversationsByIdTagsByTagId204Response{}, nil
}

// DeleteCommunicationsConversationsByIdTagsByTagId Remove a tag from a conversation
// (DELETE /api/v1/communications/conversations/{id}/tags/{tagId})
//
// RemoveTag (inventory §2, §1.4): link lookup -> 404 bare; else delete ->
// 204. NOT idempotent (inventory §1.4): removing an already-absent tag is
// 404, unlike PUT's idempotent 204 — the exact asymmetry
// TestRemoveTag_AlreadyAbsentIs404 pins.
func (s *server) DeleteCommunicationsConversationsByIdTagsByTagId(ctx context.Context, req gen.DeleteCommunicationsConversationsByIdTagsByTagIdRequestObject) (gen.DeleteCommunicationsConversationsByIdTagsByTagIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	rows, err := q.DeleteConversationTag(ctx, store.DeleteConversationTagParams{ConversationID: req.Id, TagID: req.TagId})
	if err != nil {
		return nil, fmt.Errorf("communications: remove conversation tag: %w", err)
	}
	if rows == 0 {
		return gen.DeleteCommunicationsConversationsByIdTagsByTagId404Response{}, nil
	}
	return gen.DeleteCommunicationsConversationsByIdTagsByTagId204Response{}, nil
}
