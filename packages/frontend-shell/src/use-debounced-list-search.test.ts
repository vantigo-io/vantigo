// @vitest-environment jsdom

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useDebouncedListSearch } from "./use-debounced-list-search";

describe("useDebouncedListSearch", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("debounces search navigation and resets to the first page", () => {
    const onNavigate = vi.fn();
    const { result } = renderHook(() => useDebouncedListSearch({ currentSearch: "", onNavigate }));

    act(() => result.current.setSearchInput("alice"));
    act(() => vi.advanceTimersByTime(299));
    expect(onNavigate).not.toHaveBeenCalled();

    act(() => vi.advanceTimersByTime(1));
    expect(onNavigate).toHaveBeenCalledOnce();
    expect(onNavigate).toHaveBeenCalledWith({ page: 1, search: "alice" }, { replace: true });
  });

  it("keeps the current search when changing pages", () => {
    const onNavigate = vi.fn();
    const { result } = renderHook(() => useDebouncedListSearch({ currentSearch: "alice", onNavigate }));

    act(() => result.current.onPageChange(3));

    expect(onNavigate).toHaveBeenCalledOnce();
    expect(onNavigate).toHaveBeenCalledWith({ page: 3, search: "alice" });
  });
});
