import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
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
