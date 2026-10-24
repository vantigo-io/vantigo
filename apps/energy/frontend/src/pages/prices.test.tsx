import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
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
  },
  {
    id: 2,
    priceArea: "NO1",
    start: "2026-03-29T00:00:00Z",
    end: "2026-03-29T01:00:00Z",
    nokPerKwh: 0.7,
    source: "Feed",
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
    ).toEqual(["NO1", "Jan 2, 2026, 12:00 AM", "Mar 30, 2026, 12:00 AM", "2,160", "Complete", "FetchedMar 29, 2026"]);
    const no2 = screen.getByRole("row", { name: /^NO2/ });
    expect(within(no2).getByText("3 days")).toBeInTheDocument();
    expect(within(no2).getByText("Failed")).toBeInTheDocument();
    expect(within(no2).getByText("the feed answered 500")).toBeInTheDocument();
    const no4 = screen.getByRole("row", { name: /^NO4/ });
    expect(within(no4).getByText("30 days")).toBeInTheDocument();
    expect(within(no4).getByText("Not fetched")).toBeInTheDocument();
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
    const { priceCalls } = renderPage({}, () => json({ areas: [] }));
    expect(await screen.findByText("No price areas yet")).toBeInTheDocument();
    expect(screen.queryByText("Spot prices")).not.toBeInTheDocument();
    expect(priceCalls()).toEqual([]);
  });

  it("says so when the prices cannot be read", async () => {
    renderPage({}, (url) =>
      url.pathname.endsWith("/coverage")
        ? json({ areas: [coverage.areas[0]] })
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
