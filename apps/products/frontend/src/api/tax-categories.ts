import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";
export type TaxCategoryKind = "Standard" | "Reduced" | "Zero" | "Exempt";
export interface TaxCategoryResponse {
  id: number;
  name: string;
  kind: TaxCategoryKind;
  rate: number;
}
export interface TaxCategoryInput {
  name: string;
  kind: TaxCategoryKind;
  rate: number;
}
const endpoint = "/api/v1/products/tax-categories";
export const taxCategoriesQueryOptions = () =>
  queryOptions({
    queryKey: ["tax-categories"],
    queryFn: ({ signal }) => request<TaxCategoryResponse[]>(endpoint, { signal }),
  });
const json = (method: string, body?: unknown) => ({
  method,
  headers: { "Content-Type": "application/json" },
  ...(body === undefined ? {} : { body: JSON.stringify(body) }),
});
export const createTaxCategory = (input: TaxCategoryInput) =>
  request<TaxCategoryResponse>(endpoint, json("POST", input));
export const updateTaxCategory = (id: number, input: TaxCategoryInput) =>
  request<TaxCategoryResponse>(`${endpoint}/${id}`, json("PUT", input));
export const deleteTaxCategory = (id: number) => request<void>(`${endpoint}/${id}`, { method: "DELETE" });
