import type { InventoryLowStockRow } from "../api/placements";
import type { InventoryPackaging } from "../api/stock";
import type { InventoryTransferInput, InventoryTransferSuggestion } from "../api/transfers";
import { roundQuantity } from "./lines";

/** The key a low row is selected under: one variant at one site. */
export const lowStockKey = (row: Pick<InventoryLowStockRow, "variant" | "site">) => `${row.variant.id}@${row.site.id}`;

/** The rows of every page read so far, each variant at each site once, where it was first shown. */
export const uniqueLowStockRows = (rows: readonly InventoryLowStockRow[]): InventoryLowStockRow[] => {
  const seen = new Set<string>();
  return rows.filter((row) => {
    const key = lowStockKey(row);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
};

/**
 * The site with most available across the chosen rows (I59), each other
 * site's available summed in thousandths (I11); undefined when none has any
 * of them.
 */
export const richestSource = (rows: readonly InventoryLowStockRow[]): number | undefined => {
  const totals = new Map<number, number>();
  for (const row of rows)
    for (const one of row.elsewhere)
      totals.set(one.siteId, (totals.get(one.siteId) ?? 0) + Math.round(one.available * 1000));
  let best: number | undefined;
  let most = 0;
  for (const [siteId, total] of totals)
    if (total > most) {
      best = siteId;
      most = total;
    }
  return best;
};

/** A draft transfer of the chosen rows (I59): each one's suggested quantity, with the suggestion it came from (I57). */
export const lowStockTransfer = (
  rows: readonly InventoryLowStockRow[],
  fromSiteId: number,
  toSiteId: number,
): InventoryTransferInput => ({
  fromSiteId,
  toSiteId,
  lines: rows.map((row) => ({
    variantId: row.variant.id,
    quantity: row.suggested,
    suggestion: { needed: row.needed, packagingId: row.packaging?.id ?? null, count: row.packaging?.count ?? null },
  })),
});

/**
 * A drafted transfer line's suggestion (I57) as the note's terms: the packaging
 * found among the variant's by its id, the suggested quantity worked out from
 * its count. A packaging the variant no longer has says what was needed alone.
 */
export const suggestionOfLine = (
  suggestion: InventoryTransferSuggestion,
  packagings: readonly InventoryPackaging[] | undefined,
): { needed: number; suggested: number; packaging?: { kind: string; quantity: number; count: number } } => {
  const packaging = packagings?.find((one) => one.id === suggestion.packagingId);
  if (!packaging || suggestion.count === null) return { needed: suggestion.needed, suggested: suggestion.needed };
  return {
    needed: suggestion.needed,
    suggested: roundQuantity(packaging.quantity * suggestion.count),
    packaging: { kind: packaging.kind, quantity: packaging.quantity, count: suggestion.count },
  };
};
