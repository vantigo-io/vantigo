import { infiniteQueryOptions, keepPreviousData, queryOptions } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import { ASSISTANT_QUERY_KEY, json, request } from "./request";
import type {
  AssistantAgentList,
  AssistantAvailability,
  AssistantBulkJob,
  AssistantBulkState,
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
  AssistantUsage,
} from "./types";

const base = "/api/v1/assistant";

/** An Owner's write names the version it read, bare, as the contract's If-Match says. */
const ifMatch = (version: number) => ({ "If-Match": String(version) });

/** The `code` of a problem the assistant answered, when the error carries one. */
export const errorCode = (error: unknown): string | undefined => (error as { code?: string } | undefined)?.code;

export const isUnavailable = (error: unknown) => errorCode(error) === "assistant_unavailable";
export const isVersionConflict = (error: unknown) => errorCode(error) === "version_conflict";
export const isRunInProgress = (error: unknown) => errorCode(error) === "run_in_progress";
export const isActionDecided = (error: unknown) => errorCode(error) === "action_decided";
export const isActionExpired = (error: unknown) => errorCode(error) === "action_expired";
export const isAgentUnavailable = (error: unknown) => errorCode(error) === "agent_unavailable";

// -- bulk jobs (S13) ---------------------------------------------------------

/** The states in which a bulk job is running on the server: the card polls it. */
export const bulkRunningStates: readonly AssistantBulkState[] = ["canary", "running", "stopping"];

export const bulkJobQueryKey = (id: string) => [ASSISTANT_QUERY_KEY, "bulk-job", id] as const;

export const BULK_POLL_MS = 2000;

/**
 * A bulk job's state, counters and first failures. It is polled every two
 * seconds while the job runs (`running` is what the card last knew) and read
 * once more when it ends, for the failed list; it stops there.
 */
export const bulkJobQueryOptions = (id: string, running: boolean) =>
  queryOptions({
    queryKey: bulkJobQueryKey(id),
    queryFn: ({ signal }) => request<AssistantBulkJob>(`${base}/bulk-jobs/${id}`, { signal }),
    retry: false,
    refetchInterval: (query) => {
      const state = query.state.data?.state;
      return state ? (bulkRunningStates.includes(state) ? BULK_POLL_MS : false) : running ? BULK_POLL_MS : false;
    },
  });

/** Runs the rest of a job that paused after the canary, or because its permission was about to lapse. */
export const continueBulkJob = (id: string) =>
  request<AssistantBulkJob>(`${base}/bulk-jobs/${id}/continue`, { method: "POST" });

/** Stops a job: a running one stops between chunks. */
export const stopBulkJob = (id: string) =>
  request<AssistantBulkJob>(`${base}/bulk-jobs/${id}/stop`, { method: "POST" });

/** Where the failed items download as a CSV file. */
export const bulkFailuresUrl = (id: string) => appUrl(`${base}/bulk-jobs/${id}/failures.csv`);

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

/** Starts a conversation; with an agent's id it is a conversation with that virtual employee (V31). */
export const createConversation = (origin: { app: string; route: string }, agentId?: string | null) =>
  request<AssistantConversation>(`${base}/conversations`, json("POST", agentId ? { origin, agentId } : { origin }));

export const agentsQueryKey = [ASSISTANT_QUERY_KEY, "agents"] as const;

/** The virtual employees the person may talk to: empty without agents:view or without the Agents app. */
export const agentsQueryOptions = () =>
  queryOptions({
    queryKey: agentsQueryKey,
    queryFn: ({ signal }) => request<AssistantAgentList>(`${base}/agents`, { signal }),
    retry: false,
    staleTime: 60_000,
  });

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

// -- the Owner's usage view ----------------------------------------------------

export const usageQueryKey = [ASSISTANT_QUERY_KEY, "usage"] as const;

/** What people used, per person and business day, from `from` (a date, `YYYY-MM-DD`) to today. Counts only: no content. */
export const usageQueryOptions = (from: string) =>
  queryOptions({
    queryKey: [...usageQueryKey, from] as const,
    queryFn: ({ signal }) => request<AssistantUsage>(`${base}/usage?from=${from}`, { signal }),
    retry: false,
  });
