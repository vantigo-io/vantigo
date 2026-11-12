import type {
  ManufacturingLotChoice,
  ManufacturingProductionDraftLine,
  ManufacturingProductionLotInput,
} from "../api/orders";
import { addDecimal, compareDecimal, normalizeDecimal, subtractDecimal } from "./decimal";

type DraftLot = ManufacturingProductionDraftLine["lots"][number];

/** A lot number (manufacturing phase 2 M52): 1–20 letters, digits and . _ / -. */
export const LOT_NUMBER = /^[A-Za-z0-9._/-]{1,20}$/;

const two = (n: number) => String(n).padStart(2, "0");

/**
 * A lot pattern rendered as the server renders it (M80), for the BoM
 * editor's preview: {MO} as an order number, the date tokens from day,
 * {SKU} as given and {N} as a running number. Anything else is literal.
 */
export const renderLotPattern = (pattern: string, sku: string, day = new Date(), mo = "MO-1001", n = 1): string =>
  pattern.replace(/\{[A-Z]+\}/g, (token) => {
    const year = day.getFullYear();
    switch (token) {
      case "{MO}":
        return mo;
      case "{YYYY}":
        return String(year);
      case "{YY}":
        return two(year % 100);
      case "{MM}":
        return two(day.getMonth() + 1);
      case "{DD}":
        return two(day.getDate());
      case "{YYMMDD}":
        return `${two(year % 100)}${two(day.getMonth() + 1)}${two(day.getDate())}`;
      case "{SKU}":
        return sku;
      case "{N}":
        return String(n);
      default:
        return token;
    }
  });

/**
 * The pattern rendered as the server judges it on a BoM's save
 * (lot_pattern_invalid): an order number and a counter of the widths a busy
 * shop reaches, on the last day of a year. A pattern whose preview fits may
 * still outgrow twenty characters on a later order.
 */
export const sampleLotNumber = (pattern: string, sku: string): string =>
  renderLotPattern(pattern, sku, new Date(2026, 11, 31), "MO-99999-9", 9999);

/** One lot a component line takes from, as the production dialog edits it. */
export interface LotRowValues {
  lotId: number | null;
  locationId: number | null;
  quantity: string;
  allowExpired: boolean;
}

/** What the rows take together, "0" for none; undefined while a quantity is not a decimal. */
export const sumLotRows = (rows: readonly LotRowValues[], isDecimal: (text: string) => boolean): string | undefined => {
  let sum = "0";
  for (const row of rows) {
    if (row.quantity.trim() === "" || !isDecimal(row.quantity)) return undefined;
    sum = addDecimal(sum, normalizeDecimal(row.quantity));
  }
  return sum;
};

/**
 * The rows as the production's `lots`: each row's quantity is consumed first,
 * then scrapped, in the rows' order, so the lots sum to the line's consumed
 * and scrap (picks_mismatch otherwise).
 */
export const lotRowsBody = (rows: readonly LotRowValues[], consumed: string): ManufacturingProductionLotInput[] => {
  let left = consumed;
  return rows.flatMap((row) => {
    if (row.lotId === null || row.locationId === null) return [];
    const quantity = normalizeDecimal(row.quantity);
    const take = compareDecimal(quantity, left) <= 0 ? quantity : left;
    left = subtractDecimal(left, take);
    const scrapped = subtractDecimal(quantity, take);
    return [
      {
        lotId: row.lotId,
        locationId: row.locationId,
        consumed: take,
        scrapped,
        ...(row.allowExpired ? { allowExpired: true } : {}),
      },
    ];
  });
};

/**
 * The draft's proposal made again at one location, as Inventory takes the
 * lots there with AutoLot (M65): the site's lots the draft lists, in their
 * first-expiry-first order, skipping a held or expired one and a serial
 * larger than what is left. short is what the location does not cover.
 */
export const proposeAt = (
  choices: readonly ManufacturingLotChoice[],
  total: string,
  locationId: number,
): { lots: DraftLot[]; short: string } => {
  let left = total;
  const lots: DraftLot[] = [];
  for (const choice of choices) {
    if (compareDecimal(left, "0") <= 0) break;
    if (choice.locationId !== locationId || choice.status !== "available" || choice.expired) continue;
    if (choice.serial && compareDecimal(choice.quantity, left) > 0) continue;
    const take = compareDecimal(choice.quantity, left) <= 0 ? normalizeDecimal(choice.quantity) : left;
    left = subtractDecimal(left, take);
    lots.push({
      lotId: choice.lotId,
      lotNumber: choice.lotNumber,
      expiry: choice.expiry ?? null,
      locationId: choice.locationId,
      fullCode: choice.fullCode,
      quantity: take,
    });
  }
  return { lots, short: compareDecimal(left, "0") > 0 ? left : "0" };
};

/**
 * Why a chosen row cannot be posted, by row: a quantity of 0, or a lot and
 * location another row names already (the server's 400 otherwise).
 */
export const lotRowProblems = (rows: readonly LotRowValues[], isDecimal: (text: string) => boolean) => {
  const seen = new Set<string>();
  return rows.map((row) => {
    const key = row.lotId !== null && row.locationId !== null ? `${row.lotId}:${row.locationId}` : undefined;
    const twice = key !== undefined && seen.has(key);
    if (key !== undefined) seen.add(key);
    const zero = isDecimal(row.quantity) && compareDecimal(normalizeDecimal(row.quantity), "0") <= 0;
    return { twice, zero };
  });
};
