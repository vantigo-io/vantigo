import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderRoute } from "../test/route-tree";
import { meta, stubAccountingApi } from "../test/server";

describe("the overview", () => {
  it("shows the year's vouchers, the open items and the lock date", async () => {
    stubAccountingApi();
    renderRoute("/accounting");
    expect(await screen.findByText("Vouchers this fiscal year")).toBeInTheDocument();
    expect(screen.getByText("12")).toBeInTheDocument();
    expect(screen.getByText("Open customer items")).toBeInTheDocument();
    expect(screen.getByText("Nothing needs attention.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "New journal entry" })).toHaveAttribute("href", "/accounting/journal");
  });

  it("shows only the way to Set up the books until they are set up", async () => {
    stubAccountingApi({ meta: meta({ setUp: false, settings: null }) });
    renderRoute("/accounting");
    expect(await screen.findByText("The books are not set up")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Set up the books" })).toHaveAttribute("href", "/accounting/setup");
    expect(screen.queryByText("Vouchers this fiscal year")).not.toBeInTheDocument();
  });

  it("tells someone who cannot set the books up whom to ask", async () => {
    stubAccountingApi({ meta: meta({ setUp: false, settings: null, canClose: false }) });
    renderRoute("/accounting/vouchers");
    expect(await screen.findByText(/Ask a person with the permission Manage the books/)).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Set up the books" })).not.toBeInTheDocument();
  });
});
