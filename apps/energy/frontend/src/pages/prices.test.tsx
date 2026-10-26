import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { PriceChart } from "./-price-chart";
import { PricesPage } from "./prices";

// The charts as text: each row's label and its line values ("|" between
// series), the series by name (a dashed one marked), and whether dots show.
vi.mock("@mantine/charts", () => {
  interface Props {
    data: Array<Record<string, unknown>>;
    dataKey: string;
    series: Array<{ name: string; type?: string; strokeDasharray?: string | number }>;
    withDots?: boolean;
  }
  const chart =
    (testId: string) =>
    ({ data, dataKey, series, withDots }: Props) => (
      <div
        data-testid={testId}
        data-series={series.map((item) => `${item.name}${item.strokeDasharray ? "~dashed" : ""}`).join(",")}
        data-dots={String(Boolean(withDots))}
      >
        {data
          .map(
            (item) =>
              `${String(item[dataKey])}=${series
                .filter((entry) => entry.type !== "area")
                .map((entry) => String(item[entry.name]))
                .join("|")}`,
          )
          .join(",")}
      </div>
    );
  return { LineChart: chart("price-chart"), CompositeChart: chart("price-summary-chart") };
});

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

const summaryBuckets = [
  {
    start: "2026-02-28T23:00:00Z",
    end: "2026-03-01T23:00:00Z",
    avgNokPerKwh: 1,
    minNokPerKwh: 0.1,
    maxNokPerKwh: 1.5,
    coveredMinutes: 1440,
    preliminary: false,
  },
  {
    start: "2026-03-01T23:00:00Z",
    end: "2026-03-02T23:00:00Z",
    avgNokPerKwh: 2,
    minNokPerKwh: 0.2,
    maxNokPerKwh: 3,
    coveredMinutes: 480,
    preliminary: false,
  },
];

const answerFor =
  (prices: typeof intervals, buckets: typeof summaryBuckets, areas = ["NO1"]) =>
  (url: URL) => {
    const area = url.searchParams.get("area") ?? "";
    if (url.pathname.endsWith("/coverage")) return json(coverage);
    if (url.pathname.endsWith("/summary")) {
      return json({
        priceArea: area,
        bucket: url.searchParams.get("bucket"),
        timeZone: "Europe/Oslo",
        buckets: areas.includes(area) ? buckets : [],
      });
    }
    return json(areas.includes(area) ? prices : []);
  };

const renderPage = (
  props: Parameters<typeof PricesPage>[0],
  answer: (url: URL) => Response = answerFor(intervals, summaryBuckets),
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
  const summaryCalls = () =>
    fetchMock.mock.calls
      .map(([input]) => new URL(String(input), "http://localhost"))
      .filter((url) => url.pathname === "/api/v1/energy/spot-prices/summary")
      .map((url) => Object.fromEntries(url.searchParams));
  return { fetchMock, priceCalls, summaryCalls };
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

  it("asks for the Monday-to-Monday week of the day, every interval without dots, across a month's end", async () => {
    const { priceCalls, summaryCalls } = renderPage({ search: { period: "week", date: "2026-04-01" } });
    const chart = await screen.findByTestId("price-chart");
    expect(chart).toHaveAttribute("data-dots", "false");
    expect(priceCalls()).toEqual([{ area: "NO1", from: "2026-03-29T22:00:00.000Z", to: "2026-04-05T22:00:00.000Z" }]);
    expect(summaryCalls()).toEqual([]);
  });

  it("asks for a DST month's daily summary and charts the average on the band", async () => {
    const user = userEvent.setup();
    const { summaryCalls } = renderPage({});
    await screen.findByTestId("price-chart");
    expect(screen.getByTestId("price-chart")).toHaveAttribute("data-dots", "true");

    await user.click(screen.getByRole("radio", { name: "Month" }));
    const chart = await screen.findByTestId("price-summary-chart");
    expect(summaryCalls()).toEqual([
      { area: "NO1", bucket: "day", from: "2026-02-28T23:00:00.000Z", to: "2026-03-31T22:00:00.000Z" },
    ]);
    expect(chart).toHaveAttribute("data-series", "band,NO1");
    expect(chart).toHaveTextContent("Mar 1=1,Mar 2=2");
  });

  it("asks for the calendar year's days in year mode", async () => {
    const { summaryCalls } = renderPage({ search: { period: "year", date: "2025-06-01" } });
    await screen.findByTestId("price-summary-chart");
    expect(summaryCalls()).toEqual([
      { area: "NO1", bucket: "day", from: "2024-12-31T23:00:00.000Z", to: "2025-12-31T23:00:00.000Z" },
    ]);
  });

  it("asks a range of up to two years per whole ISO week, and a longer one per month, clamped to 30 years", async () => {
    const user = userEvent.setup();
    const { summaryCalls } = renderPage({});
    await screen.findByTestId("price-chart");
    await user.click(screen.getByRole("radio", { name: "Range" }));
    await screen.findByTestId("price-summary-chart");
    // The last twelve months by default: April 2025 to March 2026.
    expect(summaryCalls().at(-1)).toEqual({
      area: "NO1",
      bucket: "week",
      from: "2025-03-30T22:00:00.000Z",
      to: "2026-04-05T22:00:00.000Z",
    });
    cleanup();

    const wide = renderPage({ search: { period: "range", from: "2020-01", to: "2026-03" } });
    await screen.findByTestId("price-summary-chart");
    expect(wide.summaryCalls()).toEqual([
      { area: "NO1", bucket: "month", from: "2019-12-31T23:00:00.000Z", to: "2026-03-31T22:00:00.000Z" },
    ]);
    cleanup();

    const clamped = renderPage({ search: { period: "range", from: "1980-01", to: "2026-03" } });
    await screen.findByTestId("price-summary-chart");
    expect(clamped.summaryCalls()).toEqual([
      { area: "NO1", bucket: "month", from: "1996-03-31T22:00:00.000Z", to: "2026-03-31T22:00:00.000Z" },
    ]);
  });

  it("labels a weekly range's buckets by ISO week", async () => {
    renderPage(
      { search: { period: "range", from: "2025-12", to: "2026-01" } },
      answerFor(intervals, [{ ...summaryBuckets[0], start: "2025-12-28T23:00:00Z", end: "2026-01-04T23:00:00Z" }]),
    );
    expect(await screen.findByTestId("price-summary-chart")).toHaveTextContent("W1 2026=1");
    expect(screen.getByTestId("price-figure-pricesLowest")).toHaveTextContent("Week 1, 2026");
  });

  it("compares areas: one request and one line each, and the band only for a single area", async () => {
    const user = userEvent.setup();
    const { priceCalls, summaryCalls } = renderPage({}, answerFor(intervals, summaryBuckets, ["NO1", "NO2"]));
    await screen.findByTestId("price-chart");

    await user.click(screen.getByRole("combobox", { name: "Compare areas" }));
    await user.click(await screen.findByRole("option", { name: "NO2" }));
    await waitFor(() => expect(priceCalls().map((call) => call.area)).toEqual(["NO1", "NO2"]));
    await waitFor(() => expect(screen.getByTestId("price-chart")).toHaveAttribute("data-series", "NO1,NO2"));
    expect(screen.getByTestId("price-chart")).toHaveTextContent("12:00 AM=0.8123|0.8123,01:00 AM=0.7|0.7");

    await user.click(screen.getByRole("radio", { name: "Month" }));
    await waitFor(() => expect(summaryCalls().map((call) => call.area)).toEqual(["NO1", "NO2"]));
    expect(await screen.findByTestId("price-summary-chart")).toHaveAttribute("data-series", "NO1,NO2");
  });

  it("says so when no compared area has prices for the period", async () => {
    renderPage({ search: { areas: ["NO2"] } });
    expect(await screen.findByText("No prices for this period.")).toBeInTheDocument();
  });

  it("shows the first area's time-weighted average, lowest and highest with their interval", async () => {
    renderPage(
      {},
      answerFor(
        [
          { ...intervals[0], nokPerKwh: 1 },
          { ...intervals[1], end: "2026-03-29T00:15:00Z", nokPerKwh: 2 },
        ],
        summaryBuckets,
      ),
    );
    // An hour at 1 and a quarter-hour at 2: 1.2, where a plain mean would say 1.5.
    expect(await screen.findByTestId("price-figure-pricesAverage")).toHaveTextContent("Average1.20 NOK/kWh");
    expect(screen.getByTestId("price-figure-pricesLowest")).toHaveTextContent("Lowest1.00 NOK/kWh12:00 AM–01:00 AM");
    expect(screen.getByTestId("price-figure-pricesHighest")).toHaveTextContent("Highest2.00 NOK/kWh01:00 AM–01:15 AM");
  });

  it("weighs a summary's average by the minutes each bucket covers, and names the day of the extremes", async () => {
    renderPage({ search: { period: "month", date: "2026-03-10" } });
    // 1 over a whole day and 2 over eight hours: 1.25.
    expect(await screen.findByTestId("price-figure-pricesAverage")).toHaveTextContent("1.25 NOK/kWh");
    expect(screen.getByTestId("price-figure-pricesLowest")).toHaveTextContent("0.10 NOK/kWhMar 1, 2026");
    expect(screen.getByTestId("price-figure-pricesHighest")).toHaveTextContent("3.00 NOK/kWhMar 2, 2026");
    expect(screen.queryByText("Preliminary")).toBeNull();
  });

  it("flags preliminary prices and draws them dashed", async () => {
    renderPage({}, answerFor([intervals[0], { ...intervals[1], state: "preliminary" }], summaryBuckets));
    const chart = await screen.findByTestId("price-chart");
    expect(screen.getByText("Preliminary")).toBeInTheDocument();
    expect(chart).toHaveAttribute("data-series", "NO1,NO1_preliminary~dashed");
    // The solid line reaches the first preliminary interval; the dashed one holds only those.
    expect(chart).toHaveTextContent("12:00 AM=0.8123|null,01:00 AM=0.7|0.7");
  });

  it("flags a preliminary day of a month and joins its dashed line to the last final day", async () => {
    renderPage(
      { search: { period: "month" } },
      answerFor(intervals, [summaryBuckets[0], { ...summaryBuckets[1], preliminary: true }]),
    );
    const chart = await screen.findByTestId("price-summary-chart");
    expect(screen.getByText("Preliminary")).toBeInTheDocument();
    expect(chart).toHaveAttribute("data-series", "band,NO1,NO1_preliminary~dashed");
    expect(chart).toHaveTextContent("Mar 1=1|1,Mar 2=null|2");
  });

  it("hands every change of the view to the host, and shows what the host holds", async () => {
    const user = userEvent.setup();
    const onSearchChange = vi.fn();
    const { summaryCalls } = renderPage({
      search: { areas: ["NO2", "NO1"], period: "year", date: "2025-06-01" },
      onSearchChange,
    });
    await waitFor(() => expect(summaryCalls().map((call) => call.area)).toEqual(["NO2", "NO1"]));
    expect(screen.getByRole("radio", { name: "Year" })).toBeChecked();

    await user.click(screen.getByRole("radio", { name: "Week" }));
    expect(onSearchChange).toHaveBeenLastCalledWith({
      areas: ["NO2", "NO1"],
      period: "week",
      date: "2025-06-01",
    });
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
        <PriceChart areas={[{ area: "NO1", intervals: hourly("2025-10-25T22:00:00Z", 25) }]} period="day" />
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
        <PriceChart areas={[{ area: "NO1", intervals: hourly("2026-01-13T23:00:00Z", 24) }]} period="day" />
      </MantineProvider>,
    );
    const shown = labels();
    expect(shown).toHaveLength(24);
    expect(shown[0]).toBe("12:00 AM");
    expect(shown.every((label) => !/[+-]\d\d$/.test(label))).toBe(true);
  });
});
