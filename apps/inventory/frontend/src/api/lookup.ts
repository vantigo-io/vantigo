import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVENTORY_QUERY_KEY, queryString, request } from "./request";

type Schemas = components["schemas"];

export type InventoryLookupMatch = Schemas["InventoryLookupMatch"];

/**
 * What a scanned or typed code names (I28): a location by its LOC token or
 * full code, then a variant by its SKU or barcode — each only as far as the
 * caller may see it. Locations come first.
 */
export const lookupQueryOptions = (q: string) =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "lookup", q],
    queryFn: ({ signal }) =>
      request<Schemas["InventoryLookupResult"]>(`/api/v1/inventory/lookup${queryString({ q })}`, { signal }).then(
        (answer) => answer.matches,
      ),
    // A scanner sends the same code again and again: the answer is reused for
    // a while rather than asked for at every scan.
    staleTime: 30_000,
  });
