import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

/** The invoice journal over a range of issue dates (D11). */
export type InvoiceJournal = Schemas["InvoicesJournalResponse"];

export const journalQueryOptions = (from: string, to: string, page: number) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "journal", from, to, page],
    queryFn: ({ signal }) =>
      request<InvoiceJournal>(`/api/v1/invoices/journal?from=${from}&to=${to}&page=${page}`, { signal }),
  });
