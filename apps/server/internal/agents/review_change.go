package agents

// Approving a change (virtual employees design V19, V20): a change an agent asks
// for is never made on the model's word; a reviewer makes it, as the agent, with
// their approval on the grant. Before it is made it is pinned again to the run's
// own conversation, checked against the gate as it stands now (a change whose
// operation became never is withdrawn, design §10), and the reviewer must satisfy
// the operation's access rule.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// approveChange makes a change as the agent on the reviewer's authority.
func (s *server) approveChange(ctx context.Context, row itemRow, reviewer uuid.UUID) (reviewOutcome, *refusal, error) {
	var ch storedChange
	if err := json.Unmarshal(row.Item, &ch); err != nil {
		return reviewOutcome{}, nil, fmt.Errorf("agents: read the stored change: %w", err)
	}
	if !s.reviewerMay(ctx, reviewer, ch.OperationID) {
		rf := notPermitted()
		return reviewOutcome{}, &rf, nil
	}
	ap, gone, err := s.openApproval(ctx, row, reviewer)
	if err != nil || gone != nil {
		if gone != nil {
			return *gone, nil, nil
		}
		return reviewOutcome{}, nil, err
	}
	defer ap.close(ctx)

	conv, stop, err := ap.readConversation(ctx)
	if err != nil || stop != nil {
		if stop != nil {
			return *stop, nil, nil
		}
		return reviewOutcome{}, nil, err
	}
	ap.rs.conv = conv
	if conv.AssignedUserID != nil && *conv.AssignedUserID != ap.agent.UserID {
		return withdrawn("conversation_taken", handGuard, "A colleague took the conversation, so the change was not made."), nil, nil
	}

	// The pins again, with the role and the levels as they are now: the run's
	// conversation alone, the status alone on the conversation, and nothing the
	// module's own tools write.
	params := map[string]any{}
	if m := objectOf(ch.PathParams); m != nil {
		params = *m
	}
	if slices.Contains(reservedOperations, ch.OperationID) || ap.rs.tpl == nil {
		return withdrawn("not_allowed", handGuard, "The change is not one an agent may make: "+ch.Summary), nil, nil
	}
	if _, ok := ap.rs.allowed(ch.OperationID); !ok {
		return withdrawn("not_allowed", handGuard, "The role may no longer make this change: "+ch.Summary), nil, nil
	}
	if refused := ap.rs.pin(ch.OperationID, params, ch.Body); refused != nil {
		return withdrawn("not_allowed", handGuard, "The change reaches beyond the conversation, so it was not made: "+ch.Summary), nil, nil
	}
	if op, _ := ap.rs.allowed(ch.OperationID); op.OwnConversation {
		params["id"] = row.ConversationID.String()
	}
	if mode, ok := s.deps.Operations.Effective(ap.rs.withGrant(ctx), ch.OperationID); !ok || mode == operations.ModeNever {
		return withdrawn("not_allowed", handGuard, "The agent may no longer make this change: "+ch.Summary), nil, nil
	}

	req := operations.CallRequest{OperationID: ch.OperationID, PathParams: params, Body: ch.Body}
	if q := objectOf(ch.Query); q != nil {
		req.Query = *q
	}
	if len(req.Body) == 0 || string(req.Body) == "null" {
		req.Body = nil
	}
	if !ap.rs.reviewerStillCurrent(ctx, row) {
		rf := itemDecided()
		return reviewOutcome{}, &rf, nil
	}
	res, err := ap.rs.call(ctx, req)
	if err != nil {
		var oe *operations.Error
		if asOpError(err, &oe) {
			s.warn("agents: an approved change was refused before it was made", "item", row.ID, "err", err)
			return withdrawn("not_allowed", handGuard, "The agent may no longer make this change ("+oe.Code+"): "+ch.Summary), nil, nil
		}
		return unknownChange(ch), nil, nil
	}
	ap.log(ctx, ch.OperationID, reviewer, res.Status, "approved")
	switch {
	case res.Status >= 200 && res.Status < 300:
		return reviewOutcome{state: itemSent, outcome: fmt.Sprintf("done %d", res.Status)}, nil, nil
	case res.Status >= 500:
		return unknownChange(ch), nil, nil
	}
	rf := refuse(http.StatusUnprocessableEntity, codeChangeRefused, fmt.Sprintf("The change was refused (%d, %s); discard it or change the record first", res.Status, errorCodeOf(res.Body)))
	return reviewOutcome{}, &rf, nil
}

// unknownChange is the outcome of a change that may or may not have been made. A
// change is not idempotent, so it is not offered again: it is withdrawn and a
// colleague is told to look.
func unknownChange(ch storedChange) reviewOutcome {
	return withdrawn("unknown", handUnknownSend, "The change may or may not have been made: the call did not answer. Check the record before making it again: "+ch.Summary)
}
