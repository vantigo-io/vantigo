import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import { LiveProvider } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { runSendBatchesQueryOptions, type SendBatch, sendBatchQueryOptions, useSendBatchesLive } from "./send-batches";

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

const settle = (invalidate: ReturnType<typeof vi.spyOn>) => {
  tick(250);
  expect(invalidate).toHaveBeenCalledTimes(1);
  invalidate.mockClear();
};

const keys = (invalidate: ReturnType<typeof vi.spyOn>) =>
  invalidate.mock.calls.map((call: unknown[]) => (call[0] as { queryKey: unknown[] }).queryKey);

describe("useSendBatchesLive", () => {
  const topic = "invoices.send-batches";

  it("a batch's page ignores another batch's event and takes its own", () => {
    const { view, socket, invalidate } = render(() => useSendBatchesLive(1001));
    expect(view.result.current).toBe(false);
    socket().receive({ type: "ok", topic });
    expect(view.result.current).toBe(true);
    settle(invalidate);
    socket().receive({ type: "event", topic, kind: "batch.changed", id: "1002" });
    tick(250);
    expect(invalidate).not.toHaveBeenCalled();
    socket().receive({ type: "event", topic, kind: "batch.changed", id: "1001" });
    tick(250);
    expect(keys(invalidate)).toEqual([["invoices", "send-batch", 1001]]);
  });

  it("a run's Sending card refetches on any batch's event", () => {
    const { socket, invalidate } = render(() => useSendBatchesLive());
    socket().receive({ type: "ok", topic });
    settle(invalidate);
    socket().receive({ type: "event", topic, kind: "batch.changed", id: "1002" });
    tick(250);
    expect(keys(invalidate)).toEqual([["invoices", "send-batches"]]);
  });
});

describe("the send batches' polls", () => {
  const running = { finishedAt: undefined } as unknown as SendBatch;
  const finished = { finishedAt: "2026-09-12T08:01:00Z" } as unknown as SendBatch;
  const interval = (options: { refetchInterval?: unknown }, data: unknown) =>
    (options.refetchInterval as (query: { state: { data: unknown } }) => number | false)({ state: { data } });

  it("poll while a batch runs and the topic is not live, never while it is", () => {
    expect(interval(sendBatchQueryOptions(1001), { batch: running })).toBe(5000);
    expect(interval(sendBatchQueryOptions(1001), { batch: finished })).toBe(false);
    expect(interval(sendBatchQueryOptions(1001, true), { batch: running })).toBe(false);
    expect(interval(runSendBatchesQueryOptions(7), { data: [running] })).toBe(5000);
    expect(interval(runSendBatchesQueryOptions(7, true), { data: [running] })).toBe(false);
  });
});
