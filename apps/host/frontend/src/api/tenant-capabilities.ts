import { request } from "./request";

export const moduleKeys = ["communications", "customers", "energy", "products"] as const;
export type ModuleKey = (typeof moduleKeys)[number];

export interface TenantModuleCapability {
  key: ModuleKey;
  enabled: boolean;
  /** Placeholder for future per-module tenant configuration. */
  config: Record<string, unknown>;
}

export interface TenantCapabilities {
  tenantId: string;
  modules: TenantModuleCapability[];
}

export const tenantCapabilitiesQueryKey = (tenantId: string | undefined) =>
  ["tenant-capabilities", tenantId ?? "none"] as const;

export const fetchTenantCapabilities = () =>
  request<TenantCapabilities>("/api/v1/identity/tenants/current/capabilities");

export const enabledModuleKeys = (capabilities: TenantCapabilities | undefined): ModuleKey[] | undefined =>
  capabilities?.modules.filter((module) => module.enabled).map((module) => module.key);
