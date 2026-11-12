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
  kind?:
    | "adjustment"
    | "move"
    | "receipt"
    | "count"
    | "issue"
    | "transfer_out"
    | "transfer_in"
    | "production"
    | "unbuild";
  from?: string;
  to?: string;
}
const positive = (value: unknown): number | undefined => {
  const n = typeof value === "number" ? value : typeof value === "string" ? Number(value) : Number.NaN;
  return Number.isSafeInteger(n) && n > 0 ? n : undefined;
};
const kinds = [
  "adjustment",
  "move",
  "receipt",
  "count",
  "issue",
  "transfer_out",
  "transfer_in",
  "production",
  "unbuild",
] as const;
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

/**
 * The transfers list's filters (phase 2 design §7): the status — absent is
 * open (draft, sent, partly received), `all` every status — the from and the
 * to site, and a number or a SKU.
 */
export interface TransfersSearch {
  status?: "all" | "draft" | "sent" | "partly_received" | "received" | "cancelled";
  fromSiteId?: number;
  toSiteId?: number;
  q?: string;
}
const transferStatusFilters = ["all", "draft", "sent", "partly_received", "received", "cancelled"] as const;
export const validateTransfersSearch = (search: Record<string, unknown>): TransfersSearch => ({
  status: transferStatusFilters.find((one) => one === search.status),
  fromSiteId: positive(search.fromSiteId),
  toSiteId: positive(search.toSiteId),
  q: typeof search.q === "string" && search.q.trim() !== "" ? search.q : undefined,
});

/** What a new transfer's form opens with: the from and the to site. */
export interface NewTransferSearch {
  fromSiteId?: number;
  toSiteId?: number;
}
export const validateNewTransferSearch = (search: Record<string, unknown>): NewTransferSearch => ({
  fromSiteId: positive(search.fromSiteId),
  toSiteId: positive(search.toSiteId),
});

/** The low-stock list's search (phase 2 design §7): the site it is filtered to, and what it is searched for. */
export interface LowStockSearch {
  siteId?: number;
  /** A SKU or a name, as the list's search took it (≤ 100 characters, as the contract allows). */
  q?: string;
}
export const validateLowStockSearch = (search: Record<string, unknown>): LowStockSearch => ({
  siteId: positive(search.siteId),
  q: typeof search.q === "string" && search.q.trim() !== "" ? search.q.trim().slice(0, 100) : undefined,
});

/** The lots list's filters (manufacturing phase 2 M60): text, site, status, hold cause and stock, in the URL. */
export interface LotsSearch {
  q?: string;
  siteId?: number;
  variantId?: number;
  status?: "available" | "quarantine" | "blocked";
  holdCause?: "inspection" | "deviation" | "complaint" | "recall" | "other";
  /** "all" lists lots holding nothing too; absent lists only lots with stock. */
  stock?: "all";
}
const lotStatusValues = ["available", "quarantine", "blocked"] as const;
const holdCauseValues = ["inspection", "deviation", "complaint", "recall", "other"] as const;
/** The expiring list's filters (manufacturing phase 2 M72): days, site and status, in the URL. */
export interface ExpiringSearch {
  days?: number;
  siteId?: number;
  status?: "available" | "quarantine" | "blocked";
}
export const validateExpiringSearch = (search: Record<string, unknown>): ExpiringSearch => {
  const days = positive(search.days);
  return {
    days: days !== undefined && days <= 365 ? days : undefined,
    siteId: positive(search.siteId),
    status: lotStatusValues.find((value) => value === search.status),
  };
};

export const validateLotsSearch = (search: Record<string, unknown>): LotsSearch => ({
  q: typeof search.q === "string" && search.q !== "" ? search.q : undefined,
  siteId: positive(search.siteId),
  variantId: positive(search.variantId),
  status: lotStatusValues.find((value) => value === search.status),
  holdCause: holdCauseValues.find((value) => value === search.holdCause),
  stock: search.stock === "all" ? "all" : undefined,
});
