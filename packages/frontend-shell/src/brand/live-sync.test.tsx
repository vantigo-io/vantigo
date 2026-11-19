import { MantineProvider } from "@mantine/core";
import { act, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { LiveEvent } from "../live";
import { BRAND_TOPIC, BrandLiveSync } from "./live-sync";
import { BRAND_REFRESH_EVENT, BrandProvider, useBrand } from "./provider";
import { type BrandPayload, builtinPayload, computeTokens } from "./tokens";

// The live client has its own tests; here the topics' handlers are captured and called as it would.
const handlers = new Map<string, (event: LiveEvent) => void>();
vi.mock("../live", () => ({
  useLiveUserId: () => "user-1",
  useLiveTopic: (topic: string | null, onEvent: (event: LiveEvent) => void) => {
    if (topic) handlers.set(topic, onEvent);
    return true;
  },
}));

const hear = (topic: string, event: LiveEvent) => act(() => handlers.get(topic)?.(event));
const resync: LiveEvent = { kind: "resync" };
const changed: LiveEvent = { kind: "brand.changed", ids: [] };

afterEach(() => {
  vi.useRealTimers();
  handlers.clear();
});

describe("BrandLiveSync", () => {
  it("subscribes to the workspace topic and to the person's own", () => {
    render(<BrandLiveSync />);
    expect([...handlers.keys()].sort()).toEqual([BRAND_TOPIC, `${BRAND_TOPIC}:user-1`]);
  });

  it("asks again after a random wait of up to five seconds, once however many events come", () => {
    vi.useFakeTimers();
    const refreshed = vi.fn();
    window.addEventListener(BRAND_REFRESH_EVENT, refreshed);
    render(<BrandLiveSync random={() => 0.5} />);
    hear(BRAND_TOPIC, resync); // the subscription's first confirmation is not a change
    hear(BRAND_TOPIC, changed);
    hear(BRAND_TOPIC, changed);
    hear(BRAND_TOPIC, { kind: "rules.changed", ids: [] });
    act(() => vi.advanceTimersByTime(2499));
    expect(refreshed).not.toHaveBeenCalled();
    act(() => vi.advanceTimersByTime(1));
    expect(refreshed).toHaveBeenCalledTimes(1);
    act(() => vi.advanceTimersByTime(10000));
    expect(refreshed).toHaveBeenCalledTimes(1);
    // The next change is waited for afresh, and a reconnect's resync counts as one.
    hear(BRAND_TOPIC, resync);
    act(() => vi.advanceTimersByTime(2500));
    expect(refreshed).toHaveBeenCalledTimes(2);
    window.removeEventListener(BRAND_REFRESH_EVENT, refreshed);
  });

  it("asks at once when the person's own switch is heard, and not for the first confirmation", () => {
    vi.useFakeTimers();
    const refreshed = vi.fn();
    window.addEventListener(BRAND_REFRESH_EVENT, refreshed);
    render(<BrandLiveSync />);
    hear(`${BRAND_TOPIC}:user-1`, resync);
    expect(refreshed).not.toHaveBeenCalled();
    hear(`${BRAND_TOPIC}:user-1`, { kind: "brand.chosen", ids: [] });
    expect(refreshed).toHaveBeenCalledTimes(1);
    window.removeEventListener(BRAND_REFRESH_EVENT, refreshed);
  });

  it("stops waiting when it unmounts", () => {
    vi.useFakeTimers();
    const refreshed = vi.fn();
    window.addEventListener(BRAND_REFRESH_EVENT, refreshed);
    const view = render(<BrandLiveSync random={() => 0.9} />);
    hear(BRAND_TOPIC, changed);
    view.unmount();
    act(() => vi.advanceTimersByTime(6000));
    expect(refreshed).not.toHaveBeenCalled();
    window.removeEventListener(BRAND_REFRESH_EVENT, refreshed);
  });
});

describe("the brand provider and a live event", () => {
  const teal = (over: Partial<BrandPayload> = {}): BrandPayload => ({
    ...builtinPayload(),
    brandId: "11111111-1111-1111-1111-111111111111",
    version: "v-1",
    name: "Acme",
    title: "Acme Portal",
    tokens: computeTokens("#0f766e", "lg"),
    ...over,
  });
  let served: BrandPayload;
  const fetchMock = vi.fn(() =>
    Promise.resolve(
      new Response(JSON.stringify(served), { status: 200, headers: { "Content-Type": "application/json" } }),
    ),
  );

  const Probe = () => {
    const brand = useBrand();
    return (
      <div>
        <span data-testid="title">{brand.title}</span>
        <span data-testid="source">{brand.source}</span>
        <span data-testid="ignored">{String(brand.choiceIgnored ?? false)}</span>
      </div>
    );
  };

  beforeEach(() => {
    served = teal();
    fetchMock.mockClear();
    vi.stubGlobal("fetch", fetchMock);
    window.localStorage.clear();
    window.sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("refetches once on an event and repaints, with the new source and the choice reported ignored", async () => {
    render(
      <MantineProvider env="test">
        <BrandProvider userId="user-1">
          <BrandLiveSync random={() => 0} />
          <Probe />
        </BrandProvider>
      </MantineProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("title")).toHaveTextContent("Acme Portal"));
    expect(fetchMock).toHaveBeenCalledTimes(1);

    served = teal({ title: "Nordlys", source: "rule", choiceIgnored: true, version: "v-2" });
    hear(BRAND_TOPIC, changed);
    hear(BRAND_TOPIC, changed);
    await waitFor(() => expect(screen.getByTestId("title")).toHaveTextContent("Nordlys"));
    expect(screen.getByTestId("source")).toHaveTextContent("rule");
    expect(screen.getByTestId("ignored")).toHaveTextContent("true");
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(document.title).toBe("Nordlys");
  });
});
