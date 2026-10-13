import type { components } from "../api-schema";
import { json, request } from "./request";

type Schemas = components["schemas"];

/** What the pay page shows of an invoice (D15): the seller, never the buyer. */
export type PayPageView = Schemas["InvoicesPayPage"];
export type PayReason = Schemas["InvoicesPayReason"];
export type PayAttemptCreated = Schemas["InvoicesPayAttemptCreated"];
export type PayAttemptStatus = Schemas["InvoicesPayAttemptStatus"];

/*
 * The pay page's three anonymous operations — the only requests the page
 * makes, every one on the public host's allowlist (D14). None needs a
 * session, so a 401 never signs anyone out. The token travels only in a
 * request's body, never in an API URL (plan reading 23): the access logs of
 * every proxy in front of the API see the paths, not the bodies.
 */

/** Reads the invoice a pay link names: `POST /pay/read`, the token in the body. */
export const readPayPage = (token: string): Promise<PayPageView> =>
  request<PayPageView>("/api/v1/invoices/pay/read", { ...json("POST", { token }), handleUnauthorized: false });

/** Starts a Vipps payment once the payer accepted the seller's terms; answers where to send them. */
export const startPayment = (token: string): Promise<PayAttemptCreated> =>
  request<PayAttemptCreated>("/api/v1/invoices/pay/attempts", {
    ...json("POST", { token, acceptTerms: true }),
    handleUnauthorized: false,
  });

/** A payment's state on return from Vipps, by its reference and the nonce its return address carried. */
export const readPayStatus = (reference: string, nonce: string, signal?: AbortSignal): Promise<PayAttemptStatus> =>
  request<PayAttemptStatus>(
    `/api/v1/invoices/pay/attempts/${encodeURIComponent(reference)}?nonce=${encodeURIComponent(nonce)}`,
    { signal, handleUnauthorized: false },
  );

/** Leaving for Vipps' address, unchanged; a seam so a test can see the address without leaving. */
export const payNavigation = {
  open: (url: string) => window.location.assign(url),
};
