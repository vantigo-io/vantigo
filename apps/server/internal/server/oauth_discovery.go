package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// The OAuth discovery documents (OAuth connections design C12, A13): the
// protected resource's metadata (RFC 9728) and the authorization server's
// (RFC 8414), built from the configuration alone. They are served from the
// root handler, ahead of the mux and the SPA, and every other path under
// /.well-known/ answers the 404 problem rather than the SPA's page.

// wellKnownPrefix is where every discovery document lives.
const wellKnownPrefix = "/.well-known/"

const (
	protectedResourcePath   = "/.well-known/oauth-protected-resource"
	authorizationServerPath = "/.well-known/oauth-authorization-server"
)

// oauthDiscovery answers a path under /.well-known/, base path already
// stripped where the request carried it. The host-root forms insert the base
// path: /.well-known/oauth-protected-resource<base>/api/mcp and
// /.well-known/oauth-authorization-server<base>; the plain forms, and the
// authorization server's under the base path, are served too. With OAuth
// off every one is 404, and with MCP off the protected resource's are.
func oauthDiscovery(cfg *config.Config) http.Handler {
	resource := map[string]any{
		"resource":                 cfg.OAuthResource,
		"authorization_servers":    []string{cfg.OAuthIssuer},
		"scopes_supported":         []string{"read", "write"},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Vantigo",
	}
	server := map[string]any{
		"issuer":                                         cfg.OAuthIssuer,
		"authorization_endpoint":                         cfg.OAuthIssuer + "/oauth/authorize",
		"token_endpoint":                                 cfg.OAuthIssuer + "/api/oauth/token",
		"response_types_supported":                       []string{"code"},
		"response_modes_supported":                       []string{"query"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"scopes_supported":                               []string{"read", "write", "offline_access"},
		"client_id_metadata_document_supported":          true,
		"authorization_response_iss_parameter_supported": true,
	}
	documents := map[string]map[string]any{}
	if cfg.OAuthEnabled {
		documents[authorizationServerPath+cfg.BasePath] = server
		documents[authorizationServerPath] = server
		if cfg.MCPEnabled {
			documents[protectedResourcePath+cfg.BasePath+"/api/mcp"] = resource
			documents[protectedResourcePath] = resource
		}
	}
	bodies := map[string][]byte{}
	for path, doc := range documents {
		body, err := json.Marshal(doc)
		if err != nil {
			panic("server: encode an OAuth discovery document: " + err.Error()) // plain strings and lists: cannot fail
		}
		bodies[path] = append(body, '\n')
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := bodies[r.URL.EscapedPath()]
		if !ok {
			httpx.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			httpx.WriteProblem(w, r, http.StatusMethodNotAllowed, "")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	})
}

// isWellKnown reports whether path is under /.well-known/.
func isWellKnown(path string) bool {
	return path == strings.TrimSuffix(wellKnownPrefix, "/") || strings.HasPrefix(path, wellKnownPrefix)
}
