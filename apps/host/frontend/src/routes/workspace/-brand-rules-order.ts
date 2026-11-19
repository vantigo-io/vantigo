import type { BrandRuleKind } from "../../api/brands";

/** Where a new rule goes by default (brands design B8): domains, then legal entities, then groups. */
const KIND_RANK: Record<BrandRuleKind, number> = { domain: 0, legal_entity: 1, access_group: 2 };

/** The list with the item at `from` moved to `to`; the same list when either is out of range. */
export function moveItem<T>(list: readonly T[], from: number, to: number): T[] {
  if (from === to || from < 0 || to < 0 || from >= list.length || to >= list.length) return [...list];
  const next = [...list];
  const [item] = next.splice(from, 1);
  next.splice(to, 0, item as T);
  return next;
}

/** The index a new rule of `kind` is inserted at: after the last rule of its kind or of an earlier one. */
export function insertIndexFor(list: readonly { kind: BrandRuleKind }[], kind: BrandRuleKind): number {
  let at = 0;
  list.forEach((item, i) => {
    if (KIND_RANK[item.kind] <= KIND_RANK[kind]) at = i + 1;
  });
  return at;
}
