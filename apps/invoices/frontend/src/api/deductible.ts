import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, request } from "./request";

/**
 * What one issued invoice of the draft's customer — an a-konto — has left to
 * deduct at one VAT code (invoices work design D7), with the snapshot a
 * deduction at that code is taxed at.
 */
export type Deductible = components["schemas"]["InvoicesDeductible"];

/** GET /invoices/{id}/deductible: the editor's "Deduct earlier invoices" step, for an invoice draft. */
export const getInvoicesByIdDeductible = (invoiceId: number, signal?: AbortSignal): Promise<Deductible[]> =>
  request<Deductible[]>(`/api/v1/invoices/${invoiceId}/deductible`, { signal });

export const deductibleQueryOptions = (invoiceId: number) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "deductible", invoiceId],
    queryFn: ({ signal }) => getInvoicesByIdDeductible(invoiceId, signal),
  });
