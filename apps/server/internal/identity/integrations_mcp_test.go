package identity_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// An integration's MCP setting (MCP server design M1-M4, M17): what its keys
// may do at /api/mcp, changed by an Owner under If-Match, audited, and read
// by the endpoint through contracts.MCPAccess.

// mcpDetail is what the setting's tests read of IntegrationDetail.
type mcpDetail struct {
	Integration struct {
		Version string `json:"version"`
	} `json:"integration"`
	MCPAccess    string `json:"mcpAccess"`
	MCPAvailable bool   `json:"mcpAvailable"`
}

func TestIntegrations_TheMCPSetting(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	id := createIntegration(t, owner, "Agent").Integration.ID
	path := integrationsPath + "/" + id.String()
	mcpPath := path + "/mcp"

	var d mcpDetail
	r := owner.do(http.MethodGet, path, nil)
	r.json(&d)
	if d.MCPAccess != "off" || !d.MCPAvailable {
		t.Fatalf("a new integration: %+v, want off and available", d)
	}

	if r := owner.do(http.MethodPut, mcpPath, map[string]any{"mcpAccess": "read"}, header("If-Match", "not-a-version")); r.status != http.StatusBadRequest {
		t.Errorf("a bad If-Match: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPut, mcpPath, map[string]any{"mcpAccess": "read"}, header("If-Match", uuid.NewString())); r.status != http.StatusPreconditionFailed || r.code() != "version_mismatch" {
		t.Errorf("a stale If-Match: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPut, mcpPath, map[string]any{"mcpAccess": "everything"}, header("If-Match", d.Integration.Version),
		skipContract("the value is outside the enum on purpose")); r.status != http.StatusBadRequest || r.code() != "invalid_request" {
		t.Errorf("an unknown mode: %d %s", r.status, r.body)
	}

	before := d.Integration.Version
	r = owner.do(http.MethodPut, mcpPath, map[string]any{"mcpAccess": "read"}, header("If-Match", `"`+before+`"`))
	if r.status != http.StatusOK {
		t.Fatalf("set read: %d %s", r.status, r.body)
	}
	r.json(&d)
	if d.MCPAccess != "read" || d.Integration.Version == before {
		t.Errorf("after read: %+v, want read and a new version", d)
	}
	events := h.auditEvents(t, "integration.mcp_changed")
	if len(events) != 1 || events[0].Actor == nil || *events[0].Actor != ownerID || events[0].TargetUser == nil || *events[0].TargetUser != id ||
		events[0].Before != `{"mcpAccess":"off"}` || events[0].After != `{"mcpAccess":"read"}` {
		t.Errorf("audit %+v", events)
	}
	// The same value again changes nothing and audits nothing.
	version := d.Integration.Version
	r = owner.do(http.MethodPut, mcpPath, map[string]any{"mcpAccess": "read"}, header("If-Match", version))
	r.json(&d)
	if r.status != http.StatusOK || d.Integration.Version != version || len(h.auditEvents(t, "integration.mcp_changed")) != 1 {
		t.Errorf("the same value: %d %+v", r.status, d)
	}

	// The endpoint reads the setting and the key's scope together; a
	// read-only key under read_write is read.
	_, secret := createKey(t, owner, id, map[string]any{"readOnly": true})
	r = owner.do(http.MethodPut, mcpPath, map[string]any{"mcpAccess": "read_write"}, header("If-Match", version))
	r.json(&d)
	p, err := h.access.Check(keyRequest(http.MethodGet, secret), contracts.Rule{Kind: contracts.RuleAnonymous, API: contracts.APIRead})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	m, err := h.access.MCP(context.Background(), p)
	if err != nil || m.Mode != "read_write" || !m.ReadOnly || m.Effective() != "read" {
		t.Errorf("MCP: %+v %v", m, err)
	}

	// No one but an Owner, and no key, changes it.
	person := h.createUser(t, owner, "person@example.test", identity.RoleUser)
	if r := h.signIn(t, person, false).do(http.MethodPut, mcpPath, map[string]any{"mcpAccess": "off"}, header("If-Match", d.Integration.Version)); r.status != http.StatusForbidden {
		t.Errorf("a person: %d %s", r.status, r.body)
	}
	if r := h.client(t).do(http.MethodPut, mcpPath, map[string]any{"mcpAccess": "off"}, header("If-Match", d.Integration.Version), header("Authorization", "Bearer "+secret)); r.status != http.StatusForbidden || r.code() != "api_not_available" {
		t.Errorf("a key: %d %s", r.status, r.body)
	}

	if r := owner.do(http.MethodPut, integrationsPath+"/"+uuid.NewString()+"/mcp", map[string]any{"mcpAccess": "off"}, header("If-Match", d.Integration.Version)); r.status != http.StatusNotFound {
		t.Errorf("an unknown integration: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPost, path+"/archive", nil); r.status != http.StatusOK {
		t.Fatalf("archive: %d", r.status)
	}
	r = owner.do(http.MethodGet, path, nil)
	r.json(&d)
	if r := owner.do(http.MethodPut, mcpPath, map[string]any{"mcpAccess": "off"}, header("If-Match", d.Integration.Version)); r.status != http.StatusConflict || r.code() != "integration_archived" {
		t.Errorf("archived: %d %s", r.status, r.body)
	}
}

// MCP_ENABLED=0 says so on the integration (M4, M21).
func TestIntegrations_MCPSwitchedOffForTheInstallation(t *testing.T) {
	t.Parallel()
	_, owner, _ := rbacHarness(t, withEnv("MCP_ENABLED", "0"))
	id := createIntegration(t, owner, "Agent").Integration.ID
	var d mcpDetail
	owner.do(http.MethodGet, integrationsPath+"/"+id.String(), nil).json(&d)
	if d.MCPAvailable || d.MCPAccess != "off" {
		t.Errorf("%+v, want unavailable", d)
	}
}
