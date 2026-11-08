package agents

// Approving a conversation the agent would start (virtual employees design V29,
// V20): the reviewer's send is the agent's start, through the same path a run
// uses. The channel must still be the role's and the address a contact of the
// customer, and S1, S2 and C1 are judged again on what is about to go.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

func (s *server) approveStart(ctx context.Context, row itemRow, reviewer uuid.UUID, edit *replyEdit) (reviewOutcome, *refusal, error) {
	if !s.reviewerMay(ctx, reviewer, opStart) {
		rf := notPermitted()
		return reviewOutcome{}, &rf, nil
	}
	var st storedStart
	if err := json.Unmarshal(row.Item, &st); err != nil {
		return reviewOutcome{}, nil, fmt.Errorf("agents: read the stored start: %w", err)
	}
	ap, gone, err := s.openApproval(ctx, row, reviewer)
	if err != nil || gone != nil {
		if gone != nil {
			return *gone, nil, nil
		}
		return reviewOutcome{}, nil, err
	}
	defer ap.close(ctx)

	if ap.rs.tpl == nil || !ap.rs.tpl.offers(toolStartConversation) || !s2roleHasChannel(ctx, s, ap.role.ID, st.ChannelID) {
		return withdrawn("not_allowed", handGuard, "The role may no longer start conversations on this channel, so nothing was sent."), nil, nil
	}
	if row.CustomerID == nil || !ap.rs.ensureContacts(ctx) || !ap.rs.contacts.includes(st.To, nil) {
		return withdrawn("not_a_contact", handGuard, "The address is no longer a contact of the customer, so nothing was sent."), nil, nil
	}

	agentText := st.Text
	text, edited := st.Text, false
	if edit != nil {
		text, edited = edit.text, edit.text != strings.TrimSpace(st.Text)
	}
	full := withDisclosure(st.Disclosure, text)
	chk := s.contentCheck(ctx, ap.agent.ID, ap.role.Instructions, full)
	dec := decideReply(ruleInput{
		Mode: modeReview, MaxRepliesPerDay: int(ap.role.MaxRepliesPerDay), Score: int(row.Score), CarriesCustomerData: runHasScopedRead(ap.rs.actionLog(ctx)),
		Facts: replyFacts{Recipient: st.To, HasCustomer: true, RecipientIsContact: true}, Check: chk,
	})
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

	res, err := ap.rs.call(ctx, startRequest(st, full, row.CustomerID, "agents-item-"+row.ID.String()))
	if err != nil {
		var oe *operations.Error
		if asOpError(err, &oe) {
			s.warn("agents: an approved start was refused before it was sent", "item", row.ID, "err", err)
			return withdrawn("not_allowed", handGuard, "The agent may no longer start this conversation ("+oe.Code+")."), nil, nil
		}
		s.warn("agents: the outcome of an approved start is unknown", "item", row.ID, "err", err)
		rf := refuse(http.StatusBadGateway, codeSendUnknown, "It is not known whether the conversation was started")
		return reviewOutcome{}, &rf, nil
	}
	ap.log(ctx, opStart, reviewer, res.Status, "approved")
	code := errorCodeOf(res.Body)
	switch {
	case res.Status >= 200 && res.Status < 300:
		out := reviewOutcome{state: itemSent, outcome: "sent"}
		if edited {
			out.state = itemEditedSent
			st.AgentText, st.Text = agentText, text
			out.editRatio = ptr(float32(editRatio(agentText, text)))
			out.item, _ = json.Marshal(st)
		}
		return out, nil, nil
	case res.Status >= 500:
		rf := refuse(http.StatusBadGateway, codeSendUnknown, "It is not known whether the conversation was started")
		return reviewOutcome{}, &rf, nil
	}
	s.warn("agents: an approved start was refused", "item", row.ID, "status", res.Status, "code", code)
	rf := refuse(http.StatusUnprocessableEntity, codeChannelRefused, "The channel refused the message ("+code+"); change it and send again")
	return reviewOutcome{}, &rf, nil
}
