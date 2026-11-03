import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { choose, retype, scan, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { inventoryProblem, stubInventoryApi } from "../test/server";

const scanField = () => screen.findByRole("textbox", { name: "Scan an item" });

describe("the line editor", () => {
  it("adds a scanned item as a line, and counts one more when it is scanned again", async () => {
    const fetchMock = stubInventoryApi();
    renderRoute("/inventory/movements/new/receipt?siteId=1&locationId=12");
    const field = await scanField();
    await scan(field, "TS-M");
    expect(await screen.findByRole("textbox", { name: "Quantity, line 1" })).toHaveValue("1");
    // The same variant by its barcode: the line it is on counts one more.
    await scan(field, "7070000000101");
    await waitFor(() => expect(screen.getByRole("textbox", { name: "Quantity, line 1" })).toHaveValue("2"));
    await scan(field, "BOLT-10");
    expect(await screen.findByRole("textbox", { name: "Quantity, line 2" })).toHaveValue("1");
    expect(screen.queryByRole("textbox", { name: "Quantity, line 3" })).not.toBeInTheDocument();

    await scan(field, "NOPE");
    expect(await screen.findByText("No item matches “NOPE”.")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Post receipt" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/movements\/receipts$/)).toHaveLength(1));
    const [receipt] = sent(fetchMock, "POST", /\/movements\/receipts$/);
    expect(receipt.body).toEqual({
      clientRequestId: expect.stringMatching(/^[0-9a-f-]{36}$/),
      siteId: 1,
      toLocationId: 12,
      lines: [
        { variantId: 101, quantity: 2 },
        { variantId: 201, quantity: 1 },
      ],
    });
    expect(await screen.findByRole("link", { name: "Open movement 901" })).toHaveAttribute(
      "href",
      "/inventory/movements/901",
    );
    // The form empties for the next receipt.
    expect(screen.queryByRole("textbox", { name: "Quantity, line 1" })).not.toBeInTheDocument();
  });

  it("marks the refused lines, and the try after the refusal is a new posting under a new key", async () => {
    let refuse = true;
    const fetchMock = stubInventoryApi({
      balances: [{ variantId: 101, locationId: 12, onHand: 5 }],
      write: (method, path) => {
        if (method !== "POST" || path !== "/movements/moves" || !refuse) return undefined;
        refuse = false;
        return inventoryProblem(409, "insufficient_stock", {
          detail: "1 line was refused.",
          lines: [{ index: 1, code: "insufficient_stock", detail: "BOLT-10: 0 pcs at OSL-A-01, 1 asked for." }],
        });
      },
    });
    renderRoute("/inventory/movements/new/move?siteId=1&locationId=12");
    const field = await scanField();
    await scan(field, "TS-M");
    await scan(field, "BOLT-10");
    await screen.findByRole("textbox", { name: "Quantity, line 2" });
    const table = screen.getByRole("table");
    await choose(table, "To, line 1", "OSL-A-02");
    await choose(table, "To, line 2", "OSL-A-02");

    await userEvent.click(screen.getByRole("button", { name: "Post move" }));
    expect(await screen.findByText("Not enough stock on the location.")).toBeInTheDocument();
    expect(screen.getByText("BOLT-10: 0 pcs at OSL-A-01, 1 asked for.")).toBeInTheDocument();
    expect(screen.getByText(/1 line was refused; it is marked below/)).toBeInTheDocument();
    const rows = within(table).getAllByRole("row");
    expect(rows[1]).not.toHaveAttribute("data-refused");
    expect(rows[2]).toHaveAttribute("data-refused", "true");

    // The person takes the bolt off and tries again: the refusal stored nothing, so this is a new key.
    await userEvent.click(screen.getByRole("button", { name: "Remove line 2" }));
    await userEvent.click(screen.getByRole("button", { name: "Post move" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/movements\/moves$/)).toHaveLength(2));
    const [failed, retried] = sent(fetchMock, "POST", /\/movements\/moves$/);
    expect(retried.body.clientRequestId).not.toBe(failed.body.clientRequestId);
    expect(retried.body.lines).toEqual([{ variantId: 101, fromLocationId: 12, toLocationId: 13, quantity: 1 }]);
    expect(await screen.findByRole("link", { name: "Open movement 901" })).toBeInTheDocument();
    expect(screen.queryByText("Not enough stock on the location.")).not.toBeInTheDocument();

    // The next move is another posting.
    await scan(field, "TS-M");
    await choose(screen.getByRole("table"), "To, line 1", "OSL-A-02");
    await userEvent.click(screen.getByRole("button", { name: "Post move" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/movements\/moves$/)).toHaveLength(3));
    expect(sent(fetchMock, "POST", /\/movements\/moves$/)[2].body.clientRequestId).not.toBe(
      retried.body.clientRequestId,
    );
  });

  it("says a refusal of the request as a whole above the button", async () => {
    stubInventoryApi({
      write: () => inventoryProblem(400, "nothing_to_post", { detail: "Every line is already what lies there." }),
    });
    renderRoute("/inventory/movements/new/adjustment?siteId=1&locationId=12");
    await scan(await scanField(), "TS-M");
    await choose(document.body, "Reason", "Correction");
    await userEvent.click(screen.getByRole("button", { name: "Post adjustment" }));
    expect(
      await screen.findByText("Nothing would change: every line already matches what lies there."),
    ).toBeInTheDocument();
  });
});

describe("posting", () => {
  it("posts an adjustment with its reason and a signed change", async () => {
    const fetchMock = stubInventoryApi({ balances: [{ variantId: 101, locationId: 12, onHand: 5 }] });
    renderRoute("/inventory/movements/new/adjustment?siteId=1&locationId=12");
    await scan(await scanField(), "TS-M");
    await retype(await screen.findByRole("textbox", { name: "Quantity, line 1" }), "-2.5");
    await choose(document.body, "Reason", "Damaged");
    await userEvent.type(screen.getByRole("textbox", { name: "Note" }), "Dropped");
    await userEvent.click(screen.getByRole("button", { name: "Post adjustment" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/movements\/adjustments$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/movements\/adjustments$/)[0].body).toEqual({
      clientRequestId: expect.any(String),
      siteId: 1,
      reason: "damaged",
      note: "Dropped",
      lines: [{ variantId: 101, locationId: 12, mode: "change", quantity: -2.5 }],
    });
    expect(fetchMock.state.balances).toEqual([{ variantId: 101, locationId: 12, onHand: 2.5 }]);
  });

  it("posts a move from the location it was opened on", async () => {
    const fetchMock = stubInventoryApi({ balances: [{ variantId: 201, locationId: 12, onHand: 5 }] });
    renderRoute("/inventory/movements/new/move?locationId=12");
    await scan(await scanField(), "BOLT-10");
    await retype(await screen.findByRole("textbox", { name: "Quantity, line 1" }), "3");
    await choose(screen.getByRole("table"), "To, line 1", "OSL-A-02");
    await userEvent.click(screen.getByRole("button", { name: "Post move" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/movements\/moves$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/movements\/moves$/)[0].body).toMatchObject({
      siteId: 1,
      lines: [{ variantId: 201, fromLocationId: 12, toLocationId: 13, quantity: 3 }],
    });
  });

  it("posts a receipt with its supplier, a line overriding the receipt's location", async () => {
    const fetchMock = stubInventoryApi();
    renderRoute("/inventory/movements/new/receipt?siteId=1&locationId=12&variantId=102");
    expect(await screen.findByRole("textbox", { name: "Quantity, line 1" })).toHaveValue("1");
    await scan(await scanField(), "BOLT-10");
    await choose(screen.getByRole("table"), "Location, line 2", "OSL-A-02");
    await userEvent.type(screen.getByRole("textbox", { name: "Supplier" }), "Bolt AS");
    await userEvent.type(screen.getByRole("textbox", { name: "Supplier's reference" }), "PO-77");
    await userEvent.click(screen.getByRole("button", { name: "Post receipt" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/movements\/receipts$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/movements\/receipts$/)[0].body).toMatchObject({
      siteId: 1,
      toLocationId: 12,
      supplierName: "Bolt AS",
      supplierReference: "PO-77",
      lines: [
        { variantId: 102, quantity: 1 },
        { variantId: 201, quantity: 1, locationId: 13 },
      ],
    });
  });

  it("tells a caller without the permission instead of showing the form", async () => {
    stubInventoryApi({ meta: { canReceive: false } });
    renderRoute("/inventory/movements/new/receipt");
    expect(await screen.findByText(/You cannot receive goods/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Post receipt" })).not.toBeInTheDocument();
  });
});

/** Holds back the lookup's answers until `release` is called, so scans overlap as a fast scanner's do. */
const holdLookups = () => {
  const inner = globalThis.fetch;
  const held: (() => void)[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) =>
      String(input).includes("/lookup")
        ? new Promise<Response>((resolve) => held.push(() => resolve(inner(input, init))))
        : inner(input, init),
    ),
  );
  return {
    count: () => held.length,
    release: () => {
      for (const one of held.splice(0)) one();
    },
  };
};

const movesSent = (fetchMock: Parameters<typeof sent>[0]) => sent(fetchMock, "POST", /\/movements\/moves$/);

/** A move of one T-shirt from OSL-A-01 to OSL-A-02, filled in and ready to post. */
const fillMove = async () => {
  renderRoute("/inventory/movements/new/move?siteId=1&locationId=12");
  await scan(await scanField(), "TS-M");
  await choose(await screen.findByRole("table"), "To, line 1", "OSL-A-02");
};

describe("quantities", () => {
  it("keeps a scanned count at three decimals: 0.118 and a scan is 1.118", async () => {
    const fetchMock = stubInventoryApi();
    renderRoute("/inventory/movements/new/receipt?siteId=1&locationId=12");
    const field = await scanField();
    await scan(field, "TS-M");
    await retype(await screen.findByRole("textbox", { name: "Quantity, line 1" }), "0.118");
    await scan(field, "TS-M");
    await waitFor(() => expect(screen.getByRole("textbox", { name: "Quantity, line 1" })).toHaveValue("1.118"));
    await userEvent.click(screen.getByRole("button", { name: "Post receipt" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/movements\/receipts$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/movements\/receipts$/)[0].body.lines).toEqual([
      { variantId: 101, quantity: 1.118 },
    ]);
  });
});

describe("scanning", () => {
  it("lands two scans fired back to back, the second counting one more of the same item", async () => {
    stubInventoryApi();
    renderRoute("/inventory/movements/new/receipt?siteId=1&locationId=12");
    const field = await scanField();
    const lookups = holdLookups();
    // The field empties on Enter, so the second code is typed while the first is still being looked up.
    await userEvent.type(field, "TS-M{Enter}");
    expect(field).toHaveValue("");
    await userEvent.type(field, "7070000000101{Enter}");
    expect(lookups.count()).toBe(2);
    lookups.release();
    await waitFor(() => expect(screen.getByRole("textbox", { name: "Quantity, line 1" })).toHaveValue("2"));
    expect(screen.queryByRole("textbox", { name: "Quantity, line 2" })).not.toBeInTheDocument();
  });

  it("says in the viewer's words when the server cannot be reached, and keeps the code", async () => {
    stubInventoryApi();
    renderRoute("/inventory/movements/new/receipt?siteId=1&locationId=12");
    const field = await scanField();
    const inner = globalThis.fetch;
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).includes("/lookup")) throw new TypeError("Failed to fetch");
        return inner(input, init);
      }),
    );
    await scan(field, "TS-M");
    expect(
      await screen.findByText("Could not reach the server. Check your connection and try again."),
    ).toBeInTheDocument();
    expect(field).toHaveValue("TS-M");
  });
});

describe("the site", () => {
  it("drops the location the form was opened on when the site changes, for the rows added after", async () => {
    const fetchMock = stubInventoryApi();
    renderRoute("/inventory/movements/new/adjustment?siteId=1&locationId=12");
    const field = await scanField();
    await choose(document.body, "Site", "BGO · Bergen butikk");
    await scan(field, "BOLT-10");
    await screen.findByRole("textbox", { name: "Quantity, line 1" });
    await choose(document.body, "Reason", "Correction");
    await userEvent.click(screen.getByRole("button", { name: "Post adjustment" }));
    expect(await screen.findByText("Pick the location.")).toBeInTheDocument();
    expect(sent(fetchMock, "POST", /\/movements\/adjustments$/)).toHaveLength(0);
  });
});

describe("the request's key", () => {
  it("says an earlier version was already posted, links to the site's movements, and posts as new under a new key", async () => {
    let reuse = true;
    const fetchMock = stubInventoryApi({
      write: (method, path) => {
        if (method !== "POST" || path !== "/movements/moves" || !reuse) return undefined;
        reuse = false;
        return inventoryProblem(409, "client_request_reused", { detail: "The key was used for another body." });
      },
    });
    await fillMove();
    await userEvent.click(screen.getByRole("button", { name: "Post move" }));
    expect(await screen.findByText("Already posted")).toBeInTheDocument();
    expect(screen.getByText(/An earlier version of this posting has already been posted/)).toBeInTheDocument();
    expect(screen.queryByText(/post again/i)).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Check the site's movements" })).toHaveAttribute(
      "href",
      "/inventory/movements?siteId=1",
    );
    // The line the person filled in is still there.
    expect(screen.getByRole("textbox", { name: "Quantity, line 1" })).toHaveValue("1");

    await userEvent.click(screen.getByRole("button", { name: "Post as a new posting" }));
    await waitFor(() => expect(movesSent(fetchMock)).toHaveLength(2));
    const [first, second] = movesSent(fetchMock);
    expect(second.body.clientRequestId).not.toBe(first.body.clientRequestId);
    expect(await screen.findByRole("link", { name: "Open movement 901" })).toBeInTheDocument();
    expect(screen.queryByText("Already posted")).not.toBeInTheDocument();
  });

  it("makes a new key after a 400 refusal of a line: nothing was stored", async () => {
    let refuse = true;
    const fetchMock = stubInventoryApi({
      write: (method, path) => {
        if (method !== "POST" || path !== "/movements/moves" || !refuse) return undefined;
        refuse = false;
        return inventoryProblem(400, "quantity_invalid", {
          lines: [{ index: 0, code: "quantity_invalid", detail: "TS-M: 1.0001 has more than three decimals." }],
        });
      },
    });
    await fillMove();
    await userEvent.click(screen.getByRole("button", { name: "Post move" }));
    expect(await screen.findByText(/1 line was refused/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Post move" }));
    await waitFor(() => expect(movesSent(fetchMock)).toHaveLength(2));
    const [first, second] = movesSent(fetchMock);
    expect(second.body.clientRequestId).not.toBe(first.body.clientRequestId);
  });

  it("keeps the key after a 500 and after a network failure: the posting may have landed", async () => {
    const failures: (() => Response)[] = [
      () => new Response(null, { status: 500 }),
      () => {
        throw new TypeError("Failed to fetch");
      },
    ];
    const fetchMock = stubInventoryApi({
      write: (method, path) => (method === "POST" && path === "/movements/moves" ? failures.shift()?.() : undefined),
    });
    await fillMove();
    await userEvent.click(screen.getByRole("button", { name: "Post move" }));
    expect(await screen.findByText("The request failed (HTTP 500).")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Post move" }));
    expect(
      await screen.findByText("Could not reach the server. Check your connection and try again."),
    ).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Post move" }));
    await waitFor(() => expect(movesSent(fetchMock)).toHaveLength(3));
    const keys = movesSent(fetchMock).map((one) => one.body.clientRequestId);
    expect(new Set(keys).size).toBe(1);
    expect(await screen.findByRole("link", { name: "Open movement 901" })).toBeInTheDocument();
  });
});
