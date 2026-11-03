import type { components } from "../api-schema";

type Packaging = Pick<components["schemas"]["InventoryPackaging"], "id" | "name" | "quantity">;
type EnteredSnapshot = components["schemas"]["InventoryEnteredSnapshot"];

/** So many of one packaging. */
export interface BreakdownPart {
  packagingId: number;
  name: string;
  count: number;
}

/** A quantity in packagings, largest first, and the rest in the variant's own unit. */
export interface Breakdown {
  parts: BreakdownPart[];
  loose: number;
}

/** Quantities are exact to three decimals (I11): the arithmetic runs in thousandths, as whole numbers. */
const thousandths = (n: number) => Math.round(n * 1000);

/**
 * A quantity broken down greedily into the variant's packagings, largest
 * first, and the loose rest (I26): 1,830 with a pallet of 600 and a box of 15
 * is 3 pallets, 2 boxes and nothing loose. Undefined when no packaging fits —
 * no packagings, nothing to break down, or less than the smallest — so the
 * base quantity is shown alone.
 */
export const breakdown = (quantity: number, packagings: readonly Packaging[] = []): Breakdown | undefined => {
  let rest = thousandths(quantity);
  if (rest <= 0) return undefined;
  const largestFirst = packagings
    .map((packaging, index) => ({ packaging, index, size: thousandths(packaging.quantity) }))
    .filter((one) => one.size > 0)
    .sort((a, b) => b.size - a.size || a.index - b.index);
  const parts: BreakdownPart[] = [];
  for (const { packaging, size } of largestFirst) {
    const count = Math.floor(rest / size);
    if (count === 0) continue;
    parts.push({ packagingId: packaging.id, name: packaging.name, count });
    rest -= count * size;
  }
  return parts.length > 0 ? { parts, loose: rest / 1000 } : undefined;
};

type FormatNumber = (n: number) => string;

/** A breakdown as words: `3 Euro pallet, 2 Box`, and the rest with its unit when there is one: `, 5 pcs`. */
export const breakdownText = (value: Breakdown, unit: string, format: FormatNumber): string =>
  [
    ...value.parts.map((part) => `${format(part.count)} ${part.name}`),
    ...(value.loose > 0 ? [`${format(value.loose)} ${unit}`] : []),
  ].join(", ");

/** What a line was entered as (I25): `3 Euro pallet + 2 Box`, and its loose rest: `+ 5 pcs`. */
export const enteredText = (entered: EnteredSnapshot, unit: string, format: FormatNumber): string =>
  [
    ...entered.packagings.map((one) => `${format(one.count)} ${one.name}`),
    ...(entered.loose ? [`${format(entered.loose)} ${unit}`] : []),
  ].join(" + ");

/** The base quantity a set of packaging counts and a loose rest make, exact to three decimals. */
export const packedQuantity = (
  packagings: readonly Packaging[],
  counts: Readonly<Record<number, number>>,
  loose: number,
): number => {
  let total = thousandths(loose);
  for (const packaging of packagings) total += (counts[packaging.id] ?? 0) * thousandths(packaging.quantity);
  return total / 1000;
};
