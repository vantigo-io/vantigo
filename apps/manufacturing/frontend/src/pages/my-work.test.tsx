import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderRoute } from "../test/route-tree";
import { stubManufacturingApi, timeLogFixture, workOrderFixture } from "../test/server";

describe("My work", () => {
  it("shows the caller's open log with its timer and today's work orders", async () => {
    const workOrder = workOrderFixture({ status: "in_progress" });
    stubManufacturingApi({
      myWork: {
        openLog: timeLogFixture({ endedAt: null, startedAt: new Date(Date.now() - 30_000).toISOString() }),
        openWorkOrder: workOrder,
        today: [workOrder],
      },
    });
    renderRoute("/manufacturing/shop-floor/mine");
    expect(await screen.findByRole("timer")).toHaveTextContent(/^0:00:3\d$/);
    expect(screen.getByRole("link", { name: "Open the work order" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "MO-12 · Cut" })).toBeInTheDocument();
  });

  it("says when nothing runs and nothing was done today", async () => {
    stubManufacturingApi();
    renderRoute("/manufacturing/shop-floor/mine");
    expect(await screen.findByText("You are not running anything.")).toBeInTheDocument();
    expect(screen.getByText("You have not worked on anything today.")).toBeInTheDocument();
  });
});
