import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import { LiveProvider } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type MailboxSync, mailboxSyncQueryKey, mailboxSyncQueryOptions, useMailboxSyncLive } from "./mailbox-sync";

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

describe("useMailboxSyncLive", () => {
  const topic = "calendar.mailbox-sync:me";

  it("listens to the caller's own topic, is live once confirmed, and refetches the card on an event or a resync", () => {
    const { view, socket, invalidate } = render(() => useMailboxSyncLive());
    expect(view.result.current).toBe(false);
    socket().receive({ type: "ok", topic });
    expect(view.result.current).toBe(true);
    settle(invalidate);
    socket().receive({ type: "event", topic, kind: "sync.changed", id: "me" });
    tick(250);
    socket().receive({ type: "resync", topic });
    tick(250);
    expect(keys(invalidate)).toEqual([[...mailboxSyncQueryKey], [...mailboxSyncQueryKey]]);
  });
});

describe("the card's poll", () => {
  const waiting = { availability: "not_connected", enabled: true, state: "pending" } as unknown as MailboxSync;
  const quiet = { availability: "available", enabled: true, state: "active" } as unknown as MailboxSync;
  const interval = (options: { refetchInterval?: unknown }, data: unknown) =>
    (options.refetchInterval as (query: { state: { data: unknown } }) => number | false)({ state: { data } });

  it("polls every fifteen seconds while it waits and the topic is not live, never while it is", () => {
    expect(interval(mailboxSyncQueryOptions(), waiting)).toBe(15_000);
    expect(interval(mailboxSyncQueryOptions(), quiet)).toBe(false);
    expect(interval(mailboxSyncQueryOptions(true), waiting)).toBe(false);
  });
});
