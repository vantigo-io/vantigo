import { queryOptions } from "@tanstack/react-query";

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
  const response = await fetch(`/api/v1/lookup/brreg?${searchParams}`, { signal });

  if (!response.ok) {
    throw new Error(`Lookup failed (HTTP ${response.status})`);
  }

  return response.json();
}

export const MIN_LOOKUP_SEARCH_LENGTH = 2;

export const brregLookupQueryOptions = (search: string) =>
  queryOptions({
    queryKey: ["lookup", "brreg", search],
    queryFn: ({ signal }) => fetchBrregLookup({ search }, signal),
    enabled: search.trim().length >= MIN_LOOKUP_SEARCH_LENGTH,
    staleTime: 60_000,
  });
