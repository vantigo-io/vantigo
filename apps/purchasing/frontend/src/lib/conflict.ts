import type { QueryClient } from "@tanstack/react-query";
import { isVersionConflict, PURCHASING_QUERY_KEY } from "../api/request";

/**
 * What a save's onError does first: a version conflict marks Purchasing's
 * reads stale without refetching them, so the form keeps what was typed
 * until *Load again* is chosen.
 */
export const markStaleOnConflict = (queryClient: QueryClient, error: unknown) =>
  isVersionConflict(error)
    ? queryClient.invalidateQueries({ queryKey: [PURCHASING_QUERY_KEY], refetchType: "none" })
    : Promise.resolve();
