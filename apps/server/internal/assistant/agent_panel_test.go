package assistant_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The picker and the naming of a conversation with a virtual employee (virtual
// employees design V31) against a fake of the agents' side. What the double check
// does to the operations is proved in internal/integration, where the agents
// module and the others are composed.

type fakeColleagues struct {
	agents map[uuid.UUID]contracts.ColleagueAgent
	paused map[uuid.UUID]bool
}

func (f *fakeColleagues) Agents(context.Context) ([]contracts.ColleagueAgent, error) {
	out := []contracts.ColleagueAgent{}
	for id, a := range f.agents {
		if !f.paused[id] {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeColleagues) Agent(_ context.Context, id uuid.UUID) (contracts.ColleagueAgent, error) {
	a, ok := f.agents[id]
	if !ok || f.paused[id] {
		return contracts.ColleagueAgent{}, contracts.ErrColleagueUnavailable
	}
	return a, nil
}

func (f *fakeColleagues) Names(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := map[uuid.UUID]string{}
	for _, id := range ids {
		if a, ok := f.agents[id]; ok {
			out[id] = a.Name
		}
	}
	return out, nil
}

func (f *fakeColleagues) Operations(_ context.Context, id uuid.UUID) (map[string]contracts.ColleagueOperation, error) {
	if _, ok := f.agents[id]; !ok || f.paused[id] {
		return nil, contracts.ErrColleagueUnavailable
	}
	return map[string]contracts.ColleagueOperation{}, nil
}

func TestAgentPicker_ListsTheActiveAgentsToAHolderOfAgentsViewAndNoOneElse(t *testing.T) {
	t.Parallel()
	hanna := contracts.ColleagueAgent{ID: uuid.New(), UserID: uuid.New(), Name: "Hanna"}
	fake := &fakeColleagues{agents: map[uuid.UUID]contracts.ColleagueAgent{hanna.ID: hanna}, paused: map[uuid.UUID]bool{}}
	h := newHarness(t, modtest.NewFakeChatModel(), modtest.WithAgentColleagues(fake))

	type list struct {
		Items []struct {
			ID   uuid.UUID `json:"id"`
			Name string    `json:"name"`
		} `json:"items"`
	}
	viewer, _ := h.SignInUser(t, "agents:view")
	var got list
	r := viewer.Do(http.MethodGet, "/api/v1/assistant/agents", nil)
	r.JSON(&got)
	if r.Status != http.StatusOK || len(got.Items) != 1 || got.Items[0].ID != hanna.ID || got.Items[0].Name != "Hanna" {
		t.Fatalf("the picker for agents:view = %d %s, want Hanna", r.Status, r.Body)
	}

	plain, _ := person(t, h)
	got = list{}
	r = plain.Do(http.MethodGet, "/api/v1/assistant/agents", nil)
	r.JSON(&got)
	if r.Status != http.StatusOK || len(got.Items) != 0 {
		t.Errorf("the picker without agents:view = %d %s, want an empty list", r.Status, r.Body)
	}

	fake.paused[hanna.ID] = true
	got = list{}
	viewer.Do(http.MethodGet, "/api/v1/assistant/agents", nil).JSON(&got)
	if len(got.Items) != 0 {
		t.Errorf("the picker offers a paused agent: %+v", got.Items)
	}
}

func TestAgentConversation_NeedsAgentsViewAndAnActiveAgentAndNamesIt(t *testing.T) {
	t.Parallel()
	hanna := contracts.ColleagueAgent{ID: uuid.New(), UserID: uuid.New(), Name: "Hanna"}
	fake := &fakeColleagues{agents: map[uuid.UUID]contracts.ColleagueAgent{hanna.ID: hanna}, paused: map[uuid.UUID]bool{}}
	h := newHarness(t, modtest.NewFakeChatModel(), modtest.WithAgentColleagues(fake))
	origin := map[string]any{"app": "agents", "route": "/agents"}

	plain, _ := person(t, h)
	if r := plain.Do(http.MethodPost, "/api/v1/assistant/conversations", map[string]any{"origin": origin, "agentId": hanna.ID}); r.Status != http.StatusForbidden {
		t.Errorf("a conversation with an agent without agents:view = %d %s, want 403", r.Status, r.Body)
	}

	viewer, _ := h.SignInUser(t, "agents:view")
	if r := viewer.Do(http.MethodPost, "/api/v1/assistant/conversations", map[string]any{"origin": origin, "agentId": uuid.New()}); r.Status != http.StatusNotFound {
		t.Errorf("a conversation with an unknown agent = %d %s, want 404", r.Status, r.Body)
	}
	r := viewer.Do(http.MethodPost, "/api/v1/assistant/conversations", map[string]any{"origin": origin, "agentId": hanna.ID})
	if r.Status != http.StatusCreated {
		t.Fatalf("start a conversation with the agent: %d %s", r.Status, r.Body)
	}
	var conv struct {
		ID    string `json:"id"`
		Agent *struct {
			ID   uuid.UUID `json:"id"`
			Name string    `json:"name"`
		} `json:"agent"`
	}
	r.JSON(&conv)
	if conv.Agent == nil || conv.Agent.ID != hanna.ID || conv.Agent.Name != "Hanna" {
		t.Fatalf("agent = %+v, want Hanna", conv.Agent)
	}

	// The list and the detail name it, also after the agent was paused; the Assistant's own do not.
	own := startConversation(t, viewer)
	fake.paused[hanna.ID] = true
	var list struct {
		Items []struct {
			ID    string `json:"id"`
			Agent *struct {
				Name string `json:"name"`
			} `json:"agent"`
		} `json:"items"`
	}
	viewer.Do(http.MethodGet, "/api/v1/assistant/conversations", nil).JSON(&list)
	named := map[string]string{}
	for _, c := range list.Items {
		if c.Agent != nil {
			named[c.ID] = c.Agent.Name
		}
	}
	if named[conv.ID] != "Hanna" || len(named) != 1 {
		t.Errorf("agents named in the list = %v, want only %s: Hanna (own %s)", named, conv.ID, own.ID)
	}

	// A message to a conversation whose agent is paused is a conflict, and so is one from a person who lost agents:view.
	if r := ask(t, viewer, conv.ID, "Hello?"); r.Status != http.StatusConflict {
		t.Errorf("a message to a paused agent = %d %s, want 409", r.Status, r.Body)
	}
}
