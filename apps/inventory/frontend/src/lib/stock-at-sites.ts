import { useQueries } from "@tanstack/react-query";
import { type InventoryStockAtSite, MAX_STOCK_AT_SITES_VARIANTS, stockAtSitesQueryOptions } from "../api/transfers";

/** The key a variant's stock at a site is found under. */
export const stockAtKey = (variantId: number, siteId: number) => `${variantId}@${siteId}`;

export interface StockAtSites {
  /** Each asked variant at each asked site; undefined until every batch has answered. */
  data: ReadonlyMap<string, InventoryStockAtSite> | undefined;
  isPending: boolean;
  error: Error | null;
}

/**
 * Variants' stock at sites (I53), for a transfer's two columns and its send
 * dialog's defaults: one `GET /stock/sites` per batch of at most 100 variants,
 * the ids sorted and de-duplicated so the same lines ask the same thing.
 */
export const useStockAtSites = (variantIds: readonly number[], siteIds: readonly (number | undefined)[]) => {
  const ids = [...new Set(variantIds)].sort((a, b) => a - b);
  const sites = [...new Set(siteIds.filter((one): one is number => one !== undefined))].sort((a, b) => a - b);
  const batches: number[][] = [];
  for (let at = 0; at < ids.length; at += MAX_STOCK_AT_SITES_VARIANTS)
    batches.push(ids.slice(at, at + MAX_STOCK_AT_SITES_VARIANTS));
  const enabled = sites.length > 0;
  return useQueries({
    queries: batches.map((batch) => ({ ...stockAtSitesQueryOptions(batch, sites), enabled })),
    combine: (results): StockAtSites => {
      const error = results.find((one) => one.error)?.error ?? null;
      const isPending = enabled && results.some((one) => one.isPending);
      if (!enabled || isPending || error) return { data: undefined, isPending, error };
      const found = new Map<string, InventoryStockAtSite>();
      for (const result of results)
        for (const one of result.data ?? []) found.set(stockAtKey(one.variantId, one.siteId), one);
      return { data: found, isPending: false, error: null };
    },
  });
};
