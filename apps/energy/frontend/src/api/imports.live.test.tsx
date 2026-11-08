import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import { LiveProvider } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  type EnergyImport,
  importQueryKey,
  importQueryOptions,
  importsQueryKey,
  importsQueryOptions,
  useImportsLive,
} from "./imports";

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

describe("useImportsLive", () => {
  const topic = "energy.imports";

  it("is live only once the server confirmed the topic, and refetches on an event", () => {
    const { view, socket, invalidate } = render(() => useImportsLive());
    expect(view.result.current).toBe(false);
    socket().receive({ type: "ok", topic });
    expect(view.result.current).toBe(true);
    settle(invalidate);
    socket().receive({ type: "event", topic, kind: "import.changed", id: "7" });
    tick(250);
    expect(keys(invalidate)).toEqual([[...importsQueryKey]]);
  });

  it("an import's page ignores another import's event, takes its own, an event without an id and a resync", () => {
    const { socket, invalidate } = render(() => useImportsLive(7));
    socket().receive({ type: "ok", topic });
    settle(invalidate);
    socket().receive({ type: "event", topic, kind: "import.changed", id: "8" });
    tick(250);
    expect(invalidate).not.toHaveBeenCalled();
    socket().receive({ type: "event", topic, kind: "import.changed", id: "7" });
    tick(250);
    socket().receive({ type: "event", topic, kind: "import.changed" });
    tick(250);
    socket().receive({ type: "resync", topic });
    tick(250);
    expect(keys(invalidate)).toEqual([[...importQueryKey(7)], [...importQueryKey(7)], [...importQueryKey(7)]]);
  });
});

describe("useImportsLive, what it refetches", () => {
  const topic = "energy.imports";
  const options = (invalidate: { mock: { calls: unknown[][] } }) =>
    invalidate.mock.calls[0]?.[0] as {
      exact?: boolean;
      predicate?: (query: { queryKey: unknown[] }) => boolean;
    };

  it("an import's page refetches its own query only, not its cached rows", () => {
    const { socket, invalidate } = render(() => useImportsLive(7));
    socket().receive({ type: "ok", topic });
    settle(invalidate);
    socket().receive({ type: "event", topic, kind: "import.changed", id: "7" });
    tick(250);
    expect(options(invalidate).exact).toBe(true);
  });

  it("the list refetches the list and the imports, not their rows", () => {
    const { socket, invalidate } = render(() => useImportsLive());
    socket().receive({ type: "ok", topic });
    settle(invalidate);
    socket().receive({ type: "event", topic, kind: "import.changed", id: "7" });
    tick(250);
    const predicate = options(invalidate).predicate;
    expect(predicate?.({ queryKey: ["energy", "imports", { page: 1, pageSize: 10 }] })).toBe(true);
    expect(predicate?.({ queryKey: ["energy", "imports", 7] })).toBe(true);
    expect(predicate?.({ queryKey: ["energy", "imports", 7, "rows", "done"] })).toBe(false);
  });
});

describe("the imports' polls", () => {
  const imp = { state: "checking" } as unknown as EnergyImport;
  const interval = (options: { refetchInterval?: unknown }, data: unknown) =>
    (options.refetchInterval as (query: { state: { data: unknown } }) => number | false)({ state: { data } });

  it("poll every two seconds while an import is running and the topic is not live, never while it is", () => {
    expect(interval(importQueryOptions(1), imp)).toBe(2000);
    expect(interval(importQueryOptions(1, true), imp)).toBe(false);
    expect(interval(importsQueryOptions(1, 10), { data: [imp] })).toBe(2000);
    expect(interval(importsQueryOptions(1, 10, true), { data: [imp] })).toBe(false);
  });
});
