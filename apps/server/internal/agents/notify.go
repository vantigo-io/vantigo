package agents

// The notice of a held item (virtual employees design V20): the role's hand-off
// person is mailed through the user directory when an agent leaves something for
// review. The notice names the agent and says what waits; it holds nothing of the
// conversation, because a mail leaves the application.

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// notifyHeldItem mails the role's hand-off person that an item waits for review.
// A role with none notifies nobody: the people find it in the queue, in the
// badge on the agents tile and in the conversation's note. A failure is logged
// and never stops the run.
func (s *server) notifyHeldItem(ctx context.Context, role store.AgentsRole, agentName string, itemID uuid.UUID, what string) {
	if role.HandOffUserID == nil || s.deps.Users == nil {
		return
	}
	link := ""
	if cfg := s.deps.Config; cfg != nil && cfg.AppOrigin != "" {
		link = cfg.AppOrigin + cfg.BasePath + "/agents/review/" + itemID.String()
	}
	en, nb := what, what
	switch what {
	case "a reply":
		nb = "et svar"
	case "a change":
		nb = "en endring"
	}
	notice := contracts.Notice{Texts: map[string]contracts.NoticeText{
		"en": {Subject: fmt.Sprintf("%s has %s waiting for review", agentName, en), Body: noticeBody(fmt.Sprintf("%s has %s waiting for review.", agentName, en), "Open it in Vantigo:", link)},
		"nb": {Subject: fmt.Sprintf("%s har %s som venter på gjennomgang", agentName, nb), Body: noticeBody(fmt.Sprintf("%s har %s som venter på gjennomgang.", agentName, nb), "Åpne det i Vantigo:", link)},
	}}
	if err := s.deps.Users.Notify(context.WithoutCancel(ctx), *role.HandOffUserID, notice); err != nil {
		s.warn("agents: notify the hand-off person of a held item", "item", itemID, "err", err)
	}
}

func noticeBody(sentence, open, link string) string {
	if link == "" {
		return sentence + "\n"
	}
	return sentence + "\n\n" + open + " " + link + "\n"
}
