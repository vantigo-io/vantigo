import { createApiClient } from "@vantigo/frontend-api-client";
import { appUrl } from "@vantigo/frontend-shell";

const client = createApiClient({ resolveUrl: appUrl });

// Tenant-prefixed API routing is shared package state so that every module's
// API client (customers, products, energy, ...) applies the same prefix.
export { setActiveTenantSlug, setTenantRoutingEnabled } from "@vantigo/frontend-api-client";

export const { clearCsrfToken, ensureCsrfToken, getCsrfToken, request, setAuthStateClearer, setUnauthorizedHandler } =
  client;
export type { ApiError, RequestOptions } from "@vantigo/frontend-api-client";
export { ApiValidationError, NotFoundError, readJson } from "@vantigo/frontend-api-client";
