/** A user's role on one project, most capable first — the order the people tab renders. */
export const projectRoles = ["manager", "member", "viewer"] as const;

export type ProjectRole = (typeof projectRoles)[number];

const roleKeys: Record<ProjectRole, string> = {
  manager: "roleManager",
  member: "roleMember",
  viewer: "roleViewer",
};

/** The `projects` catalog key naming this role. */
export const projectRoleLabelKey = (role: ProjectRole): string => roleKeys[role];

export const isProjectRole = (value: string): value is ProjectRole =>
  (projectRoles as readonly string[]).includes(value);
