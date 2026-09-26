import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/** A VAT code with every rate period it has had (D3). */
export type VatCode = Schemas["InvoicesVatCode"];
export type VatCodeRate = Schemas["InvoicesVatCodeRate"];
export type VatCodeCreateInput = Schemas["InvoicesVatCodeCreateRequest"];
export type VatCodeUpdateInput = Schemas["InvoicesVatCodeUpdateRequest"];

export const vatCodesQueryOptions = () =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "vat-codes"],
    queryFn: ({ signal }) => request<VatCode[]>("/api/v1/invoices/vat-codes", { signal }),
  });

export const createVatCode = (input: VatCodeCreateInput): Promise<VatCode> =>
  request<VatCode>("/api/v1/invoices/vat-codes", json("POST", input));

export const updateVatCode = (id: number, input: VatCodeUpdateInput): Promise<VatCode> =>
  request<VatCode>(`/api/v1/invoices/vat-codes/${id}`, json("PUT", input));

/** The rate-change rule: the open period closes the day before validFrom. */
export const addVatCodeRate = (id: number, ratePercent: number, validFrom: string): Promise<VatCode> =>
  request<VatCode>(`/api/v1/invoices/vat-codes/${id}/rates`, json("POST", { ratePercent, validFrom }));

/** Removes the latest, mistaken period and reopens the one before it. */
export const deleteVatCodeRate = (id: number, rateId: number): Promise<VatCode> =>
  request<VatCode>(`/api/v1/invoices/vat-codes/${id}/rates/${rateId}`, { method: "DELETE" });
