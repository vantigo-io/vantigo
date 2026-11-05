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
/** Deletes a draft as it was read: `expectedUpdatedAt` is its `updatedAt` (409 `version_conflict` otherwise). */
export const deleteQuote = (id: number, expectedUpdatedAt: string) =>
  send<void>(`${base}/${id}?expectedUpdatedAt=${encodeURIComponent(expectedUpdatedAt)}`, { method: "DELETE" });

/** What every write sends of the quote as the page read it: its revision and its `updatedAt`, the concurrency token. */
export interface QuoteRead {
  revision: number;
  updatedAt: string;
}
const token = (read: QuoteRead) => ({ revision: read.revision, expectedUpdatedAt: read.updatedAt });
export const sendQuote = (id: number, input: SendInput) => send<Quote>(`${base}/${id}/send`, json("POST", input));
/** Marks a draft as sent; `extra` carries a warning override (O33) when one was given. */
export const markQuoteSent = (id: number, read: QuoteRead, validUntil?: string | null, extra: object = {}) =>
  send<Quote>(`${base}/${id}/mark-sent`, json("POST", { ...token(read), validUntil: validUntil ?? null, ...extra }));
export const reviseQuote = (id: number, read: QuoteRead) =>
  send<Quote>(`${base}/${id}/revise`, json("POST", token(read)));
export const acceptQuote = (id: number, input: AcceptInput) =>
  send<Accepted>(`${base}/${id}/accept`, json("POST", input));
export const declineQuote = (id: number, read: QuoteRead, reason: string) =>
  send<Quote>(`${base}/${id}/decline`, json("POST", { ...token(read), ...(reason ? { reason } : {}) }));
export const cancelQuote = (id: number, read: QuoteRead) =>
  send<Quote>(`${base}/${id}/cancel`, json("POST", token(read)));
export const copyQuote = (id: number) => send<Quote>(`${base}/${id}/copy`, { method: "POST" });
export const applyTemplate = (id: number, read: QuoteRead, templateId: number, replace: boolean) =>
  send<TemplateApplied>(`${base}/${id}/apply-template`, json("POST", { ...token(read), templateId, replace }));

/** Where a quote's PDF is: a sent revision's stored one, or the draft printed now (O24). */
export const quotePdfUrl = (id: number, revision?: number) =>
  `${base}/${id}/pdf${revision === undefined ? "" : `?revision=${revision}`}`;
export const quoteProformaUrl = (id: number) => `${base}/${id}/proforma`;
