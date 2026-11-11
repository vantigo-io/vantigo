import { screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { endOfWeek } from "../lib/format";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubManufacturingApi, workCenter } from "../test/server";

describe("the overview", () => {
  it("shows delivery A's three counts from the stats, and no empty state when there are BoMs", async () => {
    stubManufacturingApi({ workCenters: [workCenter(1, "SAW"), workCenter(2, "PAINT", { workingState: "blocked" })] });
    renderRoute("/manufacturing");
    expect(await screen.findByText("Active work centers")).toBeInTheDocument();
    expect(screen.getByText("Blocked work centers")).toBeInTheDocument();
    expect(screen.getByText("Active bills of materials")).toBeInTheDocument();
    expect(screen.queryByText("No bills of materials yet")).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "New bill of materials" })).toBeInTheDocument();
  });

  it("lists the orders due by the Sunday of the business calendar's week", async () => {
    const fetchMock = stubManufacturingApi({ stats: { today: "2026-12-02" } });
    renderRoute("/manufacturing");
    await waitFor(() => expect(sent(fetchMock, "GET", /\/orders\?.*dueTo=2026-12-06/)).toHaveLength(1));
  });

  it("ends the week on the Sunday on or after today, whatever the viewer's zone", () => {
    expect(endOfWeek("2026-11-11")).toBe("2026-11-15");
    expect(endOfWeek("2026-11-15")).toBe("2026-11-15");
    expect(endOfWeek("2026-11-16")).toBe("2026-11-22");
    expect(endOfWeek("2026-12-28")).toBe("2027-01-03");
  });

  it("points to Bills of materials when there are none", async () => {
    stubManufacturingApi({ boms: [], meta: { canManageBoms: false } });
    renderRoute("/manufacturing");
    expect(await screen.findByText("No bills of materials yet")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Go to bills of materials" })).toHaveAttribute(
      "href",
      "/manufacturing/boms",
    );
    expect(screen.queryByRole("link", { name: "New bill of materials" })).not.toBeInTheDocument();
  });
});
