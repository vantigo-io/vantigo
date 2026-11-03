import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it } from "vitest";
import { sent } from "../test/helpers";
import { stubInventoryApi } from "../test/server";
import { useStockTotals } from "./stock-totals";

const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    {children}
  </QueryClientProvider>
);

describe("useStockTotals (I33)", () => {
  it("asks in batches of at most 100 ids, and answers 0 for a variant with no stock", async () => {
    const fetchMock = stubInventoryApi({
      balances: [
        { variantId: 101, locationId: 12, onHand: 2 },
        { variantId: 101, locationId: 13, onHand: 3.5 },
        { variantId: 201, locationId: 12, onHand: 4 },
      ],
    });
    const ids = [201, ...Array.from({ length: 149 }, (_, i) => 1000 + i), 101, 101];
    const { result } = renderHook(() => useStockTotals(ids), { wrapper });
    await waitFor(() => expect(result.current.data).toBeDefined());
    const asked = sent(fetchMock, "GET", /\/stock\?/).map((one) =>
      decodeURIComponent(new URL(one.url, "http://x").searchParams.get("variantIds") ?? "").split(","),
    );
    expect(asked.map((batch) => batch.length)).toEqual([100, 51]);
    expect(asked[0][0]).toBe("101");
    expect(result.current.data?.get(101)).toBe(5.5);
    expect(result.current.data?.get(201)).toBe(4);
    expect(result.current.data?.get(1000)).toBe(0);
    expect(result.current.data?.size).toBe(151);
  });

  it("asks nothing when held back", async () => {
    const fetchMock = stubInventoryApi();
    const { result } = renderHook(() => useStockTotals([101], { enabled: false }), { wrapper });
    expect(result.current).toEqual({ data: undefined, isPending: false, error: null });
    expect(sent(fetchMock, "GET", /\/stock\?/)).toHaveLength(0);
  });
});
