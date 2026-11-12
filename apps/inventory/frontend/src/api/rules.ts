import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVENTORY_QUERY_KEY, json, request, send } from "./request";

export type InventoryVariantRules = components["schemas"]["InventoryVariantRules"];

/** A variant's rules in Inventory (manufacturing phase 2 M100): inspect on receipt. */
export const variantRulesQueryOptions = (variantId: number) =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "variant", variantId, "rules"],
    queryFn: ({ signal }) =>
      request<InventoryVariantRules>(`/api/v1/inventory/variants/${variantId}/rules`, { signal }),
  });

/** Sets the variant's rules under the revision read (0 while it has none). */
export const putVariantRules = (variantId: number, inspectOnReceipt: boolean, revision: number) =>
  send<InventoryVariantRules>(
    `/api/v1/inventory/variants/${variantId}/rules`,
    json("PUT", { inspectOnReceipt, revision }),
  );
