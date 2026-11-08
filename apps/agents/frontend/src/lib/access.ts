/**
 * What the caller may do in the Agents app, as the host judges it from the
 * session's permissions. The pages never read the session themselves.
 */
export interface AgentsAccess {
  /** `agents:manage`: create, edit, pause and delete agents, and change the settings. */
  canManage: boolean;
}

/** Nothing allowed: what a test or a host that has not answered yet hands in. */
export const noAgentsAccess: AgentsAccess = { canManage: false };
