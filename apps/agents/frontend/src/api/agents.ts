import { queryOptions } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import { AGENTS_QUERY_KEY, ifMatch, json, request } from "./request";
import type {
  Agent,
  AgentCreateRequest,
  AgentRole,
  AgentRoleCreateRequest,
  AgentRoleUpdateRequest,
  AgentSetup,
  AgentsSettings,
  AgentsSettingsRequest,
  AgentTemplate,
  AgentUpdateRequest,
  AssignableUser,
  ChannelOption,
  PermissionLevel,
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
