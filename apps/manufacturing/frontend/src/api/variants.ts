import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { MANUFACTURING_QUERY_KEY, queryString, request } from "./request";

type Schemas = components["schemas"];

export type ManufacturingVariantRef = Schemas["ManufacturingVariantRef"];
export type ManufacturingVariantSearchItem = Schemas["ManufacturingVariantSearchItem"];

/** How many variants a picker asks for at a time; the server answers at most 50. */
export const VARIANT_SEARCH_LIMIT = 20;

/**
 * Goods variants of active products matching `q` by SKU or product name
 * (`GET /manufacturing/variants`), each with whether it has an active BoM —
 * Manufacturing's own search, so a planner needs none of Products' keys.
 */
export const variantSearchQueryOptions = (q: string, limit = VARIANT_SEARCH_LIMIT) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "variants", q, limit],
    queryFn: ({ signal }) =>
      request<Schemas["ManufacturingVariantSearchList"]>(`/api/v1/manufacturing/variants${queryString({ q, limit })}`, {
        signal,
      }).then((answer) => answer.items),
  });
