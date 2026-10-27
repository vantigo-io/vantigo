import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { ENTRA_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

/** One account in the tenant, with who already holds its keys under the configured issuer, when anyone does. */
export type EntraDirectoryUser = Schemas["EntraDirectoryUser"];
export type EntraDirectoryHolder = Schemas["EntraDirectoryHolder"];
export type EntraDirectoryUsers = Schemas["EntraDirectoryUsers"];
export type EntraDirectoryMatch = Schemas["EntraDirectoryMatch"];
export type EntraDirectoryMatches = Schemas["EntraDirectoryMatches"];

/** The two keys an announced link names: what a pick in the directory answers. */
export interface DirectoryPick {
  objectId: string;
  userPrincipalName: string;
}

/** The most accounts a search answers; that many back means there may be more, and the search should be narrowed. */
export const DIRECTORY_SEARCH_LIMIT = 25;

/** The fewest characters, once trimmed, the server searches for. */
export const DIRECTORY_SEARCH_MIN = 2;

/** The most characters the server searches for; the picker's field takes no more. */
export const DIRECTORY_SEARCH_MAX = 100;

const base = "/api/v1/entra/directory";

/** The Owner's search of the tenant by display name, mail and principal name; 503 when the tenant cannot be asked. */
export const directoryUsersQueryOptions = (q: string) =>
  queryOptions({
    queryKey: [ENTRA_QUERY_KEY, "directory", "users", q],
    queryFn: ({ signal }) => request<EntraDirectoryUsers>(`${base}/users?q=${encodeURIComponent(q)}`, { signal }),
    enabled: q.trim().length >= DIRECTORY_SEARCH_MIN,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });

/**
 * The exact matches between the users not yet connected and the tenant's accounts; 409 `sso_not_configured` while
 * single sign-on is off. The read is costly for the tenant, so it is never retried on its own nor repeated when the
 * window regains focus: the Owner asks again.
 */
export const directoryMatchesQueryOptions = () =>
  queryOptions({
    queryKey: [ENTRA_QUERY_KEY, "directory", "matches"],
    queryFn: ({ signal }) => request<EntraDirectoryMatches>(`${base}/matches`, { signal }),
    retry: false,
    refetchOnWindowFocus: false,
  });
