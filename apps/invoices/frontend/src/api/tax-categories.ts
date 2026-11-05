import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, json, request } from "./request";

/** One Products tax category mapped to one of Invoices' VAT codes (orders design O7). */
export type TaxCategoryVatCode = components["schemas"]["InvoicesTaxCategoryVatCode"];

export const taxCategoryVatCodesQueryOptions = () =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "tax-category-vat-codes"],
    queryFn: ({ signal }) =>
      request<TaxCategoryVatCode[]>("/api/v1/invoices/settings/tax-category-vat-codes", { signal }),
  });

/** Maps a tax category to a VAT code, replacing any it had. */
export const putTaxCategoryVatCode = (taxCategoryId: number, vatCodeId: number): Promise<TaxCategoryVatCode> =>
  request<TaxCategoryVatCode>(
    `/api/v1/invoices/settings/tax-category-vat-codes/${taxCategoryId}`,
    json("PUT", { vatCodeId }),
  );

/** Leaves a tax category with no VAT code. */
export const deleteTaxCategoryVatCode = (taxCategoryId: number): Promise<void> =>
  request<void>(`/api/v1/invoices/settings/tax-category-vat-codes/${taxCategoryId}`, { method: "DELETE" });

/**
 * A tax category as the products module lists it: its id and name. Invoices
 * stores only the id, and the catalog contract carries no names, so the
 * settings page reads them over HTTP from the products API — which needs the
 * products module and `products:tax-categories-view`, so it is only read for
 * a caller the host says has both.
 *
 * The shape is typed here rather than generated: this package owns only the
 * invoices contract, and these fields are all it reads of the answer.
 */
export interface TaxCategoryOption {
  id: number;
  name: string;
  kind: string;
}

export const productTaxCategoriesQueryOptions = () =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "product-tax-categories"],
    retry: false,
    queryFn: ({ signal }) => request<TaxCategoryOption[]>("/api/v1/products/tax-categories", { signal }),
  });
