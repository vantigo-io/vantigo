import { queryOptions } from "@tanstack/react-query";
import { ENTRA_QUERY_KEY, request } from "./request";

/**
 * Whether the signed-in user holds the Owner role, read from the session the
 * host's shell reads (`GET /api/v1/identity/session`, its `user.roles`) — a
 * module package cannot import the host, and calendar reads the same endpoint
 * for the caller's id. Steps 1 to 3 are the Owner's (`policy:OwnerManagement`),
 * so the page shows them read-only to anyone else; the server refuses their
 * writes regardless (plan reading 7).
 */
export const callerIsOwnerQueryOptions = () =>
  queryOptions({
    queryKey: [ENTRA_QUERY_KEY, "session-owner"],
    queryFn: async ({ signal }) =>
      (
        (await request<{ user?: { roles?: string[] } }>("/api/v1/identity/session", { signal })).user?.roles ?? []
      ).includes("Owner"),
    staleTime: Number.POSITIVE_INFINITY,
  });
