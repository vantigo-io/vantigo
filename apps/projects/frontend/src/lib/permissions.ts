/**
 * Whether the caller's permission keys include one. `"*"` is every
 * permission, exactly as the host's own `hasPermissions` reads it; a list
 * that has not loaded yet grants nothing.
 */
export const holdsPermission = (permissions: readonly string[] | undefined, permission: string): boolean =>
  permissions !== undefined && (permissions.includes("*") || permissions.includes(permission));
