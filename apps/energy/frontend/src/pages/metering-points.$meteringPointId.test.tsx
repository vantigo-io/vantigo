import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { MeteringPoint, SupplyPeriod } from "../api/energy";
import { stubFetch } from "../test/fetch";
import { MeteringPointDetailsPage } from "./metering-points.$meteringPointId";

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
  useParams: () => ({ meteringPointId: 7 }),
}));

vi.mock("@mantine/charts", () => ({ BarChart: () => null, LineChart: () => null, AreaChart: () => null }));

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

const basePoint: MeteringPoint = {
  id: 7,
  gsrn: "707057500000000007",
  meterNumber: "M-7",
  address: { streetAddress: "Storgata 1", postalCode: "9008", city: "Tromsø", countryCode: "NO" },
  priceArea: "NO4",
  connectionStatus: "Connected",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
};

const periods: SupplyPeriod[] = [
  {
    id: 5,
    meteringPointId: 7,
    customerId: 1002,
    start: "2026-01-01T00:00:00Z",
    end: null,
    status: "Active",
    tariffId: 1001,
    tariffName: "Spot 2026",
    cadence: "quarterly",
  },
  { id: 4, meteringPointId: 7, customerId: 1001, start: "2025-06-01T00:00:00Z", end: null, status: "Cancelled" },
];

const renderPage = async (point: MeteringPoint) => {
  stubFetch(async (input) => {
    const url = String(input);
    if (url === "/api/v1/energy/metering-points/7") return json(point);
    if (url.endsWith("/supply-periods")) return json(periods);
    if (url.startsWith("/api/v1/customers"))
      return json({ data: [{ id: 1002, name: "Kari Nordmann" }], pagination: {} });
    return json([]);
  });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MeteringPointDetailsPage />
      </QueryClientProvider>
    </MantineProvider>,
  );
  const title = await screen.findByRole("heading", { name: "Billing" });
  return title.closest(".mantine-Card-root") as HTMLElement;
};

describe("MeteringPointDetailsPage", () => {
  it("shows the billing facts, a point without them read as none, ordinary and standard", async () => {
    const card = await renderPage(basePoint);
    expect(within(card).getByText("None — billed by the grid company")).toBeInTheDocument();
    expect(within(card).getByText("No")).toBeInTheDocument();
    expect(within(card).getByText("Ordinary rate")).toBeInTheDocument();
    expect(within(card).getByText("Standard")).toBeInTheDocument();
    expect(within(card).getByText(/Derived from household use and the address/)).toBeInTheDocument();
  });

  it("shows the grid tariff, household use, the tax class and the exemption the server derived", async () => {
    const card = await renderPage({
      ...basePoint,
      gridTariffId: 1004,
      gridTariffName: "Grid Nord",
      household: true,
      electricityTaxClass: "exempt",
      vatTreatment: "exempt_household_north",
    });
    expect(within(card).getByText("Grid Nord")).toBeInTheDocument();
    expect(within(card).getByText("Yes")).toBeInTheDocument();
    expect(within(card).getByText("Exempt")).toBeInTheDocument();
    expect(within(card).getByText("Exempt — household in Northern Norway (VAT Act § 6-6)")).toBeInTheDocument();
  });

  it("lists each period's tariff and cadence, and offers Change terms on all but a cancelled one", async () => {
    await renderPage(basePoint);
    const active = (await screen.findByText("Kari Nordmann")).closest("tr") as HTMLElement;
    expect(within(active).getByText("Spot 2026")).toBeInTheDocument();
    expect(within(active).getByText("Quarterly")).toBeInTheDocument();
    const cancelled = screen.getByText("Customer #1001").closest("tr") as HTMLElement;
    expect(within(cancelled).getByText("None")).toBeInTheDocument();
    expect(within(cancelled).getByText("Monthly")).toBeInTheDocument();
    expect(within(cancelled).queryByRole("button", { name: "Change terms" })).not.toBeInTheDocument();

    await userEvent.click(within(active).getByRole("button", { name: "Change terms" }));
    expect(await screen.findByRole("dialog", { name: "Change terms" })).toBeInTheDocument();
  });
});
