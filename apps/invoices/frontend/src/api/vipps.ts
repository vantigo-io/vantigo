import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/**
 * The stored Vipps credentials as a client may see them (invoices payments and
 * reminders design D14): the sales unit, whether keys are stored and when
 * Vipps refused them — never a key.
 */
export type VippsSettings = Schemas["InvoicesVippsSettings"];
/**
 * PUT /settings/vipps' body. A key left out keeps the stored one; an empty one
 * is a 400, so a blank field is omitted, never sent empty.
 */
export type VippsSettingsInput = Schemas["InvoicesVippsSettingsRequest"];
/** What Vipps answered a verify: `ok`, `unauthorized` or `unreachable`. */
export type VippsVerification = Schemas["InvoicesVippsVerifyResponse"];

/** The stored credentials, for the settings page (`invoices:manage`). */
export const vippsSettingsQueryOptions = () =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "vipps-settings"],
    queryFn: ({ signal }) => request<VippsSettings>("/api/v1/invoices/settings/vipps", { signal }),
  });

/**
 * Stores the sales unit and its keys, sealed by the server and never answered.
 * A change of the sales unit while a Vipps payment is in flight is a 409
 * `payment_attempts_active`; new keys of the same sales unit are always stored. The caller invalidates `[INVOICES_QUERY_KEY]`.
 */
export const putVippsSettings = (input: VippsSettingsInput): Promise<VippsSettings> =>
  request<VippsSettings>("/api/v1/invoices/settings/vipps", json("PUT", input));

/** Removes the credentials; refused with 409 `payment_attempts_active` while a Vipps payment is in flight. */
export const deleteVippsSettings = (): Promise<void> =>
  request<void>("/api/v1/invoices/settings/vipps", { method: "DELETE" });

/** Asks Vipps for a token and a read naming the sales unit, and says how it went. */
export const verifyVippsSettings = (): Promise<VippsVerification> =>
  request<VippsVerification>("/api/v1/invoices/settings/vipps/verify", { method: "POST" });
