import type { components } from "../api-schema";
import { integrationQueryKey } from "./integrations";
import { request } from "./request";

/**
 * OAuth connections (OAuth connections design C13, C14, C25): the consent
 * page's two operations, which post the authorization request's query string
 * as the page received it, and an integration's connected apps with Revoke.
 * All are an Owner's and session-only.
 */
export type OAuthAuthorizationPreview = components["schemas"]["OAuthAuthorizationPreview"];
export type OAuthIntegrationChoice = components["schemas"]["OAuthIntegrationChoice"];
export type OAuthClientDisplay = components["schemas"]["OAuthClientDisplay"];
export type OAuthAuthorizationDecision = components["schemas"]["OAuthAuthorizationDecision"];
export type IntegrationConnection = components["schemas"]["IntegrationConnection"];

const json = { "Content-Type": "application/json" };

export const previewAuthorization = (query: string) =>
  request<OAuthAuthorizationPreview>("/api/v1/identity/oauth/authorizations/preview", {
    method: "POST",
    headers: json,
    body: JSON.stringify({ query }),
  });

export const decideAuthorization = (decision: OAuthAuthorizationDecision) =>
  request<components["schemas"]["OAuthAuthorizationRedirect"]>("/api/v1/identity/oauth/authorizations", {
    method: "POST",
    headers: json,
    body: JSON.stringify(decision),
  });

export const integrationConnectionsQueryKey = (id: string) => [...integrationQueryKey(id), "connections"] as const;

/** An integration's connected apps; 404 while the installation has OAuth switched off. */
export const listIntegrationConnections = (id: string) =>
  request<components["schemas"]["IntegrationConnectionList"]>(`/api/v1/identity/integrations/${id}/connections`).then(
    (list) => list.connections,
  );

export const revokeIntegrationConnection = (id: string, connectionId: string) =>
  request<void>(`/api/v1/identity/integrations/${id}/connections/${connectionId}`, { method: "DELETE" });
