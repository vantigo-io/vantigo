import { queryOptions } from "@tanstack/react-query";
import { GOOGLE_QUERY_KEY, request } from "./request";

/**
 * Whether the signed-in user holds the Owner role, read from the session the
 * host's shell reads (`GET /api/v1/identity/session`, its `user.roles`), as
 * Entra's page reads it — a module package cannot import the host. The
 * connection's writes are the Owner's (`policy:Owner+OwnerManagement`), so
 * the page shows its steps read-only to a `google:manage` holder who is not
 * one; the server refuses their writes regardless.
 */
export const callerIsOwnerQueryOptions = () =>
  queryOptions({
    queryKey: [GOOGLE_QUERY_KEY, "session-owner"],
    queryFn: async ({ signal }) =>
      (
        (await request<{ user?: { roles?: string[] } }>("/api/v1/identity/session", { signal })).user?.roles ?? []
      ).includes("Owner"),
    staleTime: Number.POSITIVE_INFINITY,
  });
