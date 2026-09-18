import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";

/**
 * A cross-module read. The caller's own effective access, as the platform's
 * identity module answers it. The key is the host's own, verbatim
 * (`["authorization", "me", "none"]`, as `__root.tsx` and the module access
 * guard write it), so the page reads the answer the host has already cached
 * rather than asking for it again.
 */
export interface EffectiveAccess {
  permissions: string[];
}

export const accessQueryOptions = () =>
  queryOptions({
    queryKey: ["authorization", "me", "none"],
    queryFn: ({ signal }) => request<EffectiveAccess>("/api/v1/identity/access/me", { signal }),
    staleTime: 5 * 60 * 1000,
  });
