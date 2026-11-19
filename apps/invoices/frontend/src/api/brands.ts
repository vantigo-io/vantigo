import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

/** A document's brand (brands design B27, B28): the draft's choice, or an issued document's snapshot. */
export type InvoiceBrand = Schemas["InvoicesBrand"];

/** A published brand the editor's Brand select offers; the workspace default comes first. */
export type BrandOption = Schemas["InvoicesBrandOption"];

/**
 * The brands a draft can be printed in. A short list that changes when someone
 * publishes or archives a brand, so half a minute of staleness is not visible.
 */
export const invoiceBrandsQueryOptions = () =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "brands"],
    queryFn: ({ signal }) => request<BrandOption[]>("/api/v1/invoices/brands", { signal }),
    staleTime: 30_000,
  });
