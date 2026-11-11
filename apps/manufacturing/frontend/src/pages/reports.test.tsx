import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderRoute } from "../test/route-tree";
import { stubManufacturingApi } from "../test/server";

afterEach(() => vi.restoreAllMocks());

const urls = (fetchMock: ReturnType<typeof stubManufacturingApi>, pattern: RegExp) =>
  fetchMock.actualCalls.map(([url]) => String(url)).filter((url) => pattern.test(url));

describe("the reports", () => {
  it("hide the production cost without costs-view and open on utilisation", async () => {
    const fetchMock = stubManufacturingApi({ meta: { canSeeCosts: false } });
    renderRoute("/manufacturing/reports");
    expect(await screen.findByRole("tab", { name: "Work-center utilisation" })).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "Production cost" })).not.toBeInTheDocument();
    await waitFor(() => expect(urls(fetchMock, /\/reports\/utilisation/)).toHaveLength(1));
    expect(urls(fetchMock, /\/reports\/production-cost/)).toHaveLength(0);
  });

  it("show the production cost first with costs-view, grouped by product", async () => {
    stubManufacturingApi({
      meta: { canSeeCosts: true },
      reports: {
        "production-cost": {
          from: "2026-10-13",
          to: "2026-11-11",
          siteId: null,
          groups: [
            {
              variant: {
                id: 1,
                sku: "TABLE",
                productName: "Oak table",
                unit: "pcs",
                productType: "goods",
                productStatus: "active",
              },
              totals: {
                orders: 1,
                produced: "10",
                estimate: "1500.00",
                actual: "1650.00",
                deviation: "150.00",
                deviationPercent: "10.0",
                costPerUnit: "165.00",
              },
              orders: [
                {
                  orderId: 5,
                  number: "MO-12",
                  closedAt: "2026-11-10T12:00:00Z",
                  quantity: "10",
                  produced: "10",
                  estimate: "1500.00",
                  actual: "1650.00",
                  deviation: "150.00",
                  deviationPercent: "10.0",
                  costPerUnit: "165.00",
                },
              ],
            },
          ],
        },
      },
    });
    renderRoute("/manufacturing/reports");
    expect(await screen.findByRole("link", { name: "MO-12" })).toBeInTheDocument();
    expect(screen.getByText("TABLE · Oak table · 1 order")).toBeInTheDocument();
    expect(screen.getAllByText("165.00 NOK")).toHaveLength(2);
  });

  it("build the query from the filters, and download the same report as CSV", async () => {
    const fetchMock = stubManufacturingApi({ meta: { canSeeCosts: false } });
    const createObjectURL = vi.fn(() => "blob:report");
    // jsdom has no object URLs.
    Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    renderRoute("/manufacturing/reports");
    await screen.findByRole("tab", { name: "Work-center utilisation" });
    fireEvent.change(screen.getByLabelText("From"), { target: { value: "2026-10-01" } });
    fireEvent.change(screen.getByLabelText("To"), { target: { value: "2026-10-31" } });
    await waitFor(() =>
      expect(urls(fetchMock, /\/reports\/utilisation/).at(-1)).toContain(
        "/api/v1/manufacturing/reports/utilisation?from=2026-10-01&to=2026-10-31",
      ),
    );
    expect(urls(fetchMock, /\/reports\/utilisation/).at(-1)).not.toContain("format=");
    expect(await screen.findByText(/Oct 1, 2026 – Oct 31, 2026/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Download CSV" }));
    await waitFor(() => expect(click).toHaveBeenCalledTimes(1));
    expect(urls(fetchMock, /format=csv/)).toEqual([
      "/api/v1/manufacturing/reports/utilisation?from=2026-10-01&to=2026-10-31&format=csv",
    ]);
    expect(createObjectURL).toHaveBeenCalledTimes(1);
  });
});
