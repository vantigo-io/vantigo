import type { components } from "../api-schema";
import { request } from "./request";

/**
 * API access (API access design K21): an Owner's integrations, the accounts
 * external applications act as, and their API keys. A key's secret is in the
 * answer to its creation or roll alone, never in a read.
 */
export type Integration = components["schemas"]["Integration"];
export type IntegrationDetail = components["schemas"]["IntegrationDetail"];
export type IntegrationPermission = components["schemas"]["IntegrationPermission"];
export type IntegrationCreateRequest = components["schemas"]["IntegrationCreateRequest"];
export type IntegrationUpdateRequest = components["schemas"]["IntegrationUpdateRequest"];
export type ApiKey = components["schemas"]["ApiKey"];
export type ApiKeyCreateRequest = components["schemas"]["ApiKeyCreateRequest"];
export type ApiKeyRollRequest = components["schemas"]["ApiKeyRollRequest"];
export type ApiKeySecret = components["schemas"]["ApiKeySecret"];

const json = { "Content-Type": "application/json" };
const base = "/api/v1/identity/integrations";
const post = <T>(path: string, body?: unknown) =>
  request<T>(
    path,
    body === undefined ? { method: "POST" } : { method: "POST", headers: json, body: JSON.stringify(body) },
  );

export const integrationsQueryKey = ["identity", "integrations"] as const;
export const integrationQueryKey = (id: string) => [...integrationsQueryKey, id] as const;

export const listIntegrations = () =>
  request<components["schemas"]["IntegrationList"]>(base).then((list) => list.integrations);
export const createIntegration = (body: IntegrationCreateRequest) => post<IntegrationDetail>(base, body);
export const getIntegration = (id: string) => request<IntegrationDetail>(`${base}/${id}`);
/** Replaces the name, description and contact (absent clears them) under If-Match on the version read. */
export const updateIntegration = (id: string, version: string, body: IntegrationUpdateRequest) =>
  request<IntegrationDetail>(`${base}/${id}`, {
    method: "PATCH",
    headers: { ...json, "If-Match": version },
    body: JSON.stringify(body),
  });
export const setIntegrationRoles = (id: string, roleIds: string[]) =>
  request<IntegrationDetail>(`${base}/${id}/roles`, {
    method: "PUT",
    headers: json,
    body: JSON.stringify({ roleIds }),
  });
export type IntegrationMcpAccess = components["schemas"]["IntegrationMcpAccess"];
/** Sets what the integration's keys may do at /api/mcp (MCP server design M2), under If-Match on the version read. */
export const setIntegrationMcpAccess = (id: string, version: string, mcpAccess: IntegrationMcpAccess) =>
  request<IntegrationDetail>(`${base}/${id}/mcp`, {
    method: "PUT",
    headers: { ...json, "If-Match": version },
    body: JSON.stringify({ mcpAccess }),
  });
export const disableIntegration = (id: string) => post<IntegrationDetail>(`${base}/${id}/disable`);
export const enableIntegration = (id: string) => post<IntegrationDetail>(`${base}/${id}/enable`);
export const archiveIntegration = (id: string) => post<IntegrationDetail>(`${base}/${id}/archive`);
export const createApiKey = (id: string, body: ApiKeyCreateRequest) => post<ApiKeySecret>(`${base}/${id}/keys`, body);
export const rollApiKey = (id: string, keyId: string, body: ApiKeyRollRequest) =>
  post<ApiKeySecret>(`${base}/${id}/keys/${keyId}/roll`, body);
export const revokeApiKey = (id: string, keyId: string) => post<ApiKey>(`${base}/${id}/keys/${keyId}/revoke`);

export type IntegrationRequestLog = components["schemas"]["IntegrationRequestLog"];
export type IntegrationRequest = components["schemas"]["IntegrationRequest"];
/** The filters of the request log (K23); each one left out reads everything. */
export type RequestLogFilters = {
  key?: string;
  statusClass?: number;
  reason?: string;
  day?: string;
};
export const integrationRequestsQueryKey = (id: string, filters: RequestLogFilters) =>
  [...integrationQueryKey(id), "requests", filters] as const;
/** An integration's request log over the last 30 days, or one UTC day of them. */
export const getIntegrationRequests = (id: string, filters: RequestLogFilters) => {
  const query = new URLSearchParams();
  for (const [name, value] of Object.entries(filters)) {
    if (value !== undefined && value !== "") query.set(name, String(value));
  }
  const suffix = query.size > 0 ? `?${query.toString()}` : "";
  return request<IntegrationRequestLog>(`${base}/${id}/requests${suffix}`);
};
