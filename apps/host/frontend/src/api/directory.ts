import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { request } from "./request";
import { ssoSettingsQueryKey } from "./sso";

type Schemas = components["schemas"];

/** One account in the configured provider's directory, with who already holds its keys, when anyone does. */
export type DirectoryUser = Schemas["SsoDirectoryUser"];
export type DirectoryHolder = Schemas["SsoDirectoryHolder"];
export type DirectoryUsers = Schemas["SsoDirectoryUsers"];
export type DirectoryMatch = Schemas["SsoDirectoryMatch"];
export type DirectoryMatches = Schemas["SsoDirectoryMatches"];
/** The directory the SSO settings name: the configured provider's, when a turned-on module serves it. */
export type SsoDirectory = Schemas["SsoDirectory"];

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

const base = "/api/v1/identity/owner/sso/directory";

/**
 * The Owner's search of the configured provider's directory by name and address; 409 `no_directory` when there is
 * none, 503 `provider_unreachable` when it cannot be asked.
 */
export const directoryUsersQueryOptions = (search: string) =>
  queryOptions({
    queryKey: [...ssoSettingsQueryKey, "directory", "users", search],
    queryFn: ({ signal }) => request<DirectoryUsers>(`${base}/users?search=${encodeURIComponent(search)}`, { signal }),
    enabled: search.trim().length >= DIRECTORY_SEARCH_MIN,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });

/**
 * The exact matches between the users not yet connected and the directory's accounts. The read is costly for the
 * directory, so it is never retried on its own nor repeated when the window regains focus: the Owner asks again.
 */
export const directoryMatchesQueryOptions = () =>
  queryOptions({
    queryKey: [...ssoSettingsQueryKey, "directory", "matches"],
    queryFn: ({ signal }) => request<DirectoryMatches>(`${base}/matches`, { signal }),
    retry: false,
    refetchOnWindowFocus: false,
  });
