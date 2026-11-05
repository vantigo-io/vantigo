import { queryOptions } from "@tanstack/react-query";
import { ORDERS_QUERY_KEY, request } from "./request";

/** A VAT code as Invoices lists it (`invoices:access`): what a free-text line's picker shows. */
export interface VatCodeOption {
  id: number;
  code: string;
  name: string;
  active: boolean;
  rates: { ratePercent: number; validFrom: string; validTo?: string | null }[];
}

/** Invoices' VAT codes, the active ones, for a free-text line (O16). */
export const vatCodesQueryOptions = () =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "vat-codes"],
    queryFn: ({ signal }) => request<VatCodeOption[]>("/api/v1/invoices/vat-codes", { signal }),
    select: (codes) => codes.filter((code) => code.active),
    staleTime: 300_000,
  });

/** The rate a code has now: its open-ended period's. */
export const currentRate = (code: VatCodeOption): number | undefined =>
  code.rates.find((rate) => !rate.validTo)?.ratePercent ?? code.rates.at(-1)?.ratePercent;
