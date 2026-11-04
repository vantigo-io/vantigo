import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { GOOGLE_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/**
 * The installation's one connection to a Google Workspace organisation: the
 * service account's keys and the web client's secret without their values,
 * the admin to impersonate, the scopes to delegate and what the last test found.
 */
export type GoogleConnection = Schemas["GoogleConnection"];
export type GoogleConnectionRequest = Schemas["GoogleConnectionRequest"];
export type GoogleConnectionTest = Schemas["GoogleConnectionTest"];
export type GoogleCredential = Schemas["GoogleCredential"];
export type GoogleOAuthClientRequest = Schemas["GoogleOAuthClientRequest"];
export type GoogleEvent = Schemas["GoogleEvent"];
export type GoogleSettingsRequest = Schemas["GoogleSettingsRequest"];
export type GoogleSku = Schemas["GoogleSku"];
export type GoogleAccount = Schemas["GoogleAccount"];
export type GoogleGroup = Schemas["GoogleGroup"];
export type GoogleStatus = GoogleConnection["status"];

const base = "/api/v1/google";
const ifMatch = (version: number) => ({ "If-Match": String(version) });

export const connectionQueryOptions = () =>
  queryOptions({
    queryKey: [GOOGLE_QUERY_KEY, "connection"],
    queryFn: ({ signal }) => request<GoogleConnection>(`${base}/connection`, { signal }),
  });

/** The admin to impersonate, the Owner's, under the version read; 412 `version_mismatch` when it moved on. */
export const updateConnection = (body: GoogleConnectionRequest, version: number) =>
  request<GoogleConnection>(`${base}/connection`, json("PUT", body, ifMatch(version)));

/** A service account's JSON key, sent as the file's text; the private key is sealed at once and never answered. */
export const uploadKey = (text: string) =>
  request<GoogleCredential>(`${base}/connection/credentials`, json("POST", { json: text }));

/** Revokes a key or the web client's secret here; nothing changes at Google. */
export const revokeCredential = (id: number) =>
  request<GoogleCredential>(`${base}/connection/credentials/${id}`, { method: "DELETE" });

/** The Internal web client: its id, and its secret when one is typed; the secret is never answered. */
export const updateOAuthClient = (body: GoogleOAuthClientRequest) =>
  request<GoogleConnection>(`${base}/connection/oauth-client`, json("PUT", body));

/** Asks Google for a token per scope as the admin, then reads the organisation; 200 whatever it found. */
export const testConnection = () => request<GoogleConnectionTest>(`${base}/connection/test`, { method: "POST" });

export const eventsQueryOptions = (limit = 20) =>
  queryOptions({
    queryKey: [GOOGLE_QUERY_KEY, "events", limit],
    queryFn: ({ signal }) => request<GoogleEvent[]>(`${base}/events?limit=${limit}`, { signal }),
  });

/** The account rules, `google:manage`'s, under the version read; 412 `version_mismatch` when it moved on. */
export const updateSettings = (body: GoogleSettingsRequest, version: number) =>
  request<GoogleConnection>(`${base}/connection/settings`, json("PUT", body, ifMatch(version)));

/** Reads the organisation's licences from Google and keeps them on the connection; 503 when Google cannot be asked. */
export const readSkus = () => request<GoogleSku[]>(`${base}/connection/skus`);

/** The users the module made or found, from its own rows. */
export const accountsQueryOptions = () =>
  queryOptions({
    queryKey: [GOOGLE_QUERY_KEY, "accounts"],
    queryFn: ({ signal }) => request<GoogleAccount[]>(`${base}/accounts`, { signal }),
  });

/** Confirms the adoption of a user the module found rather than made; 409 `adoption_confirmed` when there is nothing to confirm. */
export const confirmAccountAdoption = (userId: string) =>
  request<GoogleAccount>(`${base}/accounts/${encodeURIComponent(userId)}/confirm`, { method: "POST" });

/** The groups the module made or found for Staff's groups, from its own rows. */
export const groupsQueryOptions = () =>
  queryOptions({
    queryKey: [GOOGLE_QUERY_KEY, "groups"],
    queryFn: ({ signal }) => request<GoogleGroup[]>(`${base}/groups`, { signal }),
  });

/** Confirms the adoption of a group the module found rather than made; idempotent. */
export const confirmGroupAdoption = (groupId: string) =>
  request<GoogleGroup>(`${base}/groups/${encodeURIComponent(groupId)}/confirm`, { method: "POST" });

/** Turns Mail channels on or off, the Owner's; the scopes to delegate follow. */
export const setMailChannels = (enabled: boolean) =>
  request<GoogleConnection>(`${base}/connection/mail-channels`, json("PUT", { enabled }));
