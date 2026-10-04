import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/** PUT /settings/access-point's body: the provider, its legal entity, and the key — omitted to keep the stored one. */
export type AccessPointInput = Schemas["InvoicesAccessPointRequest"];
/**
 * The stored credentials as a client may see them: never the key (EHF and KID
 * design D7). With none stored, only `hasCredentials: false`.
 */
export type AccessPoint = Schemas["InvoicesAccessPointResponse"];
/** What the provider answered: `ok`, `unauthorized` or `unreachable`. */
export type AccessPointVerification = Schemas["InvoicesAccessPointVerifyResponse"];

/** The stored credentials, read for the settings page; `{hasCredentials: false}` when there are none. */
export const accessPointQueryOptions = () =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "access-point"],
    queryFn: ({ signal }) => request<AccessPoint>("/api/v1/invoices/settings/access-point", { signal }),
  });

/**
 * Stores the access point's credentials. The key is sealed by the server and
 * never answered; an omitted key keeps the stored one. A provider switch while
 * a transmission is in flight is a 409 `transmissions_active`. The caller
 * invalidates `[INVOICES_QUERY_KEY]` — meta's `ehfAvailable` follows it.
 */
export const putAccessPoint = (input: AccessPointInput): Promise<AccessPoint> =>
  request<AccessPoint>("/api/v1/invoices/settings/access-point", json("PUT", input));

/** Removes the credentials; refused with 409 `transmissions_active` while a transmission is in flight. */
export const deleteAccessPoint = (): Promise<void> =>
  request<void>("/api/v1/invoices/settings/access-point", { method: "DELETE" });

/** Asks the provider one authenticated read with the stored credentials, and says how it went. */
export const verifyAccessPoint = (): Promise<AccessPointVerification> =>
  request<AccessPointVerification>("/api/v1/invoices/settings/access-point/verify", { method: "POST" });
