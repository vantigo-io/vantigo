import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import type { GoogleConnection } from "./connection";
import { GOOGLE_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/** The caller's own Google connection, whether they may connect, and the account Vantigo knows them by. */
export type GooglePersonalConnection = Schemas["GooglePersonalConnection"];
export type GooglePersonalConnectRequest = Schemas["GooglePersonalConnectRequest"];
export type GooglePersonalAuthorize = Schemas["GooglePersonalAuthorize"];
export type GooglePersonalComplete = Schemas["GooglePersonalComplete"];
/** Connection for people, as the connection answers it: the switch, its status, the counts and the redirect URI. */
export type GooglePersonalSwitch = Schemas["GooglePersonalSwitch"];

/** A feature a connect asks Google's consent for. */
export type GoogleFeature = GooglePersonalConnectRequest["feature"];

const base = "/api/v1/google";

export const personalConnectionQueryKey = [GOOGLE_QUERY_KEY, "personal"] as const;

/** Always 200: whether the caller may connect, and their connection's state. */
export const personalConnectionQueryOptions = () =>
  queryOptions({
    queryKey: personalConnectionQueryKey,
    queryFn: ({ signal }) => request<GooglePersonalConnection>(`${base}/connection/me`, { signal }),
  });

/** Leaving for Google's sign-in, unchanged; a seam so a test can see the address without leaving. */
export const googleNavigation = {
  open: (url: string) => window.location.assign(url),
};

/**
 * Starts a connect (409 `switch_off`, `no_account` or `account_taken`; 422
 * `feature_unknown`) and sends the browser to Google's authorization
 * endpoint the answer names, which is always https.
 */
export const startConnect = async (body: GooglePersonalConnectRequest): Promise<void> => {
  const { authorizeUrl } = await request<GooglePersonalAuthorize>(`${base}/connect`, json("POST", body));
  if (!authorizeUrl.startsWith("https://")) throw new Error("The authorization address is not https");
  googleNavigation.open(authorizeUrl);
};

/** Completes a connect under the session: 410 `expired`; 409 `switch_off`, `no_account`, `account_mismatch` or `account_taken`. */
export const completeConnect = () => request<GooglePersonalComplete>(`${base}/complete`, { method: "POST" });

/** Disconnects the caller's Google; 204 whether or not there was a connection. */
export const disconnect = () => request<void>(`${base}/connection/me`, { method: "DELETE" });

/** Turns Connection for people on or off, the Owner's; off closes everyone's connection. */
export const setPersonalSwitch = (enabled: boolean) =>
  request<GoogleConnection>(`${base}/connection/personal`, json("PUT", { enabled }));
