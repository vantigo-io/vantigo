/**
 * Half-up to two places, the way the server rounds (`math/big.Rat`, half-up).
 * `Number.EPSILON` is nudged in first so that a value the binary
 * representation left a hair under — 1.005 is really 1.00499999… — still
 * rounds up rather than down.
 */
export const round2 = (value: number): number => Math.round((value + Number.EPSILON) * 100) / 100;

/** One decimal, the precision the contract allows a distance. */
export const round1 = (value: number): number => Math.round((value + Number.EPSILON) * 10) / 10;

/** The VAT rates the helper offers, as whole percentages. `"none"` clears the choice. */
export const vatRates = [25, 15, 12] as const;

export type VatRate = (typeof vatRates)[number];

export type VatChoice = VatRate | "none";

export const isVatRate = (value: unknown): value is VatRate =>
  typeof value === "number" && (vatRates as readonly number[]).includes(value);

/**
 * The VAT inside a gross amount at a rate — `gross × r / (100 + r)`, half-up
 * to two places. The helper only ever *fills* the VAT field: the number on
 * the wire is whatever stands in the field when the form is saved, so a hand
 * edit wins and the server has the last word either way.
 */
export const vatFromGross = (gross: number, rate: VatRate): number => round2((gross * rate) / (100 + rate));

/** The gross less the VAT — what the reader is shown beside the VAT field. */
export const netOf = (gross: number, vat: number): number => round2(gross - vat);

/** The largest gross the contract takes; anything above is a 400 on the amount. */
export const MAX_GROSS = 9_999_999_999.99;

/** The largest distance the contract takes on a mileage line. */
export const MAX_DISTANCE_KM = 9999.9;

/** How many passengers a mileage line may carry. */
export const MAX_PASSENGERS = 8;

/** What the contract allows in a description. */
export const DESCRIPTION_MAX_LENGTH = 500;

/** What the contract allows in a supplier, a from-place and a to-place. */
export const PLACE_MAX_LENGTH = 200;

/** What the contract allows in a supplier's invoice number. */
export const INVOICE_NUMBER_MAX_LENGTH = 100;
