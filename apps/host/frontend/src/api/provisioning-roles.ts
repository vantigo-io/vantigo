import { request } from "./request";

/** A role Staff may give the users it provisions, with the permission keys it carries. */
export interface ProvisioningRole {
  id: string;
  name: string;
  displayName: string;
  permissionKeys: string[];
}

const path = "/api/v1/identity/owner/provisioning-roles";

export const listProvisioningRoles = () => request<{ roles: ProvisioningRole[] }>(path);

/** Replaces the set whole: the roles not named are taken out of it. */
export const replaceProvisioningRoles = (roleIds: string[]) =>
  request<{ roles: ProvisioningRole[] }>(path, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ roleIds }),
  });
