package customers_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// fakeAgentMemory is the agents module's contracts.AgentMemory as a test shapes
// it (virtual employees design V27): the same entries for every customer, and
// the ids it was asked for.
type fakeAgentMemory struct {
	entries []contracts.AgentMemoryEntry
	asked   []int32
}

func (f *fakeAgentMemory) AboutCustomer(_ context.Context, id int32) ([]contracts.AgentMemoryEntry, error) {
	f.asked = append(f.asked, id)
	return f.entries, nil
}

type agentMemoryJSON struct {
	Agents []struct {
		AgentId   string `json:"agentId"`
		AgentName string `json:"agentName"`
		Notes     []struct {
			Id        string    `json:"id"`
			Text      string    `json:"text"`
			CreatedAt time.Time `json:"createdAt"`
		} `json:"notes"`
	} `json:"agents"`
	CanDelete bool `json:"canDelete"`
}

// TestGetCustomersByIdAgentMemory_ShowsWhatTheAgentsRememberToThoseWhoMaySeeIt: the
// notes cross as the contract gave them for a caller with customers:view and
// agents:view, and canDelete says agents:manage; a caller without agents:view is
// answered none and the contract is not asked, and one without customers:view is
// refused; an unknown customer is 404;
// and without the agents module (Deps.AgentMemory nil) nothing is remembered.
func TestGetCustomersByIdAgentMemory_ShowsWhatTheAgentsRememberToThoseWhoMaySeeIt(t *testing.T) {
	t.Parallel()
	agent, note := uuid.New(), uuid.New()
	at := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	fake := &fakeAgentMemory{entries: []contracts.AgentMemoryEntry{{
		AgentID: agent, AgentName: "Hanna", Notes: []contracts.AgentMemoryNote{{ID: note, Text: "Prefers e-mail", CreatedAt: at}},
	}}}
	h := newHarness(t, modtest.WithAgentMemory(fake))
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Nordlys AS").Id
	path := fmt.Sprintf("/api/v1/customers/%d/agent-memory", customer)

	viewer := h.SignIn(t, "customers:view", "agents:view")
	r := viewer.Do(http.MethodGet, path, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET: status %d body %s", r.Status, r.Body)
	}
	var got agentMemoryJSON
	r.JSON(&got)
	if len(got.Agents) != 1 || got.Agents[0].AgentName != "Hanna" || len(got.Agents[0].Notes) != 1 ||
		got.Agents[0].Notes[0].Id != note.String() || got.Agents[0].Notes[0].Text != "Prefers e-mail" || got.CanDelete {
		t.Errorf("answer = %+v, want Hanna's note, not deletable by a viewer", got)
	}
	if len(fake.asked) != 1 || fake.asked[0] != customer {
		t.Errorf("the contract was asked for %v, want the customer once", fake.asked)
	}

	manager := h.SignIn(t, "customers:view", "agents:view", "agents:manage")
	if r := manager.Do(http.MethodGet, path, nil); r.Status != http.StatusOK {
		t.Fatalf("manager GET: %d", r.Status)
	} else {
		r.JSON(&got)
		if !got.CanDelete {
			t.Error("canDelete = false for agents:manage, want true")
		}
	}

	asked := len(fake.asked)
	if r := h.SignIn(t, "agents:view").Do(http.MethodGet, path, nil); r.Status != http.StatusForbidden {
		t.Errorf("agents:view alone: status %d, want 403", r.Status)
	}
	// Without agents:view the page is as it was: an empty list, and the contract is not asked.
	r = h.SignIn(t, "customers:view").Do(http.MethodGet, path, nil)
	var plain agentMemoryJSON
	r.JSON(&plain)
	if r.Status != http.StatusOK || len(plain.Agents) != 0 || plain.CanDelete {
		t.Errorf("customers:view alone: %d %+v, want an empty list", r.Status, plain)
	}
	if len(fake.asked) != asked {
		t.Error("the contract was asked for a caller who may not see the notes")
	}
	if r := viewer.Do(http.MethodGet, "/api/v1/customers/987654/agent-memory", nil); r.Status != http.StatusNotFound {
		t.Errorf("unknown customer: status %d, want 404", r.Status)
	}

	// Without the agents module there is nothing remembered, and the page is as it was.
	bare := newHarness(t)
	bc := authenticatedClient(t, bare)
	bcustomer := createCustomer(t, bc, "Bare AS").Id
	r = bare.SignIn(t, "customers:view", "agents:view").Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/agent-memory", bcustomer), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET without the slot: %d %s", r.Status, r.Body)
	}
	var none agentMemoryJSON
	r.JSON(&none)
	if len(none.Agents) != 0 {
		t.Errorf("without the agents module: %+v, want none", none)
	}
}
