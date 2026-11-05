import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { choose, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubInventoryApi } from "../test/server";

describe("the site form's Fulfils orders", () => {
  it("follows a new site's kind: on for a warehouse, off for a store", async () => {
    const fetchMock = stubInventoryApi();
    renderRoute("/inventory");
    await userEvent.click(await screen.findByRole("button", { name: "New site" }));
    const dialog = await screen.findByRole("dialog");
    const fulfils = screen.getByRole("switch", { name: /Fulfils orders/ });
    expect(fulfils).toBeChecked();
    await choose(dialog, /Kind/, "Store");
    expect(fulfils).not.toBeChecked();
    await userEvent.type(screen.getByRole("textbox", { name: /Code/ }), "shop");
    await userEvent.type(screen.getByRole("textbox", { name: /Name/ }), "Shop");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/sites$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/sites$/)[0].body).toMatchObject({
      code: "SHOP",
      kind: "store",
      fulfilsOrders: false,
    });
  });

  it("changes an existing site's flag, which the site page shows", async () => {
    const fetchMock = stubInventoryApi();
    renderRoute("/inventory/sites/1");
    expect(await screen.findByText("Fulfils orders")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Site actions" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Edit site" }));
    const fulfils = await screen.findByRole("switch", { name: /Fulfils orders/ });
    expect(fulfils).toBeChecked();
    await userEvent.click(fulfils);
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/sites\/1$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /\/sites\/1$/)[0].body).toMatchObject({ fulfilsOrders: false, status: "active" });
  });
});
