import { createApiClient } from "@vantigo/frontend-api-client";
import { appUrl } from "@vantigo/frontend-shell";

let activeTenantSlug: string | undefined;
let tenantRoutingEnabled = false;

/** Sets the optional tenant prefix for business API calls. Identity stays global. */
export const setActiveTenantSlug = (slug: string | undefined) => {
  activeTenantSlug = slug;
};

/** Enables the future tenant-prefixed API mode without breaking single-mode deployments. */
export const setTenantRoutingEnabled = (enabled: boolean) => {
  tenantRoutingEnabled = enabled;
};

const tenantAwareUrl = (url: string) => {
  if (!tenantRoutingEnabled || !activeTenantSlug || !url.startsWith("/api/") || url.startsWith("/api/v1/identity/"))
    return url;
  return `/api/v1/t/${encodeURIComponent(activeTenantSlug)}${url.slice(7)}`;
};

const client = createApiClient({ resolveUrl: appUrl, transformUrl: tenantAwareUrl });

export const { clearCsrfToken, ensureCsrfToken, getCsrfToken, request, setAuthStateClearer, setUnauthorizedHandler } =
  client;
export type { ApiError, RequestOptions } from "@vantigo/frontend-api-client";
export { ApiValidationError, NotFoundError, readJson } from "@vantigo/frontend-api-client";
