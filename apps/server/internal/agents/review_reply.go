package agents

// Approving a reply (virtual employees design V20, V22): the reviewer's send is
// the agent's reply, through the same outbound path a run uses. The stored full
// text goes out with its disclosure, or the reviewer's text with the same
// disclosure above it; the pins, S1, S2, S6 and C1 are judged again on what is
// about to be sent, and Communications refuses a conversation a person took or
// that has a newer message (conversation_taken, conversation_moved). The send is
// idempotent per agent, conversation and answered message, so a second approval,
// a retry after an unknown outcome and the run's own send are one message.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// replyEdit is what a reviewer wrote in place of the agent's text.
type replyEdit struct {
	text              string
	components        json.RawMessage
	replaceComponents bool
}

// withdrawn is the outcome of an item that cannot be sent any more: the
// conversation is handed to the people, with the reason.
func withdrawn(outcome, code, summary string) reviewOutcome {
	return reviewOutcome{state: itemWithdrawn, outcome: outcome, handOff: &handOffRequest{code: code, summary: summary}}
}

// approvalCtx is what an approval of one item works with: the agent, its role,
// the grant that carries the reviewer's approval and the run it stands for.
type approvalCtx struct {
	s     *server
	row   itemRow
	agent store.AgentsAgent
	role  store.AgentsRole
	grant uuid.UUID
	rs    *runState
}

// openApproval reads the agent and its role and issues the grant of the
// approval: the agent as itself, in the item's run and for the run's customer,
// with the reviewer as approved_by (V6). ok is false, with the outcome, when the
// agent or its role is gone.
func (s *server) openApproval(ctx context.Context, row itemRow, reviewer uuid.UUID) (a *approvalCtx, gone *reviewOutcome, err error) {
	q := store.New(s.deps.Pool)
	agent, err := q.GetAgentByID(ctx, row.AgentID)
	if err != nil {
		return nil, nil, fmt.Errorf("agents: read the agent: %w", err)
	}
	role, err := q.GetRoleByID(ctx, row.RoleID)
	if isNoRows(err) {
		o := withdrawn("role_gone", handGuard, "The role that wrote this is gone, so nothing was sent.")
		return nil, &o, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("agents: read the role: %w", err)
	}
	if agent.State == stateDeleted {
		o := withdrawn("agent_gone", handGuard, "The agent that wrote this is deleted, so nothing was sent.")
		return nil, &o, nil
	}
	grant, err := s.deps.AgentGrants.Issue(ctx, contracts.IssueAgentGrant{
		AgentUserID: agent.UserID, AgentID: agent.ID, RunID: row.RunID, CustomerID: row.CustomerID, ApprovedBy: &reviewer,
	})
	if errors.Is(err, contracts.ErrAgentInactive) || errors.Is(err, contracts.ErrNotAgent) {
		o := withdrawn("agent_inactive", handGuard, "The agent cannot act any more, so nothing was sent.")
		return nil, &o, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("agents: issue the approval's grant: %w", err)
	}
	rs := &runState{
		s: s, run: store.AgentsRun{ID: row.RunID, AgentID: row.AgentID, RoleID: row.RoleID, ConversationID: row.ConversationID},
		agent: agent, role: role, tpl: templates[role.Template], grant: grant, customerID: row.CustomerID,
	}
	return &approvalCtx{s: s, row: row, agent: agent, role: role, grant: grant, rs: rs}, nil, nil
}

func (a *approvalCtx) close(ctx context.Context) {
	_ = a.s.deps.AgentGrants.Revoke(context.WithoutCancel(ctx), a.grant)
}

// reads the conversation as the agent, now. A conversation the agent can no
// longer read is one it may not answer.
func (a *approvalCtx) readConversation(ctx context.Context) (conversation, *reviewOutcome, error) {
	res, err := a.s.callAs(ctx, a.grant, operations.CallRequest{OperationID: opGetConversation, PathParams: a.rs.own()})
	if err != nil {
		var oe *operations.Error
		if asOpError(err, &oe) {
			o := withdrawn("conversation_unreadable", handGuard, "The agent may no longer read this conversation, so nothing was sent.")
			return conversation{}, &o, nil
		}
		return conversation{}, nil, fmt.Errorf("agents: read the conversation: %w", err)
	}
	switch {
	case res.Status == http.StatusNotFound:
		o := withdrawn("conversation_gone", handGuard, "The conversation is gone, so nothing was sent.")
		return conversation{}, &o, nil
	case res.Status != http.StatusOK:
		return conversation{}, nil, fmt.Errorf("agents: read the conversation: status %d", res.Status)
	}
	c, err := parseConversation(res.Body)
	if err != nil {
		return conversation{}, nil, err
	}
	a.s.noteLinks(ctx, c)
	return c, nil, nil
}

// blocking are the rules that stop an approval. S1 and S2 stop it only when
// they are new: a rule that already held the item is what the reviewer was
// shown and decides on, and the text does not change what S1 and S2 judge (the
// recipient and what the run read). C1 always stops it, and so does the check's
// own failure when the text is the reviewer's or the check had answered before.
func blocking(held, previously []string, edited bool) []string {
	var out []string
	for _, code := range held {
		switch code {
		case ruleS1, ruleS2:
			if !slices.Contains(previously, code) {
				out = append(out, code)
			}
		case ruleC1:
			out = append(out, code)
		case ruleCheck:
			if edited || !slices.Contains(previously, ruleCheck) {
				out = append(out, code)
			}
		}
	}
	return out
}

// approveReply sends a reply as the agent on the reviewer's authority.
func (s *server) approveReply(ctx context.Context, row itemRow, reviewer uuid.UUID, edit *replyEdit) (reviewOutcome, *refusal, error) {
	// The reviewer must be able to send this reply themselves (V20).
	if !s.reviewerMay(ctx, reviewer, opReply) {
		rf := notPermitted()
		return reviewOutcome{}, &rf, nil
	}
	var stored storedReply
	if err := json.Unmarshal(row.Item, &stored); err != nil {
		return reviewOutcome{}, nil, fmt.Errorf("agents: read the stored reply: %w", err)
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

	// A person took the conversation, or a newer message arrived: nothing is sent
	// (design §10). A newer message gets a run of its own.
	if conv.AssignedUserID != nil && *conv.AssignedUserID != ap.agent.UserID {
		return withdrawn("conversation_taken", handGuard, "A colleague took the conversation, so the reply was not sent."), nil, nil
	}
	latest := conv.latestInbound()
	if latest == nil || (stored.InReplyTo != uuid.Nil && latest.ID != stored.InReplyTo) {
		return reviewOutcome{state: itemWithdrawn, outcome: "conversation_moved", rerun: true}, nil, nil
	}
	answered := latest.ID

	// What is sent: the agent's text, or the reviewer's, with the disclosure.
	agentText := stored.Text
	text, components, edited := stored.Text, stored.Components, false
	if edit != nil {
		text, edited = edit.text, edit.text != strings.TrimSpace(stored.Text)
		if edit.replaceComponents {
			components, edited = edit.components, true
		}
	}
	if len(components) == 0 || string(components) == "null" {
		components = nil
	}
	if components != nil && (conv.Capabilities == nil || !conv.Capabilities.Components) {
		rf := refuse(http.StatusUnprocessableEntity, codeChannelRefused, "This channel takes no answer options or cards")
		return reviewOutcome{}, &rf, nil
	}
	full := withDisclosure(stored.Disclosure, text)
	if limit := conv.maxText(); limit > 0 && utf8.RuneCountInString(full) > limit {
		rf := refuse(http.StatusUnprocessableEntity, codeChannelRefused, "The message is longer than this channel takes, with the opening line the system adds")
		return reviewOutcome{}, &rf, nil
	}

	// S6, and S1, S2 and C1 on what is about to go (V18, V22).
	log := ap.rs.actionLog(ctx)
	scoped := runHasScopedRead(log)
	facts := replyFacts{Recipient: latest.Participant.Address, HasCustomer: row.CustomerID != nil, WindowState: conv.windowState()}
	if scoped {
		facts.RecipientIsContact = ap.rs.recipientIsContact(ctx)
	}
	chk := s.contentCheck(ctx, ap.agent.ID, ap.role.Instructions, full)
	dec := decideReply(ruleInput{
		Mode: modeReview, MaxRepliesPerDay: int(ap.role.MaxRepliesPerDay), Score: int(row.Score), CarriesCustomerData: scoped, Facts: facts, Check: chk,
	})
	if len(dec.Refused) > 0 {
		return withdrawn("window_closed", handGuard, "The reply window is closed, so the reply was not sent."), nil, nil
	}
	if stop := blocking(dec.Held, row.HeldBy, edited); len(stop) > 0 {
		now := append(slices.Clone(row.HeldBy), stop...)
		slices.Sort(now)
		now = slices.Compact(now)
		if err := store.New(s.deps.Pool).HoldReviewItem(ctx, store.HoldReviewItemParams{ID: row.ID, HeldBy: now}); err != nil {
			return reviewOutcome{}, nil, fmt.Errorf("agents: hold the item: %w", err)
		}
		rf := held(stop)
		return reviewOutcome{}, &rf, nil
	}

	// The send.
	res, err := ap.rs.call(ctx, replyRequest(ap.agent.ID, row.ConversationID, answered, full, components))
	if err != nil {
		var oe *operations.Error
		if asOpError(err, &oe) {
			// The gate or the contract refuses before any dispatch: the agent may no
			// longer reply here, as its levels were lowered since.
			s.warn("agents: an approved reply was refused before it was sent", "item", row.ID, "err", err)
			return withdrawn("not_allowed", handGuard, "The agent may no longer send this reply ("+oe.Code+")."), nil, nil
		}
		s.warn("agents: the outcome of an approved reply is unknown", "item", row.ID, "err", err)
		rf := refuse(http.StatusBadGateway, codeSendUnknown, "It is not known whether the reply was sent")
		return reviewOutcome{}, &rf, nil
	}
	ap.log(ctx, opReply, reviewer, res.Status, "approved")
	code := errorCodeOf(res.Body)
	switch {
	case res.Status >= 200 && res.Status < 300:
		out := reviewOutcome{state: itemSent, outcome: "sent"}
		if edited {
			out.state = itemEditedSent
			stored.AgentText = agentText
			stored.Text = text
			out.editRatio = ptr(float32(editRatio(agentText, text)))
			stored.Components = components
			out.item, _ = json.Marshal(stored)
		}
		return out, nil, nil
	case res.Status == http.StatusConflict && code == "conversation_taken":
		return withdrawn("conversation_taken", handGuard, "A colleague took the conversation, so the reply was not sent."), nil, nil
	case res.Status == http.StatusConflict && code == "conversation_moved":
		return reviewOutcome{state: itemWithdrawn, outcome: "conversation_moved", rerun: true}, nil, nil
	case res.Status == http.StatusUnprocessableEntity && code == "reply_window_closed":
		return withdrawn("window_closed", handGuard, "The reply window closed before the reply was sent."), nil, nil
	case res.Status == http.StatusConflict && code == "idempotency_key_reused":
		// A reply to this message was queued earlier, with other text (a send that failed
		// after the text was stored, or another reviewer's). If the agent's reply is in the
		// conversation, the item is done; if not, say so plainly instead of leaving it waiting.
		again, stop, rerr := ap.readConversation(ctx)
		if rerr != nil || stop != nil {
			if stop != nil {
				return *stop, nil, nil
			}
			return reviewOutcome{}, nil, rerr
		}
		if again.agentRepliedSince(ap.agent.UserID, latest.OccurredAt) {
			return reviewOutcome{state: itemSent, outcome: "already_sent"}, nil, nil
		}
		rf := refuse(http.StatusConflict, codeKeyReused, "A different reply to this message was queued before and is not in the conversation")
		return reviewOutcome{}, &rf, nil
	case res.Status >= 500:
		rf := refuse(http.StatusBadGateway, codeSendUnknown, "It is not known whether the reply was sent")
		return reviewOutcome{}, &rf, nil
	}
	s.warn("agents: an approved reply was refused", "item", row.ID, "status", res.Status, "code", code)
	rf := refuse(http.StatusUnprocessableEntity, codeChannelRefused, "The channel refused the message ("+code+"); change it and send again")
	return reviewOutcome{}, &rf, nil
}

// log writes the approved call in the run's action log (V25), with the reviewer.
func (a *approvalCtx) log(ctx context.Context, operation string, reviewer uuid.UUID, status int, mode string) {
	st := "ok"
	if status < 200 || status >= 300 {
		st = "refused"
	}
	code := int32(status)
	if err := store.New(a.s.deps.Pool).InsertAction(context.WithoutCancel(ctx), store.InsertActionParams{
		RunID: a.row.RunID, AgentID: a.agent.ID, CustomerID: a.row.CustomerID, OperationID: operation, Ref: operation,
		Mode: mode, Status: st, HttpStatus: &code, ApprovedBy: &reviewer, Now: a.s.deps.Clock(),
	}); err != nil {
		a.s.warn("agents: log an approved call", "item", a.row.ID, "err", err)
	}
}

// reviewerMay says the reviewer satisfies the access rule of the operation an
// item would call, keys and policies (V20). An operation the dispatcher does not
// know, or a rule it cannot read, is no.
func (s *server) reviewerMay(ctx context.Context, reviewer uuid.UUID, operationID string) bool {
	rule, ok := s.deps.Operations.AccessRule(operationID)
	if !ok {
		return false
	}
	met, err := s.deps.Access.UserSatisfies(ctx, reviewer, rule)
	if err != nil {
		s.warn("agents: check what a reviewer may do", "operation", operationID, "err", err)
		return false
	}
	return met
}

// editRatio is how much of the agent's text the reviewer changed, 0 to 1:
// the character-level edit distance over the longer of the two. Both are cut at
// 4,000 characters, which is far beyond a message a channel takes.
func editRatio(before, after string) float64 {
	a, b := []rune(before), []rune(after)
	const limit = 4000
	if len(a) > limit {
		a = a[:limit]
	}
	if len(b) > limit {
		b = b[:limit]
	}
	longest := max(len(a), len(b))
	if longest == 0 {
		return 0
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return float64(prev[len(b)]) / float64(longest)
}
