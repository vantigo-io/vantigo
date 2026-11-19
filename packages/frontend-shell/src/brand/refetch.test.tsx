import { MantineProvider } from "@mantine/core";
import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { BrandProvider } from "./provider";
import { BRAND_REFETCH_MAX_MS, BRAND_REFETCH_MIN_MS, brandRefetchDelay } from "./refetch";
import { type BrandPayload, builtinPayload, computeTokens } from "./tokens";

const HOUR = 60 * 60 * 1000;
const DAY = 24 * HOUR;
const NOW = Date.parse("2026-11-19T12:00:00Z");

const payload = (validUntil: string | null, over: Partial<BrandPayload> = {}): BrandPayload => ({
  ...builtinPayload(),
  brandId: "11111111-1111-1111-1111-111111111111",
  version: "11111111-1111-1111-1111-111111111111-3",
  tokens: computeTokens("#0f766e", "md"),
  ...(validUntil ? { validUntil } : {}),
  ...over,
});

describe("brandRefetchDelay", () => {
  it("is the time to the boundary when that is under six hours", () => {
    expect(brandRefetchDelay(new Date(NOW + 2 * HOUR).toISOString(), NOW)).toBe(2 * HOUR);
  });

  it("is capped at six hours, far below the 24.8 days a browser timer can hold", () => {
    expect(brandRefetchDelay(new Date(NOW + 60 * DAY).toISOString(), NOW)).toBe(BRAND_REFETCH_MAX_MS);
    expect(BRAND_REFETCH_MAX_MS).toBeLessThan(2 ** 31 - 1);
  });

  it("never asks in a tight loop for a boundary already passed", () => {
    expect(brandRefetchDelay(new Date(NOW - HOUR).toISOString(), NOW)).toBe(BRAND_REFETCH_MIN_MS);
  });

  it("is null when the payload names no boundary", () => {
    expect(brandRefetchDelay(null, NOW)).toBeNull();
    expect(brandRefetchDelay(undefined, NOW)).toBeNull();
    expect(brandRefetchDelay("not a date", NOW)).toBeNull();
  });
});

// Advances the fake clock inside act, so React renders what the fetches resolved to.
const tick = (ms: number) =>
  act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });

describe("the brand provider's refetch", () => {
  let validUntil: string | null;
  const fetchMock = vi.fn(() =>
    Promise.resolve(
      new Response(JSON.stringify(payload(validUntil)), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );

  const mount = async () => {
    render(
      <MantineProvider env="test">
        <BrandProvider userId="user-1">
          <span />
        </BrandProvider>
      </MantineProvider>,
    );
    await tick(0);
  };

  beforeEach(() => {
    vi.useFakeTimers({ now: NOW, toFake: ["setTimeout", "clearTimeout", "Date"] });
    fetchMock.mockClear();
    vi.stubGlobal("fetch", fetchMock);
    window.localStorage.clear();
    window.sessionStorage.clear();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("does not fire at once for a boundary sixty days away, and asks again after six hours", async () => {
    validUntil = new Date(NOW + 60 * DAY).toISOString();
    await mount();
    expect(fetchMock).toHaveBeenCalledTimes(1);

    // setTimeout with 60 days would overflow and fire immediately.
    await tick(1000);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await tick(BRAND_REFETCH_MAX_MS - 2000);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await tick(2000);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("asks again at the boundary when it is nearer than six hours", async () => {
    validUntil = new Date(NOW + 90 * 60 * 1000).toISOString();
    await mount();
    await tick(89 * 60 * 1000);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await tick(2 * 60 * 1000);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("sets no timer when the payload names no boundary", async () => {
    validUntil = null;
    await mount();
    await tick(30 * DAY);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("asks when a tab wakes after the boundary, though its timer never ran", async () => {
    validUntil = new Date(NOW + 2 * HOUR).toISOString();
    await mount();
    expect(fetchMock).toHaveBeenCalledTimes(1);

    // The laptop slept for three hours: the clock moved, no timer ran.
    vi.setSystemTime(NOW + 3 * HOUR);
    document.dispatchEvent(new Event("visibilitychange"));
    await tick(0);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("does not ask on becoming visible while the boundary is still ahead", async () => {
    validUntil = new Date(NOW + 2 * HOUR).toISOString();
    await mount();
    vi.setSystemTime(NOW + 30 * 60 * 1000);
    document.dispatchEvent(new Event("visibilitychange"));
    await tick(0);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
