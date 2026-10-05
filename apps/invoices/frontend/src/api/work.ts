import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import type { InvoiceDocument } from "./invoices";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/**
 * The uninvoiced view (invoices work design D3): per project and kind the
 * work the billable reads answer, each row selectable or not with its reason,
 * the totals of the selectable work per currency, the people the hours name
 * and the warnings.
 */
export type WorkView = Schemas["InvoicesWorkResponse"];
export type WorkProject = Schemas["InvoicesWorkProject"];
export type WorkHour = Schemas["InvoicesWorkHour"];
export type WorkExpense = Schemas["InvoicesWorkExpense"];
export type WorkMilestone = Schemas["InvoicesWorkMilestone"];
export type WorkHeldBy = Schemas["InvoicesWorkHeldBy"];
export type FromWorkInput = Schemas["InvoicesFromWorkRequest"];

/** The three kinds of work, as a source names its kind on the wire. */
export const WORK_KINDS = {
  hours: "time.entry",
  expenses: "expenses.entry",
  milestones: "projects.milestone",
} as const;

/** Whose work the view lists: exactly one customer's projects, or one project. */
export type WorkScope = { customerId: number; projectId?: never } | { projectId: number; customerId?: never };

const scopeQuery = (scope: WorkScope): string =>
  scope.customerId !== undefined ? `customerId=${scope.customerId}` : `projectId=${scope.projectId}`;

/** GET /invoices/work for a customer or a project. */
export const getWork = (scope: WorkScope, signal?: AbortSignal): Promise<WorkView> =>
  request<WorkView>(`/api/v1/invoices/work?${scopeQuery(scope)}`, { signal });

export const workQueryOptions = (scope: WorkScope) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "work", scope],
    queryFn: ({ signal }) => getWork(scope, signal),
  });

/**
 * The wizard (D3, D4): a new invoice draft of the chosen work — 201 — or, with
 * `invoiceId` and `revision`, the work added to that draft — 200. Either way
 * the answer is the draft. The caller invalidates every invoices query
 * (`[INVOICES_QUERY_KEY]`): the view, the list and the draft all moved.
 */
export const postFromWork = (input: FromWorkInput): Promise<InvoiceDocument> =>
  request<InvoiceDocument>("/api/v1/invoices/from-work", json("POST", input));
