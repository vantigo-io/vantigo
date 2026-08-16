import { type Dispatch, type SetStateAction, useCallback, useEffect, useRef, useState } from "react";

export interface DebouncedListSearchState {
  page: number;
  search: string;
}

export interface DebouncedListSearchNavigationOptions {
  replace?: boolean;
}

export type DebouncedListSearchNavigate = (
  state: DebouncedListSearchState,
  options?: DebouncedListSearchNavigationOptions,
) => void;

export interface UseDebouncedListSearchOptions {
  currentSearch: string;
  onNavigate: DebouncedListSearchNavigate;
  debounceMs?: number;
}

export interface UseDebouncedListSearchResult {
  searchInput: string;
  setSearchInput: Dispatch<SetStateAction<string>>;
  onPageChange: (page: number) => void;
}

/**
 * Keeps list search input local while synchronizing its debounced value and
 * pagination through a caller-provided URL/navigation adapter.
 */
export const useDebouncedListSearch = ({
  currentSearch,
  onNavigate,
  debounceMs = 300,
}: UseDebouncedListSearchOptions): UseDebouncedListSearchResult => {
  const [searchInput, setSearchInput] = useState(currentSearch);
  const [debouncedSearch, setDebouncedSearch] = useState(currentSearch);
  const onNavigateRef = useRef(onNavigate);

  useEffect(() => {
    onNavigateRef.current = onNavigate;
  }, [onNavigate]);

  useEffect(() => {
    const timeoutId = setTimeout(() => setDebouncedSearch(searchInput), debounceMs);
    return () => clearTimeout(timeoutId);
  }, [debounceMs, searchInput]);

  useEffect(() => {
    if (debouncedSearch !== currentSearch) {
      onNavigateRef.current({ page: 1, search: debouncedSearch }, { replace: true });
    }
  }, [currentSearch, debouncedSearch]);

  const onPageChange = useCallback(
    (page: number) => onNavigateRef.current({ page, search: currentSearch }),
    [currentSearch],
  );

  return { searchInput, setSearchInput, onPageChange };
};
