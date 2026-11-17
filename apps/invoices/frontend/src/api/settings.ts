import { queryOptions } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/**
 * The seller record and the series start (D2), the seller's Peppol id and the
 * KID agreement (EHF and KID design D2, D3) with the save's warnings
 * (`kid_headroom_low`).
 */
export type InvoiceSettings = Schemas["InvoicesSettingsResponse"];
export type InvoiceSettingsInput = Schemas["InvoicesSettingsRequest"];

export const invoiceSettingsQueryOptions = () =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "settings"],
    queryFn: ({ signal }) => request<InvoiceSettings>("/api/v1/invoices/settings", { signal }),
  });

/**
 * A full replace with the revision the settings were read at. `peppolId`,
 * `kidLength` and `kidAlgorithm` are required and nullable: null clears them,
 * and a null Peppol id is derived again from the organisation number.
 */
export const updateInvoiceSettings = (input: InvoiceSettingsInput): Promise<InvoiceSettings> =>
  request<InvoiceSettings>("/api/v1/invoices/settings", json("PUT", input));

/**
 * Where the seller's logo is read, with its SHA-256 as the cache key: the
 * server ignores `v`, and a new logo is a new address (invoices phase 6
 * design I1).
 */
export const invoiceLogoUrl = (sha256: string): string =>
  appUrl(`/api/v1/invoices/settings/logo?v=${encodeURIComponent(sha256)}`);

/**
 * Sets the seller's logo: one part named `file`, the `Content-Type` left to
 * the browser. A refusal is a 400 carrying its code — `logo_unsupported_type`,
 * `logo_too_large`, `logo_too_wide`, `logo_too_small` or `logo_invalid`.
 * Answers the settings.
 */
export const uploadInvoiceLogo = (file: File): Promise<InvoiceSettings> => {
  const form = new FormData();
  form.append("file", file, file.name);
  return request<InvoiceSettings>("/api/v1/invoices/settings/logo", { method: "PUT", body: form });
};

/** Removes the seller's logo; documents issued with it keep it. */
export const removeInvoiceLogo = () => request<void>("/api/v1/invoices/settings/logo", { method: "DELETE" });
