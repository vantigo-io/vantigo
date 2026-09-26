import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

/**
 * What every Invoices page needs before it draws anything (D2), answered by
 * the server rather than re-derived here: the currency, the default terms,
 * what the seller still lacks, whether the series has started, whether the
 * installation has an object store, today in Oslo, the VAT codes a new line may
 * take today and the caller's three capabilities.
 */
export type InvoicesMeta = Schemas["InvoicesMetaResponse"];
export type VatCodeInForce = Schemas["InvoicesVatCodeInForce"];

export const invoicesMetaQueryOptions = () =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "meta"],
    queryFn: ({ signal }) => request<InvoicesMeta>("/api/v1/invoices/meta", { signal }),
  });
