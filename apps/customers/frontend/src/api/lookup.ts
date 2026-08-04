import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";

export interface LookupResult {
  legalId: string;
  legalName: string;
}

export interface LookupResponse {
  data: LookupResult[];
}

export type BrregLookupParams = { search: string } | { legalId: string };

export async function fetchBrregLookup(params: BrregLookupParams, signal?: AbortSignal): Promise<LookupResponse> {
  const searchParams = new URLSearchParams(params);
  try {
    return await request(`/api/v1/lookup/brreg?${searchParams}`, { signal });
  } catch (error) {
    if ((error as { status?: number }).status) {
      throw new Error(`Lookup failed (HTTP ${(error as { status: number }).status})`, { cause: error });
    }
    throw error;
  }
}

export const MIN_LOOKUP_SEARCH_LENGTH = 2;

export const brregLookupQueryOptions = (search: string) =>
  queryOptions({
    queryKey: ["lookup", "brreg", search],
    queryFn: ({ signal }) => fetchBrregLookup({ search }, signal),
    enabled: search.trim().length >= MIN_LOOKUP_SEARCH_LENGTH,
    staleTime: 60_000,
  });
