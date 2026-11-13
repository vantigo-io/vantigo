import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { jsonResponse } from "../test/api";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubInventoryApi, variants } from "../test/server";

const tracked = { ...variants()[2], tracking: "lot" as const };

describe("inspection on receipt (manufacturing phase 2 M100)", () => {
  it("sets Inspect on receipt on a tracked variant's page under its revision", async () => {
    const fetchMock = stubInventoryApi({
      variants: [tracked],
      rules: { [tracked.id]: { inspectOnReceipt: false, revision: 0 } },
      write: (method, path) =>
        method === "PUT" && path === `/variants/${tracked.id}/rules`
          ? jsonResponse(200, {
              variantId: tracked.id,
              inspectOnReceipt: true,
              revision: 1,
              updatedAt: "2026-11-12T10:00:00Z",
            })
          : undefined,
    });
    renderRoute(`/inventory/variants/${tracked.id}`);
    await userEvent.click(await screen.findByRole("switch", { name: "Inspect on receipt" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/variants\/\d+\/rules$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /\/variants\/\d+\/rules$/)[0].body).toEqual({ inspectOnReceipt: true, revision: 0 });
  });

  it("offers no switch on an untracked variant", async () => {
    stubInventoryApi({});
    renderRoute(`/inventory/variants/${variants()[2].id}`);
    expect(await screen.findByText("Stock")).toBeInTheDocument();
    expect(screen.queryByRole("switch", { name: "Inspect on receipt" })).toBeNull();
  });

  it("links the home page to the lots awaiting inspection", async () => {
    stubInventoryApi({ lotsAwaitingInspection: 2 });
    renderRoute("/inventory");
    const link = await screen.findByRole("link", { name: "2 lots awaiting inspection" });
    expect(link.getAttribute("href")).toContain("holdCause=inspection");
    // The count is every lot with an active inspection hold, whatever its status and stock: so is the list.
    expect(link.getAttribute("href")).not.toContain("status=");
    expect(link.getAttribute("href")).toContain("stock=all");
  });
});
