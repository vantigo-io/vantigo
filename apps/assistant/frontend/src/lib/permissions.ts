import type { AssistantPermissionGroup } from "../api/types";

export type PermissionFilter = "all" | "sensitive" | "overridden";

/** The groups narrowed to the permissions the filter shows; a group left empty is dropped. */
export const filterGroups = (groups: readonly AssistantPermissionGroup[], filter: PermissionFilter) =>
  groups
    .map((group) => ({
      ...group,
      permissions: group.permissions.filter(
        (permission) =>
          filter === "all" ||
          (filter === "sensitive" && permission.sensitive) ||
          (filter === "overridden" && permission.override != null),
      ),
    }))
    .filter((group) => group.permissions.length > 0);
