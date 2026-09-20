import type { ExpenseRate } from "../api/rates";

/**
 * Every rate kind the contract knows (design §3.4), in the order the settings
 * page lists them: the three mileage kinds, the four per diem day types and
 * the three meal percentages. All ten are listed whether they carry a row or
 * not — a kind nobody can find is a kind nobody can price, and the one the
 * product deliberately leaves unseeded (`per_diem_overnight_other`) is exactly
 * the one an administrator has to be able to reach.
 *
 * **The source of truth is `ExpensesRateRequest.kind`'s description in
 * `openapi/expenses.yaml`** — OpenAPI 3.0 declares it as a plain string with
 * the names written out in prose, so there is no enum to generate from and
 * this list is hand-kept. `rate-kinds.test.ts` reads that sentence back out
 * of the generated `api-schema.d.ts` and fails if the two ever disagree.
 */
export const rateKinds = [
  "mileage",
  "mileage_passenger",
  "mileage_customer",
  "per_diem_6_12",
  "per_diem_over_12",
  "per_diem_overnight_hotel",
  "per_diem_overnight_other",
  "meal_breakfast_percent",
  "meal_lunch_percent",
  "meal_dinner_percent",
] as const;

export type RateKind = (typeof rateKinds)[number];

export const isRateKind = (value: unknown): value is RateKind =>
  typeof value === "string" && (rateKinds as readonly string[]).includes(value);

/**
 * A percentage kind carries no currency and a value between 0 and 100; a
 * money kind carries a three-letter code and a value greater than zero. The
 * form switches on this, and so does the server.
 */
export const isPercentageRateKind = (kind: string): boolean => kind.endsWith("_percent");

/** The `expenses` catalog key naming a kind, and the one naming what it is for. */
export const rateKindLabelKey = (kind: string): string => `rateKind_${kind}`;

/**
 * The sentence under a kind's heading, when it has one worth saying. Three
 * kinds need one, and each for a different reason:
 *
 * - `mileage_customer` is the company's own price, never a public rate, so it
 *   ships with no row;
 * - `per_diem_overnight_other` ships with none either, because the state
 *   agreement knows one overnight rate — a company that pays lodging without
 *   cooking facilities differently has to enter its own figure, and until it
 *   does a day of that type cannot be priced at all;
 * - a **meal percentage** with no row in force on a day makes a per diem with
 *   that meal covered refuse, rather than deducting nothing quietly.
 */
export const rateKindHintKey = (kind: string): string | undefined => {
  if (kind === "mileage_customer") return "rateKindCustomerPrice";
  if (kind === "per_diem_overnight_other") return "rateKindOvernightOtherHint";
  if (isPercentageRateKind(kind)) return "rateKindMealPercentHint";
  return undefined;
};

export interface RateGroup {
  kind: string;
  /** The kind's rows, the latest day first, the way the API answers them. */
  rates: ExpenseRate[];
}

/**
 * The rate table grouped for the settings page: **every kind the contract
 * knows, in its own order, carrying a row or not**, and then any kind the
 * table holds that this build has never heard of. A group with nothing in it
 * is what makes a rate addable — the unseeded `per_diem_overnight_other` and
 * `mileage_customer` exist only as empty groups until somebody fills them —
 * and a client that hid a row nobody can then edit would be worse than one
 * showing an unfamiliar name.
 */
export const groupRatesByKind = (rates: ExpenseRate[] | undefined): RateGroup[] => {
  const byKind = new Map<string, ExpenseRate[]>();
  for (const rate of rates ?? []) byKind.set(rate.kind, [...(byKind.get(rate.kind) ?? []), rate]);
  const groups: RateGroup[] = rateKinds.map((kind) => ({ kind, rates: byKind.get(kind) ?? [] }));
  for (const [kind, rows] of byKind) {
    if (!(rateKinds as readonly string[]).includes(kind)) groups.push({ kind, rates: rows });
  }
  return groups;
};
