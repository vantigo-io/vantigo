import type { VatCode } from "../api/vat-codes";

/** The rate a VAT code has on a day: the period covering it, or none. */
export const rateOn = (code: VatCode, day: string): number | undefined =>
  code.rates.find((r) => r.validFrom <= day && (!r.validTo || r.validTo >= day))?.ratePercent;

/**
 * A line's category and rate as the server totals a draft (D5): the code's
 * rate in force today, whether or not the code is still offered, and 0 % when
 * no period covers today — the draft then carries the warning
 * `vat_code_not_valid`, and the issue refuses it.
 */
export const draftRate = (code: VatCode | undefined, today: string): { category: string; ratePercent: number } => ({
  category: code?.ehfCategory ?? "",
  ratePercent: code ? (rateOn(code, today) ?? 0) : 0,
});
