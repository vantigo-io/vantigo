package agents_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The Owner's role: its id is fixed by identity's baseline.
const ownerRoleID = "00000000-0000-4000-8000-000000000002"

// newHarness is an installation with the agents module and an AI provider with
// two models, a local one so the data notice is not needed.
func newHarness(t *testing.T, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(agents.Module()),
		modtest.WithModule(stubKnowledge()),
		modtest.WithEnv("AI_PROVIDER", "openai-compatible"),
		modtest.WithEnv("AI_BASE_URL", "http://ollama:11434/v1"),
		modtest.WithEnv("AI_MODELS", "test-model,other-model"),
		modtest.WithEnv("OWNERS_REQUIRE_MFA", "0"),
	}
	return modtest.New(t, append(base, opts...)...)
}

// stubKnowledge stands in for Knowledge, which a test of this package may not
// compose (module boundaries): it declares the one permission an agent may be
// given a level for here, every permission of identity's and this module's
// being locked off for an agent.
func stubKnowledge() module.Module {
	return module.Module{
		Name: "knowledge",
		Permissions: []contracts.Permission{
			{Key: "knowledge:articles", Display: "Use Knowledge", Description: "Read articles.", Category: "Knowledge"},
			{Key: "knowledge:manage", Display: "Administer Knowledge", Description: "Administer articles.", Category: "Knowledge"},
		},
		Mount: func(module.Deps) (http.Handler, error) { return http.NotFoundHandler(), nil },
	}
}

// owner is a signed-in Owner.
func owner(t *testing.T, h *modtest.Harness) *modtest.Client {
	t.Helper()
	c, id := h.SignInUser(t)
	h.Exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, id, ownerRoleID)
	return c
}

// manager holds agents:view and agents:manage, and nothing of the Owner's.
func manager(t *testing.T, h *modtest.Harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t, "agents:view", "agents:manage")
}

func ifMatch(v any) modtest.RequestOption { return modtest.Header("If-Match", fmt.Sprint(v)) }

type role struct {
	ID               string   `json:"id"`
	AgentID          string   `json:"agentId"`
	Template         string   `json:"template"`
	Name             string   `json:"name"`
	Instructions     string   `json:"instructions"`
	ReplyMode        string   `json:"replyMode"`
	Threshold        int      `json:"threshold"`
	CapsToolCalls    int      `json:"capsToolCalls"`
	CapsTurns        int      `json:"capsTurns"`
	MaxRepliesPerDay int      `json:"maxRepliesPerDay"`
	HandOffUserID    *string  `json:"handOffUserId"`
	HandOffWarning   string   `json:"handOffWarning"`
	ChannelIDs       []string `json:"channelIds"`
	Version          int      `json:"version"`
}

type agent struct {
	ID                string `json:"id"`
	UserID            string `json:"userId"`
	Name              string `json:"name"`
	Style             string `json:"style"`
	Languages         string `json:"languages"`
	DisclosureWording string `json:"disclosureWording"`
	State             string `json:"state"`
	Model             string `json:"model"`
	HasAvatar         bool   `json:"hasAvatar"`
	Roles             []role `json:"roles"`
	Version           int    `json:"version"`
}

type settings struct {
	CompanyName     string   `json:"companyName"`
	RetentionMonths int      `json:"retentionMonths"`
	Version         int      `json:"version"`
	Configured      bool     `json:"configured"`
	Models          []string `json:"models"`
}

func getSettings(t *testing.T, c *modtest.Client) settings {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/agents/settings", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get the settings: %d %s", r.Status, r.Body)
	}
	var s settings
	r.JSON(&s)
	return s
}

// setCompany sets the company's name, which an agent needs before it can exist.
func setCompany(t *testing.T, c *modtest.Client, name string) {
	t.Helper()
	s := getSettings(t, c)
	r := c.Do(http.MethodPut, "/api/v1/agents/settings", map[string]any{"companyName": name, "retentionMonths": s.RetentionMonths}, ifMatch(s.Version))
	if r.Status != http.StatusOK {
		t.Fatalf("set the company's name: %d %s", r.Status, r.Body)
	}
}

func personaBody(name string) map[string]any {
	return map[string]any{"name": name, "style": "Warm and brief.", "languages": "customer", "disclosureWording": "assistant", "model": ""}
}

// createAgent makes an agent as c, who must hold agents:manage; the company's
// name is set first by an Owner when it is not.
func createAgent(t *testing.T, c *modtest.Client, name string) agent {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/agents/agents", personaBody(name))
	if r.Status != http.StatusCreated {
		t.Fatalf("create an agent: %d %s", r.Status, r.Body)
	}
	var a agent
	r.JSON(&a)
	return a
}

func getAgent(t *testing.T, c *modtest.Client, id string) agent {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/agents/agents/"+id, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get the agent: %d %s", r.Status, r.Body)
	}
	var a agent
	r.JSON(&a)
	return a
}

func addRole(t *testing.T, c *modtest.Client, agentID, name string) role {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/agents/agents/"+agentID+"/roles", map[string]any{"template": "front_desk", "name": name, "instructions": ""})
	if r.Status != http.StatusCreated {
		t.Fatalf("add a role: %d %s", r.Status, r.Body)
	}
	var out role
	r.JSON(&out)
	return out
}

// roleBody is a role's update with its current values, for a test to change one
// of.
func roleBody(r role) map[string]any {
	return map[string]any{
		"name": r.Name, "instructions": r.Instructions, "replyMode": r.ReplyMode, "threshold": r.Threshold,
		"capsToolCalls": r.CapsToolCalls, "capsTurns": r.CapsTurns, "maxRepliesPerDay": r.MaxRepliesPerDay,
		"handOffUserId": r.HandOffUserID,
	}
}

func rolePath(a agent, r role) string {
	return "/api/v1/agents/agents/" + a.ID + "/roles/" + r.ID
}

// multipartAvatar is a multipart body with one "avatar" part.
func multipartAvatar(contentType string, data []byte) (string, []byte) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="avatar"; filename="a"`)
	h.Set("Content-Type", contentType)
	part, _ := w.CreatePart(h)
	_, _ = part.Write(data)
	_ = w.Close()
	return w.FormDataContentType(), buf.Bytes()
}

func newID() string { return uuid.NewString() }
