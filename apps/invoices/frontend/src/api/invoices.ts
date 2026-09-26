import { queryOptions } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, json, readJson, request } from "./request";

type Schemas = components["schemas"];

/** One invoice or credit note as the server answers it (D4). */
export type InvoiceDocument = Schemas["InvoicesInvoiceResponse"];
export type InvoiceLine = Schemas["InvoicesLine"];
export type InvoiceVatSummary = Schemas["InvoicesVatSummary"];
export type InvoiceListItem = Schemas["InvoicesInvoiceListItem"];
export type InvoiceList = Schemas["PaginatedResponseOfInvoicesInvoiceListItem"];
export type InvoiceInput = Schemas["InvoicesInvoiceRequest"];
export type InvoiceLineInput = Schemas["InvoicesLineRequest"];
export type DeliveryAddress = Schemas["InvoicesDeliveryAddress"];
/** A refusal's body: the rule's code, and the dates or the line it names. */
export type InvoicesConflict = Schemas["InvoicesConflictProblem"];

/** The list's filters (D4). Every one is optional; paging is page/pageSize. */
export interface InvoiceListFilters {
  status?: "draft" | "issued";
  kind?: "invoice" | "credit_note";
  customerId?: number;
  search?: string;
  from?: string;
  to?: string;
  page?: number;
  pageSize?: number;
}

const listQuery = (filters: InvoiceListFilters): string => {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(filters)) {
    if (value !== undefined && value !== "") params.set(key, String(value));
  }
  const query = params.toString();
  return query ? `?${query}` : "";
};

export const invoiceListQueryOptions = (filters: InvoiceListFilters) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "list", filters],
    queryFn: ({ signal }) => request<InvoiceList>(`/api/v1/invoices${listQuery(filters)}`, { signal }),
  });

export const invoiceQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "document", id],
    queryFn: ({ signal }) => request<InvoiceDocument>(`/api/v1/invoices/${id}`, { signal }),
  });

export const createInvoice = (input: InvoiceInput): Promise<InvoiceDocument> =>
  request<InvoiceDocument>("/api/v1/invoices", json("POST", input));

/** A full replace with the revision the draft was read at. */
export const replaceInvoice = (id: number, input: InvoiceInput): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}`, json("PUT", input));

export const deleteInvoice = (id: number): Promise<void> =>
  request<void>(`/api/v1/invoices/${id}`, { method: "DELETE" });

/** Issues a draft; an omitted date is today in Oslo (D6). */
export const issueInvoice = (id: number, issueDate?: string): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}/issue`, json("POST", issueDate ? { issueDate } : {}));

/** Creates a credit-note draft of an issued invoice (D8). */
export const creditInvoice = (id: number): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}/credit`, { method: "POST" });

/** Where an issued document's stored PDF downloads from. */
export const pdfUrl = (id: number): string => appUrl(`/api/v1/invoices/${id}/pdf`);

/** Where a draft's watermarked preview renders. */
export const previewUrl = (id: number): string => appUrl(`/api/v1/invoices/${id}/preview.pdf`);

/** A fetched PDF and the name the server gives it. */
export interface FetchedPdf {
  blob: Blob;
  fileName?: string;
}

/**
 * Fetches a PDF with the session cookie. A refusal — 409 `invoice_draft` or
 * `invoice_issued`, 503 `storage_unavailable`, a 500 — throws an error carrying
 * the problem's `code` and body, as the shared client's errors do, so the page
 * words it like any other refusal rather than the browser opening raw JSON.
 */
export const fetchPdf = async (url: string): Promise<FetchedPdf> => {
  const response = await fetch(url, { credentials: "include" });
  if (response.ok) {
    const disposition = response.headers.get("Content-Disposition") ?? "";
    return { blob: await response.blob(), fileName: /filename="([^"]+)"/.exec(disposition)?.[1] };
  }
  const problem = await readJson<Record<string, unknown>>(response).catch(() => ({}) as Record<string, unknown>);
  const message = String(problem.detail ?? problem.title ?? response.statusText);
  throw Object.assign(new Error(message), {
    status: response.status,
    code: typeof problem.code === "string" ? problem.code : undefined,
    problem,
  });
};
