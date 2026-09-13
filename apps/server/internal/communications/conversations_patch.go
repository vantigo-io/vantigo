package communications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/store"
)

// PatchCommunicationsConversationsById Update a conversation
// (PATCH /api/v1/communications/conversations/{id})
//
// UpdateConversation (inventory §2's "mirror image within one handler",
// task 5 dispatch correction 2 — the authority for this ordering): a null
// body *or* an invalid `status` runs BEFORE the 404 and is a 400; a bad
// `customerId` type runs AFTER the 404 and is a 404 when the conversation
// is missing, a *different* 400 shape when it exists. Both checks live in
// this one handler, on opposite sides of the lookup:
//
//  1. nullBody or invalid status -> 400 invalid_request "Status must be
//     open, closed, or archived." (flat, no fields — same shape §3.2 lists).
//  2. conversation lookup -> 404 bare.
//  3. status assignment (already validated in step 1).
//  4. assignedUserId, if the key is present in the raw JSON: null clears it,
//     otherwise it is read completely unvalidated (task 5 dispatch point 7 —
//     see the comment on the parse below).
//  5. customerId, if the key is present: null clears it (and its
//     association source, suggestion fields and candidates); a non-integer,
//     non-null value -> 400 invalid_request "CustomerId must be an integer
//     or null." (also flat, but a DIFFERENT code path than step 1's 400 —
//     the two 400s share a shape by coincidence of both being flat, not
//     because they are the same check); an integer that does not resolve
//     through contracts.CustomerDirectory -> 422 customer_invalid.
//
// "Both conditions true at once -> 400 (status is checked first)" (dispatch
// correction 2's closing line) falls out for free: step 1 returns
// immediately, so a request with both a bad status and a bad customerId
// never reaches step 5 at all, existing conversation or not.
func (s *server) PatchCommunicationsConversationsById(ctx context.Context, req gen.PatchCommunicationsConversationsByIdRequestObject) (gen.PatchCommunicationsConversationsByIdResponseObject, error) {
	raw, _ := rawJSONBodyFrom(ctx)
	nullBody := req.Body == nil || strings.TrimSpace(string(raw)) == "null"

	var status *string
	if !nullBody && req.Body.Status != nil {
		v := strings.ToLower(strings.TrimSpace(*req.Body.Status))
		status = &v
	}
	if nullBody || (status != nil && *status != "open" && *status != "closed" && *status != "archived") {
		return gen.PatchCommunicationsConversationsById400JSONResponse(flatErrorBody(
			"invalid_request", "Status must be open, closed, or archived.")), nil
	}

	q := store.New(s.deps.Pool)
	conv, err := q.GetConversationByID(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PatchCommunicationsConversationsById404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("communications: get conversation: %w", err)
	}

	// fields tells "key absent" from "key present" (null or otherwise) for
	// assignedUserId and customerId — the distinction module.go's
	// withRawPatchBody exists to recover, since Go's generated decode
	// collapses both into the same nil *interface{} (see its own comment).
	// A nil map (raw did not parse as a JSON object, or was empty) answers
	// "not present" for every key, the safe default: untouched.
	var fields map[string]json.RawMessage
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &fields)
	}

	finalStatus := conv.Status
	if status != nil {
		finalStatus = *status
	}

	// assignedUserId (task 5 dispatch point 7): .NET's `assigned.GetGuid()`
	// runs with no ValueKind check beyond "is it null" — a non-string value
	// (a number, an object, an array) throws exactly the same as a
	// string that is not a valid GUID, both unhandled exceptions the global
	// handler turns into a sanitised 500. This is ported faithfully rather
	// than hardened: a wrong-typed or malformed value here returns a wrapped
	// error, which module.ResponseError turns into a 500 that never echoes
	// it — deliberately not a 400, because .NET never produces one either.
	assignedUserID := conv.AssignedUserID
	if rawAssigned, present := fields["assignedUserId"]; present {
		trimmed := bytes.TrimSpace(rawAssigned)
		if string(trimmed) == "null" {
			assignedUserID = nil
		} else {
			var text string
			if err := json.Unmarshal(trimmed, &text); err != nil {
				return nil, fmt.Errorf("communications: assignedUserId is not a JSON string: %w", err)
			}
			parsed, err := uuid.Parse(text)
			if err != nil {
				return nil, fmt.Errorf("communications: assignedUserId is not a valid guid: %w", err)
			}
			assignedUserID = &parsed
		}
	}

	customerID := conv.CustomerID
	customerAssociationSource := conv.CustomerAssociationSource
	suggestedCustomerID := conv.SuggestedCustomerID
	suggestedCustomerConfidence := conv.SuggestedCustomerConfidence
	suggestedCustomerReasoning := conv.SuggestedCustomerReasoning
	touchedCustomer := false

	if rawCustomer, present := fields["customerId"]; present {
		touchedCustomer = true
		trimmed := bytes.TrimSpace(rawCustomer)
		var requested *int32
		switch {
		case string(trimmed) == "null":
			requested = nil
		case isJSONIntegerLiteral(trimmed):
			n, perr := strconv.ParseInt(string(trimmed), 10, 64)
			if perr != nil || n < math.MinInt32 || n > math.MaxInt32 {
				return gen.PatchCommunicationsConversationsById400JSONResponse(flatErrorBody(
					"invalid_request", "CustomerId must be an integer or null.")), nil
			}
			v := int32(n)
			requested = &v
		default:
			return gen.PatchCommunicationsConversationsById400JSONResponse(flatErrorBody(
				"invalid_request", "CustomerId must be an integer or null.")), nil
		}

		if requested != nil {
			entry, derr := s.deps.Directory.Customer(ctx, *requested)
			if derr != nil {
				return nil, fmt.Errorf("communications: look up customer: %w", derr)
			}
			if entry == nil {
				return gen.PatchCommunicationsConversationsById422JSONResponse(flatErrorBody(
					"customer_invalid", "The selected customer does not exist.")), nil
			}
		}

		customerID = requested
		if requested != nil {
			manual := "manual"
			customerAssociationSource = &manual
		} else {
			customerAssociationSource = nil
		}
		suggestedCustomerID = nil
		suggestedCustomerConfidence = nil
		suggestedCustomerReasoning = nil
	}

	if touchedCustomer {
		if err := q.DeleteConversationCustomerCandidates(ctx, req.Id); err != nil {
			return nil, fmt.Errorf("communications: clear conversation candidates: %w", err)
		}
	}

	updated, err := q.UpdateConversationFields(ctx, store.UpdateConversationFieldsParams{
		Status: finalStatus, AssignedUserID: assignedUserID, CustomerID: customerID,
		CustomerAssociationSource: customerAssociationSource, SuggestedCustomerID: suggestedCustomerID,
		SuggestedCustomerConfidence: suggestedCustomerConfidence, SuggestedCustomerReasoning: suggestedCustomerReasoning,
		ID: req.Id,
	})
	if err != nil {
		return nil, fmt.Errorf("communications: update conversation: %w", err)
	}

	candidateIDs, err := q.ListConversationCandidateIDs(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("communications: list conversation candidates: %w", err)
	}
	if candidateIDs == nil {
		candidateIDs = []int32{}
	}

	return gen.PatchCommunicationsConversationsById200JSONResponse(gen.ConversationPatchResponse{
		Id: updated.ID, Status: updated.Status, AssignedUserId: updated.AssignedUserID,
		CustomerId: updated.CustomerID, CustomerAssociationSource: updated.CustomerAssociationSource,
		SuggestedCustomerId: updated.SuggestedCustomerID, CandidateCustomerIds: candidateIDs,
	}), nil
}

// isJSONIntegerLiteral reports whether b is a bare JSON integer token (an
// optional leading '-' then one or more digits) — the shape
// JsonElement.TryGetInt32 accepts, as opposed to a float literal like "1.0"
// or "1.5", a string, a bool, an object or an array, every one of which
// .NET's TryGetInt32 (or the surrounding ValueKind check) refuses the same
// way this refuses them: falling through to the 400 case.
func isJSONIntegerLiteral(b []byte) bool {
	i := 0
	if len(b) > 0 && b[0] == '-' {
		i = 1
	}
	if i >= len(b) {
		return false
	}
	for ; i < len(b); i++ {
		if b[i] < '0' || b[i] > '9' {
			return false
		}
	}
	return true
}
