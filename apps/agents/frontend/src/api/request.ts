import { createApiClient } from "@vantigo/frontend-api-client";
import { appUrl } from "@vantigo/frontend-shell";

const client = createApiClient({ resolveUrl: appUrl, sessionNotFoundMeansExpired: false });

export const { request, setAuthStateClearer, setUnauthorizedHandler } = client;
export type { ApiError, RequestOptions } from "@vantigo/frontend-api-client";
export { ApiConflictError, ApiValidationError, NotFoundError, readJson } from "@vantigo/frontend-api-client";

/** Every JSON write in this package carries the session cookie; a versioned one names its version in If-Match. */
export const json = (method: string, body: unknown, headers: Record<string, string> = {}): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json", ...headers },
  body: JSON.stringify(body),
});

/** A save names the version it read, bare, as the contract's If-Match says; a stale one is a 409 `version_conflict`. */
export const ifMatch = (version: number) => ({ "If-Match": String(version) });

/** The one prefix every query key in this package starts with, so the blanket invalidation each write does reaches all of them. */
export const AGENTS_QUERY_KEY = "agents";

/** The rule a refusal names (`owner_required`, `version_conflict` …), when the error carries one. */
export const errorCode = (error: unknown): string | undefined => (error as { code?: string } | undefined)?.code;

export const isVersionConflict = (error: unknown) => errorCode(error) === "version_conflict";
/** Raising a level, choosing an Auto mode or binding a channel is the Owner's alone (V12). */
export const isOwnerRequired = (error: unknown) => errorCode(error) === "owner_required";
export const isCompanyNameRequired = (error: unknown) => errorCode(error) === "company_name_required";
export const isSuggestionStale = (error: unknown) => errorCode(error) === "suggestion_stale";
export const isSuggestionDecided = (error: unknown) => errorCode(error) === "suggestion_decided";
export const isChannelBound = (error: unknown) => errorCode(error) === "channel_bound";
