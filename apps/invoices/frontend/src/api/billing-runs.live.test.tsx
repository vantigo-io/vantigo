import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import { LiveProvider } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type BillingRun, billingRunQueryOptions, billingRunsQueryOptions, useBillingRunsLive } from "./billing-runs";

class FakeSocket {
  static all: FakeSocket[] = [];
  onopen: (() => void) | null = null;
  onclose: ((event: { code: number }) => void) | null = null;
  onmessage: ((event: { data: unknown }) => void) | null = null;
  constructor() {
    FakeSocket.all.push(this);
  }
  send() {}
  close() {}
  receive(frame: Record<string, unknown>) {
    act(() => this.onmessage?.({ data: JSON.stringify(frame) }));
  }
}

const createSocket = () => new FakeSocket();
const tick = (ms: number) => act(() => void vi.advanceTimersByTime(ms));

beforeEach(() => {
  FakeSocket.all = [];
  vi.useFakeTimers();
  vi.spyOn(Math, "random").mockReturnValue(0.5);
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

/** Renders the hook under a live provider (user "me") and a query client whose invalidations are recorded. */
const render = <T,>(hook: () => T) => {
  const client = new QueryClient();
  const invalidate = vi.spyOn(client, "invalidateQueries").mockResolvedValue();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>
      <LiveProvider userId="me" url="ws://test" createSocket={createSocket}>
        {children}
      </LiveProvider>
    </QueryClientProvider>
  );
  const view = renderHook(hook, { wrapper });
  const socket = () => FakeSocket.all[FakeSocket.all.length - 1] as FakeSocket;
  act(() => socket().onopen?.());
  return { view, socket, invalidate };
};

/** Subscribing ends in a resync, which refetches whatever is shown: drop that call, then count the events'. */
const settle = (invalidate: ReturnType<typeof vi.spyOn>) => {
  tick(250);
  expect(invalidate).toHaveBeenCalledTimes(1);
  invalidate.mockClear();
};

const keys = (invalidate: ReturnType<typeof vi.spyOn>) =>
  invalidate.mock.calls.map((call: unknown[]) => (call[0] as { queryKey: unknown[] }).queryKey);

describe("useBillingRunsLive", () => {
  const topic = "invoices.billing-runs";

  it("is live only once the server confirmed the topic, and the list refetches on any event", () => {
    const { view, socket, invalidate } = render(() => useBillingRunsLive());
    expect(view.result.current).toBe(false);
    socket().receive({ type: "ok", topic });
    expect(view.result.current).toBe(true);
    settle(invalidate);
    socket().receive({ type: "event", topic, kind: "run.changed", id: "7" });
    tick(250);
    expect(keys(invalidate)).toEqual([["invoices", "billing-runs"]]);
  });

  it("a run's page ignores another run's event, takes its own, an event without an id and a resync", () => {
    const { socket, invalidate } = render(() => useBillingRunsLive(7));
    socket().receive({ type: "ok", topic });
    settle(invalidate);
    socket().receive({ type: "event", topic, kind: "run.changed", id: "8" });
    tick(250);
    expect(invalidate).not.toHaveBeenCalled();
    socket().receive({ type: "event", topic, kind: "run.changed", id: "7" });
    tick(250);
    socket().receive({ type: "event", topic, kind: "run.changed" });
    tick(250);
    socket().receive({ type: "resync", topic });
    tick(250);
    expect(keys(invalidate)).toEqual([
      ["invoices", "billing-run", 7],
      ["invoices", "billing-run", 7],
      ["invoices", "billing-run", 7],
    ]);
  });
});

describe("the billing runs' polls", () => {
  const running = { status: "running" } as unknown as BillingRun;
  const done = { status: "done" } as unknown as BillingRun;
  const interval = (options: { refetchInterval?: unknown }, data: unknown) =>
    (options.refetchInterval as (query: { state: { data: unknown } }) => number | false)({ state: { data } });

  it("poll every five seconds while a run is at work and the topic is not live, never while it is", () => {
    expect(interval(billingRunsQueryOptions(1), { data: [running] })).toBe(5000);
    expect(interval(billingRunsQueryOptions(1), { data: [done] })).toBe(false);
    expect(interval(billingRunsQueryOptions(1, true), { data: [running] })).toBe(false);
    expect(interval(billingRunQueryOptions(7, null, 1), { run: running })).toBe(5000);
    expect(interval(billingRunQueryOptions(7, null, 1), { run: done })).toBe(false);
    expect(interval(billingRunQueryOptions(7, null, 1, true), { run: running })).toBe(false);
  });
});
