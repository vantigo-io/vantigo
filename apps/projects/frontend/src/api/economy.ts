import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import type { BudgetBasis, EconomyPortfolioSearch } from "../lib/economy";
import type { ProjectStatus } from "../lib/status";
import { request } from "./request";

export type { BudgetBasis, EconomyPortfolioSearch } from "../lib/economy";

type Schemas = components["schemas"];

/**
 * The contract declares the basis and the project status as plain strings
 * (OpenAPI 3.0 without enum members), so they are narrowed here to the exact
 * strings the backend writes. Everything else is the generated shape.
 */
export type BudgetUsed = Omit<Schemas["ProjectEconomyBudgetUsed"], "basis"> & { basis: BudgetBasis };

export type EconomyBucket = Schemas["ProjectEconomyBucket"];
export type EconomyActuals = Schemas["ProjectEconomyActuals"];
export type EconomyBudget = Schemas["ProjectEconomyBudget"];
export type EconomyCost = Schemas["ProjectEconomyCost"];
export type EconomyLine = Schemas["ProjectEconomyLine"];
/**
 * One work type's share of the logged work (work types design D4): hours for
 * everyone, `billAmount` with the project's money, `costAmount` with costs on
 * top — absent, never zero, when the caller may not see them. The list itself
 * is absent without time tracking.
 */
export type EconomyWorkType = Schemas["ProjectEconomyWorkType"];

/**
 * What the project's expenses cost and will bill. The block is there when the
 * installation tracks expenses *and* the caller may see the project's money;
 * the figures of the project's own currency are present or absent **together**,
 * on the project carrying a currency, so a currencyless project answers with an
 * object that has none of them — never with zeroes.
 */
export type EconomyExpenses = Schemas["ProjectEconomyExpenses"];
export type EconomyExpenseBucket = Schemas["ProjectEconomyExpenseBucket"];
export type EconomyExpenseCurrency = Schemas["ProjectEconomyExpenseCurrency"];

/**
 * A project's budget against what has been logged on it. Every optional field
 * is *absent*, never null and never zero: an absent `budgetUsed` is "no budget
 * to measure against", not 0 %, and absent `actuals` is "this installation
 * cannot say", not "nothing was logged".
 */
export type Economy = Omit<Schemas["ProjectEconomyResponse"], "budgetUsed"> & { budgetUsed?: BudgetUsed };

export type EconomyRowActuals = Schemas["ProjectEconomyRowActuals"];
export type EconomyNextMilestone = Schemas["ProjectEconomyNextMilestone"];
export type EconomyCustomer = Schemas["ProjectEconomyCustomer"];
export type EconomyReadyAmount = Schemas["ProjectEconomyReadyAmount"];
export type EconomyTotals = Schemas["ProjectEconomyTotals"];

export type EconomyRowProject = Omit<Schemas["ProjectEconomyRowProject"], "status"> & { status: ProjectStatus };

/** One project in the portfolio. Every row is one the caller may see the money of. */
export type EconomyRow = Omit<Schemas["ProjectEconomyRow"], "budgetUsed" | "project"> & {
  budgetUsed?: BudgetUsed;
  project: EconomyRowProject;
};

export type EconomyPortfolioPage = Omit<Schemas["ProjectEconomyListResponse"], "data"> & { data: EconomyRow[] };

export const ECONOMY_PORTFOLIO_PAGE_SIZE = 25;

/**
 * One project's economy. The key sits under `["projects", …]`, so the blanket
 * invalidation every write in this package does reaches it — a milestone
 * marked invoiced changes what is ready to invoice here too.
 *
 * The endpoint never answers 403: a caller who may not see the money is
 * answered without it, so this is asked for on every project the caller sees.
 */
export const projectEconomyQueryOptions = (projectId: number) =>
  queryOptions({
    queryKey: ["projects", "economy", "project", projectId],
    queryFn: ({ signal }) => request<Economy>(`/api/v1/projects/${projectId}/economy`, { signal }),
  });

/**
 * A filter that is off is left out: `false` and absent mean the same thing to
 * the API, and an empty search is no search at all.
 */
const portfolioQuery = (params: EconomyPortfolioSearch): URLSearchParams => {
  const query = new URLSearchParams({
    page: String(params.page),
    pageSize: String(ECONOMY_PORTFOLIO_PAGE_SIZE),
  });
  const search = params.search.trim();
  if (search) query.set("search", search);
  query.set("status", params.status);
  if (params.customerId !== undefined) query.set("customerId", String(params.customerId));
  if (params.overBudget) query.set("overBudget", "true");
  if (params.hasReady) query.set("hasReady", "true");
  query.set("sort", params.sort);
  return query;
};

/**
 * The economy across the projects whose money the caller may see. Too many
 * matching projects for one answer comes back as a 400 naming `status`, which
 * the page shows above its table rather than swallowing.
 */
export const economyPortfolioQueryOptions = (params: EconomyPortfolioSearch) =>
  queryOptions({
    queryKey: ["projects", "economy", "portfolio", params],
    queryFn: ({ signal }) =>
      request<EconomyPortfolioPage>(`/api/v1/projects/economy?${portfolioQuery(params)}`, { signal }),
    placeholderData: keepPreviousData,
  });
