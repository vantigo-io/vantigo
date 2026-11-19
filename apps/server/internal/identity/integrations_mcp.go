package identity

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// An integration's MCP setting (MCP server design M1-M3, M17): what its keys
// may do at the MCP endpoint, which module.apimcp reads through
// contracts.MCPAccess.

// auditIntegrationMCPChanged is the audit action of a change of the setting.
const auditIntegrationMCPChanged = "integration.mcp_changed"

const integrationMCPMessage = "The MCP access is off, read or read_write."

// mcpAudit is the setting as the audit trail records it, before and after.
type mcpAudit struct {
	MCPAccess string `json:"mcpAccess"`
}

// PutIdentityIntegrationsByIdMcp sets the integration's MCP access under
// If-Match, audited integration.mcp_changed; the same value again is answered
// as it is, with nothing written.
// (PUT /api/v1/identity/integrations/{id}/mcp)
func (s *server) PutIdentityIntegrationsByIdMcp(ctx context.Context, req gen.PutIdentityIntegrationsByIdMcpRequestObject) (gen.PutIdentityIntegrationsByIdMcpResponseObject, error) {
	version, ok := parseIntegrationVersion(req.Params.IfMatch)
	if !ok {
		return refuse(http.StatusBadRequest, "invalid_request", integrationIfMatchMessage,
			map[string][]string{"If-Match": {integrationIfMatchMessage}}), nil
	}
	if req.Body == nil || !req.Body.McpAccess.Valid() {
		return refuse(http.StatusBadRequest, "invalid_request", invalidRequestMessage,
			map[string][]string{"mcpAccess": {integrationMCPMessage}}), nil
	}
	mode := string(req.Body.McpAccess)
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return nil, err
	}
	err = s.integrationTx(ctx, req.Id, false, func(q *store.Queries, row store.LockIntegrationRow, now time.Time) error {
		if row.Version != version {
			return integrationVersionMismatch
		}
		if row.McpAccess == mode {
			return nil
		}
		if err := q.SetIntegrationMCPAccess(ctx, store.SetIntegrationMCPAccessParams{UserID: req.Id, McpAccess: mode, Version: uuid.New()}); err != nil {
			return fmt.Errorf("identity: change the integration's MCP access: %w", err)
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &req.Id, action: auditIntegrationMCPChanged,
			before: mcpAudit{MCPAccess: row.McpAccess}, after: mcpAudit{MCPAccess: mode}, mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.PutIdentityIntegrationsByIdMcpResponseObject](err)
	}
	detail, err := s.answerDetail(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.PutIdentityIntegrationsByIdMcp200JSONResponse(detail), nil
}

func (r refusal) VisitPutIdentityIntegrationsByIdMcpResponse(w http.ResponseWriter) error {
	return r.write(w)
}
