import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { useOrdersLive } from "./orders";
import { MANUFACTURING_QUERY_KEY } from "./request";

const heard = vi.hoisted(() => new Map<string, () => void>());

vi.mock("@vantigo/frontend-shell", async (actual) => ({
  ...(await actual<typeof import("@vantigo/frontend-shell")>()),
  useLiveTopic: (topic: string, onEvent: () => void) => {
    heard.set(topic, onEvent);
    return true;
  },
}));

const invalidatedOn = (topic: string, orderId?: number) => {
  const client = new QueryClient();
  const invalidate = vi.spyOn(client, "invalidateQueries");
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  renderHook(() => useOrdersLive(orderId), { wrapper });
  heard.get(topic)?.();
  return invalidate.mock.calls.map(([filters]) => filters?.queryKey);
};

describe("the orders' live topics", () => {
  it("read the list and the overview's counts again on the list's event", () => {
    expect(invalidatedOn("manufacturing.orders")).toEqual([
      [MANUFACTURING_QUERY_KEY, "orders"],
      [MANUFACTURING_QUERY_KEY, "stats"],
    ]);
  });

  it("read only the order again on its own event", () => {
    expect(invalidatedOn("manufacturing.order:5", 5)).toEqual([[MANUFACTURING_QUERY_KEY, "order", 5]]);
  });
});
