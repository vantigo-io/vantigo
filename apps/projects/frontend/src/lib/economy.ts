import { useI18n } from "@vantigo/frontend-shell";
import "../i18n";
import { isProjectStatus, type ProjectStatus } from "./status";

/**
 * Which budget the percentages are measured against, in the order the module
 * picks it: the project's budget amount, then the fixed price of a fixed-price
 * project, then the budget hours. A caller who may not see amounts only ever
 * gets the hours basis.
 */
export const budgetBases = ["amount", "fixedPrice", "hours"] as const;

export type BudgetBasis = (typeof budgetBases)[number];

export const isBudgetBasis = (value: string): value is BudgetBasis =>
  (budgetBases as readonly string[]).includes(value);

/** Money or hours — the unit a bar drawn on a basis is measured in. */
export type BudgetUnit = "hours" | "amount";

/**
 * A bar with no basis at all is drawn in hours: hours are the one figure
 * everybody who sees the project is given.
 */
export const budgetUnit = (basis: BudgetBasis | undefined): BudgetUnit =>
  basis === "amount" || basis === "fixedPrice" ? "amount" : "hours";

/** One of the three buckets as a bar draws it. The amount is absent for a caller who may not see money. */
export interface BudgetSegment {
  hours: number;
  amount?: number | null;
}

export interface BudgetSegments {
  approved: BudgetSegment;
  submitted: BudgetSegment;
  draft: BudgetSegment;
}

/** What one bucket contributes to a bar in the bar's own unit. */
export const segmentValue = (segment: BudgetSegment, unit: BudgetUnit): number =>
  unit === "hours" ? segment.hours : (segment.amount ?? 0);

export interface BudgetBarGeometry {
  /** Each bucket's share of the bar's full width, in percent. */
  widths: { approved: number; submitted: number; draft: number };
  /** Where the budget marker stands, in percent; absent when there is no budget. */
  marker?: number;
  /** The stretch past the marker, in percent; absent when nothing has overrun. */
  overflow?: { from: number; to: number };
  total: number;
  scale: number;
}

/**
 * Where the three buckets, the budget marker and the overflow sit along the
 * bar. The scale is `max(total, budget)`, so an over-budget bar keeps its
 * marker inside itself instead of drawing past its own end, and a bar with no
 * budget is simply the three buckets against their own total.
 */
export const budgetBarGeometry = (
  segments: BudgetSegments,
  unit: BudgetUnit,
  budget: number | null | undefined,
): BudgetBarGeometry => {
  const values = {
    approved: segmentValue(segments.approved, unit),
    submitted: segmentValue(segments.submitted, unit),
    draft: segmentValue(segments.draft, unit),
  };
  const total = values.approved + values.submitted + values.draft;
  const budgeted = budget !== null && budget !== undefined && budget > 0 ? budget : undefined;
  const scale = Math.max(total, budgeted ?? 0);
  const share = (value: number) => (scale > 0 ? (value / scale) * 100 : 0);

  const marker = budgeted === undefined ? undefined : share(budgeted);
  return {
    widths: { approved: share(values.approved), submitted: share(values.submitted), draft: share(values.draft) },
    marker,
    overflow: budgeted !== undefined && total > budgeted ? { from: share(budgeted), to: share(total) } : undefined,
    total,
    scale,
  };
};

/** The orders the portfolio can be read in. */
export const economySorts = ["budgetUsed", "readyAmount", "nextMilestone", "code"] as const;

export type EconomySort = (typeof economySorts)[number];

export const isEconomySort = (value: string): value is EconomySort =>
  (economySorts as readonly string[]).includes(value);

const sortLabelKeys: Record<EconomySort, string> = {
  budgetUsed: "sortBudgetUsed",
  readyAmount: "sortReadyAmount",
  nextMilestone: "sortNextMilestone",
  code: "sortCode",
};

/** The `projects` catalog key naming this order. */
export const economySortLabelKey = (sort: EconomySort): string => sortLabelKeys[sort];

/** The portfolio's URL search params, which the host route validates. */
export interface EconomyPortfolioSearch {
  page: number;
  search: string;
  /** "all" asks for every status; the default is the work being done now. */
  status: ProjectStatus | "all";
  customerId?: number;
  overBudget: boolean;
  hasReady: boolean;
  sort: EconomySort;
}

const flag = (value: unknown): boolean => value === true || value === "true";

const optionalId = (value: unknown): number | undefined => {
  const id = Number(value);
  return value !== undefined && value !== null && value !== "" && Number.isInteger(id) && id > 0 ? id : undefined;
};

/**
 * The portfolio's search params as the router should hold them. Everything a
 * hand-edited URL invents falls back to the default rather than reaching the
 * API as a value it would refuse — the page is filterable by link, and a link
 * from anywhere must still open.
 */
export const validateEconomyPortfolioSearch = (search: Record<string, unknown>): EconomyPortfolioSearch => {
  const status = String(search.status ?? "");
  const sort = String(search.sort ?? "");
  return {
    page: Math.max(1, Number(search.page) || 1),
    search: typeof search.search === "string" ? search.search : "",
    status: status === "all" || isProjectStatus(status) ? status : "active",
    customerId: optionalId(search.customerId),
    overBudget: flag(search.overBudget),
    hasReady: flag(search.hasReady),
    sort: isEconomySort(sort) ? sort : "budgetUsed",
  };
};

/**
 * The one way this package writes hours, money and percentages of a budget.
 * An amount with no currency behind it is written as a bare number rather than
 * guessed at, and anything the API left out is the catalog's dash — never a
 * zero, which would be a different claim.
 */
export const useEconomyFormat = (currency?: string) => {
  const { t, formatters } = useI18n("projects");
  const hours = (value: number | null | undefined) =>
    value === null || value === undefined ? t("notAvailable") : t("hours", { hours: formatters.formatNumber(value) });
  const money = (value: number | null | undefined) =>
    value === null || value === undefined
      ? t("notAvailable")
      : currency
        ? formatters.formatCurrency(value, currency)
        : formatters.formatNumber(value);
  const percent = (value: number) => formatters.formatNumber(value, { maximumFractionDigits: 1 });
  const inUnit = (unit: BudgetUnit, value: number | null | undefined) =>
    unit === "hours" ? hours(value) : money(value);

  /**
   * What a percentage is a percentage of, said out loud: "of the budget
   * (480 000 kr)", "of the fixed price", "of 400 hours". A line's bar is named
   * the same way, because a line's percentage can be measured in money while
   * its remaining hours are hours. The portfolio knows the basis but not the
   * number behind it, so each basis also has a wording without one.
   */
  const basisPhrase = (basis: BudgetBasis, value?: number | null): string => {
    if (basis === "fixedPrice") return t("budgetBasisFixedPrice");
    if (value === null || value === undefined) {
      return basis === "amount" ? t("budgetBasisAmountAlone") : t("budgetBasisHoursAlone");
    }
    if (basis === "amount") return t("budgetBasisAmount", { amount: money(value) });
    return t("budgetBasisHours", { hours: formatters.formatNumber(value) });
  };

  return { hours, money, percent, inUnit, basisPhrase };
};
