import type { components } from "../api-schema";
import { request } from "./request";

/** The installation's single sign-on settings (Entra design D1 and D2): the OIDC provider and the SCIM credential. */
export type SsoSettings = components["schemas"]["SsoSettings"];
export type SsoOidcSettings = components["schemas"]["SsoOidcSettings"];
export type SsoOidcSettingsRequest = components["schemas"]["SsoOidcSettingsRequest"];
export type SsoScimTokenRequest = components["schemas"]["SsoScimTokenRequest"];
/** A newly issued SCIM token, answered this once: only its digest is kept. */
export type SsoScimToken = components["schemas"]["SsoScimToken"];
export type SsoProvider = SsoOidcSettingsRequest["provider"];

export const ssoSettingsQueryKey = ["owner", "sso"] as const;

const base = "/api/v1/identity/owner/sso";
const json = { "Content-Type": "application/json" };

export const fetchSsoSettings = () => request<SsoSettings>(base);

/** Replaces the OIDC provider whole, against the version the settings were read at; a stale one is a 412. */
export const updateOidcSettings = (body: SsoOidcSettingsRequest, version: number) =>
  request<SsoSettings>(`${base}/oidc`, {
    method: "PUT",
    headers: { ...json, "If-Match": String(version) },
    body: JSON.stringify(body),
  });

/** Turns OIDC off; without `confirm` the server refuses 409 oidc_links_exist while people are linked. */
export const removeOidc = (confirm = false) =>
  request<SsoSettings>(confirm ? `${base}/oidc?confirm=true` : `${base}/oidc`, { method: "DELETE" });

/** Issues a SCIM token, turning SCIM on; a token already set keeps working until `previousExpiresAt`. */
export const issueScimToken = (body: SsoScimTokenRequest = {}) =>
  request<SsoScimToken>(`${base}/scim/token`, { method: "POST", headers: json, body: JSON.stringify(body) });

/** Stops accepting the token a rotation replaced. */
export const endScimOverlap = () => request<SsoSettings>(`${base}/scim/previous`, { method: "DELETE" });

export const removeScim = () => request<SsoSettings>(`${base}/scim`, { method: "DELETE" });
