import type { ExpenseRate } from "../api/rates";
import { round2 } from "./money";

/** The rate kinds this delivery reads. Per diem's kinds arrive with travel claims. */
export const MILEAGE_RATE = "mileage";
export const MILEAGE_PASSENGER_RATE = "mileage_passenger";

/**
 * The rate of a kind in force on a date: the row with the greatest `validFrom`
 * on or before it (contract, `ExpensesRateResponse`). Undefined when the table
 * says nothing about that day — the case a mileage line cannot be submitted in.
 */
export const rateOn = (rates: ExpenseRate[] | undefined, kind: string, date: string): ExpenseRate | undefined => {
  let best: ExpenseRate | undefined;
  for (const rate of rates ?? []) {
    if (rate.kind !== kind || rate.validFrom > date) continue;
    if (best === undefined || rate.validFrom > best.validFrom) best = rate;
  }
  return best;
};

export interface MileagePreview {
  /** The reimbursement rate per kilometre in force on the day. */
  rate: number;
  /** The passenger supplement per kilometre, when the line carries passengers. */
  passengerRate?: number;
  /** What the server would price the line at today. It is a preview, not the truth. */
  amount: number;
}

/**
 * What a mileage line is worth on its own date (design §4):
 * `km × rate + km × passenger rate × passengers`, half-up to two places.
 *
 * It is worked out here only so the form can show a figure while it is being
 * typed. The server prices the line again on every save and one last time on
 * submit, and *that* figure is the one that counts — the preview is labelled
 * as one. Undefined when no rate applies on the date, or when the line carries
 * passengers and no supplement does.
 */
export const mileagePreview = (
  rates: ExpenseRate[] | undefined,
  date: string,
  distanceKm: number,
  passengers: number,
): MileagePreview | undefined => {
  const rate = rateOn(rates, MILEAGE_RATE, date);
  if (!rate) return undefined;
  if (passengers <= 0) return { rate: rate.value, amount: round2(distanceKm * rate.value) };
  const supplement = rateOn(rates, MILEAGE_PASSENGER_RATE, date);
  if (!supplement) return undefined;
  return {
    rate: rate.value,
    passengerRate: supplement.value,
    amount: round2(distanceKm * rate.value + distanceKm * supplement.value * passengers),
  };
};
