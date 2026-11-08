import { queryOptions } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import { AGENTS_QUERY_KEY, ifMatch, json, request } from "./request";
import type {
  Agent,
  AgentAuditList,
  AgentCreateRequest,
  AgentRole,
  AgentRoleCreateRequest,
  AgentRoleUpdateRequest,
  AgentRunDetail,
  AgentRunList,
  AgentSetup,
  AgentsSettings,
  AgentsSettingsRequest,
  AgentTemplate,
  AgentUpdateRequest,
  AgentUsage,
  AssignableUser,
  ChannelOption,
  DiscardReason,
  PermissionLevel,
  ReviewCount,
  ReviewItem,
  ReviewItemList,
  ReviewState,
  RoleCalibration,
  RunState,
  TagOption,
} from "./types";

const base = "/api/v1/agents";

export const settingsQueryKey = [AGENTS_QUERY_KEY, "settings"] as const;
export const templatesQueryKey = [AGENTS_QUERY_KEY, "templates"] as const;
export const agentsQueryKey = [AGENTS_QUERY_KEY, "agents"] as const;
export const agentQueryKey = (id: string) => [AGENTS_QUERY_KEY, "agent", id] as const;
export const setupQueryKey = (id: string) => [AGENTS_QUERY_KEY, "setup", id] as const;

export const settingsQueryOptions = () =>
  queryOptions({
    queryKey: settingsQueryKey,
    queryFn: ({ signal }) => request<AgentsSettings>(`${base}/settings`, { signal }),
  });

export const putSettings = (body: AgentsSettingsRequest, version: number) =>
  request<AgentsSettings>(`${base}/settings`, json("PUT", body, ifMatch(version)));

export const templatesQueryOptions = () =>
  queryOptions({
    queryKey: templatesQueryKey,
    queryFn: async ({ signal }) =>
      (await request<{ templates: AgentTemplate[] }>(`${base}/templates`, { signal })).templates,
    staleTime: 300_000,
  });

export const agentsQueryOptions = () =>
  queryOptions({
    queryKey: agentsQueryKey,
    queryFn: async ({ signal }) => (await request<{ agents: Agent[] }>(`${base}/agents`, { signal })).agents,
  });

export const agentQueryOptions = (id: string) =>
  queryOptions({
    queryKey: agentQueryKey(id),
    queryFn: ({ signal }) => request<Agent>(`${base}/agents/${id}`, { signal }),
  });

export const createAgent = (body: AgentCreateRequest) => request<Agent>(`${base}/agents`, json("POST", body));

export const updateAgent = (id: string, body: AgentUpdateRequest, version: number) =>
  request<Agent>(`${base}/agents/${id}`, json("PUT", body, ifMatch(version)));

export const deleteAgent = (id: string) => request<void>(`${base}/agents/${id}`, { method: "DELETE" });

export const pauseAgent = (id: string) => request<Agent>(`${base}/agents/${id}/pause`, { method: "POST" });

export const resumeAgent = (id: string) => request<Agent>(`${base}/agents/${id}/resume`, { method: "POST" });

/** The picture is sent as multipart, the part named `avatar`; the browser sets the boundary. */
export const putAvatar = (id: string, file: File) => {
  const form = new FormData();
  form.append("avatar", file);
  return request<Agent>(`${base}/agents/${id}/avatar`, { method: "PUT", body: form });
};

/** Where an agent's picture is; the version busts the browser's cache after a new one is set. */
export const avatarUrl = (agent: Pick<Agent, "id" | "hasAvatar" | "version">): string | undefined =>
  agent.hasAvatar ? appUrl(`${base}/agents/${agent.id}/avatar?v=${agent.version}`) : undefined;

export const setupQueryOptions = (id: string) =>
  queryOptions({
    queryKey: setupQueryKey(id),
    queryFn: ({ signal }) => request<AgentSetup>(`${base}/agents/${id}/setup`, { signal }),
  });

export const putPermission = (id: string, key: string, level: PermissionLevel) =>
  request<{ permissionKey: string; level: PermissionLevel; version: number }>(
    `${base}/agents/${id}/permissions/${encodeURIComponent(key)}`,
    json("PUT", { level }),
  );

export const createRole = (agentId: string, body: AgentRoleCreateRequest) =>
  request<AgentRole>(`${base}/agents/${agentId}/roles`, json("POST", body));

export const updateRole = (agentId: string, roleId: string, body: AgentRoleUpdateRequest, version: number) =>
  request<AgentRole>(`${base}/agents/${agentId}/roles/${roleId}`, json("PUT", body, ifMatch(version)));

export const deleteRole = (agentId: string, roleId: string) =>
  request<void>(`${base}/agents/${agentId}/roles/${roleId}`, { method: "DELETE" });

export const putRoleChannels = (agentId: string, roleId: string, channelIds: string[]) =>
  request<AgentRole>(`${base}/agents/${agentId}/roles/${roleId}/channels`, json("PUT", { channelIds }));

// -- Communications' lists, read for the role page; no other app's package is imported ------------

/** The channels a role can answer; needs `communications:channels-manage`, so a 403 is an expected answer. */
export const channelsQueryOptions = () =>
  queryOptions({
    queryKey: [AGENTS_QUERY_KEY, "communications-channels"],
    queryFn: ({ signal }) => request<ChannelOption[]>("/api/v1/communications/channels", { signal }),
    retry: false,
  });

/** The people a hand-off may go to. */
export const assignableUsersQueryOptions = () =>
  queryOptions({
    queryKey: [AGENTS_QUERY_KEY, "communications-assignable-users"],
    queryFn: ({ signal }) => request<AssignableUser[]>("/api/v1/communications/assignable-users", { signal }),
    retry: false,
  });

/** The tags that can mark a complaint; Communications lists them for anyone who may view conversations. */
export const tagsQueryOptions = () =>
  queryOptions({
    queryKey: [AGENTS_QUERY_KEY, "communications-tags"],
    queryFn: ({ signal }) => request<TagOption[]>("/api/v1/communications/tags", { signal }),
    retry: false,
  });

// -- The review queue (V20) --------------------------------------------------------------------------

export const reviewQueryKey = [AGENTS_QUERY_KEY, "review"] as const;
export const reviewCountQueryKey = [AGENTS_QUERY_KEY, "review-count"] as const;
export const reviewItemQueryKey = (id: string) => [AGENTS_QUERY_KEY, "review-item", id] as const;

export interface ReviewFilter {
  agentId?: string;
  /** One state, or none for every state (the waiting ones first). */
  state?: ReviewState;
}

export const reviewQueryOptions = (filter: ReviewFilter) =>
  queryOptions({
    queryKey: [...reviewQueryKey, filter],
    queryFn: ({ signal }) => {
      const query = new URLSearchParams({ limit: "100" });
      if (filter.state) query.set("state", filter.state);
      if (filter.agentId) query.set("agentId", filter.agentId);
      return request<ReviewItemList>(`${base}/review?${query}`, { signal });
    },
    // The queue changes under the reviewer's eyes: an expiry, a colleague's decision.
    refetchInterval: 30_000,
  });

/** How many items wait; the badge on the agents tile and the queue's tab. It holds no content. */
export const reviewCountQueryOptions = () =>
  queryOptions({
    queryKey: reviewCountQueryKey,
    queryFn: ({ signal }) => request<ReviewCount>(`${base}/review/count`, { signal }),
    refetchInterval: 60_000,
    retry: false,
  });

export const reviewItemQueryOptions = (id: string) =>
  queryOptions({
    queryKey: reviewItemQueryKey(id),
    queryFn: ({ signal }) => request<ReviewItem>(`${base}/review/${id}`, { signal }),
  });

export const approveItem = (item: Pick<ReviewItem, "id" | "version">) =>
  request<ReviewItem>(`${base}/review/${item.id}/approve`, { method: "POST", headers: ifMatch(item.version) });

export const sendEditedItem = (item: Pick<ReviewItem, "id" | "version">, text: string) =>
  request<ReviewItem>(`${base}/review/${item.id}/send`, json("POST", { text }, ifMatch(item.version)));

export const discardItem = (item: Pick<ReviewItem, "id" | "version">, reason: DiscardReason, text: string) =>
  request<ReviewItem>(
    `${base}/review/${item.id}/discard`,
    json("POST", text.trim() === "" ? { reason } : { reason, text: text.trim() }, ifMatch(item.version)),
  );

// -- Calibration, usage and activity -------------------------------------------------------------------

export const calibrationQueryOptions = (roleId: string) =>
  queryOptions({
    queryKey: [AGENTS_QUERY_KEY, "calibration", roleId],
    queryFn: ({ signal }) => request<RoleCalibration>(`${base}/roles/${roleId}/calibration`, { signal }),
  });

export const usageQueryOptions = (agentId: string, days = 30) =>
  queryOptions({
    queryKey: [AGENTS_QUERY_KEY, "usage", agentId, days],
    queryFn: ({ signal }) => request<AgentUsage>(`${base}/agents/${agentId}/usage?days=${days}`, { signal }),
  });

export const runsQueryOptions = (agentId: string, state?: RunState) =>
  queryOptions({
    queryKey: [AGENTS_QUERY_KEY, "runs", agentId, state ?? "all"],
    queryFn: ({ signal }) => {
      const query = new URLSearchParams({ limit: "50" });
      if (state) query.set("state", state);
      return request<AgentRunList>(`${base}/agents/${agentId}/runs?${query}`, { signal });
    },
    retry: false,
  });

export const runQueryOptions = (runId: string) =>
  queryOptions({
    queryKey: [AGENTS_QUERY_KEY, "run", runId],
    queryFn: ({ signal }) => request<AgentRunDetail>(`${base}/runs/${runId}`, { signal }),
    retry: false,
  });

export const auditQueryOptions = () =>
  queryOptions({
    queryKey: [AGENTS_QUERY_KEY, "audit"],
    queryFn: ({ signal }) => request<AgentAuditList>(`${base}/audit?limit=20`, { signal }),
  });
