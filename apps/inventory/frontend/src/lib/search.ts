/**
 * The host routes' validateSearch functions (placeholders until Task A6a
 * settles each page's search): what a URL may carry, typed.
 */
export interface LookupSearch {
  q?: string;
}
export const validateLookupSearch = (search: Record<string, unknown>): LookupSearch => ({
  q: typeof search.q === "string" && search.q !== "" ? search.q : undefined,
});

export interface MovementsSearch {
  siteId?: number;
  locationId?: number;
  variantId?: number;
  kind?: "adjustment" | "move" | "receipt" | "count" | "issue";
  from?: string;
  to?: string;
}
const positive = (value: unknown): number | undefined => {
  const n = typeof value === "number" ? value : typeof value === "string" ? Number(value) : Number.NaN;
  return Number.isSafeInteger(n) && n > 0 ? n : undefined;
};
const kinds = ["adjustment", "move", "receipt", "count", "issue"] as const;
export const validateMovementsSearch = (search: Record<string, unknown>): MovementsSearch => ({
  siteId: positive(search.siteId),
  locationId: positive(search.locationId),
  variantId: positive(search.variantId),
  kind: kinds.find((k) => k === search.kind),
  from: typeof search.from === "string" ? search.from : undefined,
  to: typeof search.to === "string" ? search.to : undefined,
});

/** What a new movement's form opens prefilled with: the site, a location (Receive here, Move from here) and a variant. */
export interface NewMovementSearch {
  siteId?: number;
  locationId?: number;
  variantId?: number;
}
export const validateNewMovementSearch = (search: Record<string, unknown>): NewMovementSearch => ({
  siteId: positive(search.siteId),
  locationId: positive(search.locationId),
  variantId: positive(search.variantId),
});

/** The counts list's search: `new` opens the Open count form, as the spotlight's Start count asks. */
export interface CountsSearch {
  new?: 1;
}
export const validateCountsSearch = (search: Record<string, unknown>): CountsSearch =>
  search.new === 1 || search.new === "1" || search.new === true || search.new === "true" ? { new: 1 } : {};
