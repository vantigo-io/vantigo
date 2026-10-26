import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { PriceChart } from "./-price-chart";
import { PricesPage } from "./prices";

vi.mock("@mantine/charts", () => ({
  LineChart: ({ data, dataKey }: { data: Array<Record<string, unknown>>; dataKey: string }) => (
    <div data-testid="price-chart">
      {data.map((item) => `${String(item[dataKey])}=${String(item.nokPerKwh)}`).join(",")}
    </div>
  ),
}));

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const coverage = {
  source: "feed",
  areas: [
    {
      priceArea: "NO1",
      intervalCount: 2160,
      daysMissingLast30: 0,
      firstStart: "2026-01-01T23:00:00Z",
      lastEnd: "2026-03-29T22:00:00Z",
      lastFetch: { day: "2026-03-29", status: "ok", attempts: 1 },
    },
    {
      priceArea: "NO2",
      intervalCount: 600,
      daysMissingLast30: 3,
      firstStart: "2026-02-01T23:00:00Z",
      lastEnd: "2026-03-26T23:00:00Z",
      lastFetch: { day: "2026-03-27", status: "failed", attempts: 4, error: "the feed answered 500" },
    },
    { priceArea: "NO4", intervalCount: 0, daysMissingLast30: 30 },
  ],
};

const intervals = [
  {
    id: 1,
    priceArea: "NO1",
    start: "2026-03-28T23:00:00Z",
    end: "2026-03-29T00:00:00Z",
    nokPerKwh: 0.8123,
    source: "Feed",
    state: "final",
  },
  {
    id: 2,
    priceArea: "NO1",
    start: "2026-03-29T00:00:00Z",
    end: "2026-03-29T01:00:00Z",
    nokPerKwh: 0.7,
    source: "Feed",
    state: "final",
  },
];

const renderPage = (
  props: { canImport?: boolean },
  answer: (url: URL) => Response = (url) =>
    url.pathname.endsWith("/coverage") ? json(coverage) : json(url.searchParams.get("area") === "NO1" ? intervals : []),
) => {
  const fetchMock = stubFetch(async (input) => answer(new URL(String(input), "http://localhost")));
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <PricesPage {...props} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  const priceCalls = () =>
    fetchMock.mock.calls
      .map(([input]) => new URL(String(input), "http://localhost"))
      .filter((url) => url.pathname === "/api/v1/energy/spot-prices")
      .map((url) => Object.fromEntries(url.searchParams));
  return { fetchMock, priceCalls };
};

// Only the clock: the day the page opens on is the market's today, a DST day
// in Oslo (23 hours), so the window shows the offset changing inside it.
beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(new Date("2026-03-29T10:00:00Z"));
});
afterEach(() => vi.useRealTimers());

describe("PricesPage", () => {
  it("shows the coverage per area: the range, the intervals, the missing days and the last fetch", async () => {
    renderPage({});
    const no1 = await screen.findByRole("row", { name: /^NO1/ });
    expect(
      within(no1)
        .getAllByRole("cell")
        .map((cell) => cell.textContent),
    ).toEqual([
      "NO1",
      "Jan 2, 2026, 12:00 AM",
      "Mar 30, 2026, 12:00 AM",
      "—",
      "2,160",
      "Complete",
      "FetchedMar 29, 2026",
    ]);
    const no2 = screen.getByRole("row", { name: /^NO2/ });
    expect(within(no2).getByText("3 days")).toBeInTheDocument();
    expect(within(no2).getByText("Failed")).toBeInTheDocument();
    expect(within(no2).getByText("the feed answered 500")).toBeInTheDocument();
    const no4 = screen.getByRole("row", { name: /^NO4/ });
    expect(within(no4).getByText("30 days")).toBeInTheDocument();
    expect(within(no4).getByText("Not fetched")).toBeInTheDocument();
  });

  it("names the source of the prices on the coverage card", async () => {
    for (const [source, label] of [
      ["service", "Spot-price service"],
      ["feed", "Public feed"],
      ["none", "Files only"],
    ] as const) {
      renderPage({}, (url) => (url.pathname.endsWith("/coverage") ? json({ ...coverage, source }) : json([])));
      expect(await screen.findByText(label)).toBeInTheDocument();
      cleanup();
    }
  });

  describe("with the spot-price service", () => {
    const serviceCoverage = {
      source: "service",
      areas: [
        {
          priceArea: "NO1",
          intervalCount: 10,
          daysMissingLast30: 0,
          resolution: "PT15M",
          history: { floorDay: "2025-01-01", complete: true, daysSkipped: 0 },
        },
        {
          priceArea: "NO2",
          intervalCount: 10,
          daysMissingLast30: 0,
          resolution: "PT60M",
          history: {
            floorDay: "2025-01-01",
            cursorDay: "2025-06-15",
            complete: false,
            daysSkipped: 3,
            error: "the service answered 503",
          },
        },
        { priceArea: "NO3", intervalCount: 10, daysMissingLast30: 0, followedByService: false },
        { priceArea: "NO4", intervalCount: 0, daysMissingLast30: 30 },
        { priceArea: "NO5", intervalCount: 0, daysMissingLast30: 30, history: { complete: false, daysSkipped: 0 } },
      ],
    };
    const serve = (url: URL) => (url.pathname.endsWith("/coverage") ? json(serviceCoverage) : json([]));

    it("shows the resolution and the history per area", async () => {
      renderPage({}, serve);
      const no1 = await screen.findByRole("row", { name: /^NO1/ });
      expect(screen.getByRole("columnheader", { name: "History" })).toBeInTheDocument();
      expect(within(no1).getByText("15 min")).toBeInTheDocument();
      expect(within(no1).getByText("Complete from Jan 1, 2025")).toBeInTheDocument();
      const no2 = screen.getByRole("row", { name: /^NO2/ });
      expect(within(no2).getByText("1 hour")).toBeInTheDocument();
      expect(within(no2).getByText("Copying… back to Jun 15, 2025")).toBeInTheDocument();
      expect(within(no2).getByText("3 days skipped")).toBeInTheDocument();
      expect(within(no2).getByText("the service answered 503")).toBeInTheDocument();
      expect(within(screen.getByRole("columnheader", { name: "History" })).queryByText("x")).toBeNull();
    });

    it("says a walk is waiting while the service has no coverage to walk to", async () => {
      renderPage({}, serve);
      const no5 = await screen.findByRole("row", { name: /^NO5/ });
      expect(within(no5).getByText("Waiting for the service's coverage")).toBeInTheDocument();
    });

    it("flags an area the service does not follow, and shows a dash where there is no history", async () => {
      renderPage({}, serve);
      const no3 = await screen.findByRole("row", { name: /^NO3/ });
      expect(within(no3).getByText("Not followed by the spot-price service")).toBeInTheDocument();
      const no4 = screen.getByRole("row", { name: /^NO4/ });
      expect(within(no4).queryByText("Not followed by the spot-price service")).toBeNull();
    });
  });

  it("hides the History column unless the service is the source", async () => {
    renderPage({});
    await screen.findByRole("row", { name: /^NO1/ });
    expect(screen.queryByRole("columnheader", { name: "History" })).toBeNull();
  });

  it("charts the first area's prices for the market's today", async () => {
    const { priceCalls } = renderPage({});
    expect(await screen.findByTestId("price-chart")).toHaveTextContent("12:00 AM=0.8123,01:00 AM=0.7");
    expect(priceCalls()).toEqual([{ area: "NO1", from: "2026-03-28T23:00:00.000Z", to: "2026-03-29T22:00:00.000Z" }]);
  });

  it("asks for the whole month in month mode, and for another area when one is chosen", async () => {
    const user = userEvent.setup();
    const { priceCalls } = renderPage({});
    await screen.findByTestId("price-chart");

    await user.click(screen.getByRole("radio", { name: "Month" }));
    await waitFor(() =>
      expect(priceCalls().at(-1)).toEqual({
        area: "NO1",
        from: "2026-02-28T23:00:00.000Z",
        to: "2026-03-31T22:00:00.000Z",
      }),
    );

    await user.click(screen.getByRole("combobox", { name: "Price area" }));
    await user.click(await screen.findByRole("option", { name: "NO2" }));
    await waitFor(() => expect(priceCalls().at(-1)?.area).toBe("NO2"));
    expect(await screen.findByText("No prices for this period.")).toBeInTheDocument();
  });

  it("offers Import prices only when the host says the caller may import", async () => {
    const user = userEvent.setup();
    renderPage({ canImport: true });
    await user.click(await screen.findByRole("button", { name: "Import prices" }));
    expect(await screen.findByRole("dialog", { name: "Import prices" })).toBeInTheDocument();
  });

  it("hides Import prices otherwise", async () => {
    renderPage({});
    await screen.findByTestId("price-chart");
    expect(screen.queryByRole("button", { name: "Import prices" })).not.toBeInTheDocument();
  });

  it("says so when no area is priced or in use, and shows no chart", async () => {
    const { priceCalls } = renderPage({}, () => json({ source: "none", areas: [] }));
    expect(await screen.findByText("No price areas yet")).toBeInTheDocument();
    expect(screen.queryByText("Spot prices")).not.toBeInTheDocument();
    expect(priceCalls()).toEqual([]);
  });

  it("says so when the prices cannot be read", async () => {
    renderPage({}, (url) =>
      url.pathname.endsWith("/coverage")
        ? json({ ...coverage, areas: [coverage.areas[0]] })
        : json({ title: "Bad Request", detail: "to is more than 35 days after from" }, 400),
    );
    expect(await screen.findByText("Failed to load the prices")).toBeInTheDocument();
    expect(screen.getByText("to is more than 35 days after from")).toBeInTheDocument();
  });

  it("says so when the coverage cannot be read", async () => {
    renderPage({}, () => json({ title: "Forbidden" }, 403));
    expect(await screen.findByText("Failed to load the price coverage")).toBeInTheDocument();
  });
});

describe("PriceChart", () => {
  const hourly = (from: string, count: number) =>
    Array.from({ length: count }, (_, index) => {
      const start = new Date(new Date(from).getTime() + index * 3_600_000);
      return {
        id: index + 1,
        priceArea: "NO1",
        start: start.toISOString(),
        end: new Date(start.getTime() + 3_600_000).toISOString(),
        nokPerKwh: index,
        source: "Feed" as const,
        state: "final" as const,
      };
    });
  const labels = () =>
    screen
      .getByTestId("price-chart")
      .textContent?.split(",")
      .map((point) => point.split("=")[0]) ?? [];

  it("tells the two runs of 02:00 apart on the day the clock goes back", () => {
    render(
      <MantineProvider env="test">
        <PriceChart intervals={hourly("2025-10-25T22:00:00Z", 25)} period="day" />
      </MantineProvider>,
    );
    const shown = labels();
    expect(shown).toHaveLength(25);
    expect(new Set(shown).size).toBe(25);
    expect(shown.slice(1, 5)).toEqual(["01:00 AM", "02:00 AM +02", "02:00 AM +01", "03:00 AM"]);
  });

  it("keeps the plain hours on an ordinary day", () => {
    render(
      <MantineProvider env="test">
        <PriceChart intervals={hourly("2026-01-13T23:00:00Z", 24)} period="day" />
      </MantineProvider>,
    );
    const shown = labels();
    expect(shown).toHaveLength(24);
    expect(shown[0]).toBe("12:00 AM");
    expect(shown.every((label) => !/[+-]\d\d$/.test(label))).toBe(true);
  });
});
