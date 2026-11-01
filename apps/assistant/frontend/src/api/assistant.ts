import { infiniteQueryOptions, keepPreviousData, queryOptions } from "@tanstack/react-query";
import { ASSISTANT_QUERY_KEY, json, request } from "./request";
import type {
  AssistantAvailability,
  AssistantConversation,
  AssistantConversationDetail,
  AssistantConversationList,
  AssistantMe,
  AssistantPermissionLevel,
  AssistantPermissionResult,
  AssistantPermissions,
  AssistantPerson,
  AssistantSettings,
  AssistantSettingsRequest,
  AssistantUpdateConversation,
} from "./types";

const base = "/api/v1/assistant";

/** An Owner's write names the version it read, bare, as the contract's If-Match says. */
const ifMatch = (version: number) => ({ "If-Match": String(version) });

/** The `code` of a problem the assistant answered, when the error carries one. */
export const errorCode = (error: unknown): string | undefined => (error as { code?: string } | undefined)?.code;

export const isUnavailable = (error: unknown) => errorCode(error) === "assistant_unavailable";
export const isVersionConflict = (error: unknown) => errorCode(error) === "version_conflict";
export const isRunInProgress = (error: unknown) => errorCode(error) === "run_in_progress";

// -- availability and the person's own state ---------------------------------

export const availabilityQueryKey = [ASSISTANT_QUERY_KEY, "availability"] as const;

/** Whether the caller has the assistant (S8): the shell decides on this whether to show anything at all. */
export const availabilityQueryOptions = () =>
  queryOptions({
    queryKey: availabilityQueryKey,
    queryFn: ({ signal }) => request<AssistantAvailability>(`${base}/availability`, { signal }),
    retry: false,
    staleTime: 300_000,
  });

export const meQueryKey = [ASSISTANT_QUERY_KEY, "me"] as const;

export const meQueryOptions = () =>
  queryOptions({
    queryKey: meQueryKey,
    queryFn: ({ signal }) => request<AssistantMe>(`${base}/me`, { signal }),
  });

/** Remembers the conversation the person has open, on the server so it follows them between devices. */
export const putMe = (lastConversationId: string | null) =>
  request<AssistantMe>(`${base}/me`, json("PUT", { lastConversationId }));

// -- conversations -----------------------------------------------------------

export interface ConversationListParams {
  q?: string;
  archived?: boolean;
  limit?: number;
}

export const conversationsQueryKey = [ASSISTANT_QUERY_KEY, "conversations"] as const;

const listUrl = (params: ConversationListParams, cursor?: string | null) => {
  const query = new URLSearchParams();
  if (params.q) query.set("q", params.q);
  if (params.archived) query.set("archived", "true");
  if (params.limit) query.set("limit", String(params.limit));
  if (cursor) query.set("cursor", cursor);
  const text = query.toString();
  return `${base}/conversations${text ? `?${text}` : ""}`;
};

/** The conversations, a page at a time: pinned first, then by last activity. */
export const conversationsQueryOptions = (params: ConversationListParams = {}) =>
  infiniteQueryOptions({
    queryKey: [...conversationsQueryKey, params] as const,
    initialPageParam: null as string | null,
    queryFn: ({ pageParam, signal }) => request<AssistantConversationList>(listUrl(params, pageParam), { signal }),
    getNextPageParam: (last) => last.nextCursor ?? undefined,
    // Typing in the search box keeps the list on screen until the new one arrives.
    placeholderData: keepPreviousData,
  });

export const conversationQueryKey = (id: string) => [ASSISTANT_QUERY_KEY, "conversation", id] as const;

export const conversationQueryOptions = (id: string) =>
  queryOptions({
    queryKey: conversationQueryKey(id),
    queryFn: ({ signal }) => request<AssistantConversationDetail>(`${base}/conversations/${id}`, { signal }),
    retry: false,
  });

export const createConversation = (origin: { app: string; route: string }) =>
  request<AssistantConversation>(`${base}/conversations`, json("POST", { origin }));

export const updateConversation = (id: string, patch: AssistantUpdateConversation) =>
  request<AssistantConversation>(`${base}/conversations/${id}`, json("PATCH", patch));

export const deleteConversation = (id: string) => request<void>(`${base}/conversations/${id}`, { method: "DELETE" });

export const stopRun = (runId: string) => request<void>(`${base}/runs/${runId}/stop`, { method: "POST" });

// -- the Owner's settings ----------------------------------------------------

export const settingsQueryKey = [ASSISTANT_QUERY_KEY, "settings"] as const;

export const settingsQueryOptions = () =>
  queryOptions({
    queryKey: settingsQueryKey,
    queryFn: ({ signal }) => request<AssistantSettings>(`${base}/settings`, { signal }),
    retry: false,
  });

export const putSettings = (patch: AssistantSettingsRequest, version: number) =>
  request<AssistantSettings>(`${base}/settings`, json("PUT", patch, ifMatch(version)));

export const permissionsQueryKey = [ASSISTANT_QUERY_KEY, "permissions"] as const;

export const permissionsQueryOptions = () =>
  queryOptions({
    queryKey: permissionsQueryKey,
    queryFn: ({ signal }) => request<AssistantPermissions>(`${base}/settings/permissions`, { signal }),
    retry: false,
  });

/** Sets one permission's level, or resets it to its default with `null`; under the settings' version. */
export const putPermission = (key: string, level: AssistantPermissionLevel | null, version: number) =>
  request<AssistantPermissionResult>(
    `${base}/settings/permissions/${encodeURIComponent(key)}`,
    json("PUT", { level }, ifMatch(version)),
  );

export const personQueryKey = (userId: string) => [ASSISTANT_QUERY_KEY, "person", userId] as const;

export const personQueryOptions = (userId: string) =>
  queryOptions({
    queryKey: personQueryKey(userId),
    queryFn: ({ signal }) => request<AssistantPerson>(`${base}/people/${userId}`, { signal }),
    retry: false,
  });

export const putPerson = (userId: string, enabled: boolean, version: number) =>
  request<AssistantPerson>(`${base}/people/${userId}`, json("PUT", { enabled }, ifMatch(version)));
