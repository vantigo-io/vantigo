import { act, render, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { LiveEvent, LiveSocket, PresenceMember } from "./client";
import { LiveProvider, useLiveStatus, useLiveTopic, usePresence } from "./index";

class FakeSocket implements LiveSocket {
  static all: FakeSocket[] = [];
  sent: Record<string, unknown>[] = [];
  closed = false;
  onopen: (() => void) | null = null;
  onclose: ((event: { code: number }) => void) | null = null;
  onmessage: ((event: { data: unknown }) => void) | null = null;
  readonly url: string;
  constructor(url: string) {
    this.url = url;
    FakeSocket.all.push(this);
  }
  send(data: string) {
    this.sent.push(JSON.parse(data));
  }
  close() {
    this.closed = true;
  }
  open() {
    act(() => this.onopen?.());
  }
  receive(frame: Record<string, unknown>) {
    act(() => this.onmessage?.({ data: JSON.stringify(frame) }));
  }
  drop(code = 1006) {
    act(() => this.onclose?.({ code }));
  }
  frames(type: string) {
    return this.sent.filter((frame) => frame.type === type);
  }
}

const createSocket = (url: string) => new FakeSocket(url);
const last = () => FakeSocket.all[FakeSocket.all.length - 1] as FakeSocket;
const wrapper = ({ children }: { children: ReactNode }) => (
  <LiveProvider userId="me" url="ws://test/api/v1/live?v=1" createSocket={createSocket}>
    {children}
  </LiveProvider>
);
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

describe("live socket", () => {
  it("debounces events 250 ms and gathers distinct ids per kind", () => {
    const events: LiveEvent[] = [];
    renderHook(() => useLiveTopic("communications.inbox", (event) => events.push(event)), { wrapper });
    last().open();
    expect(last().frames("sub")).toEqual([{ type: "sub", topic: "communications.inbox" }]);
    for (const id of ["a", "b", "a"]) {
      last().receive({ type: "event", topic: "communications.inbox", kind: "conversation.changed", id });
    }
    last().receive({ type: "event", topic: "communications.inbox", kind: "other" });
    tick(249);
    expect(events).toEqual([]);
    tick(1);
    expect(events).toEqual([
      { kind: "conversation.changed", ids: ["a", "b"] },
      { kind: "other", ids: [] },
    ]);
  });

  it("unsubscribes when the last listener unmounts", () => {
    const Listener = ({ on }: { on: boolean }) => (on ? <Hook /> : null);
    function Hook() {
      useLiveTopic("t.a", () => {});
      return null;
    }
    const view = render(
      <LiveProvider userId="me" url="ws://test" createSocket={createSocket}>
        <Listener on />
      </LiveProvider>,
    );
    last().open();
    view.rerender(
      <LiveProvider userId="me" url="ws://test" createSocket={createSocket}>
        <Listener on={false} />
      </LiveProvider>,
    );
    expect(last().frames("unsub")).toEqual([{ type: "unsub", topic: "t.a" }]);
  });

  it("subscribes two listeners on one topic once and delivers to both", () => {
    const a = vi.fn();
    const b = vi.fn();
    function Both() {
      useLiveTopic("t.a", a);
      useLiveTopic("t.a", b);
      return null;
    }
    render(<Both />, { wrapper });
    last().open();
    expect(last().frames("sub")).toHaveLength(1);
    last().receive({ type: "event", topic: "t.a", kind: "k", id: "1" });
    tick(250);
    expect(a).toHaveBeenCalledWith({ kind: "k", ids: ["1"] });
    expect(b).toHaveBeenCalledWith({ kind: "k", ids: ["1"] });
  });

  it("does not subscribe when disabled", () => {
    renderHook(() => useLiveTopic("t.a", () => {}, { enabled: false }), { wrapper });
    last().open();
    expect(last().frames("sub")).toEqual([]);
  });

  it("reconnects with backoff, re-subscribes and hands every subscriber a resync", () => {
    const events: LiveEvent[] = [];
    const status = renderHook(
      () => {
        useLiveTopic("t.a", (event) => events.push(event));
        return useLiveStatus();
      },
      { wrapper },
    );
    expect(status.result.current).toBe("connecting");
    const first = last();
    first.open();
    expect(status.result.current).toBe("live");
    first.drop();
    expect(status.result.current).toBe("connecting");
    expect(FakeSocket.all).toHaveLength(1);
    tick(999);
    expect(FakeSocket.all).toHaveLength(1);
    tick(1);
    expect(FakeSocket.all).toHaveLength(2);
    last().open();
    expect(last().frames("sub")).toEqual([{ type: "sub", topic: "t.a" }]);
    expect(status.result.current).toBe("live");
    // No resync until the server confirms the new subscription.
    tick(250);
    expect(events).toEqual([]);
    last().receive({ type: "ok", topic: "t.a" });
    tick(250);
    expect(events).toEqual([{ kind: "resync" }]);
  });

  it("doubles the backoff up to 30 s and goes unavailable after five failures, retrying every 60 s", () => {
    const status = renderHook(() => useLiveStatus(), { wrapper });
    const delays = [1000, 2000, 4000, 8000];
    for (const delay of delays) {
      last().drop();
      expect(status.result.current).toBe("connecting");
      const before = FakeSocket.all.length;
      tick(delay - 1);
      expect(FakeSocket.all).toHaveLength(before);
      tick(1);
      expect(FakeSocket.all).toHaveLength(before + 1);
    }
    last().drop();
    expect(status.result.current).toBe("unavailable");
    const before = FakeSocket.all.length;
    tick(59_999);
    expect(FakeSocket.all).toHaveLength(before);
    tick(1);
    expect(FakeSocket.all).toHaveLength(before + 1);
    last().open();
    expect(status.result.current).toBe("live");
  });

  it("is degraded after a degraded frame until a resync arrives", () => {
    const events: LiveEvent[] = [];
    const status = renderHook(
      () => {
        useLiveTopic("t.a", (event) => events.push(event));
        return useLiveStatus();
      },
      { wrapper },
    );
    last().open();
    last().receive({ type: "degraded" });
    expect(status.result.current).toBe("degraded");
    last().receive({ type: "resync" });
    expect(status.result.current).toBe("live");
    tick(250);
    expect(events).toEqual([{ kind: "resync" }]);
  });

  it("does not reconnect when the session is gone", () => {
    const status = renderHook(() => useLiveStatus(), { wrapper });
    last().open();
    last().drop(4401);
    tick(120_000);
    expect(FakeSocket.all).toHaveLength(1);
    expect(status.result.current).toBe("unavailable");
  });

  it("reports unavailable and stays inert outside a provider", () => {
    const status = renderHook(() => useLiveStatus());
    expect(status.result.current).toBe("unavailable");
    renderHook(() => useLiveTopic("t.a", () => {}));
    expect(renderHook(() => usePresence("t.a", "viewing")).result.current).toEqual([]);
  });
});

describe("presence", () => {
  const member = (userId: string, state: string, since = "2026-01-01T10:00:00Z") => ({
    userId,
    name: userId.toUpperCase(),
    state,
    since,
  });

  it("sends the state, repeats it every 20 s and withdraws it on unmount", () => {
    const Presence = ({ on }: { on: boolean }) => (on ? <Hook /> : null);
    function Hook() {
      usePresence("t.a", "viewing");
      return null;
    }
    const tree = (on: boolean) => (
      <LiveProvider userId="me" url="ws://test" createSocket={createSocket}>
        <Presence on={on} />
      </LiveProvider>
    );
    const view = render(tree(true));
    last().open();
    expect(last().frames("presence")).toEqual([{ type: "presence", topic: "t.a", state: "viewing" }]);
    tick(20_000);
    expect(last().frames("presence")).toHaveLength(2);
    view.rerender(tree(false));
    expect(last().frames("presence").at(-1)).toEqual({ type: "presence", topic: "t.a", state: null });
  });

  it("changes the announced state with the argument", () => {
    const view = renderHook(({ state }) => usePresence("t.a", state), {
      wrapper,
      initialProps: { state: "viewing" as "viewing" | "replying" },
    });
    last().open();
    view.rerender({ state: "replying" });
    expect(last().frames("presence").at(-1)).toEqual({ type: "presence", topic: "t.a", state: "replying" });
  });

  it("collapses members by person to the strongest state and leaves out the signed-in person", () => {
    const view = renderHook(() => usePresence("t.a", "viewing"), { wrapper });
    last().open();
    last().receive({
      type: "presence",
      topic: "t.a",
      members: [
        member("kari", "viewing"),
        member("kari", "editing", "2026-01-01T10:05:00Z"),
        member("kari", "replying"),
        member("ola", "replying"),
        member("me", "viewing"),
      ],
    });
    const members: readonly PresenceMember[] = view.result.current;
    expect(members).toEqual([
      { userId: "kari", name: "KARI", state: "editing", since: "2026-01-01T10:00:00Z" },
      { userId: "ola", name: "OLA", state: "replying", since: "2026-01-01T10:00:00Z" },
    ]);
    last().receive({ type: "presence", topic: "t.a", members: [] });
    expect(view.result.current).toEqual([]);
  });
});

describe("topic liveness and robustness", () => {
  it("resyncs a topic's subscribers once its subscription is confirmed", () => {
    const events: LiveEvent[] = [];
    renderHook(() => useLiveTopic("t.a", (event) => events.push(event)), { wrapper });
    last().open();
    tick(500);
    expect(events).toEqual([]);
    last().receive({ type: "ok", topic: "t.a" });
    tick(250);
    expect(events).toEqual([{ kind: "resync" }]);
  });

  it("is live per topic only after ok, and not when denied or unavailable", () => {
    const view = renderHook(
      () => ({ a: useLiveTopic("t.a", () => {}), b: useLiveTopic("t.b", () => {}), c: useLiveTopic("t.c", () => {}) }),
      {
        wrapper,
      },
    );
    expect(view.result.current).toEqual({ a: false, b: false, c: false });
    last().open();
    expect(view.result.current.a).toBe(false);
    last().receive({ type: "ok", topic: "t.a" });
    last().receive({ type: "denied", topic: "t.b", reason: "forbidden" });
    last().receive({ type: "denied", topic: "t.c", reason: "unavailable" });
    expect(view.result.current).toEqual({ a: true, b: false, c: false });
    last().receive({ type: "degraded" });
    expect(view.result.current.a).toBe(false);
  });

  it("retries a topic denied as unavailable with backoff, and never one denied for good", () => {
    renderHook(() => ({ a: useLiveTopic("t.a", () => {}), b: useLiveTopic("t.b", () => {}) }), { wrapper });
    last().open();
    last().receive({ type: "denied", topic: "t.a", reason: "unavailable" });
    last().receive({ type: "denied", topic: "t.b", reason: "limit" });
    const subs = (topic: string) =>
      last()
        .frames("sub")
        .filter((frame) => frame.topic === topic).length;
    expect([subs("t.a"), subs("t.b")]).toEqual([1, 1]);
    tick(2000);
    expect(subs("t.a")).toBe(2);
    last().receive({ type: "denied", topic: "t.a", reason: "unavailable" });
    tick(3999);
    expect(subs("t.a")).toBe(2);
    tick(1);
    expect(subs("t.a")).toBe(3);
    tick(25_000);
    expect(subs("t.b")).toBe(1);
  });

  it("is not live outside a provider", () => {
    expect(renderHook(() => useLiveTopic("t.a", () => {})).result.current).toBe(false);
  });

  it("counts a connection that closes right after opening as a failure, and 4429 goes straight to unavailable", () => {
    const status = renderHook(() => useLiveStatus(), { wrapper });
    last().open();
    last().drop(1006);
    tick(1000);
    last().open();
    last().drop(1006);
    // Second quick failure: 2 s, not 1 s.
    tick(1999);
    expect(FakeSocket.all).toHaveLength(2);
    tick(1);
    expect(FakeSocket.all).toHaveLength(3);
    last().open();
    last().drop(4429);
    expect(status.result.current).toBe("unavailable");
    tick(59_999);
    expect(FakeSocket.all).toHaveLength(3);
    tick(1);
    expect(FakeSocket.all).toHaveLength(4);
  });

  it("resets the backoff once a connection has proven itself", () => {
    renderHook(() => useLiveStatus(), { wrapper });
    last().open();
    last().drop(1006);
    tick(1000);
    last().open();
    last().receive({ type: "pong" });
    last().drop(1006);
    tick(1000);
    expect(FakeSocket.all).toHaveLength(3);
  });

  it("closes a socket that answers no ping within 10 s and reconnects", () => {
    const status = renderHook(() => useLiveStatus(), { wrapper });
    const first = last();
    first.open();
    first.receive({ type: "pong" });
    tick(30_000);
    expect(first.frames("ping")).toHaveLength(1);
    tick(9999);
    expect(first.closed).toBe(false);
    tick(1);
    expect(first.closed).toBe(true);
    expect(status.result.current).toBe("connecting");
    tick(1000);
    expect(FakeSocket.all).toHaveLength(2);
  });

  it("keeps a socket whose ping is answered", () => {
    renderHook(() => useLiveStatus(), { wrapper });
    const first = last();
    first.open();
    tick(30_000);
    first.receive({ type: "pong" });
    tick(20_000);
    expect(first.closed).toBe(false);
  });

  it("refetches on a slow-consumer resync without leaving degraded, and a reconnect resync leaves it", () => {
    const events: LiveEvent[] = [];
    const status = renderHook(
      () => {
        useLiveTopic("t.a", (event) => events.push(event));
        return useLiveStatus();
      },
      { wrapper },
    );
    last().open();
    last().receive({ type: "degraded" });
    last().receive({ type: "resync", reason: "slow_consumer" });
    expect(status.result.current).toBe("degraded");
    tick(250);
    expect(events).toEqual([{ kind: "resync" }]);
    last().receive({ type: "resync", reason: "reconnect" });
    expect(status.result.current).toBe("live");
  });

  it("withdraws presence before it unsubscribes, and sends none for an unsubscribed topic", () => {
    const Presence = ({ on }: { on: boolean }) => (on ? <Hook /> : null);
    function Hook() {
      usePresence("t.a", "viewing");
      return null;
    }
    const tree = (on: boolean) => (
      <LiveProvider userId="me" url="ws://test" createSocket={createSocket}>
        <Presence on={on} />
      </LiveProvider>
    );
    const view = render(tree(true));
    last().open();
    view.rerender(tree(false));
    const kinds = last().sent.map((frame) => `${frame.type}:${"state" in frame ? String(frame.state) : ""}`);
    expect(kinds).toEqual(["sub:", "presence:viewing", "presence:null", "unsub:"]);
  });
});
