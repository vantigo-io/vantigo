import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { scan } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubInventoryApi } from "../test/server";

describe("the lookup", () => {
  it("opens the one location a scanned full code matches", async () => {
    stubInventoryApi();
    const { router } = renderRoute("/inventory");
    const field = await screen.findByRole("textbox", { name: "Find location or item" });
    expect(field).toHaveFocus();
    await scan(field, "osl-a-01");
    await waitFor(() => expect(router.state.location.pathname).toBe("/inventory/locations/12"));
    expect(await screen.findByRole("heading", { name: /OSL-A-01/ })).toBeInTheDocument();
  });

  it("opens the variant's stock for a label's LOC token or a SKU", async () => {
    stubInventoryApi();
    const { router } = renderRoute("/inventory");
    await scan(await screen.findByRole("textbox", { name: "Find location or item" }), "TS-M");
    await waitFor(() => expect(router.state.location.pathname).toBe("/inventory/variants/101"));
  });

  it("says when nothing matches, and keeps the text", async () => {
    stubInventoryApi();
    const { router } = renderRoute("/inventory");
    const field = await screen.findByRole("textbox", { name: "Find location or item" });
    await scan(field, "NOPE");
    expect(await screen.findByText("Nothing matches “NOPE”.")).toBeInTheDocument();
    expect(field).toHaveValue("NOPE");
    expect(router.state.location.pathname).toBe("/inventory");
  });

  it("lists several matches on the lookup page, and opens one there", async () => {
    // A location coded like a SKU: the full code and the SKU both match.
    stubInventoryApi({
      locations: [
        ...(await import("../test/server")).locations(),
        {
          id: 30,
          siteId: 1,
          parentId: 10,
          fullCode: "TS-M",
          code: "TS-M",
          kind: "other",
          depth: 1,
          sortOrder: 0,
          status: "active",
          revision: 1,
          createdAt: "2026-11-02T09:00:00Z",
          updatedAt: "2026-11-02T09:00:00Z",
        },
      ],
    });
    const { router } = renderRoute("/inventory/lookup?q=TS-M");
    expect(await screen.findByText("2 matches for “TS-M”.")).toBeInTheDocument();
    await userEvent.click(screen.getAllByRole("link", { name: "TS-M" })[1]);
    await waitFor(() => expect(router.state.location.pathname).toBe("/inventory/variants/101"));
  });

  it("goes straight to the one match when opened with it", async () => {
    stubInventoryApi();
    const { router } = renderRoute("/inventory/lookup?q=LOC13");
    await waitFor(() => expect(router.state.location.pathname).toBe("/inventory/locations/13"));
  });
});
