import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderRoute } from "../test/route-tree";
import { bomFixture, stubManufacturingApi } from "../test/server";

describe("the bills of materials list", () => {
  it("lists the active BoMs with their variant, contents and default, and offers New to a manager", async () => {
    stubManufacturingApi({ boms: [bomFixture(), bomFixture({ id: 8, status: "archived", isDefault: false })] });
    renderRoute("/manufacturing/boms");
    const link = await screen.findByRole("link", { name: "TABLE" });
    const row = link.closest("tr") as HTMLElement;
    expect(within(row).getByText("Rev B")).toBeInTheDocument();
    expect(within(row).getByText("1 components, 1 operations")).toBeInTheDocument();
    expect(within(row).getByText("Default")).toBeInTheDocument();
    expect(screen.getAllByRole("link", { name: "TABLE" })).toHaveLength(1);
    expect(screen.getByRole("link", { name: "New bill of materials" })).toBeInTheDocument();
  });

  it("asks for another status through the URL", async () => {
    const fetchMock = stubManufacturingApi({ boms: [bomFixture(), bomFixture({ id: 8, status: "archived" })] });
    const { router } = renderRoute("/manufacturing/boms");
    await screen.findByRole("link", { name: "TABLE" });
    await userEvent.click(screen.getByRole("combobox", { name: "Status" }));
    await userEvent.click(await screen.findByRole("option", { name: "All" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ status: "all" }));
    await waitFor(() => expect(screen.getAllByRole("link", { name: "TABLE" })).toHaveLength(2));
    expect(fetchMock.actualCalls.map(([url]) => String(url))).toContain(
      "/api/v1/manufacturing/boms?status=all&limit=50",
    );
  });

  it("offers no New to a caller without boms-manage, and says so when there are none", async () => {
    stubManufacturingApi({ meta: { canManageBoms: false }, boms: [] });
    renderRoute("/manufacturing/boms");
    expect(await screen.findByText("No bills of materials")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "New bill of materials" })).not.toBeInTheDocument();
  });
});
