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

/**
 * The clients an Owner registers by hand (OAuth connections design C8), for
 * a client that publishes no metadata document, and the client hosts that may
 * connect by theirs (C11). Both answer 404 while the installation has OAuth
 * switched off.
 */
export type RegisteredClient = components["schemas"]["OAuthRegisteredClient"];
export type RegisteredClientRequest = components["schemas"]["OAuthRegisteredClientRequest"];

export const registeredClientsQueryKey = ["oauth", "clients"] as const;
export const listRegisteredClients = () =>
  request<components["schemas"]["OAuthRegisteredClientList"]>("/api/v1/identity/oauth-clients").then(
    (list) => list.clients,
  );

export const registerClient = (body: RegisteredClientRequest) =>
  request<RegisteredClient>("/api/v1/identity/oauth-clients", {
    method: "POST",
    headers: json,
    body: JSON.stringify(body),
  });

/** Replaces a registered client's name and redirect URIs under If-Match on the version read. */
export const updateRegisteredClient = (id: string, version: string, body: RegisteredClientRequest) =>
  request<RegisteredClient>(`/api/v1/identity/oauth-clients/${id}`, {
    method: "PATCH",
    headers: { ...json, "If-Match": version },
    body: JSON.stringify(body),
  });

/** Archives a registered client and ends its connections, under If-Match on the version read. */
export const archiveRegisteredClient = (id: string, version: string) =>
  request<void>(`/api/v1/identity/oauth-clients/${id}/archive`, { method: "POST", headers: { "If-Match": version } });

export const oauthClientHostsQueryKey = ["oauth", "settings"] as const;
export const getOAuthClientHosts = () =>
  request<components["schemas"]["OAuthClientHostsSettings"]>("/api/v1/identity/oauth/settings").then(
    (settings) => settings.clientHosts,
  );

export const setOAuthClientHosts = (clientHosts: string[]) =>
  request<components["schemas"]["OAuthClientHostsSettings"]>("/api/v1/identity/oauth/settings", {
    method: "PUT",
    headers: json,
    body: JSON.stringify({ clientHosts }),
  }).then((settings) => settings.clientHosts);
