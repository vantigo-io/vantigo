import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { EXPENSES_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

/**
 * A project the caller may book an expense on, with the active billing lines
 * they may book it against — so the expense form needs no code of the
 * projects module's own, and the picker cannot offer something the save would
 * refuse.
 */
export type ExpenseProjectOption = Schemas["ExpensesProjectOption"];
export type ExpenseProjectBillingLine = Schemas["ExpensesProjectOptionBillingLine"];

/**
 * Only ever asked for when `meta.projectsAvailable` is true: without the
 * projects module the endpoint answers 404, exactly as a path that is not
 * there, and there is no project block to draw.
 */
export const expenseProjectsQueryOptions = (kind?: ProjectPicker) =>
  queryOptions({
    queryKey: kind ? [EXPENSES_QUERY_KEY, "projects", kind] : [EXPENSES_QUERY_KEY, "projects"],
    queryFn: ({ signal }) =>
      request<ExpenseProjectOption[]>(kind ? `/api/v1/expenses/projects?kind=${kind}` : "/api/v1/expenses/projects", {
        signal,
      }),
  });

/**
 * Which picker. Left out, the projects the caller may book on — what logging
 * time needs. `supplier_invoice` is the projects they hold financial rights on
 * and that are not cancelled, which is what recording a supplier invoice needs
 * (supplier invoices design D2) and a different list altogether: a finance
 * reader logs time on nothing and may still record one.
 */
export type ProjectPicker = "supplier_invoice";
