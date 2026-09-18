import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";

/**
 * A cross-module read. The caller's own effective access, as the platform's
 * identity module answers it — the same endpoint and query key the host uses,
 * so both share one cached answer.
 */
export interface EffectiveAccess {
  permissions: string[];
}

export const accessQueryOptions = () =>
  queryOptions({
    queryKey: ["authorization", "me"],
    queryFn: ({ signal }) => request<EffectiveAccess>("/api/v1/identity/access/me", { signal }),
    staleTime: 5 * 60 * 1000,
  });
