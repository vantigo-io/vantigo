package assistant_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

type availability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
	Local     bool   `json:"local"`
}

func getAvailability(t *testing.T, c *modtest.Client) availability {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/assistant/availability", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("availability: %d %s", r.Status, r.Body)
	}
	var a availability
	r.JSON(&a)
	return a
}

// setMode changes the workspace mode as the Owner does.
func setMode(t *testing.T, h *modtest.Harness, o *modtest.Client, mode string) {
	t.Helper()
	version := modtest.One[int32](t, h, `SELECT version FROM assistant.settings`)
	r := o.Do(http.MethodPut, "/api/v1/assistant/settings", map[string]any{"mode": mode}, modtest.Header("If-Match", fmt.Sprint(version)))
	if r.Status != http.StatusOK {
		t.Fatalf("set the mode: %d %s", r.Status, r.Body)
	}
}

func TestAvailability_EveryModeAndSwitch(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel())
	o := owner(t, h)
	alice, aliceID := person(t, h)
	bob, _ := person(t, h)

	// Enabled for all is the default: everyone has it, switches ignored.
	if a := getAvailability(t, alice); !a.Available || a.Reason != "" || !a.Local {
		t.Errorf("default = %+v, want available and local", a)
	}

	// Per person: only people whose own switch is on.
	setMode(t, h, o, "per_person")
	for name, c := range map[string]*modtest.Client{"alice": alice, "bob": bob} {
		if a := getAvailability(t, c); a.Available || a.Reason != "not_enabled" {
			t.Errorf("per person, %s without a switch = %+v, want not_enabled", name, a)
		}
	}
	r := o.Do(http.MethodPut, "/api/v1/assistant/people/"+aliceID.String(), map[string]any{"enabled": true}, modtest.Header("If-Match", "0"))
	if r.Status != http.StatusOK {
		t.Fatalf("switch alice on: %d %s", r.Status, r.Body)
	}
	if a := getAvailability(t, alice); !a.Available {
		t.Errorf("per person, alice switched on = %+v, want available", a)
	}
	if a := getAvailability(t, bob); a.Available {
		t.Errorf("per person, bob = %+v, want not available", a)
	}

	// Disabled for all: nobody, whatever the switches say.
	setMode(t, h, o, "disabled")
	if a := getAvailability(t, alice); a.Available || a.Reason != "disabled" {
		t.Errorf("disabled, alice switched on = %+v, want disabled", a)
	}

	// And back: the switch is ignored in all mode.
	setMode(t, h, o, "all")
	if a := getAvailability(t, bob); !a.Available {
		t.Errorf("all, bob = %+v, want available", a)
	}
}

func TestAvailability_EveryChatEndpointRefusesWhenUnavailable(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel())
	o := owner(t, h)
	c, _ := person(t, h)
	conv := startConversation(t, c)
	setMode(t, h, o, "disabled")

	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/assistant/me", nil},
		{http.MethodPut, "/api/v1/assistant/me", map[string]any{"lastConversationId": nil}},
		{http.MethodGet, "/api/v1/assistant/conversations", nil},
		{http.MethodPost, "/api/v1/assistant/conversations", map[string]any{"origin": map[string]any{"app": "a", "route": "/a"}}},
		{http.MethodGet, "/api/v1/assistant/conversations/" + conv.ID, nil},
		{http.MethodPatch, "/api/v1/assistant/conversations/" + conv.ID, map[string]any{"pinned": true}},
		{http.MethodDelete, "/api/v1/assistant/conversations/" + conv.ID, nil},
		{http.MethodPost, "/api/v1/assistant/conversations/" + conv.ID + "/messages", map[string]any{"text": "hi"}},
		{http.MethodPost, "/api/v1/assistant/runs/" + conv.ID + "/stop", nil},
	} {
		r := c.Do(tc.method, tc.path, tc.body)
		var p struct {
			Code string `json:"code"`
		}
		r.JSON(&p)
		if r.Status != http.StatusForbidden || p.Code != "assistant_unavailable" {
			t.Errorf("%s %s = %d %s, want 403 assistant_unavailable", tc.method, tc.path, r.Status, r.Body)
		}
	}
	// The availability answer itself is never refused.
	if a := getAvailability(t, c); a.Available {
		t.Errorf("availability = %+v", a)
	}
}

func TestAvailability_NoProviderIsNotConfigured(t *testing.T) {
	t.Parallel()
	h := modtest.New(t,
		modtest.WithRecorder(recorder),
		modtest.WithModule(moduleForTests()),
	)
	c, _ := person(t, h)
	if a := getAvailability(t, c); a.Available || a.Reason != "not_configured" || a.Local {
		t.Errorf("availability = %+v, want not_configured", a)
	}
	r := c.Do(http.MethodGet, "/api/v1/assistant/conversations", nil)
	if r.Status != http.StatusForbidden {
		t.Errorf("list = %d, want 403", r.Status)
	}
}

func TestAvailability_APublicProviderIsNotLocal(t *testing.T) {
	t.Parallel()
	h := modtest.New(t,
		modtest.WithRecorder(recorder),
		modtest.WithModule(moduleForTests()),
		modtest.WithEnv("AI_PROVIDER", "openai"),
		modtest.WithEnv("AI_API_KEY", "sk-test"),
		modtest.WithEnv("AI_MODELS", "gpt-4o-mini"),
		modtest.WithTransport(modtest.NewFakeChatModel()),
	)
	c, _ := person(t, h)
	if a := getAvailability(t, c); !a.Available || a.Local {
		t.Errorf("availability = %+v, want available and not local", a)
	}
}
