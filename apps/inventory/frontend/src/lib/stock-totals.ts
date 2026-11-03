import { useQueries } from "@tanstack/react-query";
import { MAX_STOCK_TOTAL_IDS, stockTotalsQueryOptions } from "../api/stock";

export interface StockTotals {
  /** Each asked variant's on hand, 0 for one with none; undefined until every batch has answered. */
  data: ReadonlyMap<number, number> | undefined;
  isPending: boolean;
  error: Error | null;
}

export interface UseStockTotalsOptions {
  /** False holds the requests back, as for a caller without both views (design §5). */
  enabled?: boolean;
}

/**
 * The on hand of each of a page's variants, for the product list's column
 * (I33): one `GET /stock` per batch of at most 100 ids, the ids sorted and
 * de-duplicated so the same page asks the same thing.
 */
export const useStockTotals = (
  variantIds: readonly number[],
  { enabled = true }: UseStockTotalsOptions = {},
): StockTotals => {
  const ids = [...new Set(variantIds)].sort((a, b) => a - b);
  const batches: number[][] = [];
  for (let at = 0; at < ids.length; at += MAX_STOCK_TOTAL_IDS) batches.push(ids.slice(at, at + MAX_STOCK_TOTAL_IDS));
  return useQueries({
    queries: batches.map((batch) => ({ ...stockTotalsQueryOptions(batch), enabled })),
    combine: (results) => {
      const error = results.find((one) => one.error)?.error ?? null;
      const isPending = enabled && results.some((one) => one.isPending);
      if (!enabled || isPending || error) return { data: undefined, isPending, error };
      const totals = new Map<number, number>(ids.map((id) => [id, 0]));
      for (const result of results) for (const total of result.data ?? []) totals.set(total.variantId, total.onHand);
      return { data: totals, isPending: false, error: null };
    },
  });
};
