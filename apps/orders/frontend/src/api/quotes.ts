import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { type ListFilters, listQuery } from "./list-query";
import { json, ORDERS_QUERY_KEY, request, send } from "./request";
import type {
  Accepted,
  AcceptInput,
  Mailing,
  Quote,
  QuoteInput,
  QuotePage,
  QuoteStatus,
  QuoteUpdate,
  SendInput,
  StoredDocument,
  TemplateApplied,
} from "./types";

export interface QuoteListFilters extends ListFilters {
  status?: QuoteStatus[];
}

const base = "/api/v1/orders/quotes";

/** One page of quotes, newest first (O20, O31a); the shown page stays while the next is fetched. */
export const quoteListQueryOptions = (filters: QuoteListFilters) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "quotes", "list", filters],
    queryFn: ({ signal }) => request<QuotePage>(`${base}${listQuery(filters)}`, { signal }),
    placeholderData: keepPreviousData,
  });

export const quoteQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "quotes", "detail", id],
    queryFn: ({ signal }) => request<Quote>(`${base}/${id}`, { signal }),
  });

export const quoteDocumentsQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "quotes", "documents", id],
    queryFn: ({ signal }) => request<{ items: StoredDocument[] }>(`${base}/${id}/documents`, { signal }),
  });

export const quoteMailingsQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "quotes", "mailings", id],
    queryFn: ({ signal }) => request<{ items: Mailing[] }>(`${base}/${id}/mailings`, { signal }),
  });

// The writes go past the shared client (`send`), so a refusal keeps its code
// and its failing lines for the editor.
export const createQuote = (input: QuoteInput) => send<Quote>(base, json("POST", input));
export const updateQuote = (id: number, input: QuoteUpdate) => send<Quote>(`${base}/${id}`, json("PUT", input));
export const deleteQuote = (id: number) => send<void>(`${base}/${id}`, { method: "DELETE" });
export const sendQuote = (id: number, input: SendInput) => send<Quote>(`${base}/${id}/send`, json("POST", input));
export const markQuoteSent = (id: number, revision: number, validUntil?: string | null) =>
  send<Quote>(`${base}/${id}/mark-sent`, json("POST", { revision, validUntil: validUntil ?? null }));
export const reviseQuote = (id: number, revision: number) =>
  send<Quote>(`${base}/${id}/revise`, json("POST", { revision }));
export const acceptQuote = (id: number, input: AcceptInput) =>
  send<Accepted>(`${base}/${id}/accept`, json("POST", input));
export const declineQuote = (id: number, revision: number, reason: string) =>
  send<Quote>(`${base}/${id}/decline`, json("POST", { revision, ...(reason ? { reason } : {}) }));
export const cancelQuote = (id: number, revision: number) =>
  send<Quote>(`${base}/${id}/cancel`, json("POST", { revision }));
export const copyQuote = (id: number) => send<Quote>(`${base}/${id}/copy`, { method: "POST" });
export const applyTemplate = (id: number, revision: number, templateId: number, replace: boolean) =>
  send<TemplateApplied>(`${base}/${id}/apply-template`, json("POST", { revision, templateId, replace }));

/** Where a quote's PDF is: a sent revision's stored one, or the draft printed now (O24). */
export const quotePdfUrl = (id: number, revision?: number) =>
  `${base}/${id}/pdf${revision === undefined ? "" : `?revision=${revision}`}`;
export const quoteProformaUrl = (id: number) => `${base}/${id}/proforma`;
