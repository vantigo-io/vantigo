import type { AgentSetupItem, NeededLevel, PermissionLevel } from "../api/types";

const rank: Record<PermissionLevel, number> = { off: 0, read: 1, change: 2 };

/** Whether a level is at least the one a role needs. */
export const meetsLevel = (current: PermissionLevel, needed: NeededLevel): boolean => rank[current] >= rank[needed];

/** What is still missing for one needed permission (V12). */
export interface PermissionGap {
  /** The Owner has to raise the level to what the role needs. */
  needsRaise: boolean;
  /** The agent's Identity roles do not hold the permission. */
  needsRoles: boolean;
  /** The level cannot be raised: the permission is one an agent never gets. */
  locked: boolean;
}

export const permissionGap = (item: AgentSetupItem): PermissionGap => ({
  needsRaise: !meetsLevel(item.level, item.needed),
  needsRoles: !item.heldByRoles,
  locked: item.locked,
});

/** The tone a row's state chip takes: green when satisfied, red when locked, yellow otherwise. */
export const permissionTone = (item: AgentSetupItem): "teal" | "red" | "yellow" =>
  item.satisfied ? "teal" : item.locked ? "red" : "yellow";
