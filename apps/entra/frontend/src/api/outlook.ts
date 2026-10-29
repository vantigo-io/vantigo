import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { ENTRA_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/** The caller's own Outlook connection, whether they may connect, and the account Vantigo knows them by. */
export type EntraOutlookConnection = Schemas["EntraOutlookConnection"];
export type EntraOutlookConnectRequest = Schemas["EntraOutlookConnectRequest"];
export type EntraOutlookAuthorize = Schemas["EntraOutlookAuthorize"];
export type EntraOutlookComplete = Schemas["EntraOutlookComplete"];

/** A feature a connect asks Microsoft's consent for. */
export type OutlookFeature = EntraOutlookConnectRequest["feature"];

const base = "/api/v1/entra/outlook";

export const outlookConnectionQueryKey = [ENTRA_QUERY_KEY, "outlook"] as const;

/** Always 200: whether the caller may connect, and their connection's state. */
export const outlookConnectionQueryOptions = () =>
  queryOptions({
    queryKey: outlookConnectionQueryKey,
    queryFn: ({ signal }) => request<EntraOutlookConnection>(`${base}/connection`, { signal }),
  });

/** Leaving for Microsoft's sign-in, unchanged; a seam so a test can see the address without leaving. */
export const outlookNavigation = {
  open: (url: string) => window.location.assign(url),
};

/**
 * Starts a connect (409 `switch_off`, `no_account` or `account_taken`; 422
 * `feature_unknown`) and sends the browser to Microsoft's authorization
 * endpoint the answer names, which is always https.
 */
export const startConnect = async (body: EntraOutlookConnectRequest): Promise<void> => {
  const { authorizeUrl } = await request<EntraOutlookAuthorize>(`${base}/connect`, json("POST", body));
  if (!authorizeUrl.startsWith("https://")) throw new Error("The authorization address is not https");
  outlookNavigation.open(authorizeUrl);
};

/** Completes a connect under the session: 410 `expired`; 409 `switch_off`, `no_account`, `account_mismatch` or `account_taken`. */
export const completeConnect = () => request<EntraOutlookComplete>(`${base}/complete`, { method: "POST" });

/** Disconnects the caller's Outlook; 204 whether or not there was a connection. */
export const disconnect = () => request<void>(`${base}/connection`, { method: "DELETE" });
