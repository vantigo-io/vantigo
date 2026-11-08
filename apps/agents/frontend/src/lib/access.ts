/**
 * What the caller may do in the Agents app, as the host judges it from the
 * session's permissions. The pages never read the session themselves.
 */
export interface AgentsAccess {
  /** `agents:manage`: create, edit, pause and delete agents, and change the settings. */
  canManage: boolean;
  /**
   * The Owner: raising a level, choosing or loosening an Auto mode and binding a
   * channel are the Owner's besides `agents:manage`, which the API decides too.
   */
  isOwner: boolean;
  /** `agents:review`: the review queue, and approving, editing and discarding what an agent proposes. */
  canReview: boolean;
  /** `agents:activity`: an agent's runs and their transcripts. */
  canSeeActivity: boolean;
}

/** Nothing allowed: what a test or a host that has not answered yet hands in. */
export const noAgentsAccess: AgentsAccess = {
  canManage: false,
  isOwner: false,
  canReview: false,
  canSeeActivity: false,
};
