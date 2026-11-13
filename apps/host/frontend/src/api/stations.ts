import type { components } from "../api-schema";
import { request } from "./request";

/**
 * Shared shop-floor stations (manufacturing phase 2 M129–M143): the
 * administrator's list, a station's own reads with its cookie, and the PIN
 * and badge of a person.
 */
export type Station = components["schemas"]["Station"];
export type StationPairing = components["schemas"]["StationPairing"];
export type StationRequest = components["schemas"]["StationRequest"];
export type StationSelf = components["schemas"]["StationSelf"];
export type StationOperatorSession = components["schemas"]["StationOperatorSession"];
export type StationOperatorCandidate = components["schemas"]["StationOperatorCandidate"];
export type StationOperatorSignIn = components["schemas"]["StationOperatorSignIn"];
export type OperatorCredentials = components["schemas"]["OperatorCredentials"];
export type AccountPin = components["schemas"]["AccountPinResponse"];

const json = { "Content-Type": "application/json" };
const stations = "/api/v1/identity/stations";
const station = "/api/v1/identity/station";

export const stationsQueryKey = ["identity", "stations"] as const;

export const listStations = () => request<components["schemas"]["StationList"]>(stations).then((list) => list.items);
export const registerStation = (body: StationRequest) =>
  request<StationPairing>(stations, { method: "POST", headers: json, body: JSON.stringify(body) });
export const updateStation = (id: string, body: StationRequest) =>
  request<Station>(`${stations}/${id}`, { method: "PUT", headers: json, body: JSON.stringify(body) });
export const issuePairingCode = (id: string) =>
  request<StationPairing>(`${stations}/${id}/pairing`, { method: "POST" });
export const revokeStation = (id: string) => request<Station>(`${stations}/${id}/revoke`, { method: "POST" });

/*
 * The station's own requests carry its cookie and nothing else: a 401 is the
 * station's (station_unknown), never a person's session ending, so the
 * client's sign-out handling is off for them.
 */
const asStation = { handleUnauthorized: false } as const;

export const pairStation = (code: string) =>
  request<StationSelf>(`${station}/pair`, {
    ...asStation,
    method: "POST",
    headers: json,
    body: JSON.stringify({ code }),
  });
export const readStation = () => request<StationSelf>(station, asStation);
export const listStationOperators = () =>
  request<components["schemas"]["StationOperatorCandidateList"]>(`${station}/operators`, asStation).then(
    (list) => list.items,
  );
export const switchIn = (body: { userId?: string; pin?: string; badge?: string }) =>
  request<StationOperatorSignIn>(`${station}/operators`, {
    ...asStation,
    method: "POST",
    headers: json,
    body: JSON.stringify(body),
  });
export const switchOut = (sessionId: string) =>
  request<void>(`${station}/operators/${sessionId}/end`, { ...asStation, method: "POST" });

export const accountPinQueryKey = ["account", "pin"] as const;
export const readAccountPin = () => request<AccountPin>("/api/v1/identity/account/pin");
export const setAccountPin = (pin: string, currentPassword?: string) =>
  request<void>("/api/v1/identity/account/pin", {
    method: "PUT",
    headers: json,
    body: JSON.stringify(currentPassword ? { pin, currentPassword } : { pin }),
  });
export const removeAccountPin = () => request<void>("/api/v1/identity/account/pin", { method: "DELETE" });

export const operatorCredentialsQueryKey = (userId: string) => ["identity", "users", userId, "operator"] as const;
export const readOperatorCredentials = (userId: string) =>
  request<OperatorCredentials>(`/api/v1/identity/users/${userId}/operator-credentials`);
export const clearPin = (userId: string) => request<void>(`/api/v1/identity/users/${userId}/pin`, { method: "DELETE" });
export const enrolBadge = (userId: string, badge: string) =>
  request<void>(`/api/v1/identity/users/${userId}/badge`, {
    method: "PUT",
    headers: json,
    body: JSON.stringify({ badge }),
  });
export const removeBadge = (userId: string) =>
  request<void>(`/api/v1/identity/users/${userId}/badge`, { method: "DELETE" });
