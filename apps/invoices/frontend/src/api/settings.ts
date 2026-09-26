import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/** The seller record and the series start (D2). */
export type InvoiceSettings = Schemas["InvoicesSettingsResponse"];
export type InvoiceSettingsInput = Schemas["InvoicesSettingsRequest"];

export const invoiceSettingsQueryOptions = () =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "settings"],
    queryFn: ({ signal }) => request<InvoiceSettings>("/api/v1/invoices/settings", { signal }),
  });

/** A full replace with the revision the settings were read at. */
export const updateInvoiceSettings = (input: InvoiceSettingsInput): Promise<InvoiceSettings> =>
  request<InvoiceSettings>("/api/v1/invoices/settings", json("PUT", input));
