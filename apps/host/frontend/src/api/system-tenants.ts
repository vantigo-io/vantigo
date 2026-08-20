import { ensureCsrfToken, request } from "./request";
export type TenantStatus = "Active" | "Suspended";
export interface SystemTenant {
  id: string;
  name: string;
  slug: string;
  status: TenantStatus;
  enabledModules: string[];
  membershipsCount: number;
}
export interface CreateTenantRequest {
  name: string;
  slug: string;
  enabledModules: string[];
  adminUserId?: string;
  adminEmail?: string;
  adminDisplayName?: string;
}
export interface CreateTenantResponse {
  tenant: SystemTenant;
  seededAdminInvitationId?: string;
  seededAdminInvitationToken?: string;
}
// Mirrors TenantModuleCatalog on the identity service.
export const SYSTEM_MODULES = ["communications", "customers", "energy", "products"] as const;
export const normalizeSlug = (value: string) =>
  value
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
export const systemTenantError = (error: unknown) => {
  const code = (error as { code?: string })?.code;
  const messages: Record<string, string> = {
    slug_exists: "That slug is already in use.",
    tenant_exists: "This tenant already exists with different settings.",
    invitation_exists: "An active invitation already exists for this email.",
    account_exists: "An account already exists for this email; use an existing user instead.",
    invalid_slug: "Use lowercase letters, numbers, and hyphens.",
    invalid_modules: "Select only supported modules.",
    invalid_admin: "The designated admin account is not valid.",
    multi_tenant_required: "Tenant administration is available only in multi-tenant mode.",
    tenant_conflict: "The tenant changed elsewhere. Refresh and try again.",
  };
  return (code && messages[code]) || (error instanceof Error ? error.message : "The request could not be completed.");
};
const json = { headers: { "Content-Type": "application/json" } };
export const listSystemTenants = () => request<SystemTenant[]>("/api/v1/identity/admin/tenants");
export const getSystemTenant = (id: string) => request<SystemTenant>(`/api/v1/identity/admin/tenants/${id}`);
export const createSystemTenant = async (body: CreateTenantRequest) => {
  await ensureCsrfToken();
  return request<CreateTenantResponse>("/api/v1/identity/admin/tenants", {
    method: "POST",
    ...json,
    body: JSON.stringify(body),
  });
};
export const updateSystemTenant = async (
  id: string,
  body: Partial<Pick<SystemTenant, "name" | "slug" | "status" | "enabledModules">>,
) => {
  await ensureCsrfToken();
  return request<SystemTenant>(`/api/v1/identity/admin/tenants/${id}`, {
    method: "PUT",
    ...json,
    body: JSON.stringify(body),
  });
};
export const suspendSystemTenant = (id: string) =>
  request<SystemTenant>(`/api/v1/identity/admin/tenants/${id}/suspend`, { method: "POST" });
export const reactivateSystemTenant = (id: string) =>
  request<SystemTenant>(`/api/v1/identity/admin/tenants/${id}/reactivate`, { method: "POST" });
