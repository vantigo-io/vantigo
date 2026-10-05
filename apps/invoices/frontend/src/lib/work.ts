import { WORK_KINDS } from "../api/work";

/** One piece of work the panel's selection hands the wizard: what the request names, and what a line is keyed on. */
export interface ChosenWork {
  kind: string;
  id: number;
  revision: number;
  projectId: number;
  date: string;
  amount: number;
  currency: string;
  /** An hour's effective rate, its person and its work type — what splits hours into lines (D4). */
  rate?: number;
  userId?: string;
  workTypeId?: number;
  /** An expense's kind — outlay, mileage or supplier_invoice — which groups expenses under every grouping but itemised. */
  expenseKind?: string;
  /** How the panel names the row — the person and the day, an expense's description, a milestone's name — so a refusal can say which. */
  label: string;
}

/** The groupings, finest last, as the server takes them (D4). */
export const GROUPINGS = ["project", "work_type", "person", "date", "itemised"] as const;
export type Grouping = (typeof GROUPINGS)[number];

/**
 * How many lines a grouping makes of the work, by the server's rule (D4): the
 * key is always the project and the kind; a milestone is always its own line;
 * expenses group by their expense kind under every grouping but itemised;
 * hours by the grouping's term and, within it, their effective rate, since a
 * line has one unit price.
 */
export const linesFor = (grouping: Grouping, work: ChosenWork[]): number => {
  const keyOf = (w: ChosenWork): string => {
    if (w.kind === WORK_KINDS.milestones || grouping === "itemised") return `${w.kind}:${w.id}`;
    if (w.kind === WORK_KINDS.expenses) return `${w.projectId}:${w.kind}:${w.expenseKind ?? ""}`;
    const term =
      grouping === "work_type"
        ? String(w.workTypeId ?? "")
        : grouping === "person"
          ? (w.userId ?? "")
          : grouping === "date"
            ? w.date
            : "";
    return `${w.projectId}:${w.kind}:${term}:${w.rate ?? ""}`;
  };
  return new Set(work.map(keyOf)).size;
};

/** The work's amount per currency, in the order the currencies first appear. */
export const totalsOf = (work: ChosenWork[]): { currency: string; amount: number }[] => {
  const totals = new Map<string, number>();
  for (const w of work) totals.set(w.currency, Math.round(((totals.get(w.currency) ?? 0) + w.amount) * 100) / 100);
  return [...totals].map(([currency, amount]) => ({ currency, amount }));
};
