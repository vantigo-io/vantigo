import type { QueryClient } from "@tanstack/react-query";
import { ASSISTANT_QUERY_KEY } from "../api/request";

/**
 * After a confirmed change succeeds, the screen the person is on must show it
 * (design S12). The host's modules do not key their queries by one convention
 * (customers uses `customers` and `contacts`, invoices `invoices`, the tax
 * categories `tax-categories`, and a change in one module shows in another's
 * lists), so a map from an operation to "its module's" keys would be wrong as
 * often as right. This marks every query stale except the assistant's own and
 * refetches the ones on screen — `invalidateQueries` refetches only active
 * queries — which is both simpler and correct.
 */
export const invalidateAfterAssistantWrites = (queryClient: QueryClient): void => {
  void queryClient.invalidateQueries({ predicate: (query) => query.queryKey[0] !== ASSISTANT_QUERY_KEY });
};
