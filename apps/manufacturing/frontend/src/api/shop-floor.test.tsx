import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { MANUFACTURING_QUERY_KEY } from "./request";
import { useWorkCenterOrdersLive, useWorkOrderOrderLive } from "./shop-floor";

const listening = vi.hoisted(() => new Map<string, () => void>());

vi.mock("@vantigo/frontend-shell", async (actual) => ({
  ...(await actual<typeof import("@vantigo/frontend-shell")>()),
  useLiveClient: () => ({
    listen: (topic: string, onEvent: () => void) => {
      listening.set(topic, onEvent);
      return () => listening.delete(topic);
    },
  }),
}));

const render = (hook: () => void) => {
  listening.clear();
  const client = new QueryClient();
  const invalidate = vi.spyOn(client, "invalidateQueries");
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const rendered = renderHook(hook, { wrapper });
  return { invalidate, rendered };
};

describe("the shop floor's order topics", () => {
  it("read a work center's queue again on an event of any order in it, one listen per order", () => {
    const { invalidate, rendered } = render(() => useWorkCenterOrdersLive(1, [5, 6, 5]));
    expect([...listening.keys()].sort()).toEqual(["manufacturing.order:5", "manufacturing.order:6"]);
    listening.get("manufacturing.order:6")?.();
    expect(invalidate.mock.calls.map(([filters]) => filters?.queryKey)).toEqual([
      [MANUFACTURING_QUERY_KEY, "shop-floor", "work-center", 1],
    ]);
    rendered.unmount();
    expect(listening.size).toBe(0);
  });

  it("read a work order's view again on its order's event, once the order is known", () => {
    const { invalidate } = render(() => useWorkOrderOrderLive(40, 5));
    listening.get("manufacturing.order:5")?.();
    expect(invalidate.mock.calls.map(([filters]) => filters?.queryKey)).toEqual([
      [MANUFACTURING_QUERY_KEY, "shop-floor", "work-order", 40],
    ]);
    render(() => useWorkOrderOrderLive(40, undefined));
    expect(listening.size).toBe(0);
  });
});
