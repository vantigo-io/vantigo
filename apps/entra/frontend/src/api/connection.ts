import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { ENTRA_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/** The installation's one connection to a tenant, its settings, its secrets without their values and the accounts' counts. */
export type EntraConnection = Schemas["EntraConnection"];
export type EntraConnectionRequest = Schemas["EntraConnectionRequest"];
export type EntraConnectionTest = Schemas["EntraConnectionTest"];
export type EntraCredential = Schemas["EntraCredential"];
export type EntraCredentialRequest = Schemas["EntraCredentialRequest"];
export type EntraSettings = Schemas["EntraSettings"];
export type EntraSettingsRequest = Schemas["EntraSettingsRequest"];
export type EntraSku = Schemas["EntraSku"];
export type EntraEvent = Schemas["EntraEvent"];
export type EntraAccount = Schemas["EntraAccount"];
export type EntraProblem = Schemas["EntraProblem"];
export type EntraGroup = Schemas["EntraGroup"];

/**
 * The eight application permissions the module needs (research §3.6), in the
 * order the wizard lists them for the Owner to add; the server compares the
 * token's roles with the same list.
 */
export const requiredPermissions = [
  "User.ReadWrite.All",
  "User-LifeCycleInfo.ReadWrite.All",
  "User.RevokeSessions.All",
  "UserAuthMethod-TAP.ReadWrite.All",
  "LicenseAssignment.ReadWrite.All",
  "Group.ReadWrite.All",
  "Domain.Read.All",
  "Organization.Read.All",
] as const;

const base = "/api/v1/entra";
const ifMatch = (version: number) => ({ "If-Match": String(version) });

export const connectionQueryOptions = () =>
  queryOptions({
    queryKey: [ENTRA_QUERY_KEY, "connection"],
    queryFn: ({ signal }) => request<EntraConnection>(`${base}/connection`, { signal }),
  });

/** Step 1, the Owner's: the tenant and the client under the version read; `confirmChange` after a 409 `connection_in_use`. */
export const updateConnection = (body: EntraConnectionRequest, version: number) =>
  request<EntraConnection>(`${base}/connection`, json("PUT", body, ifMatch(version)));

/** Step 2, the Owner's: a client secret, sealed at once and never answered again. */
export const addCredential = (body: EntraCredentialRequest) =>
  request<EntraCredential>(`${base}/connection/credentials`, json("POST", body));

/** Revokes a secret here (it is kept, marked revoked); the only valid one is refused 409 `credential_in_use` while accounts exist. */
export const revokeCredential = (id: number) =>
  request<EntraCredential>(`${base}/connection/credentials/${id}`, { method: "DELETE" });

/** Step 3, the Owner's: a token, the organisation and the permissions; 200 whatever it found. */
export const testConnection = () => request<EntraConnectionTest>(`${base}/connection/test`, { method: "POST" });

/** Step 4: the account rules under the version read; a field left out keeps its value. */
export const updateSettings = (body: EntraSettingsRequest, version: number) =>
  request<EntraConnection>(`${base}/connection/settings`, json("PUT", body, ifMatch(version)));

/** The tenant's licences, read through the connection; 503 `credential_invalid` or `tenant_unreachable` when it cannot be asked. */
export const fetchSkus = (signal?: AbortSignal) => request<EntraSku[]>(`${base}/connection/skus`, { signal });

export const skusQueryOptions = () =>
  queryOptions({ queryKey: [ENTRA_QUERY_KEY, "skus"], queryFn: ({ signal }) => fetchSkus(signal) });

export const eventsQueryOptions = (limit = 10) =>
  queryOptions({
    queryKey: [ENTRA_QUERY_KEY, "events", limit],
    queryFn: ({ signal }) => request<EntraEvent[]>(`${base}/events?limit=${limit}`, { signal }),
  });

/** Confirms the adoption of an account the module found rather than created; 409 `adoption_confirmed` when there is nothing to confirm. */
export const confirmAdoption = (objectId: string) =>
  request<EntraAccount>(`${base}/accounts/${objectId}/confirm`, { method: "POST" });

export const accountsQueryOptions = () =>
  queryOptions({
    queryKey: [ENTRA_QUERY_KEY, "accounts"],
    queryFn: ({ signal }) => request<EntraAccount[]>(`${base}/accounts`, { signal }),
  });

/** Confirms the adoption of a group the module found by its mail nickname rather than made; idempotent. */
export const confirmGroupAdoption = (objectId: string) =>
  request<EntraGroup>(`${base}/groups/${objectId}/confirm`, { method: "POST" });

/** The groups the module keeps in the tenant for Staff's groups, deleted and retired ones included (Entra design D14). */
export const groupsQueryOptions = () =>
  queryOptions({
    queryKey: [ENTRA_QUERY_KEY, "groups"],
    queryFn: ({ signal }) => request<EntraGroup[]>(`${base}/groups`, { signal }),
  });
