import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { choose, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { inventoryProblem, stubInventoryApi } from "../test/server";

/** Catches the PDF handed to the browser: jsdom makes no object URLs. */
const catchDownloads = () => {
  const createObjectURL = vi.fn<(blob: Blob) => string>(() => "blob:labels");
  vi.stubGlobal("URL", Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() }));
  return createObjectURL;
};
const labels = (fetchMock: Parameters<typeof sent>[0]) => sent(fetchMock, "POST", /\/labels$/);

describe("printing labels (I7)", () => {
  it("prints every location of a site on L7160 by default, and downloads the PDF", async () => {
    const downloads = catchDownloads();
    const fetchMock = stubInventoryApi();
    renderRoute("/inventory/sites/1");
    await userEvent.click(await screen.findByRole("button", { name: "Print labels" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Download PDF" }));
    await waitFor(() => expect(labels(fetchMock)).toHaveLength(1));
    expect(labels(fetchMock)[0].body).toEqual({ layout: "l7160", locationIds: [10], subtree: true });
    await waitFor(() => expect(downloads).toHaveBeenCalledTimes(1));
    expect(downloads.mock.calls[0][0].type).toBe("application/pdf");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("prints the chosen locations of a site on L7163", async () => {
    catchDownloads();
    const fetchMock = stubInventoryApi();
    renderRoute("/inventory/sites/1");
    await userEvent.click(await screen.findByRole("button", { name: "Print labels" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("radio", { name: "Chosen locations" }));
    expect(within(dialog).getByRole("button", { name: "Download PDF" })).toBeDisabled();
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Locations" }));
    await userEvent.click(await screen.findByRole("option", { name: "OSL-A-02" }));
    await userEvent.click(within(dialog).getByRole("radio", { name: /L7163/ }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Download PDF" }));
    await waitFor(() => expect(labels(fetchMock)).toHaveLength(1));
    expect(labels(fetchMock)[0].body).toEqual({ layout: "l7163", locationIds: [13] });
  });

  it("prints a location with what lies below it, and says a refusal in the module's words", async () => {
    catchDownloads();
    let refuse = true;
    const fetchMock = stubInventoryApi({
      write: (_method, path) => {
        if (path !== "/labels" || !refuse) return undefined;
        refuse = false;
        return inventoryProblem(400, "labels_too_many");
      },
    });
    renderRoute("/inventory/locations/11");
    await userEvent.click(await screen.findByRole("button", { name: "Print labels" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("checkbox", { name: "Include every location below this one" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Download PDF" }));
    expect(await within(dialog).findByText("At most 1000 labels can be printed at once.")).toBeInTheDocument();
    expect(labels(fetchMock)[0].body).toEqual({ layout: "l7160", locationIds: [11], subtree: true });
  });

  it("is offered to a caller who may only view locations", async () => {
    stubInventoryApi({ meta: { canManageLocations: false, canViewStock: false, canAdjust: false } });
    renderRoute("/inventory/sites/1");
    expect(await screen.findByRole("button", { name: "Print labels" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Count" })).not.toBeInTheDocument();
  });

  it("is not offered without locations-view", async () => {
    stubInventoryApi({ meta: { canViewLocations: false } });
    renderRoute("/inventory/locations/12");
    await screen.findByRole("heading", { name: /OSL-A-01/ });
    expect(screen.queryByRole("button", { name: "Print labels" })).not.toBeInTheDocument();
  });
});

describe("Count on a location", () => {
  it("opens a count of the location's subtree", async () => {
    const fetchMock = stubInventoryApi();
    const { router } = renderRoute("/inventory/locations/11");
    await userEvent.click(await screen.findByRole("button", { name: "Count" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Open count" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/counts$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/counts$/)[0].body).toEqual({ siteId: 1, locationId: 11 });
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/inventory\/counts\/\d+$/));
  });

  it("counts another location of the site when one is chosen", async () => {
    const fetchMock = stubInventoryApi();
    renderRoute("/inventory/locations/11");
    await userEvent.click(await screen.findByRole("button", { name: "Count" }));
    const dialog = await screen.findByRole("dialog");
    await choose(dialog, "Scope", "OSL-A-02");
    await userEvent.click(within(dialog).getByRole("button", { name: "Open count" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/counts$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/counts$/)[0].body).toEqual({ siteId: 1, locationId: 13 });
  });
});
