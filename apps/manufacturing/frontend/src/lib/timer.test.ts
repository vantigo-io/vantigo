import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { elapsedMs, formatElapsed, LONG_RUNNING_MS, useElapsed } from "./timer";

describe("the running timer", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-11-11T10:00:00Z"));
  });
  afterEach(() => vi.useRealTimers());

  it("counts on the client from startedAt, a second at a time", () => {
    const { result } = renderHook(() => useElapsed("2026-11-11T09:58:55Z"));
    expect(formatElapsed(result.current)).toBe("0:01:05");
    act(() => vi.advanceTimersByTime(3000));
    expect(formatElapsed(result.current)).toBe("0:01:08");
    act(() => vi.advanceTimersByTime(60 * 60 * 1000));
    expect(formatElapsed(result.current)).toBe("1:01:08");
  });

  it("counts nothing and sets no interval without a start", () => {
    const { result } = renderHook(() => useElapsed(null));
    expect(result.current).toBe(0);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("stops its interval when unmounted", () => {
    const { unmount } = renderHook(() => useElapsed("2026-11-11T09:00:00Z"));
    expect(vi.getTimerCount()).toBe(1);
    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("passes 12 hours exactly when the warning starts", () => {
    const now = new Date("2026-11-11T10:00:00Z").getTime();
    expect(elapsedMs("2026-11-10T22:00:00Z", now)).toBe(LONG_RUNNING_MS);
    expect(elapsedMs("2026-11-11T11:00:00Z", now)).toBe(0);
    expect(formatElapsed(LONG_RUNNING_MS + 61_000)).toBe("12:01:01");
  });
});
