/** The app's paths, which the host mounts and the package's links name. */
export const AGENTS_HOME_ROUTE_PATH = "/agents";
export const AGENTS_SETTINGS_ROUTE_PATH = "/agents/settings";
export const AGENTS_REVIEW_ROUTE_PATH = "/agents/review";
export const REVIEW_ITEM_ROUTE_PATH = "/agents/review/$itemId";
export const RUN_ROUTE_PATH = "/agents/runs/$runId";
export const AGENT_ROUTE_PATH = "/agents/$agentId";
export const ROLE_ROUTE_PATH = "/agents/$agentId/roles/$roleId";

/** The agent page's tabs; the active one lives in the URL's `tab` search parameter. */
export const agentTabs = ["persona", "roles", "permissions", "usage", "activity"] as const;
export type AgentTab = (typeof agentTabs)[number];

/** A `tab` search value as a tab, the persona when it is none. */
export const parseAgentTab = (value: unknown): AgentTab =>
  agentTabs.includes(value as AgentTab) ? (value as AgentTab) : "persona";

export const agentLinkOptions = (agentId: string, tab?: AgentTab) => ({
  to: AGENT_ROUTE_PATH,
  params: { agentId },
  search: tab && tab !== "persona" ? { tab } : {},
});

export const roleLinkOptions = (agentId: string, roleId: string) => ({
  to: ROLE_ROUTE_PATH,
  params: { agentId, roleId },
});

/** Identity's roles screen, where an agent's user is given roles like a person; owned by the host. */
export const IDENTITY_ROLES_ROUTE_PATH = "/workspace/roles";

export const reviewItemLinkOptions = (itemId: string) => ({ to: REVIEW_ITEM_ROUTE_PATH, params: { itemId } });

export const runLinkOptions = (runId: string) => ({ to: RUN_ROUTE_PATH, params: { runId } });
