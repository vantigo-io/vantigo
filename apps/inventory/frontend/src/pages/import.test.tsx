import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { choose, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { inventoryProblem, stubInventoryApi } from "../test/server";

const csv = (text: string) => new File([text], "opening.csv", { type: "text/csv" });
const fileInput = () => document.querySelector('input[type="file"]') as HTMLInputElement;
const imports = (fetchMock: Parameters<typeof sent>[0]) => sent(fetchMock, "POST", /\/imports\/opening-balances/);

describe("the opening-balance import (I32)", () => {
  it("dry-runs the file, offers no commit while a row fails, and commits a clean file as one movement", async () => {
    const fetchMock = stubInventoryApi();
    renderRoute("/inventory/import");
    await screen.findByRole("combobox", { name: "Site" });
    await choose(document.body, "Site", "OSL · Oslo lager");

    await userEvent.upload(fileInput(), csv("location,sku,quantity\nOSL-A-01,TS-M,5\nOSL-Z,TS-M,1\n"));
    await userEvent.click(screen.getByRole("button", { name: "Dry run" }));
    expect(await screen.findByText("1 row cannot be posted. Fix the file and try again.")).toBeInTheDocument();
    expect(screen.getByText("No such location in the site")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Commit" })).toBeDisabled();
    expect(imports(fetchMock)[0].url).toContain("siteId=1&dryRun=true");
    expect(imports(fetchMock)[0].body).toBe("location,sku,quantity\nOSL-A-01,TS-M,5\nOSL-Z,TS-M,1\n");

    await userEvent.upload(fileInput(), csv("location,sku,quantity\nOSL-A-01,TS-M,5\nOSL-A-02,BOLT-10,2.5\n"));
    await userEvent.click(screen.getByRole("button", { name: "Dry run" }));
    expect(await screen.findByText("All 2 rows are ready to post.")).toBeInTheDocument();
    const table = screen.getByRole("table");
    expect(within(table).getAllByText("OK")).toHaveLength(2);
    expect(fetchMock.state.movements).toHaveLength(0);

    await userEvent.click(screen.getByRole("button", { name: "Commit" }));
    await waitFor(() => expect(imports(fetchMock)).toHaveLength(3));
    expect(imports(fetchMock)[2].url).toContain("siteId=1&dryRun=false");
    expect(await screen.findByRole("link", { name: "Open movement 901" })).toHaveAttribute(
      "href",
      "/inventory/movements/901",
    );
    expect(fetchMock.state.balances).toEqual([
      { variantId: 101, locationId: 12, onHand: 5 },
      { variantId: 201, locationId: 13, onHand: 2.5 },
    ]);
  });

  it("sends the file's bytes as they are, so a Windows-1252 file reaches the server undecoded", async () => {
    const fetchMock = stubInventoryApi();
    renderRoute("/inventory/import");
    await screen.findByRole("combobox", { name: "Site" });
    await choose(document.body, "Site", "OSL · Oslo lager");
    // "location,sku,quantity\nOSL-A-01,TS-M,5 Ø" with Ø as Windows-1252's single byte 0xD8: not valid UTF-8.
    const bytes = new Uint8Array([...new TextEncoder().encode("location,sku,quantity\nOSL-A-01,TS-M,5"), 0xd8]);
    await userEvent.upload(fileInput(), new File([bytes], "opening.csv", { type: "text/csv" }));
    await userEvent.click(screen.getByRole("button", { name: "Dry run" }));
    await waitFor(() => expect(imports(fetchMock)).toHaveLength(1));
    const raw = fetchMock.actualCalls.filter(([url]) => /\/imports\/opening-balances/.test(String(url)));
    const [, init] = raw[0];
    expect(new Headers(init?.headers).get("Content-Type")).toBe("text/csv");
    expect(Object.prototype.toString.call(init?.body)).toBe("[object ArrayBuffer]");
    expect([...new Uint8Array(init?.body as ArrayBuffer)]).toEqual([...bytes]);
  });

  it("forgets a commit's refusal when the file is dry-run again", async () => {
    // The second import call — the commit — is refused once.
    let calls = 0;
    stubInventoryApi({
      write: (method, path) => {
        if (method !== "POST" || !path.startsWith("/imports")) return undefined;
        calls += 1;
        return calls === 2 ? inventoryProblem(409, "client_request_reused", { title: "Reused" }) : undefined;
      },
    });
    renderRoute("/inventory/import");
    await screen.findByRole("combobox", { name: "Site" });
    await choose(document.body, "Site", "OSL · Oslo lager");
    await userEvent.upload(fileInput(), csv("location,sku,quantity\nOSL-A-01,TS-M,5\n"));
    await userEvent.click(screen.getByRole("button", { name: "Dry run" }));
    await userEvent.click(await screen.findByRole("button", { name: "Commit" }));
    expect(await screen.findByText("Could not read the file")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Dry run" }));
    await waitFor(() => expect(screen.queryByText("Could not read the file")).not.toBeInTheDocument());
  });

  it("offers a template to start from", async () => {
    stubInventoryApi();
    const createObjectURL = vi.fn<(blob: Blob) => string>(() => "blob:template");
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() }));
    renderRoute("/inventory/import");
    await userEvent.click(await screen.findByRole("button", { name: "Download template" }));
    expect(createObjectURL).toHaveBeenCalledTimes(1);
    expect(await createObjectURL.mock.calls[0][0].text()).toBe(
      "location,sku,quantity,lot,supplier_lot,production_date,best_before,expiry\n",
    );
  });

  it("tells a caller without Adjust stock instead of offering the upload", async () => {
    stubInventoryApi({ meta: { canAdjust: false } });
    renderRoute("/inventory/import");
    expect(await screen.findByText(/You cannot import stock/)).toBeInTheDocument();
    expect(fileInput()).toBeNull();
  });
});

describe("the placements import (I61)", () => {
  const placementsFile = (text: string) => new File([text], "placements.csv", { type: "text/csv" });
  const posts = (fetchMock: Parameters<typeof sent>[0]) => sent(fetchMock, "POST", /\/imports\/placements/);

  it("dry-runs a file of every site, offers no commit while a row fails, and commits a clean one", async () => {
    const fetchMock = stubInventoryApi();
    renderRoute("/inventory/import");
    await userEvent.click(await screen.findByRole("radio", { name: "Placements" }));
    expect(screen.queryByRole("combobox", { name: "Site" })).not.toBeInTheDocument();

    await userEvent.upload(
      fileInput(),
      placementsFile("sku,site,primary_location,reorder_point,maximum\nTS-M,OSL,OSL-A-01,5,20\nTS-M,TRD,,5,\n"),
    );
    await userEvent.click(screen.getByRole("button", { name: "Dry run" }));
    expect(await screen.findByText("1 row cannot be imported. Fix the file and try again.")).toBeInTheDocument();
    expect(screen.getByText("No such site")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Commit" })).toBeDisabled();
    expect(posts(fetchMock)[0].url).toContain("/imports/placements?dryRun=true");

    await userEvent.upload(
      fileInput(),
      placementsFile("sku,site,primary_location,reorder_point,maximum\nTS-M,OSL,OSL-A-01,5,20\nBOLT-10,BGO,,2,\n"),
    );
    await userEvent.click(screen.getByRole("button", { name: "Dry run" }));
    expect(await screen.findByText("All 2 rows are ready to import.")).toBeInTheDocument();
    const table = screen.getByRole("table");
    expect(within(table).getByText("OSL-A-01")).toBeInTheDocument();
    expect(fetchMock.state.placements).toEqual([]);

    await userEvent.click(screen.getByRole("button", { name: "Commit" }));
    expect(await screen.findByText("2 set, 0 removed.")).toBeInTheDocument();
    expect(posts(fetchMock)[2].url).toContain("dryRun=false");
    expect(fetchMock.state.placements).toEqual([
      { variantId: 101, siteId: 1, primaryLocationId: 12, reorderPoint: 5, maximum: 20, revision: 1 },
      { variantId: 201, siteId: 2, primaryLocationId: null, reorderPoint: 2, maximum: null, revision: 1 },
    ]);
  });

  it("says the same file was already imported when the server replays it", async () => {
    const fetchMock = stubInventoryApi();
    renderRoute("/inventory/import");
    await userEvent.click(await screen.findByRole("radio", { name: "Placements" }));
    const text = "sku,site,primary_location,reorder_point,maximum\nTS-M,OSL,,5,20\n";
    for (const _ of [1, 2]) {
      await userEvent.upload(fileInput(), placementsFile(text));
      await userEvent.click(screen.getByRole("button", { name: "Dry run" }));
      await userEvent.click(await screen.findByRole("button", { name: "Commit" }));
      await waitFor(() => expect(posts(fetchMock).filter((one) => one.url.includes("dryRun=false"))).toHaveLength(_));
    }
    expect(await screen.findByText("Already imported")).toBeInTheDocument();
    expect(screen.getByText("This file was imported before, so nothing was written this time.")).toBeInTheDocument();
    expect(screen.queryByText("Placements imported")).not.toBeInTheDocument();
  });

  it("offers a template of the placements' header", async () => {
    const createObjectURL = vi.fn<(blob: Blob) => string>(() => "blob:template");
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() }));
    stubInventoryApi();
    renderRoute("/inventory/import");
    await userEvent.click(await screen.findByRole("radio", { name: "Placements" }));
    await userEvent.click(screen.getByRole("button", { name: "Download template" }));
    expect(await createObjectURL.mock.calls[0][0].text()).toBe("sku,site,primary_location,reorder_point,maximum\n");
  });
});
