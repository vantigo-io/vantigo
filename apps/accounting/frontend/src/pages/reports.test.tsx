import { screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderRoute } from "../test/route-tree";
import { meta, stubAccountingApi } from "../test/server";

describe("the reports", () => {
  it("shows the chosen report with when it was produced and offers its CSV", async () => {
    const fetchMock = stubAccountingApi();
    renderRoute("/accounting/reports");
    expect(await screen.findByText("Saldobalanse")).toBeInTheDocument();
    expect(screen.getByText("Bankinnskudd")).toBeInTheDocument();
    expect(screen.getByText(/produced/)).toBeInTheDocument();
    await waitFor(() =>
      expect(
        fetchMock.actualCalls.some(([url]) =>
          String(url).includes("/reports/trial_balance?from=2026-01-01&to=2026-11-14"),
        ),
      ).toBe(true),
    );
    expect(screen.getByRole("link", { name: "Download CSV" })).toHaveAttribute(
      "href",
      "/api/v1/accounting/reports/trial_balance/csv?from=2026-01-01&to=2026-11-14",
    );
  });

  it("offers no CSV without the export permission", async () => {
    stubAccountingApi({ meta: meta({ canExport: false }) });
    renderRoute("/accounting/reports");
    expect(await screen.findByText("Saldobalanse")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Download CSV" })).not.toBeInTheDocument();
  });
});
