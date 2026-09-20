import type { ExpenseRate } from "../api/rates";

/**
 * Every rate kind the contract knows (design §3.4), in the order the settings
 * page lists them. The mileage three are what this delivery prices with; the
 * per diem and meal kinds are the travel claims of a later delivery, and are
 * shown only once somebody has put a row in one.
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

/** The three this delivery uses. They are always listed, empty or not, so a rate can be added. */
export const mileageRateKinds: readonly RateKind[] = ["mileage", "mileage_passenger", "mileage_customer"];

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

/** Whether the kind belongs to the travel claims that have not shipped yet. */
export const isClaimRateKind = (kind: string): boolean => kind.startsWith("per_diem") || isPercentageRateKind(kind);

export interface RateGroup {
  kind: string;
  /** The kind's rows, the latest day first, the way the API answers them. */
  rates: ExpenseRate[];
}

/**
 * The rate table grouped for the settings page: the mileage kinds always, in
 * their own order, then every other kind that actually carries a row. A kind
 * the contract does not know still gets a group of its own rather than being
 * dropped — a client that hides a row nobody can then edit is worse than one
 * that shows an unfamiliar name.
 */
export const groupRatesByKind = (rates: ExpenseRate[] | undefined): RateGroup[] => {
  const byKind = new Map<string, ExpenseRate[]>();
  for (const rate of rates ?? []) byKind.set(rate.kind, [...(byKind.get(rate.kind) ?? []), rate]);
  const ordered = [...mileageRateKinds, ...rateKinds.filter((kind) => !mileageRateKinds.includes(kind))];
  const groups: RateGroup[] = [];
  for (const kind of ordered) {
    const rows = byKind.get(kind);
    if (!rows && !mileageRateKinds.includes(kind)) continue;
    groups.push({ kind, rates: rows ?? [] });
  }
  for (const [kind, rows] of byKind) {
    if (!(ordered as readonly string[]).includes(kind)) groups.push({ kind, rates: rows });
  }
  return groups;
};
